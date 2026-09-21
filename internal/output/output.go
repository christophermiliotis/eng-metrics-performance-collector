// Package output writes a metrics Snapshot to disk in a pluggable format.
//
// Today only JSON is implemented, writing one timestamped file per run so the
// results form a time series ready to ingest into Grafana later. Adding a new
// format (CSV, Prometheus textfile, line protocol, ...) means implementing the
// Writer interface and registering it in New.
package output

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/soundcloud/engineering-performance-metrics/internal/metrics"
)

// Writer persists a snapshot somewhere. Implementations are selected by format
// name from config.
type Writer interface {
	// Write persists the snapshot and returns the location(s) written.
	Write(snap metrics.Snapshot) (path string, err error)
}

// New returns the Writer for the given format, writing into dir.
func New(format, dir string) (Writer, error) {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "json":
		return &jsonWriter{dir: dir}, nil
	case "infinity":
		// Single aggregate, long-format array for the Grafana Infinity datasource.
		return &infinityWriter{dir: dir, fileName: "timeseries.json"}, nil
	default:
		return nil, fmt.Errorf("unknown output format %q (supported: json, infinity)", format)
	}
}

// jsonWriter writes each snapshot to dir/metrics-<RFC3339>.json and refreshes a
// dir/latest.json convenience symlink-equivalent copy.
type jsonWriter struct {
	dir string
}

func (w *jsonWriter) Write(snap metrics.Snapshot) (string, error) {
	if err := os.MkdirAll(w.dir, 0o755); err != nil {
		return "", fmt.Errorf("creating output dir: %w", err)
	}

	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshalling snapshot: %w", err)
	}

	// Filesystem-safe timestamp (no colons) for the filename.
	stamp := snap.GeneratedAt.UTC().Format("2006-01-02T15-04-05Z")
	name := fmt.Sprintf("metrics-%s.json", stamp)
	path := filepath.Join(w.dir, name)

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", fmt.Errorf("writing %s: %w", path, err)
	}

	// Also refresh latest.json so dashboards / quick inspection always have a
	// stable path to the most recent run. Best-effort: a failure here doesn't
	// invalidate the timestamped snapshot already written.
	latest := filepath.Join(w.dir, "latest.json")
	_ = os.WriteFile(latest, data, 0o644)

	return path, nil
}
