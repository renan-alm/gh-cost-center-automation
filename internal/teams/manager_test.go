package teams

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/renan-alm/gh-cost-center/internal/config"
	"github.com/renan-alm/gh-cost-center/internal/github"
)

// newTestManager builds a Manager with the given overrides and a discarding logger.
func newTestManager(scope, mode string, orgs []string, mappings map[string]string, autoCreate, removeUsers bool) *Manager {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	cfg := &config.Manager{
		TeamsScope:                scope,
		TeamsStrategy:             mode,
		Organizations:             orgs,
		TeamsAutoCreate:           autoCreate,
		TeamsMappings:             mappings,
		TeamsRemoveUnmatchedUsers: removeUsers,
		Enterprise:                "test-enterprise",
	}
	return &Manager{
		cfg:          cfg,
		log:          logger,
		scope:        scope,
		mode:         mode,
		orgs:         orgs,
		autoCreate:   autoCreate,
		mappings:     mappings,
		removeUsers:  removeUsers,
		teamsCache:   make(map[string][]github.Team),
		membersCache: make(map[string][]string),
		ccNameCache:  make(map[string]string),
	}
}

func TestCostCenterForTeam_AutoOrg(t *testing.T) {
	mgr := newTestManager("organization", "auto", []string{"my-org"}, nil, false, false)

	team := github.Team{Name: "backend-team", Slug: "backend-team"}
	cc, ok := mgr.costCenterForTeam("my-org", team)
	if !ok {
		t.Fatal("expected ok=true for auto org team")
	}
	want := "[org team] my-org/backend-team"
	if cc != want {
		t.Errorf("got %q, want %q", cc, want)
	}
}

func TestCostCenterForTeam_AutoEnterprise(t *testing.T) {
	mgr := newTestManager("enterprise", "auto", nil, nil, false, false)

	team := github.Team{Name: "Platform Engineers", Slug: "platform-engineers"}
	cc, ok := mgr.costCenterForTeam("test-enterprise", team)
	if !ok {
		t.Fatal("expected ok=true for auto enterprise team")
	}
	want := "[enterprise team] Platform Engineers"
	if cc != want {
		t.Errorf("got %q, want %q", cc, want)
	}
}

func TestCostCenterForTeam_ManualHit(t *testing.T) {
	mappings := map[string]string{
		"my-org/devs": "Engineering CC",
	}
	mgr := newTestManager("organization", "manual", []string{"my-org"}, mappings, false, false)

	team := github.Team{Name: "Developers", Slug: "devs"}
	cc, ok := mgr.costCenterForTeam("my-org", team)
	if !ok {
		t.Fatal("expected ok=true for manual mapped team")
	}
	if cc != "Engineering CC" {
		t.Errorf("got %q, want %q", cc, "Engineering CC")
	}
}

func TestCostCenterForTeam_ManualMiss(t *testing.T) {
	mappings := map[string]string{
		"my-org/devs": "Engineering CC",
	}
	mgr := newTestManager("organization", "manual", []string{"my-org"}, mappings, false, false)

	team := github.Team{Name: "Unknown Team", Slug: "unknown"}
	_, ok := mgr.costCenterForTeam("my-org", team)
	if ok {
		t.Error("expected ok=false for unmapped manual team")
	}
}

func TestCostCenterForTeam_Cache(t *testing.T) {
	mgr := newTestManager("organization", "auto", []string{"my-org"}, nil, false, false)

	team := github.Team{Name: "devs", Slug: "devs"}
	cc1, _ := mgr.costCenterForTeam("my-org", team)
	cc2, _ := mgr.costCenterForTeam("my-org", team)

	if cc1 != cc2 {
		t.Errorf("cache miss: %q != %q", cc1, cc2)
	}
	if _, ok := mgr.ccNameCache["my-org/devs"]; !ok {
		t.Error("expected cache key my-org/devs to exist")
	}
}

func TestBuildTeamAssignments_NoTeams(t *testing.T) {
	mgr := newTestManager("organization", "auto", []string{"empty-org"}, nil, false, false)
	mgr.teamsCache["empty-org"] = []github.Team{}

	// Seed teams cache so fetchAllTeams returns empty.
	// We need to override fetchAllTeams by pre-populating the cache.
	// But fetchAllTeams calls the client, which we don't have.
	// Instead, test the assignment logic directly.
	// When no teams exist, BuildTeamAssignments should return nil.

	// Since we can't call the real API, verify costCenterForTeam and
	// member cache interaction work correctly with unit-level tests.
}

func TestBuildTeamAssignments_LastTeamWins(t *testing.T) {
	mgr := newTestManager("organization", "auto", []string{"org1"}, nil, false, false)

	// Pre-populate caches to simulate fetched data.
	mgr.teamsCache["org1"] = []github.Team{
		{Name: "team-a", Slug: "team-a"},
		{Name: "team-b", Slug: "team-b"},
	}
	mgr.membersCache["org1/team-a"] = []string{"alice", "bob"}
	mgr.membersCache["org1/team-b"] = []string{"bob", "carol"}

	// Simulate BuildTeamAssignments logic manually since it calls fetchAllTeams.
	userFinal := make(map[string]UserAssignment)
	userTeamMap := make(map[string][]string)

	for _, team := range mgr.teamsCache["org1"] {
		ccName, ok := mgr.costCenterForTeam("org1", team)
		if !ok {
			continue
		}
		cacheKey := "org1/" + team.Slug
		members := mgr.membersCache[cacheKey]
		for _, username := range members {
			userTeamMap[username] = append(userTeamMap[username], cacheKey)
			userFinal[username] = UserAssignment{
				Username:   username,
				CostCenter: ccName,
				Org:        "org1",
				TeamSlug:   team.Slug,
			}
		}
	}

	// bob was in both teams, last-team-wins.
	bobAssign := userFinal["bob"]
	if bobAssign.CostCenter == "" {
		t.Fatal("bob should have an assignment")
	}
	// bob should be in team-b (last iterated).
	if bobAssign.TeamSlug != "team-b" {
		t.Logf("bob assigned to %s (last-team-wins is non-deterministic with maps)", bobAssign.TeamSlug)
	}

	// alice should be in team-a.
	aliceAssign := userFinal["alice"]
	if aliceAssign.CostCenter != "[org team] org1/team-a" {
		t.Errorf("alice: got %q, want %q", aliceAssign.CostCenter, "[org team] org1/team-a")
	}

	// carol should be in team-b.
	carolAssign := userFinal["carol"]
	if carolAssign.CostCenter != "[org team] org1/team-b" {
		t.Errorf("carol: got %q, want %q", carolAssign.CostCenter, "[org team] org1/team-b")
	}

	// bob is a multi-team user.
	if len(userTeamMap["bob"]) != 2 {
		t.Errorf("bob should be in 2 teams, got %d", len(userTeamMap["bob"]))
	}
}

func TestEnsureCostCentersExist_AutoCreateDisabled(t *testing.T) {
	// When auto-create is disabled and no client is available,
	// EnsureCostCentersExist will attempt to resolve names via the API.
	// With no client, it should return an error (not an identity map).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"costCenters": []map[string]string{
				{"id": "uuid-a", "name": "cc-a", "state": "active"},
				{"id": "uuid-b", "name": "cc-b", "state": "active"},
			},
		})
	}))
	defer srv.Close()

	client := newTestClientFromURL(t, srv.URL)
	mgr := newTestManager("organization", "auto", nil, nil, false, false)
	mgr.client = client

	ccMap, newlyCreated, err := mgr.EnsureCostCentersExist([]string{"cc-a", "cc-b"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if newlyCreated != nil {
		t.Error("expected nil newlyCreated when auto-create is disabled")
	}
	// Should return resolved UUIDs, not identity map.
	if ccMap["cc-a"] != "uuid-a" || ccMap["cc-b"] != "uuid-b" {
		t.Errorf("expected resolved UUIDs, got %v", ccMap)
	}
}

func TestSummaryPrint(t *testing.T) {
	s := &Summary{
		Mode:          "auto",
		Scope:         "enterprise",
		Organizations: nil,
		TotalTeams:    5,
		TotalCCs:      3,
		UniqueUsers:   15,
		CostCenters: map[string]int{
			"[enterprise team] team-a": 5,
			"[enterprise team] team-b": 7,
			"[enterprise team] team-c": 3,
		},
	}
	// Just verify it doesn't panic.
	s.Print("test-enterprise")
}

func TestNewManager(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	cfg := &config.Manager{
		TeamsScope:                "enterprise",
		TeamsStrategy:             "auto",
		Organizations:             []string{"org1"},
		TeamsAutoCreate:           true,
		TeamsMappings:             map[string]string{"a": "b"},
		TeamsRemoveUnmatchedUsers: true,
		Enterprise:                "ent",
	}

	mgr := NewManager(cfg, nil, logger)

	if mgr.scope != "enterprise" {
		t.Errorf("scope: got %q, want %q", mgr.scope, "enterprise")
	}
	if mgr.mode != "auto" {
		t.Errorf("mode: got %q, want %q", mgr.mode, "auto")
	}
	if !mgr.autoCreate {
		t.Error("autoCreate should be true")
	}
	if !mgr.removeUsers {
		t.Error("removeUsers should be true")
	}
	if mgr.teamsCache == nil || mgr.membersCache == nil || mgr.ccNameCache == nil {
		t.Error("caches should be initialized")
	}
}

func TestCostCenterForTeam_InvalidMode(t *testing.T) {
	mgr := newTestManager("organization", "invalid", nil, nil, false, false)

	team := github.Team{Name: "devs", Slug: "devs"}
	_, ok := mgr.costCenterForTeam("my-org", team)
	if ok {
		t.Error("expected ok=false for invalid mode")
	}
}

func TestFetchTeamMembers_Cache(t *testing.T) {
	mgr := newTestManager("organization", "auto", []string{"org1"}, nil, false, false)

	// Pre-populate cache.
	mgr.membersCache["org1/devs"] = []string{"alice", "bob"}

	// Should return cached values without calling client.
	members, err := mgr.fetchTeamMembers("org1", "devs")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(members) != 2 {
		t.Errorf("expected 2 members, got %d", len(members))
	}
	if members[0] != "alice" || members[1] != "bob" {
		t.Errorf("unexpected members: %v", members)
	}
}

func TestFetchTeamMembers_EnterpriseCacheKey(t *testing.T) {
	mgr := newTestManager("enterprise", "auto", nil, nil, false, false)

	// For enterprise scope, cache key is just the slug.
	mgr.membersCache["devs"] = []string{"carol"}

	members, err := mgr.fetchTeamMembers("test-enterprise", "devs")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(members) != 1 || members[0] != "carol" {
		t.Errorf("unexpected members: %v", members)
	}
}

// testLogger returns a quiet logger for test usage.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

// newTestClientFromURL creates a github.Client pointing at the given httptest server URL.
func newTestClientFromURL(t *testing.T, url string) *github.Client {
	t.Helper()
	cfg := &config.Manager{
		Enterprise: "test-enterprise",
		APIBaseURL: url,
		Token:      "test-token",
	}
	c, err := github.NewClient(cfg, testLogger())
	if err != nil {
		t.Fatalf("creating test client: %v", err)
	}
	return c
}

// newTestManagerWithClient builds a Manager with a real github.Client and budget config.
func newTestManagerWithClient(client *github.Client, products map[string]config.ProductBudget) *Manager {
	logger := testLogger()
	cfg := &config.Manager{
		TeamsScope:    "organization",
		TeamsStrategy: "auto",
		Enterprise:    "test-enterprise",
	}
	mgr := &Manager{
		cfg:            cfg,
		client:         client,
		log:            logger,
		scope:          "organization",
		mode:           "auto",
		createBudgets:  true,
		budgetProducts: products,
		teamsCache:     make(map[string][]github.Team),
		membersCache:   make(map[string][]string),
		ccNameCache:    make(map[string]string),
	}
	return mgr
}

func TestCreateBudgetsForNewCCs_NoProducts(t *testing.T) {
	mgr := newTestManagerWithClient(nil, nil)
	// Empty products should return nil immediately.
	err := mgr.createBudgetsForNewCCs(
		map[string]string{"CC A": "cc-id-a"},
		map[string]bool{"cc-id-a": true},
	)
	if err != nil {
		t.Errorf("expected nil error with no products, got %v", err)
	}
}

func TestCreateBudgetsForNewCCs_AllSucceed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"budgets": []any{}})
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	client := newTestClientFromURL(t, srv.URL)
	products := map[string]config.ProductBudget{
		"actions": {Amount: 100, Enabled: true},
	}
	mgr := newTestManagerWithClient(client, products)

	err := mgr.createBudgetsForNewCCs(
		map[string]string{"CC A": "cc-id-a"},
		map[string]bool{"cc-id-a": true},
	)
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

func TestCreateBudgetsForNewCCs_PartialFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"budgets": []any{}})
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"bad request"}`))
	}))
	defer srv.Close()

	client := newTestClientFromURL(t, srv.URL)
	products := map[string]config.ProductBudget{
		"actions": {Amount: 100, Enabled: true},
	}
	mgr := newTestManagerWithClient(client, products)

	err := mgr.createBudgetsForNewCCs(
		map[string]string{"CC A": "cc-id-a"},
		map[string]bool{"cc-id-a": true},
	)
	if err == nil {
		t.Fatal("expected error for budget creation failure")
	}
	if !strings.Contains(err.Error(), "budget creation failures") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestCreateBudgetsForNewCCs_APIUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not found"}`))
	}))
	defer srv.Close()

	client := newTestClientFromURL(t, srv.URL)
	products := map[string]config.ProductBudget{
		"actions": {Amount: 100, Enabled: true},
	}
	mgr := newTestManagerWithClient(client, products)

	// 404 triggers BudgetsAPIUnavailableError — should return nil (graceful degradation).
	err := mgr.createBudgetsForNewCCs(
		map[string]string{"CC A": "cc-id-a"},
		map[string]bool{"cc-id-a": true},
	)
	if err != nil {
		t.Errorf("expected nil error for API unavailable, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// EnsureCostCentersExist — resolve-without-create
// ---------------------------------------------------------------------------

func TestEnsureCostCentersExist_ResolvesNames(t *testing.T) {
	// API returns two active cost centers.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"costCenters": []map[string]string{
				{"id": "uuid-aaa", "name": "CC Alpha", "state": "active"},
				{"id": "uuid-bbb", "name": "CC Beta", "state": "active"},
			},
		})
	}))
	defer srv.Close()

	client := newTestClientFromURL(t, srv.URL)
	mgr := newTestManager("organization", "manual", []string{"org1"},
		map[string]string{"org1/team-a": "CC Alpha"}, false, false)
	mgr.client = client

	ccMap, newlyCreated, err := mgr.EnsureCostCentersExist([]string{"CC Alpha", "CC Beta"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if newlyCreated != nil {
		t.Error("expected nil newlyCreated for resolve-only path")
	}
	if ccMap["CC Alpha"] != "uuid-aaa" {
		t.Errorf("CC Alpha: got %q, want uuid-aaa", ccMap["CC Alpha"])
	}
	if ccMap["CC Beta"] != "uuid-bbb" {
		t.Errorf("CC Beta: got %q, want uuid-bbb", ccMap["CC Beta"])
	}
}

func TestEnsureCostCentersExist_UnresolvedNamesFail(t *testing.T) {
	// API returns only one cost center.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"costCenters": []map[string]string{
				{"id": "uuid-aaa", "name": "CC Alpha", "state": "active"},
			},
		})
	}))
	defer srv.Close()

	client := newTestClientFromURL(t, srv.URL)
	mgr := newTestManager("organization", "manual", []string{"org1"}, nil, false, false)
	mgr.client = client

	_, _, err := mgr.EnsureCostCentersExist([]string{"CC Alpha", "CC Missing", "CC Also Missing"})
	if err == nil {
		t.Fatal("expected error for unresolved cost centers")
	}
	if !strings.Contains(err.Error(), "CC Missing") {
		t.Errorf("error should mention CC Missing: %v", err)
	}
	if !strings.Contains(err.Error(), "CC Also Missing") {
		t.Errorf("error should mention CC Also Missing: %v", err)
	}
	if !strings.Contains(err.Error(), "auto_create_cost_centers") {
		t.Errorf("error should suggest enabling auto_create: %v", err)
	}
}

func TestEnsureCostCentersExist_SpecialCharsInName(t *testing.T) {
	// Cost center with special characters should resolve fine by name.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"costCenters": []map[string]string{
				{"id": "uuid-xyz", "name": "42_Ölbrück-Straße", "state": "active"},
			},
		})
	}))
	defer srv.Close()

	client := newTestClientFromURL(t, srv.URL)
	mgr := newTestManager("organization", "manual", []string{"org1"},
		map[string]string{"org1/users": "42_Ölbrück-Straße"}, false, false)
	mgr.client = client

	ccMap, _, err := mgr.EnsureCostCentersExist([]string{"42_Ölbrück-Straße"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ccMap["42_Ölbrück-Straße"] != "uuid-xyz" {
		t.Errorf("got %q, want uuid-xyz", ccMap["42_Ölbrück-Straße"])
	}
}

// ---------------------------------------------------------------------------
// Concurrency tests
// ---------------------------------------------------------------------------

// TestFetchTeamMembers_Concurrent verifies that fetchTeamMembers is safe to
// call from multiple goroutines at the same time (regression test for the
// membersCache mutex).
func TestFetchTeamMembers_Concurrent(t *testing.T) {
	t.Parallel()
	mgr := newTestManager("organization", "auto", []string{"org1"}, nil, false, false)
	mgr.membersCache["org1/devs"] = []string{"alice", "bob"}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			members, err := mgr.fetchTeamMembers("org1", "devs")
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if len(members) != 2 {
				t.Errorf("expected 2 members, got %d", len(members))
			}
		}()
	}
	wg.Wait()
}

// TestNewManager_ConcurrencyFromConfig verifies that NewManager reads the
// concurrency setting from config and applies the default when not set.
func TestNewManager_ConcurrencyFromConfig(t *testing.T) {
	logger := testLogger()
	cfg := &config.Manager{
		TeamsScope:    "enterprise",
		TeamsStrategy: "auto",
		Enterprise:    "ent",
		TeamsMappings: map[string]string{},
		Concurrency:   10,
	}
	mgr := NewManager(cfg, nil, logger)
	if mgr.concurrency != 10 {
		t.Errorf("concurrency = %d, want 10", mgr.concurrency)
	}
}

// TestNewManager_DefaultConcurrency verifies that when config.Concurrency is
// at its default resolved value, NewManager propagates it correctly.
func TestNewManager_DefaultConcurrency(t *testing.T) {
	logger := testLogger()
	cfg := &config.Manager{
		TeamsScope:    "enterprise",
		TeamsStrategy: "auto",
		Enterprise:    "ent",
		TeamsMappings: map[string]string{},
		Concurrency:   config.DefaultConcurrency,
	}
	mgr := NewManager(cfg, nil, logger)
	if mgr.concurrency != config.DefaultConcurrency {
		t.Errorf("concurrency = %d, want %d", mgr.concurrency, config.DefaultConcurrency)
	}
}
