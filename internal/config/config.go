package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

// Default values.
const (
	DefaultCostCenterMode    = "users"
	DefaultTeamsStrategy     = "auto"
	DefaultTeamsScope        = "enterprise"
	DefaultLogLevel          = "INFO"
	DefaultExportDir         = "exports"
	DefaultNoPRUsCCID        = "CC-001-NO-PRUS"
	DefaultPRUsAllowedCCID   = "CC-002-PRUS-ALLOWED"
	DefaultNoPRUsCCName      = "00 - No PRU overages"
	DefaultPRUsAllowedCCName = "01 - PRU overages allowed"
	DefaultAPIBaseURL        = "https://api.github.com"

	// DefaultConcurrency is the default number of concurrent GitHub API workers.
	DefaultConcurrency = 5

	timestampFileName = ".last_run_timestamp"
)

// Valid mode values.
var validModes = map[string]bool{
	"users":       true,
	"teams":       true,
	"repos":       true,
	"custom-prop": true,
}

// Placeholder values that indicate the config has not been customised.
var placeholderEnterpriseValues = map[string]bool{
	"":                             true,
	"REPLACE_WITH_ENTERPRISE_SLUG": true,
	"your_enterprise_name":         true,
}

// Manager loads, validates, and exposes configuration.
type Manager struct {
	cfg  Config
	path string
	log  *slog.Logger

	// Resolved values after applying env overrides and defaults.
	Enterprise    string
	APIBaseURL    string
	Organizations []string

	// Cost center mode.
	CostCenterMode string

	// Users (PRU) mode fields.
	NoPRUsCostCenterID        string
	PRUsAllowedCostCenterID   string
	PRUsExceptionUsers        []string
	AutoCreate                bool
	NoPRUsCostCenterName      string
	PRUsAllowedCostCenterName string
	EnableIncremental         bool

	// Teams mode fields.
	TeamsScope                string
	TeamsStrategy             string
	TeamsAutoCreate           bool
	TeamsRemoveUnmatchedUsers bool
	TeamsMappings             map[string]string

	// Repos mode fields.
	ReposMappings []ExplicitMapping

	// Custom-prop mode fields.
	CustomPropCostCenters []CustomPropCostCenter

	// Budgets.
	BudgetsEnabled bool
	BudgetProducts map[string]ProductBudget

	// Logging & export.
	ExportDir string
	LogLevel  string
	LogFile   string

	// Concurrency limits the number of parallel GitHub API workers.
	Concurrency int

	// Token from --token flag.
	Token string

	timestampFile string
}

// Load reads the YAML config at path, applies env-var overrides, and validates.
func Load(path string, logger *slog.Logger) (*Manager, error) {
	if logger == nil {
		logger = slog.Default()
	}

	loadDotEnv(path, logger)

	m := &Manager{
		path: path,
		log:  logger,
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			logger.Warn("Config file not found, using defaults", "path", path)
		} else {
			return nil, fmt.Errorf("reading config file: %w", err)
		}
	} else {
		if err := yaml.Unmarshal(data, &m.cfg); err != nil {
			return nil, fmt.Errorf("parsing config YAML: %w", err)
		}
	}

	if err := m.resolve(); err != nil {
		return nil, err
	}

	return m, nil
}

// loadDotEnv loads .env files if present, without overriding already-exported
// environment variables.
func loadDotEnv(configPath string, logger *slog.Logger) {
	tryLoad := func(envPath string) bool {
		if envPath == "" {
			return false
		}
		if _, err := os.Stat(envPath); err != nil {
			return false
		}
		if err := godotenv.Load(envPath); err != nil {
			logger.Error("Found .env but failed to load", "path", envPath, "error", err)
			return false
		}
		logger.Debug("Loaded environment file", "path", envPath)
		return true
	}

	if tryLoad(".env") {
		return
	}
	configDir := filepath.Dir(configPath)
	if tryLoad(filepath.Join(configDir, ".env")) {
		return
	}
	_ = tryLoad(filepath.Join(configDir, "..", ".env"))
}

// Raw returns the underlying parsed Config struct.
func (m *Manager) Raw() *Config {
	return &m.cfg
}

// resolve applies env-var overrides, defaults, and validation.
func (m *Manager) resolve() error {
	// --- Enterprise ---
	m.Enterprise = envOrFallback("GITHUB_ENTERPRISE", m.cfg.GitHub.Enterprise)
	if placeholderEnterpriseValues[m.Enterprise] {
		if v := os.Getenv("GITHUB_ENTERPRISE"); v != "" && !placeholderEnterpriseValues[v] {
			m.Enterprise = v
		} else {
			return fmt.Errorf("github enterprise must be configured (set env GITHUB_ENTERPRISE or update config github.enterprise)")
		}
	}

	// --- API base URL ---
	rawURL := envOrFallback("GITHUB_API_BASE_URL", m.cfg.GitHub.APIBaseURL)
	if rawURL == "" {
		rawURL = DefaultAPIBaseURL
	}
	apiURL, err := validateAPIURL(rawURL, m.log)
	if err != nil {
		return err
	}
	m.APIBaseURL = apiURL

	// --- Organizations ---
	m.Organizations = m.cfg.GitHub.Organizations
	if m.Organizations == nil {
		m.Organizations = []string{}
	}

	// --- Concurrency ---
	m.Concurrency = m.cfg.GitHub.Concurrency
	if m.Concurrency <= 0 {
		m.Concurrency = DefaultConcurrency
	}

	// --- Cost center mode ---
	m.CostCenterMode = defaultString(m.cfg.CostCenter.Mode, DefaultCostCenterMode)
	if !validModes[m.CostCenterMode] {
		return fmt.Errorf("invalid cost_center.mode %q: must be one of: users, teams, repos, custom-prop", m.CostCenterMode)
	}

	// --- Validate and resolve per-mode settings ---
	switch m.CostCenterMode {
	case "users":
		if err := m.resolveUsersMode(); err != nil {
			return err
		}
	case "teams":
		if err := m.resolveTeamsMode(); err != nil {
			return err
		}
	case "repos":
		if err := m.resolveReposMode(); err != nil {
			return err
		}
	case "custom-prop":
		if err := m.resolveCustomPropMode(); err != nil {
			return err
		}
	}

	// --- Budgets ---
	b := m.cfg.Budgets
	m.BudgetsEnabled = b.Enabled
	m.BudgetProducts = b.Products
	if m.BudgetProducts == nil {
		m.BudgetProducts = map[string]ProductBudget{
			"copilot": {Amount: 100, Enabled: true},
			"actions": {Amount: 125, Enabled: true},
		}
	}

	// --- Logging ---
	m.LogLevel = defaultString(m.cfg.Logging.Level, DefaultLogLevel)
	m.LogFile = m.cfg.Logging.File

	// --- Export ---
	m.ExportDir = defaultString(m.cfg.ExportDir, DefaultExportDir)
	m.timestampFile = filepath.Join(m.ExportDir, timestampFileName)

	return nil
}

// resolveUsersMode resolves PRU-based (users) mode settings.
func (m *Manager) resolveUsersMode() error {
	u := m.cfg.CostCenter.Users

	m.NoPRUsCostCenterID = defaultString(u.NoPRUsCostCenterID, DefaultNoPRUsCCID)
	m.PRUsAllowedCostCenterID = defaultString(u.PRUsAllowedCostCenterID, DefaultPRUsAllowedCCID)
	m.NoPRUsCostCenterName = defaultString(u.NoPRUsCostCenterName, DefaultNoPRUsCCName)
	m.PRUsAllowedCostCenterName = defaultString(u.PRUsAllowedCostCenterName, DefaultPRUsAllowedCCName)

	m.PRUsExceptionUsers = u.ExceptionUsers
	if m.PRUsExceptionUsers == nil {
		m.PRUsExceptionUsers = []string{}
	}

	m.AutoCreate = u.AutoCreate
	m.EnableIncremental = u.EnableIncremental

	m.log.Info("Users (PRU) mode enabled",
		"exception_users", len(m.PRUsExceptionUsers),
		"auto_create", m.AutoCreate)
	return nil
}

// resolveTeamsMode resolves teams-based mode settings.
func (m *Manager) resolveTeamsMode() error {
	t := m.cfg.CostCenter.Teams

	m.TeamsScope = defaultString(t.Scope, DefaultTeamsScope)
	m.TeamsStrategy = defaultString(t.Strategy, DefaultTeamsStrategy)
	m.TeamsAutoCreate = t.AutoCreate
	m.TeamsRemoveUnmatchedUsers = t.RemoveUnmatchedUsers

	m.TeamsMappings = t.Mappings
	if m.TeamsMappings == nil {
		m.TeamsMappings = map[string]string{}
	}

	// Validate: organization scope requires organizations
	if m.TeamsScope == "organization" && len(m.Organizations) == 0 {
		return fmt.Errorf("teams mode with scope 'organization' requires github.organizations to be configured")
	}

	if m.TeamsStrategy != "auto" && m.TeamsStrategy != "manual" {
		return fmt.Errorf("invalid cost_center.teams.strategy %q: must be 'auto' or 'manual'", m.TeamsStrategy)
	}

	// Warn about mapping values that don't look like UUIDs when auto-create
	// is disabled. These will be resolved by name at runtime, but a mismatch
	// will cause a failure.
	if !m.TeamsAutoCreate && m.TeamsStrategy == "manual" {
		for teamKey, ccValue := range m.TeamsMappings {
			if !looksLikeUUID(ccValue) {
				m.log.Warn("Mapping value is not a UUID — it will be resolved by name against existing cost centers at runtime",
					"mapping", teamKey, "value", ccValue,
					"hint", "if this is a cost center name, ensure it matches exactly as shown in enterprise billing settings")
			}
		}
	}

	m.log.Info("Teams mode enabled",
		"scope", m.TeamsScope,
		"strategy", m.TeamsStrategy,
		"auto_create", m.TeamsAutoCreate)
	return nil
}

// resolveReposMode resolves repository (explicit mapping) mode settings.
func (m *Manager) resolveReposMode() error {
	if len(m.Organizations) == 0 {
		return fmt.Errorf("repos mode requires github.organizations to be configured")
	}

	r := m.cfg.CostCenter.Repos
	if len(r.Mappings) == 0 {
		return fmt.Errorf("repos mode requires at least one mapping in cost_center.repos.mappings")
	}

	if err := validateExplicitMappings(r.Mappings); err != nil {
		return err
	}

	m.ReposMappings = r.Mappings
	m.log.Info("Repos mode enabled", "mappings", len(r.Mappings))
	return nil
}

// resolveCustomPropMode resolves custom-property mode settings.
func (m *Manager) resolveCustomPropMode() error {
	if len(m.Organizations) == 0 {
		return fmt.Errorf("custom-prop mode requires github.organizations to be configured")
	}

	cp := m.cfg.CostCenter.CustomProp
	if len(cp.CostCenters) == 0 {
		return fmt.Errorf("custom-prop mode requires at least one entry in cost_center.custom_prop.cost_centers")
	}

	if err := validateCustomPropCostCenters(cp.CostCenters); err != nil {
		return err
	}

	m.CustomPropCostCenters = cp.CostCenters
	m.log.Info("Custom-prop mode enabled", "cost_centers", len(cp.CostCenters))
	return nil
}

// EnableAutoCreation turns on auto-creation mode at runtime (--create-cost-centers).
func (m *Manager) EnableAutoCreation() {
	m.AutoCreate = true
}

// CheckConfigWarnings logs warnings for the users (PRU) mode.
func (m *Manager) CheckConfigWarnings() {
	if m.CostCenterMode != "users" {
		return
	}
	if m.AutoCreate {
		return
	}
	if len(m.PRUsExceptionUsers) == 0 {
		m.log.Info("No PRUs exception users configured — all users will be assigned to the default no_prus cost center")
	}
}

// timestampData represents the JSON stored in the last-run timestamp file.
type timestampData struct {
	LastRun string `json:"last_run"`
	SavedAt string `json:"saved_at"`
}

// SaveLastRunTimestamp persists the given timestamp (or now) to the export dir.
func (m *Manager) SaveLastRunTimestamp(t *time.Time) error {
	now := time.Now().UTC()
	if t == nil {
		t = &now
	}

	dir := filepath.Dir(m.timestampFile)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating export directory: %w", err)
	}

	td := timestampData{
		LastRun: t.UTC().Format(time.RFC3339),
		SavedAt: now.Format(time.RFC3339),
	}

	data, err := json.MarshalIndent(td, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling timestamp: %w", err)
	}

	if err := os.WriteFile(m.timestampFile, data, 0o644); err != nil {
		return fmt.Errorf("writing timestamp file: %w", err)
	}

	m.log.Info("Saved last run timestamp", "timestamp", td.LastRun)
	return nil
}

// LoadLastRunTimestamp reads the last-run timestamp from the export dir.
// Returns nil if no previous timestamp exists.
func (m *Manager) LoadLastRunTimestamp() (*time.Time, error) {
	data, err := os.ReadFile(m.timestampFile)
	if err != nil {
		if os.IsNotExist(err) {
			m.log.Info("No previous run timestamp found — will process all users")
			return nil, nil
		}
		return nil, fmt.Errorf("reading timestamp file: %w", err)
	}

	var td timestampData
	if err := json.Unmarshal(data, &td); err != nil {
		return nil, fmt.Errorf("parsing timestamp file: %w", err)
	}

	if td.LastRun == "" {
		m.log.Warn("Invalid timestamp file format")
		return nil, nil
	}

	t, err := time.Parse(time.RFC3339, td.LastRun)
	if err != nil {
		return nil, fmt.Errorf("parsing timestamp value: %w", err)
	}

	m.log.Info("Loaded last run timestamp", "timestamp", td.LastRun)
	return &t, nil
}

// Summary returns a human-readable map of current configuration for display.
func (m *Manager) Summary() map[string]any {
	s := map[string]any{
		"enterprise":       m.Enterprise,
		"api_base_url":     m.APIBaseURL,
		"organizations":    m.Organizations,
		"cost_center_mode": m.CostCenterMode,
		"budgets_enabled":  m.BudgetsEnabled,
		"log_level":        m.LogLevel,
		"export_dir":       m.ExportDir,
	}

	switch m.CostCenterMode {
	case "users":
		s["no_prus_cost_center_id"] = m.NoPRUsCostCenterID
		s["prus_allowed_cost_center_id"] = m.PRUsAllowedCostCenterID
		s["prus_exception_users_count"] = len(m.PRUsExceptionUsers)
		s["auto_create"] = m.AutoCreate
		s["enable_incremental"] = m.EnableIncremental
		if m.Enterprise != "" {
			s["no_prus_cost_center_url"] = fmt.Sprintf(
				"https://github.com/enterprises/%s/billing/cost_centers/%s",
				m.Enterprise, m.NoPRUsCostCenterID,
			)
			s["prus_allowed_cost_center_url"] = fmt.Sprintf(
				"https://github.com/enterprises/%s/billing/cost_centers/%s",
				m.Enterprise, m.PRUsAllowedCostCenterID,
			)
		}

	case "teams":
		s["teams_scope"] = m.TeamsScope
		s["teams_strategy"] = m.TeamsStrategy
		s["teams_auto_create"] = m.TeamsAutoCreate
		s["teams_remove_unmatched_users"] = m.TeamsRemoveUnmatchedUsers
		s["teams_mappings_count"] = len(m.TeamsMappings)

	case "repos":
		s["repos_mappings_count"] = len(m.ReposMappings)

	case "custom-prop":
		s["custom_prop_cost_centers_count"] = len(m.CustomPropCostCenters)
	}

	return s
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// validateAPIURL validates and normalises a GitHub API base URL.
func validateAPIURL(raw string, log *slog.Logger) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("GitHub API base URL must be a non-empty string")
	}

	raw = strings.TrimRight(raw, "/")

	if !strings.HasPrefix(raw, "https://") {
		return "", fmt.Errorf("GitHub API base URL must use HTTPS: %s", raw)
	}

	switch {
	case raw == DefaultAPIBaseURL:
		log.Info("Using standard GitHub API", "url", raw)

	case strings.Contains(raw, ".ghe.com"):
		u, err := url.Parse(raw)
		if err != nil {
			return "", fmt.Errorf("invalid API URL: %w", err)
		}
		host := u.Hostname()
		if !strings.HasPrefix(host, "api.") || !strings.HasSuffix(host, ".ghe.com") {
			return "", fmt.Errorf(
				"GitHub Enterprise Data Resident API URL should match 'https://api.{subdomain}.ghe.com', got: %s", raw)
		}
		subdomain := host[4 : len(host)-8] // strip "api." and ".ghe.com"
		if subdomain == "" {
			return "", fmt.Errorf("invalid GHE Data Resident URL — missing subdomain: %s", raw)
		}
		log.Info("Using GitHub Enterprise Data Resident API", "subdomain", subdomain, "url", raw)

	case strings.Contains(raw, "/api/v3"):
		log.Info("Using GitHub Enterprise Server API", "url", raw)

	default:
		log.Warn("Using custom GitHub API URL (non-standard pattern)",
			"url", raw,
			"expected", "https://api.github.com | https://api.{subdomain}.ghe.com | https://{hostname}/api/v3")
	}

	return raw, nil
}

// validateExplicitMappings checks that each explicit mapping has the required fields.
func validateExplicitMappings(mappings []ExplicitMapping) error {
	for i, em := range mappings {
		if em.CostCenter == "" {
			return fmt.Errorf("repos.mappings[%d]: missing 'cost_center'", i)
		}
		if em.PropertyName == "" {
			return fmt.Errorf("repos.mappings[%d]: missing 'property_name'", i)
		}
		if len(em.PropertyValues) == 0 {
			return fmt.Errorf("repos.mappings[%d]: missing 'property_values'", i)
		}
	}
	return nil
}

// validateCustomPropCostCenters validates each entry in the custom-prop cost centers list.
func validateCustomPropCostCenters(entries []CustomPropCostCenter) error {
	seen := make(map[string]bool, len(entries))
	for i, cc := range entries {
		if cc.Name == "" {
			return fmt.Errorf("custom_prop.cost_centers[%d]: missing 'name'", i)
		}
		if len(cc.Filters) == 0 {
			return fmt.Errorf("custom_prop.cost_centers[%d] (%q): must have at least one filter", i, cc.Name)
		}
		for j, f := range cc.Filters {
			if f.Property == "" {
				return fmt.Errorf("custom_prop.cost_centers[%d] (%q) filter[%d]: missing 'property'", i, cc.Name, j)
			}
			if f.Value == "" {
				return fmt.Errorf("custom_prop.cost_centers[%d] (%q) filter[%d]: missing 'value'", i, cc.Name, j)
			}
		}
		if seen[cc.Name] {
			return fmt.Errorf("custom_prop.cost_centers: duplicate name %q", cc.Name)
		}
		seen[cc.Name] = true
	}
	return nil
}

// envOrFallback returns the env var value if set, otherwise the YAML fallback.
func envOrFallback(envKey, yamlValue string) string {
	if v := os.Getenv(envKey); v != "" {
		return v
	}
	return yamlValue
}

// defaultString returns val if non-empty, otherwise def.
func defaultString(val, def string) string {
	if val != "" {
		return val
	}
	return def
}

// uuidPattern matches a standard UUID format (lowercase hex).
var uuidPattern = regexp.MustCompile(
	`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`,
)

// looksLikeUUID returns true if the string matches UUID format.
func looksLikeUUID(s string) bool {
	return uuidPattern.MatchString(strings.ToLower(s))
}
