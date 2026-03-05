package repository

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/renan-alm/gh-cost-center/internal/config"
	"github.com/renan-alm/gh-cost-center/internal/github"
)

// CustomPropertyResult records the outcome of processing a single custom-property cost center.
type CustomPropertyResult struct {
	CostCenter    string
	CostCenterID  string
	Filters       []config.CustomPropertyFilter
	ReposMatched  int
	ReposAssigned int
	Success       bool
	Message       string
}

// CustomPropertySummary holds the overall result of a custom-property assignment run.
type CustomPropertySummary struct {
	TotalRepos    int
	TotalCCs      int
	AppliedCCs    int
	Results       []CustomPropertyResult
}

// Print displays the summary to stdout.
func (s *CustomPropertySummary) Print() {
	fmt.Println()
	fmt.Println(strings.Repeat("=", 80))
	fmt.Println("CUSTOM-PROPERTY ASSIGNMENT SUMMARY")
	fmt.Println(strings.Repeat("=", 80))
	fmt.Printf("Total repositories in organization: %d\n", s.TotalRepos)
	fmt.Printf("Cost centers processed: %d / %d\n", s.AppliedCCs, s.TotalCCs)

	for _, r := range s.Results {
		fmt.Println()
		fmt.Printf("Cost Center: %s\n", r.CostCenter)
		fmt.Println("  Filters (AND):")
		for _, f := range r.Filters {
			fmt.Printf("    %s = %q\n", f.Property, f.Value)
		}
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

// CustomPropertyManager discovers repositories using GitHub custom property
// filters and assigns them to cost centers.  Each cost center entry in the
// configuration specifies a set of filters that are combined with AND logic —
// a repository must satisfy every filter to be included in that cost center.
type CustomPropertyManager struct {
	cfg        *config.Manager
	client     *github.Client
	log        *slog.Logger
	costCenters []config.CustomPropertyCostCenter
}

// NewCustomPropertyManager creates a CustomPropertyManager from configuration.
// It returns an error if no custom-property cost centers are configured.
func NewCustomPropertyManager(cfg *config.Manager, client *github.Client, logger *slog.Logger) (*CustomPropertyManager, error) {
	if len(cfg.CustomPropertyCostCenters) == 0 {
		return nil, fmt.Errorf("custom-property mode requires at least one entry in the 'cost-centers' config section")
	}
	return &CustomPropertyManager{
		cfg:        cfg,
		client:     client,
		log:        logger,
		costCenters: cfg.CustomPropertyCostCenters,
	}, nil
}

// ValidateConfiguration checks the custom-property cost center definitions and
// returns a list of human-readable issues found.
func (m *CustomPropertyManager) ValidateConfiguration() []string {
	var issues []string
	seen := make(map[string]bool, len(m.costCenters))
	for i, cc := range m.costCenters {
		if cc.Name == "" {
			issues = append(issues, fmt.Sprintf("cost center %d: missing name", i+1))
		}
		if cc.Type != "custom-property" {
			issues = append(issues, fmt.Sprintf("cost center %d (%q): unsupported type %q", i+1, cc.Name, cc.Type))
		}
		if len(cc.Filters) == 0 {
			issues = append(issues, fmt.Sprintf("cost center %d (%q): no filters defined", i+1, cc.Name))
		}
		for j, f := range cc.Filters {
			if f.Property == "" {
				issues = append(issues, fmt.Sprintf("cost center %d (%q) filter %d: missing property", i+1, cc.Name, j+1))
			}
			if f.Value == "" {
				issues = append(issues, fmt.Sprintf("cost center %d (%q) filter %d: missing value", i+1, cc.Name, j+1))
			}
		}
		if cc.Name != "" && seen[cc.Name] {
			issues = append(issues, fmt.Sprintf("duplicate cost center name %q", cc.Name))
		}
		seen[cc.Name] = true
	}
	return issues
}

// PrintConfigSummary displays the custom-property configuration.
func (m *CustomPropertyManager) PrintConfigSummary(org string) {
	fmt.Println()
	fmt.Println(strings.Repeat("=", 80))
	fmt.Println("Custom-Property Cost Center Assignment")
	fmt.Println(strings.Repeat("=", 80))
	fmt.Printf("Organization:  %s\n", org)
	fmt.Printf("Cost Centers:  %d\n", len(m.costCenters))
	for i, cc := range m.costCenters {
		fmt.Printf("\n  Cost Center %d: %s\n", i+1, cc.Name)
		fmt.Println("    Filters (AND logic — all must match):")
		for _, f := range cc.Filters {
			fmt.Printf("      %s = %q\n", f.Property, f.Value)
		}
	}
	fmt.Println(strings.Repeat("=", 80))
}

// Run executes the full custom-property assignment flow.
// mode is "plan" or "apply".  createBudgets enables budget creation for new CCs.
func (m *CustomPropertyManager) Run(org, mode string, createBudgets bool) (*CustomPropertySummary, error) {
	m.log.Info("Starting custom-property cost center assignment",
		"org", org, "mode", mode, "cost_centers", len(m.costCenters))

	// Fetch all repos with custom properties.
	m.log.Info("Fetching repositories with custom properties...", "org", org)
	allRepos, err := m.client.GetOrgReposWithProperties(org, "")
	if err != nil {
		return nil, fmt.Errorf("fetching repos with properties: %w", err)
	}
	if len(allRepos) == 0 {
		m.log.Warn("No repositories found", "org", org)
		return &CustomPropertySummary{TotalRepos: 0, TotalCCs: len(m.costCenters)}, nil
	}
	m.log.Info("Repositories found", "org", org, "count", len(allRepos))

	// Preload existing cost centers for efficient lookups.
	activeCCs, err := m.client.GetAllActiveCostCenters()
	if err != nil {
		return nil, fmt.Errorf("fetching active cost centers: %w", err)
	}
	m.log.Info("Existing cost centers loaded", "count", len(activeCCs))

	summary := &CustomPropertySummary{
		TotalRepos: len(allRepos),
		TotalCCs:   len(m.costCenters),
	}

	// Process each custom-property cost center.
	for i, cc := range m.costCenters {
		m.log.Info("Processing cost center",
			"index", i+1, "total", len(m.costCenters),
			"name", cc.Name, "filters", len(cc.Filters))

		result := m.processCostCenter(cc, allRepos, activeCCs, mode, createBudgets)
		if result.Success {
			summary.AppliedCCs++
		}
		summary.Results = append(summary.Results, result)
	}

	return summary, nil
}

// processCostCenter handles one custom-property cost center — finds matching
// repos and (in apply mode) ensures the CC exists and assigns the repos.
func (m *CustomPropertyManager) processCostCenter(
	cc config.CustomPropertyCostCenter,
	allRepos []github.RepoProperties,
	activeCCs map[string]string,
	mode string,
	createBudgets bool,
) CustomPropertyResult {
	result := CustomPropertyResult{
		CostCenter: cc.Name,
		Filters:    cc.Filters,
	}

	// Find repos that satisfy all filters (AND logic).
	matching := findReposMatchingAllFilters(allRepos, cc.Filters)
	result.ReposMatched = len(matching)

	if len(matching) == 0 {
		result.Message = "no repositories matched all filters"
		m.log.Warn("No repos matched all filters",
			"cost_center", cc.Name, "filters", len(cc.Filters))
		return result
	}

	m.log.Info("Repositories matched",
		"cost_center", cc.Name, "count", len(matching))

	// Plan mode — report what would happen without making changes.
	if mode == "plan" {
		result.ReposAssigned = len(matching)
		result.Success = true
		result.Message = fmt.Sprintf("would assign %d repositories (plan mode)", len(matching))
		m.log.Info("MODE=plan: would assign repos",
			"cost_center", cc.Name, "count", len(matching))
		for _, r := range matching {
			m.log.Debug("Would assign", "repo", r.RepositoryFullName, "cost_center", cc.Name)
		}
		return result
	}

	// Apply mode — ensure the cost center exists.
	ccID, ok := activeCCs[cc.Name]
	if !ok {
		m.log.Info("Cost center does not exist, creating...", "name", cc.Name)
		var err error
		ccID, err = m.client.CreateCostCenterWithPreload(cc.Name, activeCCs)
		if err != nil {
			result.Message = fmt.Sprintf("failed to create cost center: %v", err)
			m.log.Error("Failed to create cost center", "name", cc.Name, "error", err)
			return result
		}
		activeCCs[cc.Name] = ccID
		m.log.Info("Created cost center", "name", cc.Name, "id", ccID)

		if createBudgets && m.cfg.BudgetsEnabled {
			m.createBudgets(ccID, cc.Name)
		}
	} else {
		m.log.Info("Cost center already exists", "name", cc.Name, "id", ccID)
	}

	result.CostCenterID = ccID

	// Collect repo full names.
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
		m.log.Error("No valid repo names", "cost_center", cc.Name)
		return result
	}

	for i, name := range repoNames {
		if i < 10 {
			m.log.Info("Assigning repo", "repo", name, "cost_center", cc.Name)
		}
	}
	if len(repoNames) > 10 {
		m.log.Info("...and more", "remaining", len(repoNames)-10)
	}

	if err := m.client.AddRepositoriesToCostCenter(ccID, repoNames); err != nil {
		result.Message = fmt.Sprintf("failed to assign repos: %v", err)
		m.log.Error("Failed to assign repos", "cost_center", cc.Name, "error", err)
		return result
	}

	result.ReposAssigned = len(repoNames)
	result.Success = true
	result.Message = fmt.Sprintf("successfully assigned %d/%d repositories",
		len(repoNames), len(matching))
	m.log.Info("Successfully assigned repos",
		"cost_center", cc.Name, "assigned", len(repoNames))

	return result
}

// createBudgets creates configured budgets for a newly-created cost center.
func (m *CustomPropertyManager) createBudgets(ccID, ccName string) {
	m.log.Info("Creating budgets for cost center", "name", ccName)

	for product, pc := range m.cfg.BudgetProducts {
		if !pc.Enabled {
			m.log.Debug("Skipping disabled product budget", "product", product)
			continue
		}

		ok, err := m.client.CreateProductBudget(ccID, ccName, product, pc.Amount)
		if err != nil {
			if _, unavailable := err.(*github.BudgetsAPIUnavailableError); unavailable {
				m.log.Warn("Budgets API unavailable, skipping remaining budgets", "error", err)
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

// findReposMatchingAllFilters returns the repos from repos that satisfy every
// filter in filters (AND logic).
func findReposMatchingAllFilters(
	repos []github.RepoProperties,
	filters []config.CustomPropertyFilter,
) []github.RepoProperties {
	if len(filters) == 0 {
		return nil
	}

	var matched []github.RepoProperties
	for _, repo := range repos {
		if repoMatchesAllFilters(repo, filters) {
			matched = append(matched, repo)
		}
	}
	return matched
}

// repoMatchesAllFilters returns true when the repository satisfies every
// filter (AND logic).
func repoMatchesAllFilters(repo github.RepoProperties, filters []config.CustomPropertyFilter) bool {
	// Build a property lookup map for this repo.
	propMap := make(map[string]any, len(repo.Properties))
	for _, p := range repo.Properties {
		propMap[p.PropertyName] = p.Value
	}

	for _, f := range filters {
		val, exists := propMap[f.Property]
		if !exists {
			return false
		}
		if !matchesValue(val, map[string]bool{f.Value: true}) {
			return false
		}
	}
	return true
}
