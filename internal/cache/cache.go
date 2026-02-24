// Package cache provides a file-based cost center cache that reduces
// API calls on repeated runs.  Each entry has a configurable TTL
// (default 24 hours) and the cache is stored as JSON.
package cache

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	// DefaultTTLHours is the default time-to-live for cache entries.
	DefaultTTLHours = 24
	// DefaultCacheDir is the directory relative to the working directory.
	DefaultCacheDir = ".cache"
	// DefaultCacheFile is the filename inside the cache directory.
	DefaultCacheFile = "cost_centers.json"
	// currentVersion is the cache format version.
	currentVersion = 1
)

// Entry represents a single cached cost center lookup.
type Entry struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	CachedAt time.Time `json:"cached_at"`
	TTLHours int       `json:"ttl_hours"`
}

// IsExpired reports whether the entry has exceeded its TTL.
func (e Entry) IsExpired() bool {
	ttl := time.Duration(e.TTLHours) * time.Hour
	return time.Since(e.CachedAt) > ttl
}

// cacheData is the on-disk JSON structure.
type cacheData struct {
	Version int              `json:"version"`
	Entries map[string]Entry `json:"entries"`
}

// Stats holds cache statistics for display.
type Stats struct {
	TotalEntries   int
	ExpiredEntries int
	ValidEntries   int
	FilePath       string
	FileSizeBytes  int64
}

// Cache is a file-backed cost center cache.
type Cache struct {
	mu       sync.Mutex
	filePath string
	ttlHours int
	data     cacheData
	log      *slog.Logger
}

// New creates or loads a cache from the given directory.
// If dir is empty, DefaultCacheDir is used.
func New(dir string, logger *slog.Logger) (*Cache, error) {
	if dir == "" {
		dir = DefaultCacheDir
	}
	path := filepath.Join(dir, DefaultCacheFile)

	c := &Cache{
		filePath: path,
		ttlHours: DefaultTTLHours,
		log:      logger,
		data: cacheData{
			Version: currentVersion,
			Entries: make(map[string]Entry),
		},
	}

	if err := c.load(); err != nil {
		c.log.Debug("No existing cache file, starting fresh", "path", path, "error", err)
	}

	return c, nil
}

// Get retrieves a cached entry by key.  Returns the entry and true if
// a valid (non-expired) entry exists, or a zero Entry and false otherwise.
func (c *Cache) Get(key string) (Entry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	e, ok := c.data.Entries[key]
	if !ok {
		return Entry{}, false
	}
	if e.IsExpired() {
		c.log.Debug("Cache entry expired", "key", key)
		return Entry{}, false
	}
	c.log.Debug("Cache hit", "key", key, "id", e.ID)
	return e, true
}

// Set stores or updates a cache entry and flushes to disk.
func (c *Cache) Set(key, id, name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.data.Entries[key] = Entry{
		ID:       id,
		Name:     name,
		CachedAt: time.Now().UTC(),
		TTLHours: c.ttlHours,
	}
	c.log.Debug("Cache set", "key", key, "id", id)
	return c.save()
}

// GetStats returns statistics about the current cache.
func (c *Cache) GetStats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()

	s := Stats{
		TotalEntries: len(c.data.Entries),
		FilePath:     c.filePath,
	}

	for _, e := range c.data.Entries {
		if e.IsExpired() {
			s.ExpiredEntries++
		} else {
			s.ValidEntries++
		}
	}

	if info, err := os.Stat(c.filePath); err == nil {
		s.FileSizeBytes = info.Size()
	}

	return s
}

// Clear removes all cache entries and deletes the cache file.
func (c *Cache) Clear() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.data.Entries = make(map[string]Entry)
	c.log.Info("Cache cleared")

	if err := os.Remove(c.filePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing cache file: %w", err)
	}
	return nil
}

// CleanupExpired removes expired entries and saves to disk.
// Returns the number of entries removed.
func (c *Cache) CleanupExpired() (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	removed := 0
	for key, e := range c.data.Entries {
		if e.IsExpired() {
			delete(c.data.Entries, key)
			removed++
			c.log.Debug("Removed expired entry", "key", key)
		}
	}

	if removed > 0 {
		if err := c.save(); err != nil {
			return removed, err
		}
	}

	c.log.Info("Cleanup complete", "removed", removed, "remaining", len(c.data.Entries))
	return removed, nil
}

// FilePath returns the path to the cache file.
func (c *Cache) FilePath() string {
	return c.filePath
}

// load reads the cache file from disk. Returns an error if the file
// does not exist or cannot be parsed.
func (c *Cache) load() error {
	f, err := os.Open(c.filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	var d cacheData
	if err := json.NewDecoder(f).Decode(&d); err != nil {
		return fmt.Errorf("decoding cache file: %w", err)
	}

	if d.Version != currentVersion {
		c.log.Warn("Cache version mismatch, starting fresh",
			"expected", currentVersion, "found", d.Version)
		return nil
	}

	if d.Entries == nil {
		d.Entries = make(map[string]Entry)
	}

	c.data = d
	c.log.Debug("Cache loaded", "entries", len(c.data.Entries), "path", c.filePath)
	return nil
}

// save writes the cache data to disk, creating the directory if needed.
func (c *Cache) save() error {
	dir := filepath.Dir(c.filePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating cache directory: %w", err)
	}

	f, err := os.Create(c.filePath)
	if err != nil {
		return fmt.Errorf("creating cache file: %w", err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(c.data); err != nil {
		return fmt.Errorf("encoding cache file: %w", err)
	}

	c.log.Debug("Cache saved", "entries", len(c.data.Entries), "path", c.filePath)
	return nil
}
