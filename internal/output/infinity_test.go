package output

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/soundcloud/engineering-performance-metrics/internal/metrics"
)

func snapAt(ts time.Time, prsOpened float64) metrics.Snapshot {
	return metrics.Snapshot{
		GeneratedAt: ts,
		Owner:       "soundcloud",
		Repo:        "media-streaming",
		WindowFrom:  ts.Add(-30 * 24 * time.Hour),
		WindowTo:    ts,
		WindowDays:  30,
		Results: []metrics.Result{
			{
				ID:    "prs_opened",
				Title: "Pull requests opened",
				Unit:  "count",
				Value: prsOpened,
				Breakdown: map[string]float64{
					"alice": prsOpened - 1,
					"bob":   1,
				},
			},
		},
	}
}

func readRows(t *testing.T, path string) []flatRow {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading output: %v", err)
	}
	var rows []flatRow
	if err := json.Unmarshal(data, &rows); err != nil {
		t.Fatalf("parsing output: %v", err)
	}
	return rows
}

func TestInfinity_AppendsAcrossRuns(t *testing.T) {
	dir := t.TempDir()
	w := &infinityWriter{dir: dir, fileName: "timeseries.json"}

	t1 := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	t2 := t1.Add(12 * time.Hour)

	if _, err := w.Write(snapAt(t1, 5)); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(snapAt(t2, 8)); err != nil {
		t.Fatal(err)
	}

	rows := readRows(t, filepath.Join(dir, "timeseries.json"))
	// Each run: 1 headline + 2 breakdown rows = 3 rows; two runs = 6.
	if len(rows) != 6 {
		t.Fatalf("row count = %d, want 6", len(rows))
	}

	// Headline for the second run should be 8.
	var got float64
	for _, r := range rows {
		if r.Timestamp.Equal(t2) && r.BreakdownKey == "" {
			got = r.Value
		}
	}
	if got != 8 {
		t.Errorf("t2 headline = %v, want 8", got)
	}
}

func TestInfinity_IdempotentReRun(t *testing.T) {
	dir := t.TempDir()
	w := &infinityWriter{dir: dir, fileName: "timeseries.json"}

	t1 := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)

	if _, err := w.Write(snapAt(t1, 5)); err != nil {
		t.Fatal(err)
	}
	// Re-run for the SAME timestamp with a corrected value.
	if _, err := w.Write(snapAt(t1, 9)); err != nil {
		t.Fatal(err)
	}

	rows := readRows(t, filepath.Join(dir, "timeseries.json"))
	// Still just one run's worth of rows (3), not duplicated to 6.
	if len(rows) != 3 {
		t.Fatalf("row count = %d, want 3 (re-run must replace, not duplicate)", len(rows))
	}
	for _, r := range rows {
		if r.BreakdownKey == "" && r.Value != 9 {
			t.Errorf("headline = %v, want 9 (latest value)", r.Value)
		}
	}
}
