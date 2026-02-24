package github

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/renan-alm/gh-cost-center/internal/cache"
	"github.com/renan-alm/gh-cost-center/internal/config"
)

const (
	userAgent        = "gh-cost-center"
	apiVersion       = "2022-11-28"
	acceptHeader     = "application/vnd.github+json"
	maxRetries       = 3
	retryBackoffBase = 1 * time.Second

	// rateLimitFallback is used when the X-RateLimit-Reset header is missing.
	rateLimitFallback = 60 * time.Second
)

// retryableStatusCodes lists HTTP status codes eligible for automatic retry.
var retryableStatusCodes = map[int]bool{
	http.StatusTooManyRequests:     true, // 429
	http.StatusInternalServerError: true, // 500
	http.StatusBadGateway:          true, // 502
	http.StatusServiceUnavailable:  true, // 503
	http.StatusGatewayTimeout:      true, // 504
}

// Client wraps an http.Client configured for the GitHub REST API.
// It transparently handles authentication, retries on transient errors,
// and rate-limit back-off.
type Client struct {
	http       *http.Client
	baseURL    string
	enterprise string
	log        *slog.Logger
	ccCache    *cache.Cache // optional cost center cache
}

// NewClient creates a Client from a loaded config.Manager.
//
// Authentication is resolved in this order:
//  1. GITHUB_TOKEN environment variable (set by gh CLI for extensions).
//  2. GH_TOKEN environment variable.
//
// The caller typically does not need to set either variable manually
// because `gh` injects the token when running extensions.
func NewClient(cfg *config.Manager, logger *slog.Logger) (*Client, error) {
	if cfg.Enterprise == "" {
		return nil, fmt.Errorf("enterprise slug is required")
	}

	baseURL := strings.TrimRight(cfg.APIBaseURL, "/")

	return &Client{
		http:       &http.Client{Timeout: 30 * time.Second},
		baseURL:    baseURL,
		enterprise: cfg.Enterprise,
		log:        logger,
	}, nil
}

// SetCache attaches a cost center cache to the client.  When set, cost
// center lookups check the cache before making API calls and update the
// cache when the API responds.
func (c *Client) SetCache(cc *cache.Cache) {
	c.ccCache = cc
}

// APIError is returned when the GitHub API responds with a non-2xx status
// that is not retried (or all retries are exhausted).
type APIError struct {
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("GitHub API error %d: %s", e.StatusCode, e.Body)
}

// --------------------------------------------------------------------
// Core request helpers
// --------------------------------------------------------------------

// doJSON performs an HTTP request, retrying on transient errors and rate
// limits. If dest is non-nil the response body is JSON-decoded into it.
// The body parameter, when non-nil, is JSON-encoded as the request body.
func (c *Client) doJSON(method, url string, body any, dest any) (*http.Response, error) {
	attempt := 0
	for attempt < maxRetries {
		resp, err := c.do(method, url, body)
		if err != nil {
			if isTransient(err) && attempt < maxRetries-1 {
				wait := c.backoff(attempt, nil)
				c.log.Warn("transient error, retrying",
					"attempt", attempt+1,
					"wait", wait,
					"err", err,
				)
				time.Sleep(wait)
				attempt++
				continue
			}
			return nil, err
		}

		// Successful 2xx — decode response.
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if dest != nil {
				defer func() { _ = resp.Body.Close() }()
				if err := json.NewDecoder(resp.Body).Decode(dest); err != nil {
					return resp, fmt.Errorf("decoding response from %s %s: %w", method, url, err)
				}
			} else {
				_ = resp.Body.Close()
			}
			return resp, nil
		}

		// Read error body for logging / APIError.
		errBody := readBody(resp)
		_ = resp.Body.Close()

		// Rate limit — sleep until reset and then retry (does not count
		// against the retry budget).
		if resp.StatusCode == http.StatusTooManyRequests {
			wait := c.rateLimitWait(resp)
			c.log.Warn("rate limit hit, waiting",
				"wait", wait,
				"url", url,
			)
			time.Sleep(wait)
			continue // do NOT increment attempt
		}

		// Retryable server error.
		if retryableStatusCodes[resp.StatusCode] && attempt < maxRetries-1 {
			wait := c.backoff(attempt, resp)
			c.log.Warn("retryable HTTP error, retrying",
				"status", resp.StatusCode,
				"attempt", attempt+1,
				"wait", wait,
				"url", url,
			)
			time.Sleep(wait)
			attempt++
			continue
		}

		// Non-retryable error — return APIError.
		return resp, &APIError{
			StatusCode: resp.StatusCode,
			Body:       errBody,
		}
	}

	// Should not typically be reached, but guard against it.
	return nil, fmt.Errorf("request to %s %s failed after %d retries", method, url, maxRetries)
}

// do builds and executes a single HTTP request (no retry logic).
func (c *Client) do(method, url string, body any) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshalling request body: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Accept", acceptHeader)
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	c.log.Debug("HTTP request",
		"method", method,
		"url", url,
	)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, url, err)
	}
	return resp, nil
}

// --------------------------------------------------------------------
// URL helpers
// --------------------------------------------------------------------

// enterpriseURL builds a full API URL for an enterprise-scoped endpoint.
//
//	c.enterpriseURL("/copilot/billing/seats")
//	→ "https://api.github.com/enterprises/SLUG/copilot/billing/seats"
func (c *Client) enterpriseURL(path string) string {
	return fmt.Sprintf("%s/enterprises/%s%s", c.baseURL, c.enterprise, path)
}

// --------------------------------------------------------------------
// Retry / back-off helpers
// --------------------------------------------------------------------

// backoff returns the duration to wait before the next retry.
// It uses exponential back-off: base * 2^attempt.
func (c *Client) backoff(attempt int, _ *http.Response) time.Duration {
	return retryBackoffBase * time.Duration(math.Pow(2, float64(attempt)))
}

// rateLimitWait computes how long to wait based on the X-RateLimit-Reset
// header.  Falls back to rateLimitFallback when the header is absent.
func (c *Client) rateLimitWait(resp *http.Response) time.Duration {
	resetStr := resp.Header.Get("X-RateLimit-Reset")
	if resetStr == "" {
		return rateLimitFallback
	}
	resetUnix, err := strconv.ParseInt(resetStr, 10, 64)
	if err != nil {
		return rateLimitFallback
	}
	wait := time.Until(time.Unix(resetUnix, 0)) + time.Second // +1s safety margin
	if wait <= 0 {
		return time.Second
	}
	return wait
}

// isTransient returns true for errors that are typically caused by network
// hiccups and are safe to retry (connection refused, timeouts, etc.).
func isTransient(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	for _, substr := range []string{
		"connection refused",
		"connection reset",
		"i/o timeout",
		"TLS handshake timeout",
		"EOF",
	} {
		if strings.Contains(s, substr) {
			return true
		}
	}
	return false
}

// readBody reads and returns the response body as a string, capped at 4 KB.
func readBody(resp *http.Response) string {
	if resp.Body == nil {
		return ""
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return "<error reading body>"
	}
	return string(b)
}
