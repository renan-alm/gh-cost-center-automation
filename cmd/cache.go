package cmd

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/spf13/cobra"

	"github.com/renan-alm/gh-cost-center/internal/cache"
)

var (
	cacheStats   bool
	cacheClear   bool
	cacheCleanup bool
)

var cacheCmd = &cobra.Command{
	Use:   "cache",
	Short: "Manage the cost center cache",
	Long: `View, clear, or clean up the cost center cache.

The cache stores cost center lookups to reduce API calls on repeated runs.
Cache entries expire after 24 hours.

Examples:
  # Show cache statistics
  gh cost-center cache --stats

  # Clear the entire cache
  gh cost-center cache --clear

  # Remove only expired entries
  gh cost-center cache --cleanup`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !cacheStats && !cacheClear && !cacheCleanup {
			return cmd.Help()
		}

		cc, err := cache.New("", slog.Default())
		if err != nil {
			return fmt.Errorf("opening cache: %w", err)
		}

		if cacheStats {
			runCacheStats(cc)
		}
		if cacheClear {
			if err := runCacheClear(cc); err != nil {
				return err
			}
		}
		if cacheCleanup {
			if err := runCacheCleanup(cc); err != nil {
				return err
			}
		}
		return nil
	},
}

func runCacheStats(cc *cache.Cache) {
	stats := cc.GetStats()
	fmt.Println()
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println("COST CENTER CACHE STATISTICS")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("Cache file:      %s\n", stats.FilePath)
	fmt.Printf("File size:       %d bytes\n", stats.FileSizeBytes)
	fmt.Printf("Total entries:   %d\n", stats.TotalEntries)
	fmt.Printf("Valid entries:   %d\n", stats.ValidEntries)
	fmt.Printf("Expired entries: %d\n", stats.ExpiredEntries)
	fmt.Println(strings.Repeat("=", 60))
}

func runCacheClear(cc *cache.Cache) error {
	if err := cc.Clear(); err != nil {
		return fmt.Errorf("clearing cache: %w", err)
	}
	fmt.Println("Cache cleared successfully.")
	return nil
}

func runCacheCleanup(cc *cache.Cache) error {
	removed, err := cc.CleanupExpired()
	if err != nil {
		return fmt.Errorf("cleaning up cache: %w", err)
	}
	stats := cc.GetStats()
	fmt.Printf("Removed %d expired entries. %d entries remaining.\n", removed, stats.TotalEntries)
	return nil
}

func init() {
	cacheCmd.Flags().BoolVar(&cacheStats, "stats", false, "show cache statistics")
	cacheCmd.Flags().BoolVar(&cacheClear, "clear", false, "clear the entire cache")
	cacheCmd.Flags().BoolVar(&cacheCleanup, "cleanup", false, "remove expired cache entries")

	rootCmd.AddCommand(cacheCmd)
}
