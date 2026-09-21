// Package config loads and validates the tool's runtime configuration.
//
// Configuration is layered, in increasing order of precedence:
//
//  1. Built-in defaults (see Default).
//  2. A JSON config file (--config, default ./config.json if present).
//  3. Environment variables (GITHUB_TOKEN, EPM_* overrides).
//  4. Command-line flags.
//
// This makes the tool comfortable both for ad-hoc local runs (flags) and for
// scheduled/CI runs (env vars + a committed config file).
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// Config is the fully resolved configuration for a single run.
type Config struct {
	// GitHub target.
	Owner string `json:"owner"` // e.g. "soundcloud"
	Repo  string `json:"repo"`  // e.g. "media-streaming"

	// APIBaseURL allows pointing at GitHub Enterprise. Defaults to the public
	// github.com REST API. Must not have a trailing slash.
	APIBaseURL string `json:"api_base_url"`

	// Token is the GitHub PAT. It is never read from the JSON file for safety;
	// it is sourced exclusively from the GITHUB_TOKEN environment variable.
	Token string `json:"-"`

	// LookbackDays is the size of the rolling window. Each run measures the
	// window [now-LookbackDays, now].
	LookbackDays int `json:"lookback_days"`

	// DeployWorkflowFile identifies the GitHub Actions workflow whose
	// successful runs count as production deployments, e.g.
	// "deploy-production.yml". Matched against the workflow file's base name.
	DeployWorkflowFile string `json:"deploy_workflow_file"`

	// DeployBranch is the branch deployments are expected to run against
	// (head_branch on the workflow run). Defaults to the repo default branch
	// when empty.
	DeployBranch string `json:"deploy_branch"`

	// OutputDir is where timestamped JSON snapshots are written.
	OutputDir string `json:"output_dir"`

	// OutputFormat selects the output formatter ("json" today; pluggable).
	OutputFormat string `json:"output_format"`

	// Metrics optionally restricts which metrics run. Empty means "all
	// registered metrics". Values are metric IDs (see internal/metrics).
	Metrics []string `json:"metrics"`

	// HTTPTimeoutSeconds bounds each individual GitHub API request.
	HTTPTimeoutSeconds int `json:"http_timeout_seconds"`
}

// Default returns the baseline configuration before file/env/flag overrides.
func Default() Config {
	return Config{
		Owner:              "",
		Repo:               "my-repo",
		APIBaseURL:         "https://api.github.com",
		LookbackDays:       30,
		DeployWorkflowFile: "",
		DeployBranch:       "",
		OutputDir:          "./snapshots",
		OutputFormat:       "json",
		HTTPTimeoutSeconds: 30,
	}
}

// Lookback returns the rolling window duration.
func (c Config) Lookback() time.Duration {
	return time.Duration(c.LookbackDays) * 24 * time.Hour
}

// LoadFile reads and merges a JSON config file into c. A missing file is not an
// error unless required is true (i.e. the user explicitly passed --config).
func (c *Config) LoadFile(path string, required bool) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && !required {
			return nil
		}
		return fmt.Errorf("reading config file %q: %w", path, err)
	}
	// Decode onto the existing struct so unspecified fields keep their
	// current (default) values.
	if err := json.Unmarshal(data, c); err != nil {
		return fmt.Errorf("parsing config file %q: %w", path, err)
	}
	return nil
}

// ApplyEnv overlays environment-variable overrides onto c. The token is always
// taken from GITHUB_TOKEN.
func (c *Config) ApplyEnv() {
	c.Token = strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
	if v := os.Getenv("EPM_OWNER"); v != "" {
		c.Owner = v
	}
	if v := os.Getenv("EPM_REPO"); v != "" {
		c.Repo = v
	}
	if v := os.Getenv("EPM_API_BASE_URL"); v != "" {
		c.APIBaseURL = v
	}
	if v := os.Getenv("EPM_DEPLOY_WORKFLOW_FILE"); v != "" {
		c.DeployWorkflowFile = v
	}
	if v := os.Getenv("EPM_OUTPUT_DIR"); v != "" {
		c.OutputDir = v
	}
}

// Validate checks that the resolved configuration is internally consistent and
// usable. It returns a single error describing the first problem found.
func (c Config) Validate() error {
	switch {
	case c.Token == "":
		return errors.New("missing GitHub token: set the GITHUB_TOKEN environment variable")
	case c.Owner == "":
		return errors.New("owner is required")
	case c.Repo == "":
		return errors.New("repo is required")
	case c.LookbackDays <= 0:
		return errors.New("lookback_days must be > 0")
	case c.OutputDir == "":
		return errors.New("output_dir is required")
	case strings.HasSuffix(c.APIBaseURL, "/"):
		return errors.New("api_base_url must not have a trailing slash")
	}
	return nil
}

// WantsMetric reports whether the metric with the given id should run, honoring
// the optional allow-list. An empty allow-list means "run everything".
func (c Config) WantsMetric(id string) bool {
	if len(c.Metrics) == 0 {
		return true
	}
	for _, m := range c.Metrics {
		if m == id {
			return true
		}
	}
	return false
}
