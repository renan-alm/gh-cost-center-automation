package repository

import (
	"log/slog"
	"os"
	"testing"

	"github.com/renan-alm/gh-cost-center/internal/config"
	"github.com/renan-alm/gh-cost-center/internal/github"
)

// newTestManager builds a Manager with test defaults.
func newTestManager(mappings []config.ExplicitMapping) *Manager {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	cfg := &config.Manager{
		RepositoryConfig: &config.RepositoryConfig{
			ExplicitMappings: mappings,
		},
		BudgetsEnabled: false,
	}
	return &Manager{
		cfg:      cfg,
		log:      logger,
		mappings: mappings,
	}
}

// --- NewManager tests ---

func TestNewManager_NilConfig(t *testing.T) {
	cfg := &config.Manager{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	_, err := NewManager(cfg, nil, logger)
	if err == nil {
		t.Fatal("expected error for nil RepositoryConfig")
	}
}

func TestNewManager_NoMappings(t *testing.T) {
	cfg := &config.Manager{
		RepositoryConfig: &config.RepositoryConfig{
			ExplicitMappings: nil,
		},
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	_, err := NewManager(cfg, nil, logger)
	if err == nil {
		t.Fatal("expected error for empty mappings")
	}
}

func TestNewManager_Valid(t *testing.T) {
	cfg := &config.Manager{
		RepositoryConfig: &config.RepositoryConfig{
			ExplicitMappings: []config.ExplicitMapping{
				{CostCenter: "cc1", PropertyName: "team", PropertyValues: []string{"eng"}},
			},
		},
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	mgr, err := NewManager(cfg, nil, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mgr.mappings) != 1 {
		t.Errorf("expected 1 mapping, got %d", len(mgr.mappings))
	}
}

// --- ValidateConfiguration tests ---

func TestValidateConfiguration_AllValid(t *testing.T) {
	mgr := newTestManager([]config.ExplicitMapping{
		{CostCenter: "cc1", PropertyName: "team", PropertyValues: []string{"eng"}},
		{CostCenter: "cc2", PropertyName: "dept", PropertyValues: []string{"sales", "marketing"}},
	})

	issues := mgr.ValidateConfiguration()
	if len(issues) != 0 {
		t.Errorf("expected no issues, got %v", issues)
	}
}

func TestValidateConfiguration_MissingCostCenter(t *testing.T) {
	mgr := newTestManager([]config.ExplicitMapping{
		{CostCenter: "", PropertyName: "team", PropertyValues: []string{"eng"}},
	})

	issues := mgr.ValidateConfiguration()
	if len(issues) != 1 {
		t.Errorf("expected 1 issue, got %d: %v", len(issues), issues)
	}
}

func TestValidateConfiguration_MissingPropertyName(t *testing.T) {
	mgr := newTestManager([]config.ExplicitMapping{
		{CostCenter: "cc1", PropertyName: "", PropertyValues: []string{"eng"}},
	})

	issues := mgr.ValidateConfiguration()
	if len(issues) != 1 {
		t.Errorf("expected 1 issue, got %d: %v", len(issues), issues)
	}
}

func TestValidateConfiguration_MissingPropertyValues(t *testing.T) {
	mgr := newTestManager([]config.ExplicitMapping{
		{CostCenter: "cc1", PropertyName: "team", PropertyValues: nil},
	})

	issues := mgr.ValidateConfiguration()
	if len(issues) != 1 {
		t.Errorf("expected 1 issue, got %d: %v", len(issues), issues)
	}
}

func TestValidateConfiguration_MultipleIssues(t *testing.T) {
	mgr := newTestManager([]config.ExplicitMapping{
		{CostCenter: "", PropertyName: "", PropertyValues: nil},
	})

	issues := mgr.ValidateConfiguration()
	if len(issues) != 3 {
		t.Errorf("expected 3 issues, got %d: %v", len(issues), issues)
	}
}

// --- findMatchingRepos tests ---

func TestFindMatchingRepos_StringValue(t *testing.T) {
	repos := []github.RepoProperties{
		{
			RepositoryName:     "repo1",
			RepositoryFullName: "org/repo1",
			Properties: []github.Property{
				{PropertyName: "team", Value: "engineering"},
			},
		},
		{
			RepositoryName:     "repo2",
			RepositoryFullName: "org/repo2",
			Properties: []github.Property{
				{PropertyName: "team", Value: "sales"},
			},
		},
		{
			RepositoryName:     "repo3",
			RepositoryFullName: "org/repo3",
			Properties: []github.Property{
				{PropertyName: "team", Value: "engineering"},
			},
		},
	}

	matched := findMatchingRepos(repos, "team", []string{"engineering"})
	if len(matched) != 2 {
		t.Errorf("expected 2 matches, got %d", len(matched))
	}
}

func TestFindMatchingRepos_ArrayValue(t *testing.T) {
	repos := []github.RepoProperties{
		{
			RepositoryName:     "repo1",
			RepositoryFullName: "org/repo1",
			Properties: []github.Property{
				{PropertyName: "tags", Value: []any{"go", "backend"}},
			},
		},
		{
			RepositoryName:     "repo2",
			RepositoryFullName: "org/repo2",
			Properties: []github.Property{
				{PropertyName: "tags", Value: []any{"python", "frontend"}},
			},
		},
	}

	matched := findMatchingRepos(repos, "tags", []string{"go"})
	if len(matched) != 1 {
		t.Errorf("expected 1 match, got %d", len(matched))
	}
	if matched[0].RepositoryName != "repo1" {
		t.Errorf("expected repo1, got %s", matched[0].RepositoryName)
	}
}

func TestFindMatchingRepos_MultipleValues(t *testing.T) {
	repos := []github.RepoProperties{
		{
			RepositoryName:     "repo1",
			RepositoryFullName: "org/repo1",
			Properties: []github.Property{
				{PropertyName: "team", Value: "engineering"},
			},
		},
		{
			RepositoryName:     "repo2",
			RepositoryFullName: "org/repo2",
			Properties: []github.Property{
				{PropertyName: "team", Value: "sales"},
			},
		},
		{
			RepositoryName:     "repo3",
			RepositoryFullName: "org/repo3",
			Properties: []github.Property{
				{PropertyName: "team", Value: "devops"},
			},
		},
	}

	matched := findMatchingRepos(repos, "team", []string{"engineering", "devops"})
	if len(matched) != 2 {
		t.Errorf("expected 2 matches, got %d", len(matched))
	}
}

func TestFindMatchingRepos_NoMatch(t *testing.T) {
	repos := []github.RepoProperties{
		{
			RepositoryName:     "repo1",
			RepositoryFullName: "org/repo1",
			Properties: []github.Property{
				{PropertyName: "team", Value: "sales"},
			},
		},
	}

	matched := findMatchingRepos(repos, "team", []string{"engineering"})
	if len(matched) != 0 {
		t.Errorf("expected 0 matches, got %d", len(matched))
	}
}

func TestFindMatchingRepos_DifferentPropertyName(t *testing.T) {
	repos := []github.RepoProperties{
		{
			RepositoryName:     "repo1",
			RepositoryFullName: "org/repo1",
			Properties: []github.Property{
				{PropertyName: "dept", Value: "engineering"},
			},
		},
	}

	matched := findMatchingRepos(repos, "team", []string{"engineering"})
	if len(matched) != 0 {
		t.Errorf("should not match different property name, got %d", len(matched))
	}
}

func TestFindMatchingRepos_NoProperties(t *testing.T) {
	repos := []github.RepoProperties{
		{
			RepositoryName:     "repo1",
			RepositoryFullName: "org/repo1",
			Properties:         nil,
		},
	}

	matched := findMatchingRepos(repos, "team", []string{"engineering"})
	if len(matched) != 0 {
		t.Errorf("expected 0 matches for repo with no properties, got %d", len(matched))
	}
}

// --- matchesValue tests ---

func TestMatchesValue_StringMatch(t *testing.T) {
	allowed := map[string]bool{"eng": true, "devops": true}
	if !matchesValue("eng", allowed) {
		t.Error("expected true for matching string")
	}
}

func TestMatchesValue_StringNoMatch(t *testing.T) {
	allowed := map[string]bool{"eng": true}
	if matchesValue("sales", allowed) {
		t.Error("expected false for non-matching string")
	}
}

func TestMatchesValue_ArrayMatch(t *testing.T) {
	allowed := map[string]bool{"go": true}
	val := []any{"python", "go", "javascript"}
	if !matchesValue(val, allowed) {
		t.Error("expected true for array containing matching value")
	}
}

func TestMatchesValue_ArrayNoMatch(t *testing.T) {
	allowed := map[string]bool{"rust": true}
	val := []any{"python", "go"}
	if matchesValue(val, allowed) {
		t.Error("expected false for array not containing matching value")
	}
}

func TestMatchesValue_NilValue(t *testing.T) {
	allowed := map[string]bool{"eng": true}
	if matchesValue(nil, allowed) {
		t.Error("expected false for nil value")
	}
}

func TestMatchesValue_IntValue(t *testing.T) {
	allowed := map[string]bool{"eng": true}
	if matchesValue(42, allowed) {
		t.Error("expected false for int value")
	}
}

func TestMatchesValue_EmptyArray(t *testing.T) {
	allowed := map[string]bool{"eng": true}
	val := []any{}
	if matchesValue(val, allowed) {
		t.Error("expected false for empty array")
	}
}

// --- Summary.Print test ---

func TestSummaryPrint(t *testing.T) {
	s := &Summary{
		TotalRepos:      10,
		MappingsTotal:   2,
		MappingsApplied: 1,
		MappingResults: []MappingResult{
			{
				CostCenter:     "cc1",
				PropertyName:   "team",
				PropertyValues: []string{"eng"},
				ReposMatched:   5,
				ReposAssigned:  5,
				Success:        true,
				Message:        "ok",
			},
			{
				CostCenter:     "cc2",
				PropertyName:   "team",
				PropertyValues: []string{"sales"},
				ReposMatched:   0,
				ReposAssigned:  0,
				Success:        false,
				Message:        "no repos matched",
			},
		},
	}
	// Just verify it does not panic.
	s.Print()
}

// --- PrintConfigSummary test ---

func TestPrintConfigSummary(t *testing.T) {
	mgr := newTestManager([]config.ExplicitMapping{
		{CostCenter: "cc1", PropertyName: "team", PropertyValues: []string{"eng"}},
		{CostCenter: "cc2", PropertyName: "dept", PropertyValues: []string{"sales", "marketing"}},
	})

	// Just verify it does not panic.
	mgr.PrintConfigSummary("test-org")
}
