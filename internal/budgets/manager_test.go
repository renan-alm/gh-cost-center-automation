package budgets

import (
	"log/slog"
	"os"
	"testing"

	"github.com/renan-alm/gh-cost-center/internal/config"
)

func TestNewManager(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	products := map[string]config.ProductBudget{
		"actions": {Amount: 100, Enabled: true},
		"copilot": {Amount: 200, Enabled: false},
	}

	mgr := NewManager(nil, logger, products)
	if mgr == nil {
		t.Fatal("expected non-nil manager")
	}
	if len(mgr.products) != 2 {
		t.Errorf("expected 2 products, got %d", len(mgr.products))
	}
}

func TestIsAvailable_Initially(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	mgr := NewManager(nil, logger, nil)

	if !mgr.IsAvailable() {
		t.Error("expected IsAvailable() == true initially")
	}
}

func TestIsAvailable_AfterUnavailable(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	mgr := NewManager(nil, logger, nil)
	mgr.unavailable = true

	if mgr.IsAvailable() {
		t.Error("expected IsAvailable() == false after marking unavailable")
	}
}

func TestEnsureBudgets_SkipsWhenUnavailable(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	products := map[string]config.ProductBudget{
		"actions": {Amount: 100, Enabled: true},
	}
	mgr := NewManager(nil, logger, products)
	mgr.unavailable = true

	// Should return immediately without panic (no client set).
	mgr.EnsureBudgetsForCostCenter("cc-id-1", "Test CC")
}
