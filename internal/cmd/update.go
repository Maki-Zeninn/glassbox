// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"
	"strings"

	"github.com/dotandev/glassbox/internal/updater"
	"github.com/dotandev/glassbox/internal/version"
	"github.com/spf13/cobra"
)

var (
	updateVersionFlag  string
	updateDetailedFlag bool
	updateYesFlag      bool
)

var updateCmd = &cobra.Command{
	Use:     "update",
	GroupID: "utility",
	Short:   "Update Glassbox to the latest version or a specific version",
	Long: `Check for the latest version of Glassbox and upgrade to it.
You can also specify a target version using the --version flag.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		checker := updater.NewChecker(version.Version)

		targetVersion := updateVersionFlag
		if targetVersion == "" {
			targetVersion = "latest"
		}

		fmt.Printf("Checking for version %s...\n", targetVersion)
		release, err := checker.FetchReleaseInfo(cmd.Context(), targetVersion)
		if err != nil {
			return fmt.Errorf("failed to fetch release information: %w", err)
		}

		fmt.Printf("Found version: %s\n\n", release.TagName)

		if release.Body != "" {
			fmt.Println("Changelog:")
			fmt.Println("----------")
			body := release.Body
			if !updateDetailedFlag && len(body) > 500 {
				body = body[:500] + "\n... (use --detailed to see full changelog)"
			}
			fmt.Println(body)
			fmt.Println("----------")
			fmt.Println()
		}

		if !updateYesFlag {
			confirmed, err := confirmWithForceOrNonInteractive(cmd,
				fmt.Sprintf("Do you want to proceed with the update to %s? [y/N]: ", release.TagName),
				false, false)
			if err != nil {
				return err
			}
			if !confirmed {
				fmt.Println("Update cancelled.")
				return nil
			}
		}

		return checker.PerformUpdate(cmd.Context(), release.TagName)
	},
}

func init() {
	updateCmd.Flags().StringVar(&updateVersionFlag, "version", "", "Target version to update to")
	updateCmd.Flags().BoolVar(&updateDetailedFlag, "detailed", false, "Show full changelog details")
	updateCmd.Flags().BoolVarP(&updateYesFlag, "yes", "y", false, "Skip confirmation prompt")

	rootCmd.AddCommand(updateCmd)
}
