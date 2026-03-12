// Package config provides typed configuration models and loading for gh-cost-center.
package config

// Config is the top-level configuration structure that mirrors the YAML file.
type Config struct {
	GitHub     GitHubConfig     `yaml:"github"`
	CostCenter CostCenterConfig `yaml:"cost_center"`
	Budgets    BudgetsConfig    `yaml:"budgets"`
	Logging    LoggingConfig    `yaml:"logging"`
	ExportDir  string           `yaml:"export_dir"`
}

// GitHubConfig holds GitHub-related settings.
type GitHubConfig struct {
	Enterprise    string   `yaml:"enterprise"`
	APIBaseURL    string   `yaml:"api_base_url"`
	Organizations []string `yaml:"organizations"`
	// Concurrency controls the maximum number of concurrent GitHub API requests.
	// Defaults to DefaultConcurrency when zero or negative.
	Concurrency int `yaml:"concurrency"`
}

// CostCenterConfig holds the mode selector and per-mode settings.
type CostCenterConfig struct {
	Mode       string           `yaml:"mode"` // "users", "teams", "repos", or "custom-prop"
	Users      UsersConfig      `yaml:"users"`
	Teams      TeamsConfig      `yaml:"teams"`
	Repos      ReposConfig      `yaml:"repos"`
	CustomProp CustomPropConfig `yaml:"custom_prop"`
}

// UsersConfig holds PRU-based cost center settings.
type UsersConfig struct {
	NoPRUsCostCenterID        string   `yaml:"no_prus_cost_center_id"`
	PRUsAllowedCostCenterID   string   `yaml:"prus_allowed_cost_center_id"`
	ExceptionUsers            []string `yaml:"exception_users"`
	AutoCreate                bool     `yaml:"auto_create"`
	NoPRUsCostCenterName      string   `yaml:"no_prus_cost_center_name"`
	PRUsAllowedCostCenterName string   `yaml:"prus_allowed_cost_center_name"`
	EnableIncremental         bool     `yaml:"enable_incremental"`
}

// TeamsConfig holds teams-based cost center settings.
type TeamsConfig struct {
	Scope                string            `yaml:"scope"`    // "organization" or "enterprise"
	Strategy             string            `yaml:"strategy"` // "auto" or "manual"
	AutoCreate           bool              `yaml:"auto_create"`
	RemoveUnmatchedUsers bool              `yaml:"remove_unmatched_users"`
	Mappings             map[string]string `yaml:"mappings"` // "org/team-slug" -> "cost-center-name"
}

// ReposConfig holds repository-based (explicit OR-mapping) cost center settings.
type ReposConfig struct {
	Mappings []ExplicitMapping `yaml:"mappings"`
}

// ExplicitMapping maps a custom-property value set to a cost center.
type ExplicitMapping struct {
	CostCenter     string   `yaml:"cost_center"`
	PropertyName   string   `yaml:"property_name"`
	PropertyValues []string `yaml:"property_values"`
}

// CustomPropConfig holds AND-filter custom-property cost center definitions.
type CustomPropConfig struct {
	CostCenters []CustomPropCostCenter `yaml:"cost_centers"`
}

// CustomPropCostCenter defines a cost center discovered via GitHub custom
// property filters.  A repository is included when it satisfies ALL filters
// (AND logic).  Use separate entries for OR logic across different property
// combinations.
type CustomPropCostCenter struct {
	Name    string                 `yaml:"name"`
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
