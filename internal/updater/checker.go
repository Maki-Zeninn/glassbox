// Copyright 2026 Glassbox Users
// SPDX-License-Identifier: Apache-2.0

package updater

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/hashicorp/go-version"
)

const (
	// GitHubAPIURL is the endpoint for fetching the latest release.
	// GitHub unauthenticated API rate limit is 60 req/hr per IP. The Checker
	// honours CheckInterval to stay well below this. In shared CI environments,
	// set GITHUB_TOKEN to use authenticated requests with a 5000 req/hr limit.
	GitHubAPIURL = "https://api.github.com/repos/dotandev/glassbox/releases/latest"
	// CheckInterval is how often we check for updates (24 hours)
	CheckInterval = 24 * time.Hour
	// UpdateCheckTimeout is the maximum time to wait for GitHub API
	UpdateCheckTimeout = 5 * time.Second
)

// Checker handles update checking logic
type Checker struct {
	currentVersion string
	cacheDir       string
	apiURL         string       // overrides GitHubAPIURL when non-empty (for tests)
	httpClient     *http.Client // overrides the default client when non-nil (for tests)
}

// GitHubRelease represents the GitHub API response for a release
type GitHubRelease struct {
	TagName string `json:"tag_name"`
	Body    string `json:"body"`
}

// CacheData stores the last check timestamp and latest version
type CacheData struct {
	LastCheck     time.Time `json:"last_check"`
	LatestVersion string    `json:"latest_version"`
}

// NewChecker creates a new update checker
func NewChecker(currentVersion string) *Checker {
	cacheDir := getCacheDir()
	return &Checker{
		currentVersion: currentVersion,
		cacheDir:       cacheDir,
	}
}

// CheckForUpdates runs the update check in a goroutine (non-blocking)
func (c *Checker) CheckForUpdates() {
	// Check if update checking is disabled
	if c.isUpdateCheckDisabled() {
		return
	}

	// Check if we should perform the check based on cache
	shouldCheck, err := c.shouldCheck()
	if err != nil || !shouldCheck {
		return
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), UpdateCheckTimeout)
	defer cancel()

	// Fetch latest version from GitHub
	latestVersion, err := c.fetchLatestVersion(ctx)
	if err != nil {
		// Silent failure - don't bother the user
		return
	}

	// Update cache with the latest version (banner is shown from cache at next run start)
	if err := c.updateCache(latestVersion); err != nil {
		// Silent failure
		return
	}
	// Do not display here; banner is shown once per run from ShowBannerFromCache to avoid
	// mid-output or duplicate messages.
}

// shouldCheck determines if we should check based on cache
func (c *Checker) shouldCheck() (bool, error) {
	cacheFile := filepath.Join(c.cacheDir, "last_update_check")

	// The cache file stores CacheData JSON. If the file is absent or
	// unreadable, treat this as "check now" to ensure updates are detected on
	// first run.
	data, err := os.ReadFile(cacheFile)
	if err != nil {
		// Cache doesn't exist or can't be read - should check
		return true, nil
	}

	var cache CacheData
	if err := json.Unmarshal(data, &cache); err != nil {
		// Corrupted cache - should check
		return true, nil
	}

	// Check if enough time has passed
	return time.Since(cache.LastCheck) >= CheckInterval, nil
}

// fetchLatestVersion calls the GitHub API to get the latest release tag.
// Uses c.apiURL when set (allows test override) and c.httpClient when non-nil.
func (c *Checker) fetchLatestVersion(ctx context.Context) (string, error) {
	url := c.apiURL
	if url == "" {
		url = GitHubAPIURL
	}

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", err
	}

	// Set User-Agent header (GitHub API requires it)
	req.Header.Set("User-Agent", "Glassbox-cli")
	req.Header.Set("Accept", "application/vnd.github+json")

	hc := c.httpClient
	if hc == nil {
		hc = &http.Client{Timeout: UpdateCheckTimeout}
	}

	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	// Handle rate limiting or other errors silently
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var release GitHubRelease
	if err := json.Unmarshal(body, &release); err != nil {
		return "", fmt.Errorf("malformed release metadata: %w", err)
	}

	tag := strings.TrimSpace(release.TagName)
	if tag == "" {
		return "", fmt.Errorf("release metadata missing tag_name")
	}
	// Validate that the tag looks like a semver-ish string before accepting it.
	// This guards against a compromised or malformed API response supplying
	// arbitrary data that could confuse the version comparator or the banner.
	if !isValidVersionTag(tag) {
		return "", fmt.Errorf("release tag %q does not look like a valid version", tag)
	}

	return tag, nil
}

// isValidVersionTag returns true when tag is a plausible semver/calver string.
// It intentionally accepts only printable ASCII that starts with an optional "v"
// followed by digits and dots (with optional pre-release suffixes like "-rc1").
// Any URL or shell metacharacter in the tag causes rejection.
func isValidVersionTag(tag string) bool {
	if len(tag) == 0 || len(tag) > 64 {
		return false
	}
	t := strings.TrimPrefix(tag, "v")
	if len(t) == 0 {
		return false
	}
	// Must start with a digit.
	if t[0] < '0' || t[0] > '9' {
		return false
	}
	// Allow [0-9A-Za-z.+-] only.
	for _, ch := range t {
		if !((ch >= '0' && ch <= '9') || (ch >= 'A' && ch <= 'Z') ||
			(ch >= 'a' && ch <= 'z') || ch == '.' || ch == '+' || ch == '-') {
			return false
		}
	}
	return true
}

// FetchReleaseInfo gets full information for a specific version or latest
func (c *Checker) FetchReleaseInfo(ctx context.Context, ver string) (*GitHubRelease, error) {
	base := c.apiURL
	if base == "" {
		base = GitHubAPIURL
	}

	url := base
	if ver != "" && ver != "latest" {
		if !strings.HasPrefix(ver, "v") {
			ver = "v" + ver
		}
		url = fmt.Sprintf("https://api.github.com/repos/dotandev/glassbox/releases/tags/%s", ver)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", "Glassbox-cli")
	req.Header.Set("Accept", "application/vnd.github+json")

	hc := c.httpClient
	if hc == nil {
		hc = &http.Client{Timeout: UpdateCheckTimeout}
	}

	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("release %s not found", ver)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code from GitHub: %d", resp.StatusCode)
	}

	var release GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, err
	}

	return &release, nil
}

// newTestChecker constructs a Checker with an overridden API URL and HTTP
// client, used exclusively by tests to avoid real network calls.
func newTestChecker(currentVersion, cacheDir, apiURL string, hc *http.Client) *Checker {
	return &Checker{
		currentVersion: currentVersion,
		cacheDir:       cacheDir,
		apiURL:         apiURL,
		httpClient:     hc,
	}
}

// compareVersions compares current vs latest version
func (c *Checker) compareVersions(current, latest string) (bool, error) {
	// Strip 'v' prefix if present
	current = strings.TrimPrefix(current, "v")
	latest = strings.TrimPrefix(latest, "v")

	// Skip comparison if running dev version
	if current == "dev" || current == "" {
		return false, nil
	}

	currentVer, err := version.NewVersion(current)
	if err != nil {
		return false, err
	}

	latestVer, err := version.NewVersion(latest)
	if err != nil {
		return false, err
	}

	// Return true if latest is greater than current
	return latestVer.GreaterThan(currentVer), nil
}

// displayNotification prints the update message to stderr (small one-line banner)
func (c *Checker) displayNotification(latestVersion string) {
	message := fmt.Sprintf(
		"Upgrade available: %s — run 'go install github.com/dotandev/glassbox/cmd/glassbox@latest' to update\n",
		latestVersion,
	)
	fmt.Fprint(os.Stderr, message)
}

func (c *Checker) PerformUpdate(ctx context.Context, version string) error {
	// For idempotency, if the version requested is already what we have, skip
	if version != "" && version != "latest" {
		v := strings.TrimPrefix(version, "v")
		cur := strings.TrimPrefix(c.currentVersion, "v")
		if v == cur {
			return nil // Already at version
		}
	}

	target := "github.com/dotandev/glassbox/cmd/glassbox@latest"
	if version != "" && version != "latest" {
		if !strings.HasPrefix(version, "v") {
			version = "v" + version
		}
		target = fmt.Sprintf("github.com/dotandev/glassbox/cmd/glassbox@%s", version)
	}

	fmt.Printf("Updating to %s via 'go install'...\n", target)

	// Prepare go install command
	cmd := exec.CommandContext(ctx, "go", "install", target)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("update failed: %w", err)
	}

	fmt.Println("Update successful. Please restart Glassbox.")
	return nil
}

// ShowBannerFromCache reads the last update check cache and, if a newer version
// was found, prints a small "Upgrade available" banner to stderr. Called at CLI
// start so the banner appears once per run without blocking. Skips if update
// checking is disabled or cache is missing/invalid.
func ShowBannerFromCache(currentVersion string) {
	c := NewChecker(currentVersion)
	c.showBannerFromCache()
}

// ShowBannerFromCacheWithCacheDir is for testing: same as ShowBannerFromCache
// but uses the given cache directory (full path to the Glassbox cache dir, e.g. …/Glassbox).
func ShowBannerFromCacheWithCacheDir(currentVersion, cacheDir string) {
	c := &Checker{currentVersion: currentVersion, cacheDir: cacheDir}
	c.showBannerFromCache()
}

// showBannerFromCache reads cache and displays the banner if cached latest > current.
func (c *Checker) showBannerFromCache() {
	if c.isUpdateCheckDisabled() {
		return
	}
	cacheFile := filepath.Join(c.cacheDir, "last_update_check")
	data, err := os.ReadFile(cacheFile)
	if err != nil {
		return
	}
	var cache CacheData
	if err := json.Unmarshal(data, &cache); err != nil || cache.LatestVersion == "" {
		return
	}
	needsUpdate, err := c.compareVersions(c.currentVersion, cache.LatestVersion)
	if err != nil || !needsUpdate {
		return
	}
	c.displayNotification(cache.LatestVersion)
}

// updateCache updates the cache file with the latest check time and version
func (c *Checker) updateCache(latestVersion string) error {
	// Ensure cache directory exists
	if err := os.MkdirAll(c.cacheDir, 0755); err != nil {
		return err
	}

	cache := CacheData{
		LastCheck:     time.Now(),
		LatestVersion: latestVersion,
	}

	data, err := json.Marshal(cache)
	if err != nil {
		return err
	}

	cacheFile := filepath.Join(c.cacheDir, "last_update_check")
	return os.WriteFile(cacheFile, data, 0644)
}

// isUpdateCheckDisabled checks if the user has opted out
func (c *Checker) isUpdateCheckDisabled() bool {
	// Check environment variable (takes precedence)
	if os.Getenv("GLASSBOX_NO_UPDATE_CHECK") != "" {
		return true
	}

	// Check config file
	configPath := getConfigPath()
	if configPath != "" {
		if disabled := checkConfigFile(configPath); disabled {
			return true
		}
	}

	return false
}

// getConfigPath returns the path to the config file
func getConfigPath() string {
	// Try to use OS-specific config directory
	if configDir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(configDir, "glassbox", "config.yaml")
	}

	// Fallback to home directory
	if homeDir, err := os.UserHomeDir(); err == nil {
		return filepath.Join(homeDir, ".config", "glassbox", "config.yaml")
	}

	return ""
}

// checkConfigFile reads the config file and checks if updates are disabled
func checkConfigFile(configPath string) bool {
	data, err := os.ReadFile(configPath)
	if err != nil {
		// Config file doesn't exist or can't be read - updates are enabled
		return false
	}

	// Simple YAML parsing - look for "check_for_updates: false"
	// This is a basic implementation that avoids adding a YAML dependency
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		// Check for "check_for_updates: false" or "check_for_updates:false"
		if strings.HasPrefix(line, "check_for_updates:") {
			value := strings.TrimSpace(strings.TrimPrefix(line, "check_for_updates:"))
			if value == "false" {
				return true
			}
		}
	}

	return false
}

// getCacheDir returns the appropriate cache directory for the platform
func getCacheDir() string {
	// Try to use OS-specific cache directory
	if cacheDir, err := os.UserCacheDir(); err == nil {
		return filepath.Join(cacheDir, "glassbox")
	}

	// Fallback to home directory
	if homeDir, err := os.UserHomeDir(); err == nil {
		return filepath.Join(homeDir, ".cache", "glassbox")
	}

	// Last resort - use temp directory
	return filepath.Join(os.TempDir(), "glassbox")
}
