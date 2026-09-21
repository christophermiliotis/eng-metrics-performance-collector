// Command metrics collects engineering-performance metrics for a GitHub repo
// over a rolling window and writes a timestamped JSON snapshot.
//
// Typical usage:
//
//	export GITHUB_TOKEN=ghp_...
//	metrics --config config.json
//	metrics --owner soundcloud --repo media-streaming --lookback-days 14 \
//	        --deploy-workflow deploy-production.yml --out ./snapshots
//
// Run on a schedule (e.g. every 12h via cron) to build a time series.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/soundcloud/engineering-performance-metrics/internal/config"
	"github.com/soundcloud/engineering-performance-metrics/internal/github"
	"github.com/soundcloud/engineering-performance-metrics/internal/metrics"
	"github.com/soundcloud/engineering-performance-metrics/internal/output"
)

func main() {
	if err := run(); err != nil {
		log.SetFlags(0)
		log.Fatalf("error: %v", err)
	}
}

func run() error {
	cfg := config.Default()

	// --- Flags. Defaults come from cfg so file/env can pre-seed them. -------
	var (
		configPath     = flag.String("config", "", "path to JSON config file (optional)")
		owner          = flag.String("owner", "", "GitHub repo owner/org (overrides config)")
		repo           = flag.String("repo", "", "GitHub repo name (overrides config)")
		apiBase        = flag.String("api-base-url", "", "GitHub API base URL (for GHE)")
		lookbackDays   = flag.Int("lookback-days", 0, "rolling window size in days (overrides config)")
		deployWorkflow = flag.String("deploy-workflow", "", "deploy workflow file name, e.g. deploy-production.yml")
		deployBranch   = flag.String("deploy-branch", "", "branch deployments run against (default: repo default branch)")
		outDir         = flag.String("out", "", "output directory for snapshots (overrides config)")
		format         = flag.String("format", "", "output format: json (default) or infinity (Grafana Infinity datasource)")
		metricsList    = flag.String("metrics", "", "comma-separated metric IDs to run (default: all)")
		listMetrics    = flag.Bool("list-metrics", false, "print available metric IDs and exit")
		quiet          = flag.Bool("quiet", false, "suppress progress logging")
	)
	flag.Parse()

	if *listMetrics {
		for _, m := range metrics.All() {
			fmt.Printf("%-24s %s\n", m.ID(), m.Title())
		}
		return nil
	}

	// --- Layer config: file -> env -> flags. --------------------------------
	if *configPath != "" {
		if err := cfg.LoadFile(*configPath, true); err != nil {
			return err
		}
	} else {
		// Opportunistically load ./config.json if present.
		if err := cfg.LoadFile("config.json", false); err != nil {
			return err
		}
	}
	cfg.ApplyEnv()
	applyFlags(&cfg, *owner, *repo, *apiBase, *lookbackDays, *deployWorkflow, *deployBranch, *outDir, *format, *metricsList)

	if err := cfg.Validate(); err != nil {
		return err
	}

	logf := metrics.Logf(func(format string, args ...any) {
		if !*quiet {
			log.Printf(format, args...)
		}
	})

	// --- Resolve the window. To = now; From = now - lookback. ---------------
	now := time.Now().UTC()
	from := now.Add(-cfg.Lookback())

	selected := metrics.Selected(cfg.WantsMetric)
	if len(selected) == 0 {
		return fmt.Errorf("no metrics selected (check --metrics / config.metrics)")
	}
	logf("running %d metric(s) for %s/%s over the last %d days", len(selected), cfg.Owner, cfg.Repo, cfg.LookbackDays)

	// --- Context with signal handling so a long run is interruptible. -------
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	gh := github.NewClient(cfg.APIBaseURL, cfg.Token, time.Duration(cfg.HTTPTimeoutSeconds)*time.Second)

	snap, err := metrics.Run(ctx, gh, selected, metrics.RunOptions{
		Owner:              cfg.Owner,
		Repo:               cfg.Repo,
		From:               from,
		To:                 now,
		DeployWorkflowFile: cfg.DeployWorkflowFile,
		DeployBranch:       cfg.DeployBranch,
	}, now, logf)
	if err != nil {
		return err
	}

	w, err := output.New(cfg.OutputFormat, cfg.OutputDir)
	if err != nil {
		return err
	}
	path, err := w.Write(snap)
	if err != nil {
		return err
	}

	logf("wrote snapshot to %s", path)
	printSummary(snap)
	return nil
}

// applyFlags overlays any explicitly-set flags onto cfg. Zero values mean
// "flag not set" (we don't distinguish an explicit --lookback-days=0, which is
// invalid anyway).
func applyFlags(cfg *config.Config, owner, repo, apiBase string, lookbackDays int, deployWorkflow, deployBranch, outDir, format, metricsList string) {
	if owner != "" {
		cfg.Owner = owner
	}
	if repo != "" {
		cfg.Repo = repo
	}
	if apiBase != "" {
		cfg.APIBaseURL = apiBase
	}
	if lookbackDays > 0 {
		cfg.LookbackDays = lookbackDays
	}
	if deployWorkflow != "" {
		cfg.DeployWorkflowFile = deployWorkflow
	}
	if deployBranch != "" {
		cfg.DeployBranch = deployBranch
	}
	if outDir != "" {
		cfg.OutputDir = outDir
	}
	if format != "" {
		cfg.OutputFormat = format
	}
	if metricsList != "" {
		var ids []string
		for _, s := range strings.Split(metricsList, ",") {
			if s = strings.TrimSpace(s); s != "" {
				ids = append(ids, s)
			}
		}
		cfg.Metrics = ids
	}
}

// printSummary prints a compact human-readable digest to stdout so an ad-hoc
// run is useful without opening the JSON file.
func printSummary(snap metrics.Snapshot) {
	fmt.Printf("\n%s/%s — %s window (%s → %s)\n",
		snap.Owner, snap.Repo,
		fmt.Sprintf("%dd", snap.WindowDays),
		snap.WindowFrom.Format("2006-01-02"),
		snap.WindowTo.Format("2006-01-02"))
	fmt.Println(strings.Repeat("-", 60))
	for _, r := range snap.Results {
		fmt.Printf("%-28s %10.2f %s\n", r.Title, r.Value, r.Unit)
	}
	fmt.Println()
}
