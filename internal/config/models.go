// Package config provides typed configuration models and loading for gh-cost-center.
package config

// Config is the top-level configuration structure that mirrors the YAML file.
type Config struct {
	GitHub                    GitHubConfig               `yaml:"github"`
	Logging                   LoggingConfig              `yaml:"logging"`
	CostCenters               CostCentersConfig          `yaml:"cost_centers"`
	Teams                     TeamsConfig                `yaml:"teams"`
	Budgets                   BudgetsConfig              `yaml:"budgets"`
	ExportDir                 string                     `yaml:"export_dir"`
	CustomPropertyCostCenters []CustomPropertyCostCenter `yaml:"cost-centers"`
}

// GitHubConfig holds GitHub-related settings.
type GitHubConfig struct {
	Enterprise  string         `yaml:"enterprise"`
	APIBaseURL  string         `yaml:"api_base_url"`
	CostCenters CostCenterMode `yaml:"cost_centers"`
}

// CostCenterMode selects the assignment mode and holds per-mode config.
type CostCenterMode struct {
	Mode             string           `yaml:"mode"` // "users", "teams", or "repository"
	RepositoryConfig RepositoryConfig `yaml:"repository_config"`
}

// RepositoryConfig configures repository-based cost center assignment.
type RepositoryConfig struct {
	ExplicitMappings []ExplicitMapping `yaml:"explicit_mappings"`
}

// ExplicitMapping maps a custom-property value set to a cost center.
type ExplicitMapping struct {
	CostCenter     string   `yaml:"cost_center"`
	PropertyName   string   `yaml:"property_name"`
	PropertyValues []string `yaml:"property_values"`
}

// CustomPropertyCostCenter defines a cost center discovered via GitHub custom
// property filters.  A repository is included in the cost center when it
// satisfies ALL filters (AND logic).  Use separate cost-center entries when
// OR logic across different property combinations is required.
type CustomPropertyCostCenter struct {
	Name    string                 `yaml:"name"`
	Type    string                 `yaml:"type"` // must be "custom-property"
	Filters []CustomPropertyFilter `yaml:"filters"`
}

// CustomPropertyFilter is a single property=value predicate applied during
// repository discovery.
type CustomPropertyFilter struct {
	Property string `yaml:"property"`
	Value    string `yaml:"value"`
}

// LoggingConfig controls log level and output file.
type LoggingConfig struct {
	Level string `yaml:"level"`
	File  string `yaml:"file"`
}

// CostCentersConfig holds PRU-tier cost center settings.
type CostCentersConfig struct {
	// Current keys
	NoPRUsCostCenterID      string   `yaml:"no_prus_cost_center_id"`
	PRUsAllowedCostCenterID string   `yaml:"prus_allowed_cost_center_id"`
	PRUsExceptionUsers      []string `yaml:"prus_exception_users"`

	// Auto-creation
	AutoCreate                bool   `yaml:"auto_create"`
	NoPRUsCostCenterName      string `yaml:"no_prus_cost_center_name"`
	PRUsAllowedCostCenterName string `yaml:"prus_allowed_cost_center_name"`

	// Incremental processing
	EnableIncremental bool `yaml:"enable_incremental"`

	// Backward-compatible keys (old names)
	NoPRUsCostCenterOld      string `yaml:"no_prus_cost_center"`
	PRUsAllowedCostCenterOld string `yaml:"prus_allowed_cost_center"`
	NoPRUNameOld             string `yaml:"no_pru_name"`
	PRUAllowedNameOld        string `yaml:"pru_allowed_name"`
}

// TeamsConfig holds teams-integration settings.
type TeamsConfig struct {
	Enabled       bool              `yaml:"enabled"`
	Scope         string            `yaml:"scope"` // "organization" or "enterprise"
	Mode          string            `yaml:"mode"`  // "auto" or "manual"
	Organizations []string          `yaml:"organizations"`
	AutoCreate    bool              `yaml:"auto_create_cost_centers"`
	TeamMappings  map[string]string `yaml:"team_mappings"`

	// Current key
	RemoveUsersNoLongerInTeams *bool `yaml:"remove_users_no_longer_in_teams"`
	// Backward-compatible key (old name)
	RemoveOrphanedUsersOld *bool `yaml:"remove_orphaned_users"`
}

// BudgetsConfig holds budget auto-creation settings.
type BudgetsConfig struct {
	Enabled  bool                     `yaml:"enabled"`
	Products map[string]ProductBudget `yaml:"products"`
}

// ProductBudget is the budget configuration for a single product.
type ProductBudget struct {
	Amount  int  `yaml:"amount"`
	Enabled bool `yaml:"enabled"`
}
