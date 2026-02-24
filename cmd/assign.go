package cmd

import (
	"bufio"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/renan-alm/gh-cost-center/internal/github"
	"github.com/renan-alm/gh-cost-center/internal/pru"
	"github.com/renan-alm/gh-cost-center/internal/repository"
	"github.com/renan-alm/gh-cost-center/internal/teams"
)

var (
	// assign flags
	assignMode           string
	assignYes            bool
	assignTeams          bool
	assignRepo           bool
	assignUsers          string
	assignIncremental    bool
	assignCreateCC       bool
	assignCreateBudgets  bool
	assignCheckCurrentCC bool
)

var assignCmd = &cobra.Command{
	Use:   "assign",
	Short: "Assign users or repositories to cost centers",
	Long: `Assign users or repositories to cost centers based on the selected mode.

Modes:
  PRU-based (default):   Assigns all Copilot users to cost centers based on
                         PRU exception rules.
  Teams-based (--teams): Assigns users based on GitHub team membership.
  Repository (--repo):   Assigns repos based on custom property values.

The --mode flag controls execution:
  plan  - Preview changes without applying (default)
  apply - Push assignments to GitHub Enterprise

Examples:
  # Preview PRU-based assignments
  gh cost-center assign --mode plan

  # Apply PRU-based assignments (skip confirmation)
  gh cost-center assign --mode apply --yes

  # Preview teams-based assignments
  gh cost-center assign --teams --mode plan

  # Apply teams-based assignments
  gh cost-center assign --teams --mode apply --yes

  # Apply with cost center auto-creation
  gh cost-center assign --mode apply --yes --create-cost-centers

  # Process only new users since last run
  gh cost-center assign --mode apply --yes --incremental

  # Apply repository-based assignments
  gh cost-center assign --repo --mode apply --yes`,
	RunE: runAssign,
}

func init() {
	assignCmd.Flags().StringVar(&assignMode, "mode", "plan", "execution mode: plan (preview) or apply (push changes)")
	assignCmd.Flags().BoolVarP(&assignYes, "yes", "y", false, "skip confirmation prompt in apply mode")
	assignCmd.Flags().BoolVar(&assignTeams, "teams", false, "enable teams-based assignment mode")
	assignCmd.Flags().BoolVar(&assignRepo, "repo", false, "enable repository-based assignment mode")
	assignCmd.Flags().StringVar(&assignUsers, "users", "", "comma-separated list of specific users to process")
	assignCmd.Flags().BoolVar(&assignIncremental, "incremental", false, "only process users added since last run (PRU mode)")
	assignCmd.Flags().BoolVar(&assignCreateCC, "create-cost-centers", false, "create cost centers if they don't exist")
	assignCmd.Flags().BoolVar(&assignCreateBudgets, "create-budgets", false, "create budgets for new cost centers")
	assignCmd.Flags().BoolVar(&assignCheckCurrentCC, "check-current", false, "check current cost center membership before assigning")

	rootCmd.AddCommand(assignCmd)
}

// runAssign dispatches to the appropriate assignment mode.
func runAssign(cmd *cobra.Command, _ []string) error {
	if assignMode != "plan" && assignMode != "apply" {
		return fmt.Errorf("invalid --mode %q: must be 'plan' or 'apply'", assignMode)
	}

	if assignTeams {
		return runTeamsAssign(cmd)
	}
	if assignRepo {
		return runRepoAssign(cmd)
	}

	return runPRUAssign(cmd)
}

// runPRUAssign implements the default PRU-based assignment flow.
func runPRUAssign(cmd *cobra.Command) error {
	logger := slog.Default()

	// Enable auto-creation if flag was passed.
	autoCreate := assignCreateCC || cfgManager.AutoCreate
	if assignCreateCC {
		cfgManager.EnableAutoCreation()
	}

	// Initialize PRU manager.
	mgr := pru.NewManager(cfgManager, logger)

	// Show configuration.
	mgr.PrintConfigSummary(cfgManager, autoCreate)

	// Create GitHub API client.
	client, err := github.NewClient(cfgManager, logger)
	if err != nil {
		return fmt.Errorf("creating GitHub client: %w", err)
	}

	// Fetch Copilot users.
	logger.Info("Fetching Copilot license holders...")
	users, err := client.GetCopilotUsers()
	if err != nil {
		return fmt.Errorf("fetching copilot users: %w", err)
	}
	logger.Info("Found Copilot license holders", "count", len(users))

	// Incremental processing: filter to new users since last run.
	originalCount := len(users)
	if assignIncremental {
		ts, err := cfgManager.LoadLastRunTimestamp()
		if err != nil {
			return fmt.Errorf("loading last run timestamp: %w", err)
		}
		if ts != nil {
			users = github.FilterUsersByTimestamp(users, *ts)
			logger.Info("Incremental mode",
				"new_users", len(users),
				"total_users", originalCount,
				"since", ts.Format("2006-01-02T15:04:05Z"),
			)
			if len(users) == 0 {
				logger.Info("No new users found since last run — nothing to process")
				if assignMode == "apply" {
					if err := cfgManager.SaveLastRunTimestamp(nil); err != nil {
						logger.Error("Failed to save timestamp", "error", err)
					}
				}
				return nil
			}
		} else {
			logger.Info("Incremental mode: no previous timestamp found, processing all users")
		}
	}

	// Auto-create cost centers if requested.
	if autoCreate {
		if assignMode == "plan" {
			logger.Info("MODE=plan: Would create cost centers if they don't exist")
			logger.Info("Would create", "no_pru", cfgManager.NoPRUsCostCenterName)
			logger.Info("Would create", "pru_allowed", cfgManager.PRUsAllowedCostCenterName)
		} else {
			logger.Info("Creating cost centers if they don't exist...")
			noPRUID, pruAllowedID, err := client.EnsureCostCentersExist(
				cfgManager.NoPRUsCostCenterName,
				cfgManager.PRUsAllowedCostCenterName,
			)
			if err != nil {
				return fmt.Errorf("creating cost centers: %w", err)
			}
			// Update IDs in config and manager.
			cfgManager.NoPRUsCostCenterID = noPRUID
			cfgManager.PRUsAllowedCostCenterID = pruAllowedID
			mgr.SetCostCenterIDs(noPRUID, pruAllowedID)

			logger.Info("Updated cost center IDs",
				"no_pru", noPRUID,
				"pru_allowed", pruAllowedID,
			)
		}
	}

	// Filter to specific users if --users flag was provided.
	if assignUsers != "" {
		users = filterUsersByLogin(users, assignUsers)
		logger.Info("Filtered to specified users", "count", len(users))
	}

	// Build assignment groups.
	groups := mgr.AssignmentGroups(users)

	pruCount := len(groups[mgr.PRUAllowedCCID()])
	noPRUCount := len(groups[mgr.NoPRUCCID()])

	// Log individual assignments in plan mode.
	if assignMode == "plan" {
		logger.Info("MODE=plan (no changes will be made)")
		for _, u := range users {
			cc := mgr.AssignCostCenter(u)
			logger.Debug("Would assign", "user", u.Login, "cc", cc)
		}
	}

	// Print assignment summary.
	fmt.Printf("\n=== Assignment Summary ===\n")
	fmt.Printf("PRUs Allowed (%s): %d users\n", mgr.PRUAllowedCCID(), pruCount)
	fmt.Printf("No PRUs (%s): %d users\n", mgr.NoPRUCCID(), noPRUCount)
	fmt.Printf("Total: %d users\n", len(users))

	// Execute assignments.
	var assignmentResults map[string]map[string]bool

	if assignMode == "plan" {
		logger.Info("Would sync full assignment state (plan mode)")
		for ccID, usernames := range groups {
			logger.Info("Would add users to cost center", "cc", ccID, "count", len(usernames))
		}
	} else {
		// Apply mode — safety confirmation unless --yes.
		if !assignYes {
			if !confirmApply(groups, assignCheckCurrentCC) {
				logger.Warn("Aborted by user before applying assignments")
				return nil
			}
		}

		// Remove empty groups.
		toSync := make(map[string][]string)
		for cc, names := range groups {
			if len(names) > 0 {
				toSync[cc] = names
			}
		}

		if len(toSync) == 0 {
			logger.Warn("No users to sync")
		} else {
			logger.Info("Applying full assignment state to GitHub Enterprise...")
			// ignore_current_cost_center is the inverse of --check-current
			ignoreCurrentCC := !assignCheckCurrentCC
			results, err := client.BulkUpdateCostCenterAssignments(toSync, ignoreCurrentCC)
			if err != nil {
				return fmt.Errorf("applying assignments: %w", err)
			}
			assignmentResults = results

			// Process and log results.
			logAssignmentResults(results, logger)
		}

		// Save timestamp for incremental processing.
		if assignIncremental {
			if err := cfgManager.SaveLastRunTimestamp(nil); err != nil {
				logger.Error("Failed to save timestamp", "error", err)
			} else {
				logger.Info("Saved current timestamp for next incremental run")
			}
		}
	}

	// Show success summary.
	var origPtr *int
	if assignIncremental {
		origPtr = &originalCount
	}
	pru.ShowSuccessSummary(cfgManager, users, origPtr, assignmentResults, assignMode == "apply")

	logger.Info("Assign command completed successfully")
	return nil
}

// confirmApply shows a confirmation prompt and returns true if the user types "apply".
func confirmApply(groups map[string][]string, checkCurrent bool) bool {
	fmt.Println("\nYou are about to APPLY cost center assignments to GitHub Enterprise.")
	fmt.Println("This will push assignments for ALL processed users (no diff).")

	if checkCurrent {
		fmt.Println("Current cost center membership will be checked — users in other cost centers will be SKIPPED.")
	} else {
		fmt.Println("Fast mode: Users will be assigned WITHOUT checking current cost center membership.")
	}

	fmt.Println("Summary:")
	for ccID, usernames := range groups {
		fmt.Printf("  - %s: %d users\n", ccID, len(usernames))
	}

	fmt.Print("\nProceed? Type 'apply' to continue: ")
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		return strings.TrimSpace(strings.ToLower(scanner.Text())) == "apply"
	}
	return false
}

// logAssignmentResults logs per-cost-center and overall success/failure counts.
func logAssignmentResults(results map[string]map[string]bool, logger *slog.Logger) {
	totalAttempted := 0
	totalSuccessful := 0
	totalFailed := 0

	for ccID, userResults := range results {
		ccSuccessful := 0
		for _, ok := range userResults {
			if ok {
				ccSuccessful++
			}
		}
		ccFailed := len(userResults) - ccSuccessful
		totalAttempted += len(userResults)
		totalSuccessful += ccSuccessful
		totalFailed += ccFailed

		if ccFailed > 0 {
			logger.Warn("Cost center partial success",
				"cost_center_id", ccID,
				"successful", ccSuccessful,
				"total", len(userResults),
			)
			var failedUsers []string
			for username, ok := range userResults {
				if !ok {
					failedUsers = append(failedUsers, username)
				}
			}
			logger.Error("Failed users", "cost_center_id", ccID, "users", strings.Join(failedUsers, ", "))
		} else {
			logger.Info("Cost center all successful",
				"cost_center_id", ccID,
				"count", ccSuccessful,
			)
		}
	}

	if totalFailed > 0 {
		logger.Warn("FINAL RESULT",
			"successful", totalSuccessful,
			"total", totalAttempted,
			"failed", totalFailed,
		)
	} else {
		logger.Info("FINAL RESULT: All users successfully assigned",
			"count", totalSuccessful,
		)
	}
}

// runTeamsAssign implements the teams-based assignment flow.
func runTeamsAssign(_ *cobra.Command) error {
	logger := slog.Default()

	// Create GitHub API client.
	client, err := github.NewClient(cfgManager, logger)
	if err != nil {
		return fmt.Errorf("creating GitHub client: %w", err)
	}

	// Enable auto-creation if flag was passed.
	if assignCreateCC {
		cfgManager.EnableAutoCreation()
	}

	// Initialize teams manager.
	mgr := teams.NewManager(cfgManager, client, logger)

	// Wire budget creation if requested.
	if assignCreateBudgets && cfgManager.BudgetsEnabled {
		mgr.SetBudgetConfig(true, cfgManager.BudgetProducts)
	}

	// Show configuration.
	mgr.PrintConfigSummary(assignCheckCurrentCC, assignCreateBudgets)

	// Sync assignments (plan or apply).
	ignoreCurrentCC := !assignCheckCurrentCC
	results, err := mgr.SyncTeamAssignments(assignMode, ignoreCurrentCC)
	if err != nil {
		return fmt.Errorf("syncing team assignments: %w", err)
	}

	if assignMode == "apply" {
		if !assignYes && results == nil {
			// In apply mode without --yes, SyncTeamAssignments would have
			// already applied.  Log completion.
			logger.Info("Teams assignment completed")
		}
		if results != nil {
			logAssignmentResults(results, logger)
		}
	}

	logger.Info("Teams assign command completed successfully")
	return nil
}

// runRepoAssign implements the repository-based assignment flow.
func runRepoAssign(_ *cobra.Command) error {
	logger := slog.Default()

	// Determine organization name from config.
	if len(cfgManager.TeamsOrganizations) == 0 {
		return fmt.Errorf("repository mode requires at least one organization in teams.organizations config")
	}
	org := cfgManager.TeamsOrganizations[0]

	// Create GitHub API client.
	client, err := github.NewClient(cfgManager, logger)
	if err != nil {
		return fmt.Errorf("creating GitHub client: %w", err)
	}

	// Initialize repository manager.
	mgr, err := repository.NewManager(cfgManager, client, logger)
	if err != nil {
		return fmt.Errorf("initializing repository manager: %w", err)
	}

	// Validate configuration.
	if issues := mgr.ValidateConfiguration(); len(issues) > 0 {
		for _, issue := range issues {
			logger.Error("Configuration issue", "detail", issue)
		}
		return fmt.Errorf("invalid repository configuration: %d issues found", len(issues))
	}

	// Show config summary.
	mgr.PrintConfigSummary(org)

	// Confirmation in apply mode.
	if assignMode == "apply" && !assignYes {
		fmt.Print("\nProceed with APPLY? Type 'apply' to continue: ")
		scanner := bufio.NewScanner(os.Stdin)
		if scanner.Scan() {
			if strings.TrimSpace(strings.ToLower(scanner.Text())) != "apply" {
				logger.Warn("Aborted by user")
				return nil
			}
		}
	}

	// Run assignment.
	createBudgets := assignCreateBudgets && cfgManager.BudgetsEnabled
	summary, err := mgr.Run(org, assignMode, createBudgets)
	if err != nil {
		return fmt.Errorf("repository assignment failed: %w", err)
	}

	// Print summary.
	if summary != nil {
		summary.Print()
	}

	logger.Info("Repository assign command completed successfully")
	return nil
}

// filterUsersByLogin filters a user slice to only those whose login appears in
// the comma-separated list.
func filterUsersByLogin(users []github.CopilotUser, commaSep string) []github.CopilotUser {
	wanted := make(map[string]bool)
	for _, u := range strings.Split(commaSep, ",") {
		u = strings.TrimSpace(u)
		if u != "" {
			wanted[strings.ToLower(u)] = true
		}
	}

	var filtered []github.CopilotUser
	for _, u := range users {
		if wanted[strings.ToLower(u.Login)] {
			filtered = append(filtered, u)
		}
	}
	return filtered
}
