package github

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"os"
	"os/exec"
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

	// rateLimitWarnThreshold triggers a warning log when X-RateLimit-Remaining
	// drops below this value, giving operators an early signal before the limit
	// is exhausted.
	rateLimitWarnThreshold = 100
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
	token      string // Bearer token for GitHub API
	log        *slog.Logger
	ccCache    *cache.Cache // optional cost center cache
}

// NewClient creates a Client from a loaded config.Manager.
//
// Authentication is resolved in this order:
//  1. Explicit token passed via --token flag (stored in cfg.Token).
//  2. GITHUB_TOKEN environment variable (set by gh CLI for extensions).
//  3. GH_TOKEN environment variable.
//  4. `gh auth token` shell-out (silent fallback if gh is installed).
//
// Returns an error if no token can be obtained.
func NewClient(cfg *config.Manager, logger *slog.Logger) (*Client, error) {
	if cfg.Enterprise == "" {
		return nil, fmt.Errorf("enterprise slug is required")
	}

	baseURL := strings.TrimRight(cfg.APIBaseURL, "/")

	token := resolveToken(cfg.Token, logger)
	if token == "" {
		return nil, fmt.Errorf("no GitHub token found: set GITHUB_TOKEN, GH_TOKEN, use --token flag, or run 'gh auth login'")
	}

	logger.Debug("GitHub token resolved", "source", tokenSource(cfg.Token))

	return &Client{
		http:       &http.Client{Timeout: 30 * time.Second},
		baseURL:    baseURL,
		enterprise: cfg.Enterprise,
		token:      token,
		log:        logger,
	}, nil
}

// resolveToken returns the first non-empty token from the chain:
// flag → GITHUB_TOKEN → GH_TOKEN → gh auth token.
func resolveToken(flagToken string, logger *slog.Logger) string {
	if flagToken != "" {
		return flagToken
	}
	if v := os.Getenv("GITHUB_TOKEN"); v != "" {
		return v
	}
	if v := os.Getenv("GH_TOKEN"); v != "" {
		return v
	}
	// Fallback: try `gh auth token`.
	out, err := exec.Command("gh", "auth", "token").Output()
	if err != nil {
		logger.Debug("gh auth token fallback failed", "error", err)
		return ""
	}
	return strings.TrimSpace(string(out))
}

// tokenSource returns a log-safe label describing where the token came from.
func tokenSource(flagToken string) string {
	if flagToken != "" {
		return "--token flag"
	}
	if os.Getenv("GITHUB_TOKEN") != "" {
		return "GITHUB_TOKEN env"
	}
	if os.Getenv("GH_TOKEN") != "" {
		return "GH_TOKEN env"
	}
	return "gh auth token"
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
			// Proactively warn when the primary rate limit is running low so
			// operators can act before the quota is exhausted.
			if remaining := resp.Header.Get("X-RateLimit-Remaining"); remaining != "" {
				if n, err := strconv.ParseInt(remaining, 10, 64); err == nil && n < rateLimitWarnThreshold {
					c.log.Warn("GitHub API rate limit running low",
						"remaining", n,
						"url", url,
					)
				}
			}
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

		// Secondary rate limit — GitHub sends 403 with a Retry-After header
		// when abuse detection or concurrent-request limits are triggered.
		// Treat this as a retryable condition identical to a 429.
		if resp.StatusCode == http.StatusForbidden && resp.Header.Get("Retry-After") != "" {
			wait := c.rateLimitWait(resp)
			c.log.Warn("secondary rate limit hit (403), waiting",
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
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
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

// rateLimitWait computes how long to wait based on response headers.
// It checks Retry-After first (used for secondary rate limits and abuse
// detection), then falls back to X-RateLimit-Reset (primary rate limits),
// and finally rateLimitFallback when neither header is present.
func (c *Client) rateLimitWait(resp *http.Response) time.Duration {
	// Retry-After takes precedence — used for secondary rate limits and
	// abuse detection.  The value is a delay in whole seconds.
	if s := resp.Header.Get("Retry-After"); s != "" {
		if seconds, err := strconv.ParseInt(s, 10, 64); err == nil && seconds >= 0 {
			return time.Duration(seconds) * time.Second
		}
	}

	// X-RateLimit-Reset is used for primary rate limits.
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
