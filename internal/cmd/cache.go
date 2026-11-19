// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/dotandev/glassbox/internal/cache"
	"github.com/dotandev/glassbox/internal/errors"
	"github.com/dotandev/glassbox/internal/rpc"
	"github.com/spf13/cobra"
)

var (
	cacheForceFlag     bool
	cacheStatusRPCFlag bool
	cleanOlderThanFlag int
	cleanNetworkFlag   string
	cleanAllFlag       bool
	artifactTypeFlag   string
	artifactNetworkFlag string
)

// getCacheDir returns the default cache directory
func getCacheDir() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = "."
	}
	return filepath.Join(homeDir, ".Glassbox", "cache")
}

var cacheCmd = &cobra.Command{
	Use:     "cache",
	GroupID: "management",
	Short:   "Manage transaction and simulation cache",
	Long: `Manage the local cache that stores transaction data and simulation results.
Caching improves performance and enables offline analysis.

Cache location: ~/.glassbox/cache (configurable via GLASSBOX_CACHE_DIR)

Available subcommands:
  status  - View cache size and usage statistics
  clean   - Remove old files using LRU strategy
  clear   - Delete all cached data`,
	Example: `  # Check cache status
  Glassbox cache status

  # Clean old cache entries
  Glassbox cache clean

  # Force clean without confirmation
  Glassbox cache clean --force

  # Clear all cache
  Glassbox cache clear --force`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

var cacheStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Display cache statistics",
	Long:  `Display the current cache size, number of cached files, and disk usage statistics.`,
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cacheDir := getCacheDir()
		manager := cache.NewManager(cacheDir, cache.DefaultConfig())

		size, err := manager.GetCacheSize()
		if err != nil {
			return errors.WrapValidationError(fmt.Sprintf("failed to calculate cache size: %v", err))
		}

		files, err := manager.ListCachedFiles()
		if err != nil {
			return errors.WrapValidationError(fmt.Sprintf("failed to list cache files: %v", err))
		}

		fmt.Printf("Cache directory: %s\n", cacheDir)
		fmt.Printf("Cache size: %s\n", formatBytes(size))
		fmt.Printf("Files cached: %d\n", len(files))
		fmt.Printf("Maximum size: %s\n", formatBytes(cache.DefaultConfig().MaxSizeBytes))

		if cacheStatusRPCFlag {
			rpcDir, err := rpc.GetCachePath()
			if err != nil {
				return errors.WrapValidationError(fmt.Sprintf("failed to locate RPC cache: %v", err))
			}
			rpcPath := filepath.Join(rpcDir, rpc.CacheDBName)
			count, err := rpc.CountEntries()
			if err != nil {
				return errors.WrapValidationError(fmt.Sprintf("failed to inspect RPC cache: %v", err))
			}
			fmt.Printf("RPC cache DB: %s\n", rpcPath)
			fmt.Printf("RPC cache entries: %d\n", count)
			if info, err := os.Stat(rpcPath); err == nil {
				fmt.Printf("RPC cache DB size: %s\n", formatBytes(info.Size()))
			}
		}

		if size > cache.DefaultConfig().MaxSizeBytes {
			fmt.Printf("\n[!]  Cache size exceeds maximum limit. Run 'Glassbox cache clean' to free space.\n")
		}

		return nil
	},
}

var cacheCleanCmd = &cobra.Command{
	Use:   "clean",
	Short: "Remove old cached files using LRU strategy",
	Long: `Remove old cached files using LRU (Least Recently Used) strategy.

This command will:
  1. Identify the oldest cached files
  2. Prompt for confirmation before deletion
  3. Delete files until cache size is reduced to 50% of maximum

Use --force to skip the confirmation prompt.`,
	Example: `  # Clean cache with confirmation
  Glassbox cache clean

  # Force clean without prompt
  Glassbox cache clean --force`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cacheDir := getCacheDir()
		manager := cache.NewManager(cacheDir, cache.DefaultConfig())

		status, err := manager.Clean(cacheForceFlag)
		if err != nil {
			return errors.WrapValidationError(fmt.Sprintf("cache cleanup failed: %v", err))
		}

		if status.FilesDeleted == 0 && status.OriginalSize > 0 {
			fmt.Println("No files needed to be deleted")
		}

		return nil
	},
}

var cacheClearCmd = &cobra.Command{
	Use:   "clear",
	Short: "Delete all cached files",
	Long: `Remove all cached files from the cache directory.

[!]  Warning: This action cannot be undone. Use --force to skip confirmation.`,
	Example: `  # Clear cache with confirmation
  Glassbox cache clear

  # Force clear without prompt
  Glassbox cache clear --force`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cacheDir := getCacheDir()

		// Check if cache exists
		if _, err := os.Stat(cacheDir); os.IsNotExist(err) {
			fmt.Println("Cache directory does not exist")
			return nil
		}

		// Get confirmation unless force flag is set
		if !cacheForceFlag {
			confirmed, confirmErr := confirmWithForceOrNonInteractive(cmd,
				fmt.Sprintf("This will delete ALL cached files in %s\nAre you sure? (yes/no): ", cacheDir),
				false, false)
			if confirmErr != nil {
				return confirmErr
			}
			if !confirmed {
				fmt.Println("Cache clear cancelled")
				return nil
			}
		}

		err := os.RemoveAll(cacheDir)
		if err != nil {
			return errors.WrapValidationError(fmt.Sprintf("failed to clear cache directory: %v", err))
		}

		fmt.Println("Cache cleared successfully")
		return nil
	},
}

// formatBytes converts bytes to human-readable format
func formatBytes(bytes int64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	size := float64(bytes)
	unitIndex := 0

	for size >= 1024 && unitIndex < len(units)-1 {
		size /= 1024
		unitIndex++
	}

	if unitIndex == 0 {
		return fmt.Sprintf("%.0f %s", size, units[unitIndex])
	}
	return fmt.Sprintf("%.2f %s", size, units[unitIndex])
}

var cacheRPCCmd = &cobra.Command{
	Use:   "rpc",
	Short: "Manage the local SQLite RPC fetch cache",
	Long: `Manage entries in the local SQLite RPC fetch cache (~/.glassbox/cache.db).

 Filter options:
   --older-than <days>  Remove entries created more than N days ago
   --network <name>     Remove entries for a specific network (e.g. mainnet, testnet)
   --all                Remove all cached RPC entries

 At least one filter must be specified. Filters can be combined.`,
	Example: `  # Remove entries older than 7 days
  Glassbox cache rpc --older-than 7

  # Remove all testnet entries
  Glassbox cache rpc --network testnet

  # Remove testnet entries older than 30 days
  Glassbox cache rpc --older-than 30 --network testnet

  # Remove all RPC cache entries
  Glassbox cache rpc --all`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !cleanAllFlag && cleanOlderThanFlag == 0 && cleanNetworkFlag == "" {
			return fmt.Errorf("no filter specified: use --all, --older-than, or --network")
		}

		filter := rpc.CleanFilter{
			OlderThan: time.Duration(cleanOlderThanFlag) * 24 * time.Hour,
			Network:   cleanNetworkFlag,
			All:       cleanAllFlag,
		}

		removed, err := rpc.CleanByFilter(filter)
		if err != nil {
			return errors.WrapValidationError(fmt.Sprintf("RPC cache clean failed: %v", err))
		}

		fmt.Printf("%d RPC cache entries removed.\n", removed)
		return nil
	},
}

var cacheArtifactCmd = &cobra.Command{
	Use:   "artifact",
	Short: "Manage cached contract artifacts and ledger entries",
	Long: `Manage disk-backed cache for contract WASM bytecode and ledger entries.

The artifact cache stores contract code and ledger entries persistently between
debug sessions to avoid repeated network fetches.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

var cacheArtifactListCmd = &cobra.Command{
	Use:   "list",
	Short: "List cached artifacts",
	Long:  `List all cached contract code and ledger entries. Optionally filter by type and network.`,
	Example: `  # List all cached artifacts
  Glassbox cache artifact list

  # List only contract code
  Glassbox cache artifact list --type contract_code

  # List only mainnet artifacts
  Glassbox cache artifact list --network mainnet`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cacheDir := getCacheDir()
		manager := cache.NewManager(cacheDir, cache.DefaultConfig())
		artifactCache := cache.NewArtifactCache(manager, 24*time.Hour)

		artifactType, _ := cmd.Flags().GetString("type")
		network, _ := cmd.Flags().GetString("network")

		var artifacts []cache.CachedArtifact
		if artifactType == "" && network == "" {
			// List all artifacts
			var allContracts []cache.CachedArtifact
			mainnetContracts, _ := artifactCache.List(cache.ArtifactContractCode, "mainnet")
			testnetContracts, _ := artifactCache.List(cache.ArtifactContractCode, "testnet")
			futurenetContracts, _ := artifactCache.List(cache.ArtifactContractCode, "futurenet")
			allContracts = append(allContracts, mainnetContracts...)
			allContracts = append(allContracts, testnetContracts...)
			allContracts = append(allContracts, futurenetContracts...)

			var allEntries []cache.CachedArtifact
			mainnetEntries, _ := artifactCache.List(cache.ArtifactLedgerEntry, "mainnet")
			testnetEntries, _ := artifactCache.List(cache.ArtifactLedgerEntry, "testnet")
			futurenetEntries, _ := artifactCache.List(cache.ArtifactLedgerEntry, "futurenet")
			allEntries = append(allEntries, mainnetEntries...)
			allEntries = append(allEntries, testnetEntries...)
			allEntries = append(allEntries, futurenetEntries...)

			artifacts = append(allContracts, allEntries...)
		} else if artifactType != "" {
			artifacts, _ = artifactCache.List(cache.ArtifactType(artifactType), network)
		} else {
			artifacts, _ = artifactCache.List(cache.ArtifactContractCode, network)
		}

		if len(artifacts) == 0 {
			fmt.Println("No cached artifacts found")
			return nil
		}

		fmt.Printf("Cached artifacts (%d):\n", len(artifacts))
		for _, a := range artifacts {
			keyPreview := a.Key
			if len(keyPreview) > 16 {
				keyPreview = keyPreview[:16]
			}
			fmt.Printf("  [%s/%s] %s... (%s)\n", a.Type, a.Network, keyPreview, formatBytes(a.Size))
		}
		return nil
	},
}

var cacheArtifactClearCmd = &cobra.Command{
	Use:   "clear",
	Short: "Clear cached artifacts",
	Long: `Remove all cached contract code or ledger entries.

Use --type to specify the artifact type (contract_code or ledger_entry).
Use --network to limit the clear to a specific network.
Use --force to skip confirmation.`,
	Example: `  # Clear all contract code cache
  Glassbox cache artifact clear --type contract_code --force

  # Clear all ledger entry cache for testnet
  Glassbox cache artifact clear --type ledger_entry --network testnet --force`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		artifactType, _ := cmd.Flags().GetString("type")
		network, _ := cmd.Flags().GetString("network")

		if artifactType == "" || network == "" {
			return fmt.Errorf("--type and --network are required")
		}

		if !cacheForceFlag {
			confirmed, confirmErr := confirmWithForceOrNonInteractive(cmd,
				fmt.Sprintf("This will delete all cached %s artifacts for %s. Continue? (yes/no): ", artifactType, network),
				false, false)
			if confirmErr != nil {
				return confirmErr
			}
			if !confirmed {
				fmt.Println("Cancelled")
				return nil
			}
		}

		cacheDir := getCacheDir()
		manager := cache.NewManager(cacheDir, cache.DefaultConfig())
		artifactCache := cache.NewArtifactCache(manager, 24*time.Hour)

		count, err := artifactCache.Clear(cache.ArtifactType(artifactType), network)
		if err != nil {
			return errors.WrapValidationError(fmt.Sprintf("failed to clear artifacts: %v", err))
		}

		fmt.Printf("Removed %d cached artifacts.\n", count)
		return nil
	},
}

func init() {
	cacheStatusCmd.Flags().BoolVar(&cacheStatusRPCFlag, "rpc", false, "Include persistent RPC cache statistics")

	// Add subcommands to cache command
	cacheCmd.AddCommand(cacheStatusCmd)
	cacheCmd.AddCommand(cacheCleanCmd)
	cacheCmd.AddCommand(cacheClearCmd)
	cacheCmd.AddCommand(cacheRPCCmd)
	cacheCmd.AddCommand(cacheArtifactCmd)

	// Wire artifact subcommands
	cacheArtifactCmd.AddCommand(cacheArtifactListCmd)
	cacheArtifactCmd.AddCommand(cacheArtifactClearCmd)

	cacheArtifactListCmd.Flags().StringP("type", "t", "", "Filter by artifact type (contract_code, ledger_entry)")
	cacheArtifactListCmd.Flags().StringP("network", "n", "", "Filter by network (mainnet, testnet, futurenet)")
	cacheArtifactClearCmd.Flags().StringVarP(&artifactTypeFlag, "type", "t", "", "Artifact type to clear (contract_code, ledger_entry)")
	cacheArtifactClearCmd.Flags().StringVarP(&artifactNetworkFlag, "network", "n", "", "Network to clear (mainnet, testnet, futurenet)")

	// Add flags
	cacheCleanCmd.Flags().BoolVarP(&cacheForceFlag, "force", "f", false, "Skip confirmation prompt")
	cacheClearCmd.Flags().BoolVarP(&cacheForceFlag, "force", "f", false, "Skip confirmation prompt")
	cacheRPCCmd.Flags().IntVar(&cleanOlderThanFlag, "older-than", 0, "Remove entries older than N days")
	cacheRPCCmd.Flags().StringVar(&cleanNetworkFlag, "network", "", "Remove entries for a specific network")
	cacheRPCCmd.Flags().BoolVar(&cleanAllFlag, "all", false, "Remove all RPC cache entries")

	// Add cache command to root
	rootCmd.AddCommand(cacheCmd)
}
