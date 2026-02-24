// Package repository implements repository-based cost center assignment for
// GitHub Enterprise using custom properties to map repos to cost centers.
package repository

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/renan-alm/gh-cost-center/internal/config"
	"github.com/renan-alm/gh-cost-center/internal/github"
)

// MappingResult records the outcome of processing a single explicit mapping.
type MappingResult struct {
	CostCenter     string
	CostCenterID   string
	PropertyName   string
	PropertyValues []string
	ReposMatched   int
	ReposAssigned  int
	Success        bool
	Message        string
}

// Summary holds the overall result of a repository assignment run.
type Summary struct {
	TotalRepos      int
	MappingsTotal   int
	MappingsApplied int
	MappingResults  []MappingResult
}

// Print displays the summary to stdout.
func (s *Summary) Print() {
	fmt.Println()
	fmt.Println(strings.Repeat("=", 80))
	fmt.Println("REPOSITORY ASSIGNMENT SUMMARY")
	fmt.Println(strings.Repeat("=", 80))
	fmt.Printf("Total repositories in organization: %d\n", s.TotalRepos)
	fmt.Printf("Mappings processed: %d / %d\n", s.MappingsApplied, s.MappingsTotal)

	for _, r := range s.MappingResults {
		fmt.Println()
		fmt.Printf("Cost Center: %s\n", r.CostCenter)
		fmt.Printf("  Property:  %s\n", r.PropertyName)
		fmt.Printf("  Values:    %s\n", strings.Join(r.PropertyValues, ", "))
		fmt.Printf("  Matched:   %d repositories\n", r.ReposMatched)
		fmt.Printf("  Assigned:  %d repositories\n", r.ReposAssigned)
		if r.Success {
			fmt.Println("  Status:    Success")
		} else {
			fmt.Printf("  Status:    Failed \u2014 %s\n", r.Message)
		}
	}
	fmt.Println(strings.Repeat("=", 80))
}

// Manager handles repository-based cost center assignment.
type Manager struct {
	cfg      *config.Manager
	client   *github.Client
	log      *slog.Logger
	mappings []config.ExplicitMapping
}

// NewManager creates a new repository manager from configuration.
func NewManager(cfg *config.Manager, client *github.Client, logger *slog.Logger) (*Manager, error) {
	if cfg.RepositoryConfig == nil {
		return nil, fmt.Errorf("repository mode requires 'repository_config' in configuration")
	}
	if len(cfg.RepositoryConfig.ExplicitMappings) == 0 {
		return nil, fmt.Errorf("repository mode requires at least one explicit mapping in repository_config")
	}
	return &Manager{
		cfg:      cfg,
		client:   client,
		log:      logger,
		mappings: cfg.RepositoryConfig.ExplicitMappings,
	}, nil
}

// ValidateConfiguration checks mapping definitions and returns any issues.
func (m *Manager) ValidateConfiguration() []string {
	var issues []string
	for i, mp := range m.mappings {
		if mp.CostCenter == "" {
			issues = append(issues, fmt.Sprintf("mapping %d: missing cost_center", i+1))
		}
		if mp.PropertyName == "" {
			issues = append(issues, fmt.Sprintf("mapping %d: missing property_name", i+1))
		}
		if len(mp.PropertyValues) == 0 {
			issues = append(issues, fmt.Sprintf("mapping %d: missing property_values", i+1))
		}
	}
	return issues
}

// PrintConfigSummary displays the repository mode configuration.
func (m *Manager) PrintConfigSummary(org string) {
	fmt.Println()
	fmt.Println(strings.Repeat("=", 80))
	fmt.Println("Repository-Based Cost Center Assignment")
	fmt.Println(strings.Repeat("=", 80))
	fmt.Printf("Organization: %s\n", org)
	fmt.Printf("Mappings:     %d\n", len(m.mappings))
	for i, mp := range m.mappings {
		fmt.Printf("\n  Mapping %d:\n", i+1)
		fmt.Printf("    Cost Center:    %s\n", mp.CostCenter)
		fmt.Printf("    Property:       %s\n", mp.PropertyName)
		fmt.Printf("    Values:         %s\n", strings.Join(mp.PropertyValues, ", "))
	}
	fmt.Println(strings.Repeat("=", 80))
}

// Run executes the full repository-based assignment flow.
// mode is "plan" or "apply".  createBudgets enables budget creation for new CCs.
func (m *Manager) Run(org, mode string, createBudgets bool) (*Summary, error) {
	m.log.Info("Starting repository-based cost center assignment",
		"org", org, "mode", mode, "mappings", len(m.mappings))

	// Fetch all repos with custom properties.
	m.log.Info("Fetching repositories with custom properties...", "org", org)
	allRepos, err := m.client.GetOrgReposWithProperties(org, "")
	if err != nil {
		return nil, fmt.Errorf("fetching repos with properties: %w", err)
	}
	if len(allRepos) == 0 {
		m.log.Warn("No repositories found", "org", org)
		return &Summary{TotalRepos: 0, MappingsTotal: len(m.mappings)}, nil
	}
	m.log.Info("Repositories found", "org", org, "count", len(allRepos))

	// Preload existing cost centers for efficient lookups.
	activeCCs, err := m.client.GetAllActiveCostCenters()
	if err != nil {
		return nil, fmt.Errorf("fetching active cost centers: %w", err)
	}
	m.log.Info("Existing cost centers loaded", "count", len(activeCCs))

	summary := &Summary{
		TotalRepos:    len(allRepos),
		MappingsTotal: len(m.mappings),
	}

	// Process each mapping.
	for i, mp := range m.mappings {
		m.log.Info("Processing mapping",
			"index", i+1, "total", len(m.mappings),
			"cost_center", mp.CostCenter,
			"property", mp.PropertyName,
			"values", strings.Join(mp.PropertyValues, ","))

		result := m.processMapping(mp, allRepos, activeCCs, mode, createBudgets)
		if result.Success {
			summary.MappingsApplied++
		}
		summary.MappingResults = append(summary.MappingResults, result)
	}

	return summary, nil
}

// processMapping handles a single explicit mapping -- find matching repos,
// ensure CC exists, and assign.
func (m *Manager) processMapping(
	mp config.ExplicitMapping,
	allRepos []github.RepoProperties,
	activeCCs map[string]string,
	mode string,
	createBudgets bool,
) MappingResult {
	result := MappingResult{
		CostCenter:     mp.CostCenter,
		PropertyName:   mp.PropertyName,
		PropertyValues: mp.PropertyValues,
	}

	// Validate mapping fields.
	if mp.CostCenter == "" || mp.PropertyName == "" || len(mp.PropertyValues) == 0 {
		result.Message = "invalid mapping: missing cost_center, property_name, or property_values"
		m.log.Error("Invalid mapping configuration", "cost_center", mp.CostCenter)
		return result
	}

	// Find matching repos.
	matching := findMatchingRepos(allRepos, mp.PropertyName, mp.PropertyValues)
	result.ReposMatched = len(matching)

	if len(matching) == 0 {
		result.Message = "no matching repositories found"
		m.log.Warn("No repos matched",
			"cost_center", mp.CostCenter,
			"property", mp.PropertyName,
			"values", strings.Join(mp.PropertyValues, ","))
		return result
	}

	m.log.Info("Repositories matched",
		"cost_center", mp.CostCenter, "count", len(matching))

	// Plan mode -- just report what would happen.
	if mode == "plan" {
		result.ReposAssigned = len(matching)
		result.Success = true
		result.Message = fmt.Sprintf("would assign %d repositories (plan mode)", len(matching))

		m.log.Info("MODE=plan: would assign repos",
			"cost_center", mp.CostCenter, "count", len(matching))
		for _, r := range matching {
			m.log.Debug("Would assign", "repo", r.RepositoryFullName, "cost_center", mp.CostCenter)
		}
		return result
	}

	// Apply mode -- ensure CC exists.
	ccID, ok := activeCCs[mp.CostCenter]
	if !ok {
		m.log.Info("Cost center does not exist, creating...", "name", mp.CostCenter)
		var err error
		ccID, err = m.client.CreateCostCenterWithPreload(mp.CostCenter, activeCCs)
		if err != nil {
			result.Message = fmt.Sprintf("failed to create cost center: %v", err)
			m.log.Error("Failed to create cost center",
				"name", mp.CostCenter, "error", err)
			return result
		}
		activeCCs[mp.CostCenter] = ccID
		m.log.Info("Created cost center", "name", mp.CostCenter, "id", ccID)

		// Create budgets if enabled.
		if createBudgets && m.cfg.BudgetsEnabled {
			m.createBudgets(ccID, mp.CostCenter)
		}
	} else {
		m.log.Info("Cost center already exists", "name", mp.CostCenter, "id", ccID)
	}

	result.CostCenterID = ccID

	// Extract repo full names.
	repoNames := make([]string, 0, len(matching))
	for _, r := range matching {
		if r.RepositoryFullName != "" {
			repoNames = append(repoNames, r.RepositoryFullName)
		} else {
			m.log.Warn("Repository missing full name, skipping", "name", r.RepositoryName)
		}
	}

	if len(repoNames) == 0 {
		result.Message = "no valid repository names to assign"
		m.log.Error("No valid repo names", "cost_center", mp.CostCenter)
		return result
	}

	// Log repos being assigned.
	for i, name := range repoNames {
		if i < 10 {
			m.log.Info("Assigning repo", "repo", name, "cost_center", mp.CostCenter)
		}
	}
	if len(repoNames) > 10 {
		m.log.Info("...and more", "remaining", len(repoNames)-10)
	}

	// Call API to assign repos.
	if err := m.client.AddRepositoriesToCostCenter(ccID, repoNames); err != nil {
		result.Message = fmt.Sprintf("failed to assign repos: %v", err)
		m.log.Error("Failed to assign repos",
			"cost_center", mp.CostCenter, "error", err)
		return result
	}

	result.ReposAssigned = len(repoNames)
	result.Success = true
	result.Message = fmt.Sprintf("successfully assigned %d/%d repositories",
		len(repoNames), len(matching))
	m.log.Info("Successfully assigned repos",
		"cost_center", mp.CostCenter, "assigned", len(repoNames))

	return result
}

// createBudgets creates configured budgets for a single cost center.
func (m *Manager) createBudgets(ccID, ccName string) {
	m.log.Info("Creating budgets for cost center", "name", ccName)

	for product, pc := range m.cfg.BudgetProducts {
		if !pc.Enabled {
			m.log.Debug("Skipping disabled product budget", "product", product)
			continue
		}

		ok, err := m.client.CreateProductBudget(ccID, ccName, product, pc.Amount)
		if err != nil {
			// If budgets API is unavailable, log and stop trying.
			if _, unavailable := err.(*github.BudgetsAPIUnavailableError); unavailable {
				m.log.Warn("Budgets API unavailable, skipping remaining budgets",
					"error", err)
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

// findMatchingRepos returns repos whose custom properties match the mapping criteria.
func findMatchingRepos(
	repos []github.RepoProperties,
	propertyName string,
	propertyValues []string,
) []github.RepoProperties {
	valueSet := make(map[string]bool, len(propertyValues))
	for _, v := range propertyValues {
		valueSet[v] = true
	}

	var matched []github.RepoProperties
	for _, repo := range repos {
		for _, prop := range repo.Properties {
			if prop.PropertyName != propertyName {
				continue
			}
			// Property value can be a string or []string.
			if matchesValue(prop.Value, valueSet) {
				matched = append(matched, repo)
				break
			}
		}
	}
	return matched
}

// matchesValue checks if a property value (string or []any) matches any value
// in the allowed set.
func matchesValue(val any, allowed map[string]bool) bool {
	switch v := val.(type) {
	case string:
		return allowed[v]
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok && allowed[s] {
				return true
			}
		}
	}
	return false
}
