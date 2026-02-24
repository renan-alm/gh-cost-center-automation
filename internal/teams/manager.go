// Package teams implements teams-based cost center assignment for GitHub
// Enterprise Copilot users.  It supports both organization-level and
// enterprise-level team scopes, with auto or manual cost center naming modes.
package teams

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/renan-alm/gh-cost-center/internal/config"
	"github.com/renan-alm/gh-cost-center/internal/github"
)

// UserAssignment records the cost center assignment for a user found via a
// team.  Only the final (last-team-wins) assignment is kept per user.
type UserAssignment struct {
	Username   string
	CostCenter string
	Org        string
	TeamSlug   string
}

// Manager handles teams-based cost center assignment logic.
type Manager struct {
	cfg    *config.Manager
	client *github.Client
	log    *slog.Logger

	// Configuration copied from config for convenience.
	scope       string // "organization" or "enterprise"
	mode        string // "auto" or "manual"
	orgs        []string
	autoCreate  bool
	mappings    map[string]string // team key -> CC name (manual mode)
	removeUsers bool

	// Budget creation support.
	createBudgets  bool
	budgetProducts map[string]config.ProductBudget

	// Caches populated during a run.
	teamsCache   map[string][]github.Team // org/enterprise -> teams
	membersCache map[string][]string      // team-key -> usernames
	ccNameCache  map[string]string        // team-key -> CC name
}

// NewManager creates a new teams manager from the resolved configuration.
func NewManager(cfg *config.Manager, client *github.Client, logger *slog.Logger) *Manager {
	return &Manager{
		cfg:          cfg,
		client:       client,
		log:          logger,
		scope:        cfg.TeamsScope,
		mode:         cfg.TeamsMode,
		orgs:         cfg.TeamsOrganizations,
		autoCreate:   cfg.TeamsAutoCreate,
		mappings:     cfg.TeamsMappings,
		removeUsers:  cfg.TeamsRemoveUsersNoLongerInTeams,
		teamsCache:   make(map[string][]github.Team),
		membersCache: make(map[string][]string),
		ccNameCache:  make(map[string]string),
	}
}

// SetBudgetConfig enables budget creation for newly-created cost centers.
func (m *Manager) SetBudgetConfig(enabled bool, products map[string]config.ProductBudget) {
	m.createBudgets = enabled
	m.budgetProducts = products
}

// PrintConfigSummary displays the teams mode configuration.
func (m *Manager) PrintConfigSummary(checkCurrent, createBudgets bool) {
	fmt.Println("\n===== Teams Mode Configuration =====")
	fmt.Printf("Scope: %s\n", m.scope)
	fmt.Printf("Mode: %s\n", m.mode)

	if m.scope == "enterprise" {
		fmt.Printf("Enterprise: %s\n", m.cfg.Enterprise)
	} else {
		fmt.Printf("Organizations: %s\n", strings.Join(m.orgs, ", "))
	}

	fmt.Printf("Auto-create cost centers: %v\n", m.autoCreate)
	fmt.Printf("Full sync (remove users who left teams): %v\n", m.removeUsers)
	fmt.Printf("Check current cost center: %v\n", checkCurrent)
	fmt.Printf("Create budgets: %v\n", createBudgets)

	switch m.mode {
	case "auto":
		if m.scope == "enterprise" {
			fmt.Println("Cost center naming: [enterprise team] {team-name}")
		} else {
			fmt.Println("Cost center naming: [org team] {org-name}/{team-name}")
		}
	case "manual":
		fmt.Printf("Manual mappings configured: %d\n", len(m.mappings))
		for teamKey, cc := range m.mappings {
			fmt.Printf("  - %s -> %s\n", teamKey, cc)
		}
	}
	fmt.Println("===== End of Configuration =====")
}

// fetchAllTeams fetches teams from all configured sources (orgs or enterprise).
func (m *Manager) fetchAllTeams() (map[string][]github.Team, error) {
	allTeams := make(map[string][]github.Team)

	if m.scope == "enterprise" {
		m.log.Info("Fetching enterprise teams", "enterprise", m.cfg.Enterprise)
		teams, err := m.client.GetEnterpriseTeams()
		if err != nil {
			return nil, fmt.Errorf("fetching enterprise teams: %w", err)
		}
		allTeams[m.cfg.Enterprise] = teams
		m.teamsCache[m.cfg.Enterprise] = teams
		m.log.Info("Found enterprise teams", "count", len(teams))
	} else {
		if len(m.orgs) == 0 {
			m.log.Warn("No organizations configured for organization scope")
			return allTeams, nil
		}
		for _, org := range m.orgs {
			m.log.Info("Fetching teams from organization", "org", org)
			teams, err := m.client.GetOrgTeams(org)
			if err != nil {
				return nil, fmt.Errorf("fetching teams for org %s: %w", org, err)
			}
			allTeams[org] = teams
			m.teamsCache[org] = teams
			m.log.Info("Found teams in organization", "org", org, "count", len(teams))
		}
	}

	total := 0
	for _, t := range allTeams {
		total += len(t)
	}
	m.log.Info("Total teams fetched", "count", total)
	return allTeams, nil
}

// fetchTeamMembers fetches the members of a team, using an in-memory cache.
func (m *Manager) fetchTeamMembers(orgOrEnterprise, teamSlug string) ([]string, error) {
	var cacheKey string
	if m.scope == "enterprise" {
		cacheKey = teamSlug
	} else {
		cacheKey = orgOrEnterprise + "/" + teamSlug
	}

	if cached, ok := m.membersCache[cacheKey]; ok {
		return cached, nil
	}

	var members []github.TeamMember
	var err error
	if m.scope == "enterprise" {
		members, err = m.client.GetEnterpriseTeamMembers(teamSlug)
	} else {
		members, err = m.client.GetOrgTeamMembers(orgOrEnterprise, teamSlug)
	}
	if err != nil {
		return nil, fmt.Errorf("fetching members for team %s: %w", cacheKey, err)
	}

	usernames := make([]string, 0, len(members))
	for _, member := range members {
		if member.Login != "" {
			usernames = append(usernames, member.Login)
		}
	}

	m.membersCache[cacheKey] = usernames
	return usernames, nil
}

// costCenterForTeam determines the cost center name for a given team.
func (m *Manager) costCenterForTeam(orgOrEnterprise string, team github.Team) (string, bool) {
	var teamKey string
	if m.scope == "enterprise" {
		teamKey = team.Slug
	} else {
		teamKey = orgOrEnterprise + "/" + team.Slug
	}

	// Check cache.
	if cc, ok := m.ccNameCache[teamKey]; ok {
		return cc, true
	}

	var ccName string

	switch m.mode {
	case "manual":
		cc, ok := m.mappings[teamKey]
		if !ok {
			m.log.Warn("No mapping found for team in manual mode",
				"team", teamKey,
				"hint", "add mapping to config.teams.team_mappings")
			return "", false
		}
		ccName = cc

	case "auto":
		if m.scope == "enterprise" {
			ccName = fmt.Sprintf("[enterprise team] %s", team.Name)
		} else {
			ccName = fmt.Sprintf("[org team] %s/%s", orgOrEnterprise, team.Name)
		}

	default:
		m.log.Error("Invalid teams mode", "mode", m.mode)
		return "", false
	}

	m.ccNameCache[teamKey] = ccName
	return ccName, true
}

// BuildTeamAssignments builds the complete team->members mapping with cost
// centers.  Users can only belong to ONE cost center; if a user appears in
// multiple teams the last-team-wins.
//
// Returns a map of costCenterName -> []UserAssignment.
func (m *Manager) BuildTeamAssignments() (map[string][]UserAssignment, error) {
	m.log.Info("Building team-based cost center assignments...")

	allTeams, err := m.fetchAllTeams()
	if err != nil {
		return nil, err
	}

	if len(allTeams) == 0 {
		m.log.Warn("No teams found in any configured source")
		return nil, nil
	}

	// Track final assignment per user (last-team-wins).
	userFinal := make(map[string]UserAssignment) // username -> assignment

	// Track multi-team users for conflict reporting.
	userTeamMap := make(map[string][]string) // username -> list of team keys

	for orgOrEnterprise, teams := range allTeams {
		sourceLabel := "organization"
		if m.scope == "enterprise" {
			sourceLabel = "enterprise"
		}
		m.log.Info("Processing teams",
			"source", sourceLabel,
			"name", orgOrEnterprise,
			"count", len(teams))

		for _, team := range teams {
			ccName, ok := m.costCenterForTeam(orgOrEnterprise, team)
			if !ok {
				m.log.Debug("Skipping team (no cost center mapping)", "team", team.Slug)
				continue
			}

			members, err := m.fetchTeamMembers(orgOrEnterprise, team.Slug)
			if err != nil {
				return nil, err
			}

			if len(members) == 0 {
				m.log.Info("Team has no members, skipping", "team", team.Slug)
				continue
			}

			var teamKey string
			if m.scope == "enterprise" {
				teamKey = team.Slug
			} else {
				teamKey = orgOrEnterprise + "/" + team.Slug
			}

			for _, username := range members {
				userTeamMap[username] = append(userTeamMap[username], teamKey)
				// Last-team-wins: overwrite any previous assignment.
				userFinal[username] = UserAssignment{
					Username:   username,
					CostCenter: ccName,
					Org:        orgOrEnterprise,
					TeamSlug:   team.Slug,
				}
			}

			m.log.Info("Team assignment",
				"team", team.Name,
				"key", teamKey,
				"cost_center", ccName,
				"members", len(members))
		}
	}

	// Report multi-team users.
	var multiTeamUsers []string
	for user, teams := range userTeamMap {
		if len(teams) > 1 {
			multiTeamUsers = append(multiTeamUsers, user)
		}
	}
	if len(multiTeamUsers) > 0 {
		sort.Strings(multiTeamUsers)
		m.log.Warn("Users in multiple teams (last-team-wins)",
			"count", len(multiTeamUsers))
		limit := 10
		if len(multiTeamUsers) < limit {
			limit = len(multiTeamUsers)
		}
		for _, user := range multiTeamUsers[:limit] {
			m.log.Warn("Multi-team user",
				"user", user,
				"teams", strings.Join(userTeamMap[user], ", "),
				"assigned_to", userFinal[user].CostCenter)
		}
		if len(multiTeamUsers) > 10 {
			m.log.Warn("More multi-team users not shown",
				"remaining", len(multiTeamUsers)-10)
		}
	}

	// Convert to costCenter -> []UserAssignment.
	assignments := make(map[string][]UserAssignment)
	for _, ua := range userFinal {
		assignments[ua.CostCenter] = append(assignments[ua.CostCenter], ua)
	}

	m.log.Info("Team assignment summary",
		"cost_centers", len(assignments),
		"unique_users", len(userFinal))

	return assignments, nil
}

// EnsureCostCentersExist ensures all required cost centers exist, creating
// them if auto-create is enabled.  Returns a map of ccName -> ccID and a set
// of newly-created cost center IDs.
func (m *Manager) EnsureCostCentersExist(ccNames []string) (map[string]string, map[string]bool, error) {
	if !m.autoCreate {
		m.log.Info("Auto-creation disabled, assuming cost center IDs are valid")
		identity := make(map[string]string, len(ccNames))
		for _, n := range ccNames {
			identity[n] = n
		}
		return identity, nil, nil
	}

	m.log.Info("Ensuring cost centers exist", "count", len(ccNames))

	// Preload active cost centers for performance.
	activeMap, err := m.client.GetAllActiveCostCenters()
	if err != nil {
		m.log.Warn("Failed to preload cost centers, falling back to individual creation", "error", err)
		activeMap = make(map[string]string)
	} else {
		m.log.Info("Preloaded active cost centers", "count", len(activeMap))
	}

	ccMap := make(map[string]string, len(ccNames))
	newlyCreated := make(map[string]bool)
	preloadHits := 0
	apiCalls := 0

	for _, name := range ccNames {
		if id, ok := activeMap[name]; ok {
			ccMap[name] = id
			preloadHits++
			m.log.Debug("Preload hit", "name", name, "id", id)
			continue
		}

		// Need to create.
		apiCalls++
		id, err := m.client.CreateCostCenterWithPreload(name, activeMap)
		if err != nil {
			m.log.Error("Failed to create/find cost center", "name", name, "error", err)
			ccMap[name] = name // fallback to name
			continue
		}
		ccMap[name] = id
		newlyCreated[id] = true
		m.log.Debug("Created cost center", "name", name, "id", id)
	}

	total := preloadHits + apiCalls
	hitRate := 0.0
	if total > 0 {
		hitRate = float64(preloadHits) / float64(total) * 100
	}
	m.log.Info("Cost center resolution complete",
		"resolved", len(ccMap),
		"preload_hits", preloadHits,
		"api_calls", apiCalls,
		"hit_rate_pct", fmt.Sprintf("%.1f", hitRate))

	if len(newlyCreated) > 0 {
		m.log.Debug("Newly created cost centers", "count", len(newlyCreated))
	}

	return ccMap, newlyCreated, nil
}

// SyncTeamAssignments is the main orchestration function.  In plan mode it
// previews changes; in apply mode it pushes assignments to GitHub Enterprise
// and optionally removes users who left teams.
func (m *Manager) SyncTeamAssignments(mode string, ignoreCurrentCC bool) (map[string]map[string]bool, error) {
	assignments, err := m.BuildTeamAssignments()
	if err != nil {
		return nil, err
	}
	if len(assignments) == 0 {
		m.log.Warn("No team assignments to sync")
		return nil, nil
	}

	// Collect unique cost center names.
	ccNames := make([]string, 0, len(assignments))
	for name := range assignments {
		ccNames = append(ccNames, name)
	}
	sort.Strings(ccNames)

	// Ensure cost centers exist.
	var ccMap map[string]string
	var newlyCreated map[string]bool

	if mode == "plan" {
		ccMap = make(map[string]string, len(ccNames))
		for _, n := range ccNames {
			ccMap[n] = n
		}
		newlyCreated = make(map[string]bool)
		m.log.Info("Plan mode: would ensure cost centers exist", "count", len(ccNames))
	} else {
		ccMap, newlyCreated, err = m.EnsureCostCentersExist(ccNames)
		if err != nil {
			return nil, fmt.Errorf("ensuring cost centers exist: %w", err)
		}

		// Create budgets for newly-created cost centers.
		if m.createBudgets && len(newlyCreated) > 0 {
			m.createBudgetsForNewCCs(ccMap, newlyCreated)
		}
	}

	// Convert assignments to use actual cost center IDs and deduplicate.
	idBased := make(map[string][]string) // ccID -> []usernames
	for ccName, userAssigns := range assignments {
		ccID := ccMap[ccName]
		seen := make(map[string]bool)
		for _, ua := range userAssigns {
			if !seen[ua.Username] {
				seen[ua.Username] = true
				idBased[ccID] = append(idBased[ccID], ua.Username)
			}
		}
	}

	// Summary.
	totalUsers := 0
	for _, users := range idBased {
		totalUsers += len(users)
	}
	m.log.Info("Prepared assignments",
		"cost_centers", len(idBased),
		"total_users", totalUsers)

	if mode == "plan" {
		m.log.Info("MODE=plan: would sync the following assignments:")
		for ccID, users := range idBased {
			m.log.Info("Would assign", "cost_center", ccID, "users", len(users))
		}
		if m.removeUsers {
			m.log.Info("Full sync mode is ENABLED -- in apply mode, users no longer in teams would be removed")
		}
		return nil, nil
	}

	// Apply mode: sync assignments.
	m.log.Info("Syncing team-based assignments to GitHub Enterprise...")
	results, err := m.client.BulkUpdateCostCenterAssignments(idBased, ignoreCurrentCC)
	if err != nil {
		return nil, fmt.Errorf("applying team assignments: %w", err)
	}

	// Handle user removal.
	m.log.Info("Checking for users no longer in teams...")
	removedResults := m.handleUserRemoval(idBased, ccMap, newlyCreated)

	// Merge removal results.
	if m.removeUsers {
		for ccID, userResults := range removedResults {
			if _, ok := results[ccID]; !ok {
				results[ccID] = make(map[string]bool)
			}
			for user, ok := range userResults {
				results[ccID][user] = ok
			}
		}
	}

	return results, nil
}

// handleUserRemoval detects (and optionally removes) users who are in a cost
// center but no longer in the corresponding team.  Newly-created cost centers
// are skipped as an optimisation -- they cannot have stale members.
func (m *Manager) handleUserRemoval(
	expectedAssignments map[string][]string,
	ccNameToID map[string]string,
	newlyCreated map[string]bool,
) map[string]map[string]bool {
	results := make(map[string]map[string]bool)

	// Build reverse map: ccID -> ccName (for logging).
	idToName := make(map[string]string, len(ccNameToID))
	for name, id := range ccNameToID {
		idToName[id] = name
	}

	// Filter out newly-created cost centers.
	toCheck := make(map[string][]string)
	skipped := 0
	for ccID, users := range expectedAssignments {
		if newlyCreated[ccID] {
			skipped++
			continue
		}
		toCheck[ccID] = users
	}
	if skipped > 0 {
		m.log.Info("Skipping newly created cost centers (no stale members possible)",
			"skipped", skipped)
	}

	m.log.Info("Checking cost centers for users no longer in teams",
		"count", len(toCheck))

	totalFound := 0
	totalRemoved := 0

	for ccID, expectedUsers := range toCheck {
		currentMembers, err := m.client.GetCostCenterMembers(ccID)
		if err != nil {
			m.log.Error("Failed to get cost center members", "cc", ccID, "error", err)
			continue
		}

		expectedSet := make(map[string]bool, len(expectedUsers))
		for _, u := range expectedUsers {
			expectedSet[u] = true
		}

		// Find users in CC but not in expected team members.
		var stale []string
		for _, member := range currentMembers {
			if !expectedSet[member] {
				stale = append(stale, member)
			}
		}

		if len(stale) == 0 {
			continue
		}

		displayName := idToName[ccID]
		if displayName == "" {
			displayName = ccID
		}
		totalFound += len(stale)

		sort.Strings(stale)
		m.log.Warn("Users no longer in team for cost center",
			"cost_center", displayName,
			"count", len(stale))
		for _, user := range stale {
			m.log.Warn("User no longer in team", "user", user, "cost_center", displayName)
		}

		if m.removeUsers {
			m.log.Info("Removing users no longer in team",
				"cost_center", displayName,
				"count", len(stale))
			removalStatus, err := m.client.RemoveUsersFromCostCenter(ccID, stale)
			if err != nil {
				m.log.Error("Failed to remove users", "cost_center", displayName, "error", err)
			}
			results[ccID] = removalStatus
			successful := 0
			for _, ok := range removalStatus {
				if ok {
					successful++
				}
			}
			totalRemoved += successful
		} else {
			m.log.Info("Full sync DISABLED -- users will remain in cost center",
				"cost_center", displayName)
		}
	}

	if totalFound > 0 {
		if m.removeUsers {
			m.log.Info("User removal summary",
				"found", totalFound,
				"removed", totalRemoved)
		} else {
			m.log.Warn("Users no longer in teams (NOT removed -- full sync disabled)",
				"count", totalFound)
		}
	} else {
		m.log.Info("All cost centers are in sync with teams -- no stale members found")
	}

	return results
}

// GenerateSummary builds and returns a teams-aware summary report.
func (m *Manager) GenerateSummary() (*Summary, error) {
	assignments, err := m.BuildTeamAssignments()
	if err != nil {
		return nil, err
	}

	totalTeams := 0
	for _, teams := range m.teamsCache {
		totalTeams += len(teams)
	}

	// Unique users (each user in exactly one CC due to dedup).
	allUsers := make(map[string]bool)
	ccBreakdown := make(map[string]int)
	for ccName, userAssigns := range assignments {
		for _, ua := range userAssigns {
			allUsers[ua.Username] = true
		}
		ccBreakdown[ccName] = len(userAssigns)
	}

	return &Summary{
		Mode:          m.mode,
		Scope:         m.scope,
		Organizations: m.orgs,
		TotalTeams:    totalTeams,
		TotalCCs:      len(assignments),
		UniqueUsers:   len(allUsers),
		CostCenters:   ccBreakdown,
	}, nil
}

// Summary holds the teams-mode summary statistics.
type Summary struct {
	Mode          string
	Scope         string
	Organizations []string
	TotalTeams    int
	TotalCCs      int
	UniqueUsers   int
	CostCenters   map[string]int // CC name -> user count
}

// Print displays the summary to stdout.
func (s *Summary) Print(enterprise string) {
	fmt.Println("\n=== Teams Cost Center Summary ===")
	fmt.Printf("Scope: %s\n", s.Scope)
	fmt.Printf("Mode: %s\n", s.Mode)

	if s.Scope == "enterprise" {
		fmt.Printf("Enterprise: %s\n", enterprise)
	} else {
		fmt.Printf("Organizations: %s\n", strings.Join(s.Organizations, ", "))
	}

	fmt.Printf("Total teams: %d\n", s.TotalTeams)
	fmt.Printf("Cost centers: %d\n", s.TotalCCs)
	fmt.Printf("Unique users: %d\n", s.UniqueUsers)
	fmt.Println("Note: Each user is assigned to exactly ONE cost center")

	if len(s.CostCenters) > 0 {
		fmt.Println("\nPer-Cost-Center Breakdown:")
		// Sort for deterministic output.
		names := make([]string, 0, len(s.CostCenters))
		for n := range s.CostCenters {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, name := range names {
			fmt.Printf("  %s: %d users\n", name, s.CostCenters[name])
		}
	}
}

// createBudgetsForNewCCs creates configured budgets for each newly-created
// cost center.  Stops attempting if the budgets API is unavailable (404).
func (m *Manager) createBudgetsForNewCCs(ccMap map[string]string, newlyCreated map[string]bool) {
	if len(m.budgetProducts) == 0 {
		m.log.Debug("No budget products configured, skipping budget creation")
		return
	}

	m.log.Info("Creating budgets for newly-created cost centers",
		"count", len(newlyCreated))

	// Build reverse map: ccID -> ccName.
	idToName := make(map[string]string, len(ccMap))
	for name, id := range ccMap {
		idToName[id] = name
	}

	budgetsDisabled := false
	for ccID := range newlyCreated {
		if budgetsDisabled {
			break
		}
		ccName := idToName[ccID]
		if ccName == "" {
			ccName = ccID
		}

		m.log.Info("Creating budgets for cost center", "name", ccName)
		for product, pc := range m.budgetProducts {
			if !pc.Enabled {
				continue
			}
			ok, err := m.client.CreateProductBudget(ccID, ccName, product, pc.Amount)
			if err != nil {
				if _, is404 := err.(*github.BudgetsAPIUnavailableError); is404 {
					m.log.Warn("Budgets API unavailable, disabling further attempts",
						"error", err)
					budgetsDisabled = true
					break
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
}
