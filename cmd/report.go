package cmd

import (
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"

	"github.com/renan-alm/gh-cost-center/internal/github"
	"github.com/renan-alm/gh-cost-center/internal/pru"
	"github.com/renan-alm/gh-cost-center/internal/teams"
)

var reportTeams bool

var reportCmd = &cobra.Command{
	Use:   "report",
	Short: "Generate cost center summary report",
	Long: `Generate and display a cost center summary report.

Shows per-cost-center user counts and assignment breakdown.
Use --teams for teams-aware reporting.

Examples:
  gh cost-center report
  gh cost-center report --teams`,
	RunE: runReport,
}

func init() {
	reportCmd.Flags().BoolVar(&reportTeams, "teams", false, "generate teams-aware report")

	rootCmd.AddCommand(reportCmd)
}

func runReport(_ *cobra.Command, _ []string) error {
	if reportTeams {
		return runTeamsReport()
	}

	logger := slog.Default()

	// Create GitHub API client.
	client, err := github.NewClient(cfgManager, logger)
	if err != nil {
		return fmt.Errorf("creating GitHub client: %w", err)
	}

	// Initialize PRU manager.
	mgr := pru.NewManager(cfgManager, logger)

	// Fetch Copilot users.
	users, err := client.GetCopilotUsers()
	if err != nil {
		return fmt.Errorf("fetching copilot users: %w", err)
	}

	// Generate and display summary.
	summary := mgr.GenerateSummary(users)

	fmt.Println("\n=== Cost Center Summary ===")
	logger.Info("Cost Center Assignment Summary")
	for cc, count := range summary {
		fmt.Printf("%s: %d users\n", cc, count)
		logger.Info("Cost center", "id", cc, "users", count)
	}

	return nil
}

// runTeamsReport generates a teams-aware cost center report.
func runTeamsReport() error {
	logger := slog.Default()

	client, err := github.NewClient(cfgManager, logger)
	if err != nil {
		return fmt.Errorf("creating GitHub client: %w", err)
	}

	mgr := teams.NewManager(cfgManager, client, logger)

	summary, err := mgr.GenerateSummary()
	if err != nil {
		return fmt.Errorf("generating teams summary: %w", err)
	}

	summary.Print(cfgManager.Enterprise)

	return nil
}
