// Package budgets provides helper functions for creating product budgets for
// newly-created cost centers.  It wraps the lower-level github.Client budget
// operations and handles the case where the Budgets API is unavailable.
package budgets

import (
	"log/slog"

	"github.com/renan-alm/gh-cost-center/internal/config"
	"github.com/renan-alm/gh-cost-center/internal/github"
)

// Manager orchestrates product-budget creation for cost centers.
type Manager struct {
	client      *github.Client
	log         *slog.Logger
	products    map[string]config.ProductBudget
	unavailable bool
}

// NewManager creates a budget manager from a GitHub client, logger, and product budget map.
func NewManager(client *github.Client, logger *slog.Logger, products map[string]config.ProductBudget) *Manager {
	return &Manager{
		client:   client,
		log:      logger,
		products: products,
	}
}

// IsAvailable returns false once the budgets API has been detected as unavailable.
func (m *Manager) IsAvailable() bool {
	return !m.unavailable
}

// EnsureBudgetsForCostCenter creates all enabled product budgets for a cost center.
// If the budgets API is unavailable, it sets a flag and returns early.
func (m *Manager) EnsureBudgetsForCostCenter(ccID, ccName string) {
	if m.unavailable {
		return
	}

	m.log.Info("Creating budgets for cost center", "name", ccName)

	for product, pc := range m.products {
		if !pc.Enabled {
			m.log.Debug("Skipping disabled product budget", "product", product)
			continue
		}

		ok, err := m.client.CreateProductBudget(ccID, ccName, product, pc.Amount)
		if err != nil {
			if _, uaErr := err.(*github.BudgetsAPIUnavailableError); uaErr {
				m.log.Warn("Budgets API unavailable, disabling budget creation",
					"error", err)
				m.unavailable = true
				return
			}
			m.log.Error("Failed to create budget",
				"product", product, "cost_center", ccName, "error", err)
			continue
		}
		if ok {
			m.log.Info("Budget created",
				"product", product, "cost_center", ccName, "amount", pc.Amount)
		}
	}
}
