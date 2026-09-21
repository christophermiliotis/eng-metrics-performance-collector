package output

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/soundcloud/engineering-performance-metrics/internal/metrics"
)

// infinityWriter maintains a single, ever-growing JSON array of flat rows in
// long (tidy) format, suitable for the Grafana Infinity datasource. Unlike the
// per-run jsonWriter, Infinity reads ONE resource per query and plots best from
// a flat array where each object is one observation, so every run appends its
// rows to the same file.
//
// Each run contributes:
//   - one "headline" row per metric (breakdown_key = ""), and
//   - one row per breakdown entry (breakdown_key = the author/reviewer/stat).
//
// Re-running the tool for an already-recorded timestamp is idempotent: rows
// with a matching timestamp are replaced, not duplicated.
type infinityWriter struct {
	dir      string
	fileName string
}

// flatRow is one observation in the time series. Field names are chosen to be
// convenient as Grafana Infinity columns / filters.
type flatRow struct {
	Timestamp    time.Time `json:"timestamp"` // = snapshot.generated_at (Infinity "time" field)
	Owner        string    `json:"owner"`
	Repo         string    `json:"repo"`
	WindowFrom   time.Time `json:"window_from"`
	WindowTo     time.Time `json:"window_to"`
	WindowDays   int       `json:"window_days"`
	MetricID     string    `json:"metric_id"`
	MetricTitle  string    `json:"metric_title"`
	Unit         string    `json:"unit"`
	BreakdownKey string    `json:"breakdown_key"` // "" = headline value for the metric
	Value        float64   `json:"value"`
}

func (w *infinityWriter) Write(snap metrics.Snapshot) (string, error) {
	if err := os.MkdirAll(w.dir, 0o755); err != nil {
		return "", fmt.Errorf("creating output dir: %w", err)
	}
	path := filepath.Join(w.dir, w.fileName)

	// Load existing rows (if any). A missing or empty file is fine.
	existing, err := loadFlatRows(path)
	if err != nil {
		return "", err
	}

	// Idempotency: drop any rows already recorded for this exact timestamp so a
	// re-run replaces rather than duplicates them.
	ts := snap.GeneratedAt
	kept := existing[:0]
	for _, r := range existing {
		if !r.Timestamp.Equal(ts) {
			kept = append(kept, r)
		}
	}

	// Build this run's rows.
	rows := flattenSnapshot(snap)
	all := append(kept, rows...)

	// Stable order: by timestamp, then metric, then breakdown key. Keeps the
	// file diff-friendly and predictable for anyone eyeballing it.
	sort.SliceStable(all, func(i, j int) bool {
		if !all[i].Timestamp.Equal(all[j].Timestamp) {
			return all[i].Timestamp.Before(all[j].Timestamp)
		}
		if all[i].MetricID != all[j].MetricID {
			return all[i].MetricID < all[j].MetricID
		}
		return all[i].BreakdownKey < all[j].BreakdownKey
	})

	if err := writeJSONAtomic(path, all); err != nil {
		return "", err
	}
	return path, nil
}

// flattenSnapshot converts a Snapshot into long-format rows.
func flattenSnapshot(snap metrics.Snapshot) []flatRow {
	var rows []flatRow
	for _, res := range snap.Results {
		base := flatRow{
			Timestamp:   snap.GeneratedAt,
			Owner:       snap.Owner,
			Repo:        snap.Repo,
			WindowFrom:  snap.WindowFrom,
			WindowTo:    snap.WindowTo,
			WindowDays:  snap.WindowDays,
			MetricID:    res.ID,
			MetricTitle: res.Title,
			Unit:        res.Unit,
		}

		// Headline value.
		headline := base
		headline.BreakdownKey = ""
		headline.Value = res.Value
		rows = append(rows, headline)

		// Breakdown entries (per author / reviewer / sub-stat).
		for key, val := range res.Breakdown {
			br := base
			br.BreakdownKey = key
			br.Value = val
			rows = append(rows, br)
		}
	}
	return rows
}

// loadFlatRows reads and decodes the aggregate file, tolerating a missing file.
func loadFlatRows(path string) ([]flatRow, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	var rows []flatRow
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil, fmt.Errorf("parsing existing time-series file %s (is it valid JSON?): %w", path, err)
	}
	return rows, nil
}

// writeJSONAtomic marshals v and writes it via a temp file + rename so a crash
// mid-write can never corrupt the accumulated time series.
func writeJSONAtomic(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling rows: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("renaming %s -> %s: %w", tmp, path, err)
	}
	return nil
}
