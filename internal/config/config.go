package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

// Default values mirroring the Python implementation.
const (
	DefaultCostCenterMode    = "users"
	DefaultTeamsScope        = "enterprise"
	DefaultTeamsMode         = "auto"
	DefaultLogLevel          = "INFO"
	DefaultExportDir         = "exports"
	DefaultNoPRUsCCID        = "CC-001-NO-PRUS"
	DefaultPRUsAllowedCCID   = "CC-002-PRUS-ALLOWED"
	DefaultNoPRUsCCName      = "00 - No PRU overages"
	DefaultPRUsAllowedCCName = "01 - PRU overages allowed"
	DefaultAPIBaseURL        = "https://api.github.com"

	timestampFileName = ".last_run_timestamp"
)

// Placeholder values that indicate the config has not been customised.
var placeholderEnterpriseValues = map[string]bool{
	"":                             true,
	"REPLACE_WITH_ENTERPRISE_SLUG": true,
	"your_enterprise_name":         true,
}

var placeholderCCValues = map[string][]string{
	"NoPRUsCostCenterID":      {"REPLACE_WITH_NO_PRUS_COST_CENTER_ID", DefaultNoPRUsCCID},
	"PRUsAllowedCostCenterID": {"REPLACE_WITH_PRUS_ALLOWED_COST_CENTER_ID", DefaultPRUsAllowedCCID},
}

// Manager loads, validates, and exposes configuration.
type Manager struct {
	cfg  Config
	path string
	log  *slog.Logger

	// Resolved values after applying fallback chains and env overrides.
	Enterprise                      string
	APIBaseURL                      string
	CostCenterMode                  string
	NoPRUsCostCenterID              string
	PRUsAllowedCostCenterID         string
	PRUsExceptionUsers              []string
	AutoCreate                      bool
	NoPRUsCostCenterName            string
	PRUsAllowedCostCenterName       string
	EnableIncremental               bool
	TeamsEnabled                    bool
	TeamsScope                      string
	TeamsMode                       string
	TeamsOrganizations              []string
	TeamsAutoCreate                 bool
	TeamsMappings                   map[string]string
	TeamsRemoveUsersNoLongerInTeams bool
	BudgetsEnabled                  bool
	BudgetProducts                  map[string]ProductBudget
	ExportDir                       string
	LogLevel                        string
	LogFile                         string
	RepositoryConfig                *RepositoryConfig
	CustomPropertyCostCenters       []CustomPropertyCostCenter
	Token                           string // Explicit token from --token flag

	timestampFile string
}

// Load reads the YAML config at path, applies env-var overrides, backward-
// compatible fallback chains, and validates required fields.
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
			// Continue with zero-value Config; defaults are applied below.
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
// environment variables. Precedence remains:
//  1. existing process environment
//  2. .env file values
//  3. config YAML/defaults
func loadDotEnv(configPath string, logger *slog.Logger) {
	tryLoad := func(envPath string) bool {
		if envPath == "" {
			return false
		}
		if _, err := os.Stat(envPath); err != nil {
			return false
		}
		if err := godotenv.Load(envPath); err != nil {
			logger.Warn("Found .env but failed to load", "path", envPath, "error", err)
			return false
		}
		logger.Debug("Loaded environment file", "path", envPath)
		return true
	}

	// Try current working directory first (common local-dev pattern).
	if tryLoad(".env") {
		return
	}

	// Then try alongside config file and one level up (e.g. config/config.yaml -> .env at repo root).
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

// resolve applies env-var overrides, fallbacks, defaults, and validation.
func (m *Manager) resolve() error {
	// --- Enterprise ---
	m.Enterprise = envOrFallback("GITHUB_ENTERPRISE", m.cfg.GitHub.Enterprise)
	if placeholderEnterpriseValues[m.Enterprise] {
		// Re-check env explicitly in case YAML had a placeholder.
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

	// --- Cost center mode ---
	m.CostCenterMode = defaultString(m.cfg.GitHub.CostCenters.Mode, DefaultCostCenterMode)

	// --- Repository config (only when mode is "repository") ---
	if m.CostCenterMode == "repository" {
		rc := m.cfg.GitHub.CostCenters.RepositoryConfig
		if err := validateRepositoryConfig(&rc); err != nil {
			return err
		}
		m.RepositoryConfig = &rc
		m.log.Info("Repository mode enabled", "mappings", len(rc.ExplicitMappings))
	}

	// --- Custom-property cost centers (independent of mode) ---
	if len(m.cfg.CustomPropertyCostCenters) > 0 {
		if err := validateCustomPropertyCostCenters(m.cfg.CustomPropertyCostCenters); err != nil {
			return err
		}
		m.CustomPropertyCostCenters = m.cfg.CustomPropertyCostCenters
		m.log.Info("Custom-property cost centers configured",
			"count", len(m.CustomPropertyCostCenters))
	}

	// --- Cost centers (PRU-tier) with backward-compatible fallback chains ---
	cc := m.cfg.CostCenters

	m.NoPRUsCostCenterID = firstNonEmpty(cc.NoPRUsCostCenterID, cc.NoPRUsCostCenterOld, DefaultNoPRUsCCID)
	m.PRUsAllowedCostCenterID = firstNonEmpty(cc.PRUsAllowedCostCenterID, cc.PRUsAllowedCostCenterOld, DefaultPRUsAllowedCCID)
	m.NoPRUsCostCenterName = firstNonEmpty(cc.NoPRUsCostCenterName, cc.NoPRUNameOld, DefaultNoPRUsCCName)
	m.PRUsAllowedCostCenterName = firstNonEmpty(cc.PRUsAllowedCostCenterName, cc.PRUAllowedNameOld, DefaultPRUsAllowedCCName)

	m.PRUsExceptionUsers = cc.PRUsExceptionUsers
	if m.PRUsExceptionUsers == nil {
		m.PRUsExceptionUsers = []string{}
	}

	m.AutoCreate = cc.AutoCreate
	m.EnableIncremental = cc.EnableIncremental

	// --- Teams ---
	t := m.cfg.Teams
	m.TeamsEnabled = t.Enabled
	m.TeamsScope = defaultString(t.Scope, DefaultTeamsScope)
	m.TeamsMode = defaultString(t.Mode, DefaultTeamsMode)
	m.TeamsOrganizations = t.Organizations
	if m.TeamsOrganizations == nil {
		m.TeamsOrganizations = []string{}
	}
	m.TeamsAutoCreate = t.AutoCreate
	m.TeamsMappings = t.TeamMappings
	if m.TeamsMappings == nil {
		m.TeamsMappings = map[string]string{}
	}
	// Backward-compatible fallback: remove_users_no_longer_in_teams → remove_orphaned_users
	m.TeamsRemoveUsersNoLongerInTeams = boolPtrDefault(t.RemoveUsersNoLongerInTeams, boolPtrDefault(t.RemoveOrphanedUsersOld, true))

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

// EnableAutoCreation turns on auto-creation mode at runtime (--create-cost-centers).
func (m *Manager) EnableAutoCreation() {
	m.AutoCreate = true
}

// CheckConfigWarnings logs warnings for placeholder values still present.
func (m *Manager) CheckConfigWarnings() {
	if m.AutoCreate {
		return
	}
	for field, placeholders := range placeholderCCValues {
		var val string
		switch field {
		case "NoPRUsCostCenterID":
			val = m.NoPRUsCostCenterID
		case "PRUsAllowedCostCenterID":
			val = m.PRUsAllowedCostCenterID
		}
		for _, p := range placeholders {
			if val == p {
				m.log.Warn("Configuration appears to be a placeholder — update config/config.yaml with real cost center IDs before applying",
					"field", field, "value", val)
				break
			}
		}
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
		"enterprise":                  m.Enterprise,
		"api_base_url":                m.APIBaseURL,
		"cost_center_mode":            m.CostCenterMode,
		"no_prus_cost_center_id":      m.NoPRUsCostCenterID,
		"prus_allowed_cost_center_id": m.PRUsAllowedCostCenterID,
		"prus_exception_users_count":  len(m.PRUsExceptionUsers),
		"auto_create":                 m.AutoCreate,
		"enable_incremental":          m.EnableIncremental,
		"teams_enabled":               m.TeamsEnabled,
		"teams_scope":                 m.TeamsScope,
		"teams_mode":                  m.TeamsMode,
		"budgets_enabled":             m.BudgetsEnabled,
		"log_level":                   m.LogLevel,
		"export_dir":                  m.ExportDir,
	}

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

// validateRepositoryConfig checks that each explicit mapping has the required fields.
func validateRepositoryConfig(rc *RepositoryConfig) error {
	for i, em := range rc.ExplicitMappings {
		if em.CostCenter == "" {
			return fmt.Errorf("explicit_mapping[%d]: missing 'cost_center'", i)
		}
		if em.PropertyName == "" {
			return fmt.Errorf("explicit_mapping[%d]: missing 'property_name'", i)
		}
		if len(em.PropertyValues) == 0 {
			return fmt.Errorf("explicit_mapping[%d]: missing 'property_values'", i)
		}
	}
	return nil
}

// validateCustomPropertyCostCenters validates each entry in the cost-centers list.
func validateCustomPropertyCostCenters(entries []CustomPropertyCostCenter) error {
	seen := make(map[string]bool, len(entries))
	for i, cc := range entries {
		if cc.Name == "" {
			return fmt.Errorf("cost-centers[%d]: missing 'name'", i)
		}
		if cc.Type != "custom-property" {
			return fmt.Errorf("cost-centers[%d] (%q): unsupported type %q — only 'custom-property' is supported",
				i, cc.Name, cc.Type)
		}
		if len(cc.Filters) == 0 {
			return fmt.Errorf("cost-centers[%d] (%q): must have at least one filter", i, cc.Name)
		}
		for j, f := range cc.Filters {
			if f.Property == "" {
				return fmt.Errorf("cost-centers[%d] (%q) filter[%d]: missing 'property'", i, cc.Name, j)
			}
			if f.Value == "" {
				return fmt.Errorf("cost-centers[%d] (%q) filter[%d]: missing 'value'", i, cc.Name, j)
			}
		}
		if seen[cc.Name] {
			return fmt.Errorf("cost-centers: duplicate name %q", cc.Name)
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

// firstNonEmpty returns the first non-empty string from the arguments.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// defaultString returns val if non-empty, otherwise def.
func defaultString(val, def string) string {
	if val != "" {
		return val
	}
	return def
}

// boolPtrDefault dereferences a *bool, returning def if the pointer is nil.
func boolPtrDefault(p *bool, def bool) bool {
	if p != nil {
		return *p
	}
	return def
}
