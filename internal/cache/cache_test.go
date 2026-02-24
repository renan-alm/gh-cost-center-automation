package cache

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// testLogger returns a quiet logger for tests.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestNew_CreatesEmptyCache(t *testing.T) {
	dir := t.TempDir()
	c, err := New(dir, testLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c == nil {
		t.Fatal("expected non-nil cache")
	}
	if len(c.data.Entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(c.data.Entries))
	}
}

func TestSetAndGet(t *testing.T) {
	dir := t.TempDir()
	c, _ := New(dir, testLogger())

	if err := c.Set("my-cc", "uuid-123", "My Cost Center"); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	e, ok := c.Get("my-cc")
	if !ok {
		t.Fatal("expected cache hit")
	}
	if e.ID != "uuid-123" {
		t.Errorf("ID: got %q, want %q", e.ID, "uuid-123")
	}
	if e.Name != "My Cost Center" {
		t.Errorf("Name: got %q, want %q", e.Name, "My Cost Center")
	}
	if e.TTLHours != DefaultTTLHours {
		t.Errorf("TTLHours: got %d, want %d", e.TTLHours, DefaultTTLHours)
	}
}

func TestGet_Miss(t *testing.T) {
	dir := t.TempDir()
	c, _ := New(dir, testLogger())

	_, ok := c.Get("nonexistent")
	if ok {
		t.Error("expected cache miss")
	}
}

func TestGet_Expired(t *testing.T) {
	dir := t.TempDir()
	c, _ := New(dir, testLogger())

	// Insert an entry that is already expired.
	c.data.Entries["old"] = Entry{
		ID:       "uuid-old",
		Name:     "Old CC",
		CachedAt: time.Now().Add(-25 * time.Hour),
		TTLHours: DefaultTTLHours,
	}

	_, ok := c.Get("old")
	if ok {
		t.Error("expected cache miss for expired entry")
	}
}

func TestClear(t *testing.T) {
	dir := t.TempDir()
	c, _ := New(dir, testLogger())

	_ = c.Set("a", "id-a", "A")
	_ = c.Set("b", "id-b", "B")

	if err := c.Clear(); err != nil {
		t.Fatalf("Clear failed: %v", err)
	}

	if len(c.data.Entries) != 0 {
		t.Errorf("expected 0 entries after clear, got %d", len(c.data.Entries))
	}

	// File should be removed.
	if _, err := os.Stat(c.filePath); !os.IsNotExist(err) {
		t.Error("expected cache file to be removed after clear")
	}
}

func TestCleanupExpired(t *testing.T) {
	dir := t.TempDir()
	c, _ := New(dir, testLogger())

	// One valid, one expired.
	_ = c.Set("valid", "id-valid", "Valid")
	c.data.Entries["expired"] = Entry{
		ID:       "id-expired",
		Name:     "Expired",
		CachedAt: time.Now().Add(-48 * time.Hour),
		TTLHours: DefaultTTLHours,
	}

	removed, err := c.CleanupExpired()
	if err != nil {
		t.Fatalf("CleanupExpired failed: %v", err)
	}
	if removed != 1 {
		t.Errorf("expected 1 removed, got %d", removed)
	}
	if len(c.data.Entries) != 1 {
		t.Errorf("expected 1 remaining entry, got %d", len(c.data.Entries))
	}
	if _, ok := c.data.Entries["valid"]; !ok {
		t.Error("expected valid entry to remain")
	}
}

func TestCleanupExpired_NoneExpired(t *testing.T) {
	dir := t.TempDir()
	c, _ := New(dir, testLogger())

	_ = c.Set("fresh", "id-1", "Fresh")

	removed, err := c.CleanupExpired()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if removed != 0 {
		t.Errorf("expected 0 removed, got %d", removed)
	}
}

func TestGetStats(t *testing.T) {
	dir := t.TempDir()
	c, _ := New(dir, testLogger())

	_ = c.Set("a", "id-a", "A")
	c.data.Entries["b"] = Entry{
		ID:       "id-b",
		Name:     "B",
		CachedAt: time.Now().Add(-48 * time.Hour),
		TTLHours: DefaultTTLHours,
	}

	stats := c.GetStats()
	if stats.TotalEntries != 2 {
		t.Errorf("TotalEntries: got %d, want 2", stats.TotalEntries)
	}
	if stats.ValidEntries != 1 {
		t.Errorf("ValidEntries: got %d, want 1", stats.ValidEntries)
	}
	if stats.ExpiredEntries != 1 {
		t.Errorf("ExpiredEntries: got %d, want 1", stats.ExpiredEntries)
	}
}

func TestPersistence(t *testing.T) {
	dir := t.TempDir()

	// Write entries.
	c1, _ := New(dir, testLogger())
	_ = c1.Set("cc1", "id-1", "CC One")
	_ = c1.Set("cc2", "id-2", "CC Two")

	// Reload from disk.
	c2, _ := New(dir, testLogger())
	e, ok := c2.Get("cc1")
	if !ok {
		t.Fatal("expected cc1 to survive reload")
	}
	if e.ID != "id-1" {
		t.Errorf("ID: got %q, want %q", e.ID, "id-1")
	}

	e2, ok := c2.Get("cc2")
	if !ok {
		t.Fatal("expected cc2 to survive reload")
	}
	if e2.Name != "CC Two" {
		t.Errorf("Name: got %q, want %q", e2.Name, "CC Two")
	}
}

func TestFilePath(t *testing.T) {
	dir := t.TempDir()
	c, _ := New(dir, testLogger())

	want := filepath.Join(dir, DefaultCacheFile)
	if c.FilePath() != want {
		t.Errorf("FilePath: got %q, want %q", c.FilePath(), want)
	}
}

func TestEntryIsExpired(t *testing.T) {
	e := Entry{
		CachedAt: time.Now().Add(-1 * time.Hour),
		TTLHours: 2,
	}
	if e.IsExpired() {
		t.Error("expected entry to still be valid (1h old, 2h TTL)")
	}

	e2 := Entry{
		CachedAt: time.Now().Add(-3 * time.Hour),
		TTLHours: 2,
	}
	if !e2.IsExpired() {
		t.Error("expected entry to be expired (3h old, 2h TTL)")
	}
}

func TestClear_NoFile(t *testing.T) {
	dir := t.TempDir()
	c, _ := New(dir, testLogger())

	// Clear without any file should not error.
	if err := c.Clear(); err != nil {
		t.Fatalf("Clear on empty cache should not error: %v", err)
	}
}

func TestNew_DefaultDir(t *testing.T) {
	// Test that passing empty string uses DefaultCacheDir.
	// We can\'t easily test the actual default dir, but verify filepath contains it.
	c, _ := New("", testLogger())
	if c.filePath != filepath.Join(DefaultCacheDir, DefaultCacheFile) {
		t.Errorf("expected default path, got %q", c.filePath)
	}
}
