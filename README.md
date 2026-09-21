# engineering-performance-metrics

A small, dependency-free Go tool that collects engineering-performance metrics
from a GitHub repository over a rolling time window and writes a timestamped
JSON snapshot per run. Run it on a schedule (e.g. every 12 hours) to build a
time series you can later feed into Grafana.

## Metrics

| ID | What it measures |
|----|------------------|
| `prs_opened` | Number of PRs opened in the window, broken down per author. |
| `reviews_per_reviewer` | Per reviewer, the number of distinct PRs they reviewed — credited when they **approve** or **leave at least one comment** (a `COMMENTED`/`CHANGES_REQUESTED` review, or a conversation comment). Self-reviews excluded. |
| `pr_time_in_review_days` | Time from a PR being opened to merged, in days. Headline value is the **median**; mean and per-PR series are also recorded. |
| `time_to_first_review_hours` | Time from a (non-draft) PR being opened until the **first human review** — the earliest approval, review comment, or conversation comment from a non-author, non-bot user. In hours; **P75 headline** (surfaces the slow tail), falling back to the **median** when fewer than 8 PRs were reviewed in the window (P75 is unstable on small samples — breakdown key `headline_is_p75` flags which was used). Median/mean/P75 are all in the breakdown. |
| `change_lead_time_days` | **DORA.** Time from merge-to-main until deployed to production, in days. PRs are attributed to the deployment that shipped them via **merge-commit SHA** (compare API). Median headline. |
| `deployment_frequency` | **DORA.** Number of production deployments in the window, plus per-day / per-week rates. |

A "production deployment" is a **successful run of the configured GitHub Actions
deploy workflow** (`deploy_workflow_file`) on the deploy branch.

## Quick start

```sh
export GITHUB_TOKEN=ghp_xxx            # PAT with repo:read + actions:read
cp config.example.json config.json    # edit as needed
go run ./cmd/metrics --config config.json
```

Or fully via flags:

```sh
go run ./cmd/metrics \
  --owner christophermiliotis --repo my-repo \
  --lookback-days 14 \
  --deploy-workflow deploy-production.yml \
  --deploy-branch main \
  --out ./snapshots
```

Build a binary:

```sh
go build -o bin/metrics ./cmd/metrics
./bin/metrics --config config.json
```

### Useful flags

- `--list-metrics` — print available metric IDs and exit.
- `--metrics prs_opened,deployment_frequency` — run only a subset.
- `--quiet` — suppress progress logging.

## Configuration

Resolved in increasing order of precedence: **defaults → `config.json` → environment → flags.**

| Field (JSON) | Flag | Env | Default                   |
|---|---|---|---------------------------|
| `owner` | `--owner` | `EPM_OWNER` | _empty_                   |
| `repo` | `--repo` | `EPM_REPO` | _empty_         |
| `api_base_url` | `--api-base-url` | `EPM_API_BASE_URL` | `https://api.github.com`  |
| `lookback_days` | `--lookback-days` | – | `30`                      |
| `deploy_workflow_file` | `--deploy-workflow` | `EPM_DEPLOY_WORKFLOW_FILE` | _(empty)_                 |
| `deploy_branch` | `--deploy-branch` | – | repo default branch       |
| `output_dir` | `--out` | `EPM_OUTPUT_DIR` | `./snapshots`             |
| `output_format` | `--format` | – | `json` (also: `infinity`) |
| `metrics` | `--metrics` | – | all                       |
| `http_timeout_seconds` | – | – | `30`                      |

**Bot filtering** is automatic and needs no configuration: GitHub App accounts
(dependabot, renovate, gemini-code-assist, …) always have a `[bot]` suffix on
their login, so any actor whose login ends in `[bot]` is excluded from
`prs_opened` (bot-authored PRs), `reviews_per_reviewer` (bot reviewers), and
`time_to_first_review_hours` (bot reviews don't count as the first human
review).

The **token is only ever read from `GITHUB_TOKEN`** — never the config file.

> **Set `deploy_workflow_file` before relying on the DORA metrics.** It's the
> workflow file's base name, e.g. `deploy-production.yml`. Without it,
> `deployment_frequency` reports 0 and `change_lead_time_days` cannot be
> computed (both say so in their `notes`).

## Output

One file per run: `snapshots/metrics-2026-06-26T12-00-00Z.json`, plus a
`snapshots/latest.json` copy of the most recent run. Shape:

```jsonc
{
  "generated_at": "2026-06-26T12:00:00Z",
  "owner": "christophermiliotis",
  "repo": "my-repo",
  "window_from": "2026-05-27T12:00:00Z",
  "window_to": "2026-06-26T12:00:00Z",
  "window_days": 30,
  "default_branch": "main",
  "deploy_workflow": "deploy-production.yml",
  "results": [
    {
      "id": "prs_opened",
      "title": "Pull requests opened",
      "unit": "count",
      "value": 42,
      "breakdown": { "alice": 12, "bob": 9 },
      "notes": "..."
    }
  ]
}
```

Each result carries a headline `value`, an optional `breakdown` map (per
author/reviewer or sub-stats), an optional per-item `series`, and human-readable
`notes` documenting caveats.

## Visualising in Grafana (Infinity datasource)

Set `output_format` to `infinity` (or run with `--format infinity`). Instead of
one file per run, the tool maintains a **single, ever-growing** array of flat
rows in `<output_dir>/timeseries.json` — the long ("tidy") format the
[Infinity datasource](https://grafana.com/grafana/plugins/yesoreyeram-infinity-datasource/)
plots from directly. No database or scraper required.

Each run appends:

- one **headline** row per metric (`breakdown_key: ""`), and
- one row per **breakdown** entry (author, reviewer, or sub-stat).

Re-running for an already-recorded `timestamp` **replaces** those rows rather
than duplicating them, and writes are atomic (temp file + rename) so a crash
mid-write can't corrupt the accumulated series.

Row shape:

```jsonc
[
  {
    "timestamp": "2026-07-01T12:00:00Z",   // = generated_at; Infinity "time" field
    "owner": "christophermiliotis",
    "repo": "my-repo",
    "window_from": "2026-06-01T12:00:00Z",
    "window_to": "2026-07-01T12:00:00Z",
    "window_days": 30,
    "metric_id": "prs_opened",
    "metric_title": "Pull requests opened",
    "unit": "count",
    "breakdown_key": "",                    // "" = headline; else author/reviewer/stat
    "value": 42
  },
  { "...": "...", "metric_id": "prs_opened", "breakdown_key": "alice", "value": 12 }
]
```

### Setup

1. **Install the plugin** (once, on your Grafana instance):
   ```sh
   grafana-cli plugins install yesoreyeram-infinity-datasource
   # then restart Grafana
   ```
2. **Expose `timeseries.json`.** Infinity needs a URL (or a local path, if
   enabled in the plugin settings). Simplest: point `output_dir` somewhere
   Grafana can reach and serve the file over HTTP — an internal static host, an
   S3/GCS object, or even `python3 -m http.server` in the output dir for a quick
   local trial.
3. **Add the datasource:** Grafana → Connections → Add data source → **Infinity**.
   Defaults are fine; optionally set the base URL under *URL, Headers & Params*.
4. **Build a panel.** Add a panel, pick the Infinity datasource, and configure
   the query:
   - **Type:** JSON · **Source:** URL · **Format:** Table
   - **URL:** `https://.../timeseries.json`
   - **Rows/Root selector:** leave blank (the file is a top-level array)
   - **Columns:** `timestamp` (as *Time*), `value` (as *Number*), plus
     `metric_id`, `breakdown_key`, `repo` (as *String*) for filtering.

### Suggested panels

| Panel | Query filter | Notes |
|---|---|---|
| PRs opened over time | `metric_id = prs_opened`, `breakdown_key = ""` | Time series; headline trend. |
| PRs opened per author | `metric_id = prs_opened`, `breakdown_key != ""` | Bar chart or time series grouped by `breakdown_key`. |
| Reviews per reviewer | `metric_id = reviews_per_reviewer`, `breakdown_key != ""` | Bar/table, top-N reviewers. |
| Median PR review time | `metric_id = pr_time_in_review_days`, `breakdown_key = median_days` | Time series; watch the trend. |
| Deployment frequency | `metric_id = deployment_frequency`, `breakdown_key = ""` | Stat + time series. |
| Change lead time (DORA) | `metric_id = change_lead_time_days`, `breakdown_key = median_days` | Time series; DORA scorecard. |

Filter rows in the Infinity query (or with Grafana **Filter data by values** /
**Filter by name** transformations). Use a dashboard **variable** on
`metric_id` / `breakdown_key` to make panels reusable, and a filter on `repo`
so the same dashboard serves multiple repos once you point the tool at more
than one.

> **Note:** Infinity re-fetches the whole file per query. At a 12-hour cadence
> the file stays small for years, so no pruning is needed for a single team's repo.

## Scheduling (later)

The tool is **stateless** — each invocation writes one snapshot. To capture a
12-hour cadence, run it from cron:

```cron
0 */12 * * * cd /path/to/engineering-performance-metrics && GITHUB_TOKEN=ghp_xxx ./bin/metrics --config config.json >> run.log 2>&1
```

## Adding a new metric

Adding a metric is a **single-file change** — no central list to edit:

1. Create `internal/metrics/<your_metric>.go`.
2. Define a type with `ID()`, `Title()`, and `Compute(ctx, *Window) (Result, error)`.
3. Optionally implement `Needs() DataNeeds` to declare whether you need per-PR
   reviews/comments or deployment data (lets the collector skip expensive
   fetches when nothing needs them). Omit it and everything is fetched.
4. `Register(&yourMetric{})` from an `init()` in the file.

The runner discovers it via the registry automatically. See
`prs_opened.go` for the simplest example and `change_lead_time.go` for one that
uses deployment + SHA-attribution data.

## Design notes & caveats

- **Stdlib only.** No third-party dependencies; the GitHub client
  (`internal/github`) covers just the handful of REST endpoints needed, with
  pagination and rate-limit back-off.
- **One data fetch, many metrics.** A single `Window` is collected once and
  shared, so adding metrics doesn't multiply API calls.
- **Lead-time attribution** walks `compare(prevDeploySHA...thisDeploySHA)` to
  find exactly which commits each deploy shipped, then matches PR merge commits.
  Squash/rebase merges keep the merge-commit SHA, so they attribute correctly;
  if a PR's merge SHA can't be matched to a deploy it's reported under
  `merged_not_deployed` rather than skewing the median.
- **Draft PRs:** GitHub's list payload doesn't expose the draft→ready
  transition time, so review time for PRs opened as drafts is measured from
  `created_at`. Noted in the metric output.
- **Output is pluggable.** Two formats today: `json` (one file per run) and
  `infinity` (a single aggregate long-format array for Grafana). Adding CSV /
  Prometheus textfile / InfluxDB line protocol means implementing the
  `output.Writer` interface and a case in `output.New`.

## Tests

```sh
go test ./...
```

Metric computations are pure functions of the `Window`, so they're unit-tested
without any network access (`internal/metrics/metrics_test.go`).
