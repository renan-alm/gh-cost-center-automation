package config

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// helper to write a temp YAML config and return its path.
func writeConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("writing temp config: %v", err)
	}
	return p
}

func logger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

// ---------- Loading the example config ----------

func TestLoad_ExampleConfig(t *testing.T) {
	// Use the repo's config.example.yaml — it must parse without error
	// when the enterprise env var is provided.
	t.Setenv("GITHUB_ENTERPRISE", "test-enterprise")
	// Locate config.example.yaml relative to this test file (internal/config/ → ../../config/).
	examplePath := filepath.Join("..", "..", "config", "config.example.yaml")
	m, err := Load(examplePath, logger())
	if err != nil {
		t.Fatalf("Load config.example.yaml: %v", err)
	}
	if m.Enterprise != "test-enterprise" {
		t.Errorf("enterprise = %q, want %q", m.Enterprise, "test-enterprise")
	}
	if m.CostCenterMode != "users" {
		t.Errorf("cost_center_mode = %q, want %q", m.CostCenterMode, "users")
	}
}

// ---------- Minimal valid config ----------

func TestLoad_MinimalConfig(t *testing.T) {
	yaml := `
github:
  enterprise: "my-ent"
`
	m, err := Load(writeConfig(t, yaml), logger())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m.Enterprise != "my-ent" {
		t.Errorf("enterprise = %q", m.Enterprise)
	}
	if m.APIBaseURL != DefaultAPIBaseURL {
		t.Errorf("api_base_url = %q, want default", m.APIBaseURL)
	}
	if m.CostCenterMode != DefaultCostCenterMode {
		t.Errorf("mode = %q, want %q", m.CostCenterMode, DefaultCostCenterMode)
	}
}

// ---------- Missing enterprise ----------

func TestLoad_MissingEnterprise(t *testing.T) {
	yaml := `
github:
  enterprise: ""
`
	t.Setenv("GITHUB_ENTERPRISE", "")
	_, err := Load(writeConfig(t, yaml), logger())
	if err == nil {
		t.Fatal("expected error for missing enterprise")
	}
}

// ---------- Enterprise placeholder in YAML, real value in env ----------

func TestLoad_EnterprisePlaceholderWithEnvOverride(t *testing.T) {
	yaml := `
github:
  enterprise: "your_enterprise_name"
`
	t.Setenv("GITHUB_ENTERPRISE", "real-ent")
	m, err := Load(writeConfig(t, yaml), logger())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m.Enterprise != "real-ent" {
		t.Errorf("enterprise = %q, want %q", m.Enterprise, "real-ent")
	}
}

// ---------- Env var overrides ----------

func TestLoad_EnvVarOverrides(t *testing.T) {
	yaml := `
github:
  enterprise: "yaml-ent"
  api_base_url: "https://api.github.com"
`
	t.Setenv("GITHUB_ENTERPRISE", "env-ent")
	t.Setenv("GITHUB_API_BASE_URL", "https://api.corp.ghe.com")
	m, err := Load(writeConfig(t, yaml), logger())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m.Enterprise != "env-ent" {
		t.Errorf("enterprise = %q, want env-ent", m.Enterprise)
	}
	if m.APIBaseURL != "https://api.corp.ghe.com" {
		t.Errorf("api_base_url = %q, want env value", m.APIBaseURL)
	}
}

func TestLoad_DotEnvLoadsWhenEnvMissing(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	tmp := t.TempDir()
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(wd)
	})

	if err := os.WriteFile(filepath.Join(tmp, ".env"), []byte("GITHUB_ENTERPRISE=dotenv-ent\n"), 0o644); err != nil {
		t.Fatalf("writing .env: %v", err)
	}

	yaml := `
github:
  enterprise: ""
`
	if err := os.Unsetenv("GITHUB_ENTERPRISE"); err != nil {
		t.Fatalf("unset env: %v", err)
	}
	m, err := Load(writeConfig(t, yaml), logger())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m.Enterprise != "dotenv-ent" {
		t.Errorf("enterprise = %q, want %q", m.Enterprise, "dotenv-ent")
	}
}

func TestLoad_ExistingEnvBeatsDotEnv(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	tmp := t.TempDir()
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(wd)
	})

	if err := os.WriteFile(filepath.Join(tmp, ".env"), []byte("GITHUB_ENTERPRISE=dotenv-ent\n"), 0o644); err != nil {
		t.Fatalf("writing .env: %v", err)
	}

	yaml := `
github:
  enterprise: "yaml-ent"
`
	t.Setenv("GITHUB_ENTERPRISE", "session-ent")
	m, err := Load(writeConfig(t, yaml), logger())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m.Enterprise != "session-ent" {
		t.Errorf("enterprise = %q, want %q", m.Enterprise, "session-ent")
	}
}

// ---------- Backward-compatible fallback chains ----------

func TestLoad_FallbackChains(t *testing.T) {
	yaml := `
github:
  enterprise: "ent"
cost_centers:
  no_prus_cost_center: "OLD-CC-001"
  prus_allowed_cost_center: "OLD-CC-002"
  no_pru_name: "Old No PRU"
  pru_allowed_name: "Old PRU Allowed"
`
	m, err := Load(writeConfig(t, yaml), logger())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m.NoPRUsCostCenterID != "OLD-CC-001" {
		t.Errorf("NoPRUsCostCenterID = %q, want OLD-CC-001", m.NoPRUsCostCenterID)
	}
	if m.PRUsAllowedCostCenterID != "OLD-CC-002" {
		t.Errorf("PRUsAllowedCostCenterID = %q, want OLD-CC-002", m.PRUsAllowedCostCenterID)
	}
	if m.NoPRUsCostCenterName != "Old No PRU" {
		t.Errorf("NoPRUsCostCenterName = %q", m.NoPRUsCostCenterName)
	}
	if m.PRUsAllowedCostCenterName != "Old PRU Allowed" {
		t.Errorf("PRUsAllowedCostCenterName = %q", m.PRUsAllowedCostCenterName)
	}
}

func TestLoad_NewKeysOverrideOldKeys(t *testing.T) {
	yaml := `
github:
  enterprise: "ent"
cost_centers:
  no_prus_cost_center_id: "NEW-CC-001"
  no_prus_cost_center: "OLD-CC-001"
  prus_allowed_cost_center_id: "NEW-CC-002"
  prus_allowed_cost_center: "OLD-CC-002"
`
	m, err := Load(writeConfig(t, yaml), logger())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m.NoPRUsCostCenterID != "NEW-CC-001" {
		t.Errorf("NoPRUsCostCenterID = %q, want NEW-CC-001", m.NoPRUsCostCenterID)
	}
	if m.PRUsAllowedCostCenterID != "NEW-CC-002" {
		t.Errorf("PRUsAllowedCostCenterID = %q, want NEW-CC-002", m.PRUsAllowedCostCenterID)
	}
}

// ---------- Teams backward-compatible key ----------

func TestLoad_TeamsRemoveOrphanedFallback(t *testing.T) {
	yaml := `
github:
  enterprise: "ent"
teams:
  enabled: true
  remove_orphaned_users: false
`
	m, err := Load(writeConfig(t, yaml), logger())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m.TeamsRemoveUsersNoLongerInTeams != false {
		t.Error("expected TeamsRemoveUsersNoLongerInTeams = false (via old key)")
	}
}

func TestLoad_TeamsNewKeyOverridesOld(t *testing.T) {
	yaml := `
github:
  enterprise: "ent"
teams:
  enabled: true
  remove_users_no_longer_in_teams: true
  remove_orphaned_users: false
`
	m, err := Load(writeConfig(t, yaml), logger())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m.TeamsRemoveUsersNoLongerInTeams != true {
		t.Error("expected new key to take precedence")
	}
}

// ---------- API URL validation ----------

func TestValidateAPIURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
		want    string
	}{
		{"standard github", "https://api.github.com", false, "https://api.github.com"},
		{"standard with trailing slash", "https://api.github.com/", false, "https://api.github.com"},
		{"ghe data resident", "https://api.corp.ghe.com", false, "https://api.corp.ghe.com"},
		{"ghe server", "https://github.myco.com/api/v3", false, "https://github.myco.com/api/v3"},
		{"http rejected", "http://api.github.com", true, ""},
		{"empty string", "", true, ""},
		{"bad ghe pattern", "https://corp.ghe.com", true, ""},
		{"custom non-standard", "https://custom.example.com", false, "https://custom.example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := validateAPIURL(tt.url, logger())
			if tt.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got = %q, want %q", got, tt.want)
			}
		})
	}
}

// ---------- Repository config validation ----------

func TestValidateRepositoryConfig(t *testing.T) {
	valid := &RepositoryConfig{
		ExplicitMappings: []ExplicitMapping{
			{CostCenter: "CC1", PropertyName: "team", PropertyValues: []string{"a"}},
		},
	}
	if err := validateRepositoryConfig(valid); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	missing := &RepositoryConfig{
		ExplicitMappings: []ExplicitMapping{
			{CostCenter: "", PropertyName: "team", PropertyValues: []string{"a"}},
		},
	}
	if err := validateRepositoryConfig(missing); err == nil {
		t.Fatal("expected error for missing cost_center")
	}

	noValues := &RepositoryConfig{
		ExplicitMappings: []ExplicitMapping{
			{CostCenter: "CC1", PropertyName: "team", PropertyValues: []string{}},
		},
	}
	if err := validateRepositoryConfig(noValues); err == nil {
		t.Fatal("expected error for empty property_values")
	}
}

// ---------- Custom-property cost centers ----------

func TestLoad_CustomPropertyCostCenters(t *testing.T) {
	yaml := `
github:
  enterprise: "ent"
cost-centers:
  - name: "Backend Engineering"
    type: "custom-property"
    filters:
      - property: "team"
        value: "backend"
      - property: "cost-center-id"
        value: "CC-1234"
  - name: "Frontend Engineering"
    type: "custom-property"
    filters:
      - property: "team"
        value: "frontend"
`
	m, err := Load(writeConfig(t, yaml), logger())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(m.CustomPropertyCostCenters) != 2 {
		t.Fatalf("expected 2 custom-property cost centers, got %d", len(m.CustomPropertyCostCenters))
	}

	backend := m.CustomPropertyCostCenters[0]
	if backend.Name != "Backend Engineering" {
		t.Errorf("name = %q", backend.Name)
	}
	if backend.Type != "custom-property" {
		t.Errorf("type = %q", backend.Type)
	}
	if len(backend.Filters) != 2 {
		t.Fatalf("expected 2 filters, got %d", len(backend.Filters))
	}
	if backend.Filters[0].Property != "team" || backend.Filters[0].Value != "backend" {
		t.Errorf("filter[0] = {%q, %q}", backend.Filters[0].Property, backend.Filters[0].Value)
	}
	if backend.Filters[1].Property != "cost-center-id" || backend.Filters[1].Value != "CC-1234" {
		t.Errorf("filter[1] = {%q, %q}", backend.Filters[1].Property, backend.Filters[1].Value)
	}
}

func TestLoad_CustomPropertyCostCenters_InvalidType(t *testing.T) {
	yaml := `
github:
  enterprise: "ent"
cost-centers:
  - name: "Backend"
    type: "teams"
    filters:
      - property: "team"
        value: "backend"
`
	_, err := Load(writeConfig(t, yaml), logger())
	if err == nil {
		t.Fatal("expected error for unsupported type")
	}
}

func TestLoad_CustomPropertyCostCenters_MissingName(t *testing.T) {
	yaml := `
github:
  enterprise: "ent"
cost-centers:
  - name: ""
    type: "custom-property"
    filters:
      - property: "team"
        value: "backend"
`
	_, err := Load(writeConfig(t, yaml), logger())
	if err == nil {
		t.Fatal("expected error for missing name")
	}
}

func TestLoad_CustomPropertyCostCenters_NoFilters(t *testing.T) {
	yaml := `
github:
  enterprise: "ent"
cost-centers:
  - name: "Backend"
    type: "custom-property"
    filters: []
`
	_, err := Load(writeConfig(t, yaml), logger())
	if err == nil {
		t.Fatal("expected error for empty filters")
	}
}

func TestLoad_CustomPropertyCostCenters_DuplicateName(t *testing.T) {
	yaml := `
github:
  enterprise: "ent"
cost-centers:
  - name: "Backend"
    type: "custom-property"
    filters:
      - property: "team"
        value: "backend"
  - name: "Backend"
    type: "custom-property"
    filters:
      - property: "env"
        value: "prod"
`
	_, err := Load(writeConfig(t, yaml), logger())
	if err == nil {
		t.Fatal("expected error for duplicate name")
	}
}

func TestLoad_CustomPropertyAndRepositoryModeCoexist(t *testing.T) {
	yaml := `
github:
  enterprise: "ent"
  cost_centers:
    mode: "repository"
    repository_config:
      explicit_mappings:
        - cost_center: "Platform"
          property_name: "team"
          property_values:
            - "platform"
cost-centers:
  - name: "Backend"
    type: "custom-property"
    filters:
      - property: "team"
        value: "backend"
`
	m, err := Load(writeConfig(t, yaml), logger())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m.RepositoryConfig == nil {
		t.Error("expected RepositoryConfig to be set")
	}
	if len(m.CustomPropertyCostCenters) != 1 {
		t.Errorf("expected 1 custom-property cost center, got %d", len(m.CustomPropertyCostCenters))
	}
}

func TestValidateCustomPropertyCostCenters_Valid(t *testing.T) {
	entries := []CustomPropertyCostCenter{
		{
			Name: "Backend",
			Type: "custom-property",
			Filters: []CustomPropertyFilter{
				{Property: "team", Value: "backend"},
				{Property: "env", Value: "prod"},
			},
		},
	}
	if err := validateCustomPropertyCostCenters(entries); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateCustomPropertyCostCenters_MissingFilterProperty(t *testing.T) {
	entries := []CustomPropertyCostCenter{
		{
			Name: "Backend",
			Type: "custom-property",
			Filters: []CustomPropertyFilter{
				{Property: "", Value: "backend"},
			},
		},
	}
	if err := validateCustomPropertyCostCenters(entries); err == nil {
		t.Fatal("expected error for missing filter property")
	}
}

func TestValidateCustomPropertyCostCenters_MissingFilterValue(t *testing.T) {
	entries := []CustomPropertyCostCenter{
		{
			Name: "Backend",
			Type: "custom-property",
			Filters: []CustomPropertyFilter{
				{Property: "team", Value: ""},
			},
		},
	}
	if err := validateCustomPropertyCostCenters(entries); err == nil {
		t.Fatal("expected error for missing filter value")
	}
}

// ---------- Repository mode ----------

func TestLoad_RepositoryMode(t *testing.T) {
	yaml := `
github:
  enterprise: "ent"
  cost_centers:
    mode: "repository"
    repository_config:
      explicit_mappings:
        - cost_center: "Platform"
          property_name: "team"
          property_values:
            - "platform"
            - "infra"
`
	m, err := Load(writeConfig(t, yaml), logger())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m.CostCenterMode != "repository" {
		t.Errorf("mode = %q", m.CostCenterMode)
	}
	if m.RepositoryConfig == nil {
		t.Fatal("RepositoryConfig is nil")
	}
	if len(m.RepositoryConfig.ExplicitMappings) != 1 {
		t.Fatalf("expected 1 mapping, got %d", len(m.RepositoryConfig.ExplicitMappings))
	}
	if m.RepositoryConfig.ExplicitMappings[0].CostCenter != "Platform" {
		t.Error("wrong cost center")
	}
}

// ---------- Timestamp save/load round trip ----------

func TestTimestamp_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	yaml := `
github:
  enterprise: "ent"
export_dir: "` + dir + `"
`
	m, err := Load(writeConfig(t, yaml), logger())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ts := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	if err := m.SaveLastRunTimestamp(&ts); err != nil {
		t.Fatalf("SaveLastRunTimestamp: %v", err)
	}
	got, err := m.LoadLastRunTimestamp()
	if err != nil {
		t.Fatalf("LoadLastRunTimestamp: %v", err)
	}
	if got == nil {
		t.Fatal("got nil timestamp")
	}
	if !got.Equal(ts) {
		t.Errorf("timestamp = %v, want %v", got, ts)
	}
}

func TestTimestamp_NoFile(t *testing.T) {
	dir := t.TempDir()
	yaml := `
github:
  enterprise: "ent"
export_dir: "` + dir + `"
`
	m, err := Load(writeConfig(t, yaml), logger())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got, err := m.LoadLastRunTimestamp()
	if err != nil {
		t.Fatalf("LoadLastRunTimestamp: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

// ---------- Placeholder warnings ----------

func TestCheckConfigWarnings_NoAutoCreate(t *testing.T) {
	yaml := `
github:
  enterprise: "ent"
cost_centers:
  no_prus_cost_center_id: "REPLACE_WITH_NO_PRUS_COST_CENTER_ID"
`
	m, err := Load(writeConfig(t, yaml), logger())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Should not panic; warnings go to log.
	m.CheckConfigWarnings()
}

func TestCheckConfigWarnings_AutoCreateSkips(t *testing.T) {
	yaml := `
github:
  enterprise: "ent"
cost_centers:
  auto_create: true
  no_prus_cost_center_id: "REPLACE_WITH_NO_PRUS_COST_CENTER_ID"
`
	m, err := Load(writeConfig(t, yaml), logger())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// With auto_create=true, placeholders should be silently accepted.
	m.CheckConfigWarnings()
}

// ---------- Summary ----------

func TestSummary_ContainsExpectedKeys(t *testing.T) {
	yaml := `
github:
  enterprise: "ent"
`
	m, err := Load(writeConfig(t, yaml), logger())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	s := m.Summary()
	expectedKeys := []string{
		"enterprise",
		"api_base_url",
		"cost_center_mode",
		"no_prus_cost_center_id",
		"prus_allowed_cost_center_id",
		"auto_create",
		"teams_enabled",
		"budgets_enabled",
		"log_level",
		"export_dir",
		"prus_exception_users_count",
	}
	for _, k := range expectedKeys {
		if _, ok := s[k]; !ok {
			t.Errorf("Summary missing key %q", k)
		}
	}
}

// ---------- Config file not found defaults ----------

func TestLoad_FileNotFound(t *testing.T) {
	t.Setenv("GITHUB_ENTERPRISE", "env-ent")
	m, err := Load("/nonexistent/config.yaml", logger())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m.Enterprise != "env-ent" {
		t.Errorf("enterprise = %q", m.Enterprise)
	}
}

// ---------- EnableAutoCreation ----------

func TestEnableAutoCreation(t *testing.T) {
	yaml := `
github:
  enterprise: "ent"
cost_centers:
  auto_create: false
`
	m, err := Load(writeConfig(t, yaml), logger())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m.AutoCreate {
		t.Fatal("expected auto_create=false initially")
	}
	m.EnableAutoCreation()
	if !m.AutoCreate {
		t.Fatal("expected auto_create=true after EnableAutoCreation()")
	}
}

// ---------- Budgets defaults ----------

func TestLoad_BudgetDefaults(t *testing.T) {
	yaml := `
github:
  enterprise: "ent"
`
	m, err := Load(writeConfig(t, yaml), logger())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(m.BudgetProducts) != 2 {
		t.Errorf("expected 2 default budget products, got %d", len(m.BudgetProducts))
	}
	if m.BudgetProducts["copilot"].Amount != 100 {
		t.Errorf("copilot amount = %d", m.BudgetProducts["copilot"].Amount)
	}
}

// ---------- Timestamp file JSON structure ----------

func TestTimestamp_JSONFormat(t *testing.T) {
	dir := t.TempDir()
	yaml := `
github:
  enterprise: "ent"
export_dir: "` + dir + `"
`
	m, err := Load(writeConfig(t, yaml), logger())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ts := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := m.SaveLastRunTimestamp(&ts); err != nil {
		t.Fatalf("SaveLastRunTimestamp: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, timestampFileName))
	if err != nil {
		t.Fatalf("reading timestamp file: %v", err)
	}
	var td timestampData
	if err := json.Unmarshal(data, &td); err != nil {
		t.Fatalf("unmarshalling: %v", err)
	}
	if td.LastRun != "2025-01-01T00:00:00Z" {
		t.Errorf("last_run = %q", td.LastRun)
	}
	if td.SavedAt == "" {
		t.Error("saved_at is empty")
	}
}

// ---------- Helper tests ----------

func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("", "", "c"); got != "c" {
		t.Errorf("got %q", got)
	}
	if got := firstNonEmpty("a", "b"); got != "a" {
		t.Errorf("got %q", got)
	}
	if got := firstNonEmpty(""); got != "" {
		t.Errorf("got %q", got)
	}
}

func TestBoolPtrDefault(t *testing.T) {
	tr := true
	fa := false
	if got := boolPtrDefault(&tr, false); got != true {
		t.Error("expected true")
	}
	if got := boolPtrDefault(&fa, true); got != false {
		t.Error("expected false")
	}
	if got := boolPtrDefault(nil, true); got != true {
		t.Error("expected default true")
	}
}
