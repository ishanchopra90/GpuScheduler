#!/usr/bin/env bash
# grafana-export-csv.sh: Export Prometheus data backing Grafana dashboards to CSV files.
# Reads panel expressions from config/grafana/dashboards/*.json, calls Prometheus query_range,
# and writes one CSV per panel into a configurable output directory.
# Filenames include the dashboard row/section title when present (e.g. Operator_manager_Memory_working_set.csv)
# so panels that share the same title (e.g. "Memory (working set)") get distinct files per component.
#
# Usage:
#   ./scripts/grafana-export-csv.sh --output-dir /path/to/csv_data
#   ./scripts/grafana-export-csv.sh --output-dir ./raw_data/baseline_demo/csv_data --last 2h
#   ./scripts/grafana-export-csv.sh --output-dir ./out --start 1700000000 --end 1700003600
#   ./scripts/grafana-export-csv.sh --output-dir ./out --start-datetime "2026-03-10 17:50:00" --end-datetime "2026-03-10 18:07:59"
#   ./scripts/grafana-export-csv.sh --output-dir ./out --prometheus-url http://localhost:9090 --namespace-regex 'system|gpu-scheduler-system'
#
# Options:
#   --output-dir DIR    (required) Directory to write CSV files into.
#   --prometheus-url URL  Prometheus base URL (default: http://localhost:9090).
#   --start TIMESTAMP   Start time (Unix seconds). With --end, overrides --last.
#   --end TIMESTAMP     End time (Unix seconds).
#   --start-datetime DT Start time as "YYYY-MM-DD HH:MM:SS" (local time). Use with --end-datetime.
#   --end-datetime DT   End time as "YYYY-MM-DD HH:MM:SS" (local time).
#   --last DURATION     Time window before now, e.g. 1h, 30m (default: 1h). Ignored if --start/--end or --start-datetime/--end-datetime set.
#   --namespace-regex RE  Regex for Grafana $namespace variable in queries (default: system|gpu-scheduler-system).
#   --dashboards-dir DIR  Directory containing dashboard JSON files (default: config/grafana/dashboards).
#
# Prerequisites: curl, jq. Prometheus must be reachable (e.g. port-forward).
# Run from repo root so default --dashboards-dir resolves.

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
OUTPUT_DIR=""
PROMETHEUS_URL="${PROMETHEUS_URL:-http://localhost:9090}"
START=""
END=""
START_DATETIME=""
END_DATETIME=""
LAST="1h"
NAMESPACE_REGEX="${NAMESPACE_REGEX:-system|gpu-scheduler-system}"
DASHBOARDS_DIR="$REPO_ROOT/config/grafana/dashboards"

usage() {
  echo "Usage:"
  echo "  $0 --output-dir DIR [OPTIONS]"
  echo ""
  echo "Options:"
  echo "  --output-dir DIR       (required) Directory to write CSV files into."
  echo "  --prometheus-url URL    Prometheus base URL (default: http://localhost:9090)."
  echo "  --start TIMESTAMP       Start time (Unix seconds). With --end, overrides --last."
  echo "  --end TIMESTAMP         End time (Unix seconds)."
  echo "  --start-datetime DT     Start time as \"YYYY-MM-DD HH:MM:SS\" (local time). Use with --end-datetime."
  echo "  --end-datetime DT       End time as \"YYYY-MM-DD HH:MM:SS\" (local time)."
  echo "  --last DURATION         Time window before now, e.g. 1h, 30m (default: 1h). Ignored if --start/--end or datetime set."
  echo "  --namespace-regex RE    Regex for Grafana \$namespace variable in queries (default: system|gpu-scheduler-system)."
  echo "  --dashboards-dir DIR    Directory containing dashboard JSON files (default: config/grafana/dashboards)."
  echo ""
  echo "Example: $0 --output-dir /Users/you/Code/GpuScheduler/raw_data/baseline_demo/csv_data --last 1h"
  exit 1
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --output-dir)
      OUTPUT_DIR="$2"
      shift 2
      ;;
    --prometheus-url)
      PROMETHEUS_URL="${2%/}"
      shift 2
      ;;
    --start)
      START="$2"
      shift 2
      ;;
    --end)
      END="$2"
      shift 2
      ;;
    --last)
      LAST="$2"
      shift 2
      ;;
    --start-datetime)
      START_DATETIME="$2"
      shift 2
      ;;
    --end-datetime)
      END_DATETIME="$2"
      shift 2
      ;;
    --namespace-regex)
      NAMESPACE_REGEX="$2"
      shift 2
      ;;
    --dashboards-dir)
      DASHBOARDS_DIR="$2"
      shift 2
      ;;
    -h|--help)
      usage
      ;;
    *)
      echo "Unknown option: $1" >&2
      usage
      ;;
  esac
done

if [[ -z "$OUTPUT_DIR" ]]; then
  echo "Error: --output-dir is required" >&2
  usage
fi

# Resolve relative output dir from repo root
if [[ "$OUTPUT_DIR" != /* ]]; then
  OUTPUT_DIR="$REPO_ROOT/$OUTPUT_DIR"
fi
mkdir -p "$OUTPUT_DIR"

# Find jq; prepend Homebrew paths so script finds jq when run under bash (e.g. without .zshrc).
if [[ -z "$JQ" ]]; then
  for dir in /opt/homebrew/bin /usr/local/bin; do
    if [[ -x "$dir/jq" ]]; then
      export PATH="$dir:$PATH"
      break
    fi
  done
  JQ=$(command -v jq 2>/dev/null) || true
fi
if [[ -z "$JQ" ]]; then
  echo "Error: jq not found. Install with: brew install jq (or set JQ=/path/to/jq)" >&2
  exit 1
fi

# Convert "YYYY-MM-DD HH:MM:SS" to Unix seconds (local time). Portable: macOS (BSD date) vs Linux (GNU date).
datetime_to_unix() {
  local dt="$1"
  if date --version >/dev/null 2>&1; then
    date -d "$dt" +%s
  else
    date -j -f "%Y-%m-%d %H:%M:%S" "$dt" +%s
  fi
}

# If --start-datetime/--end-datetime were given, convert to Unix and set START/END
if [[ -n "$START_DATETIME" && -n "$END_DATETIME" ]]; then
  if [[ -n "$START" || -n "$END" ]]; then
    echo "Error: use either --start/--end or --start-datetime/--end-datetime, not both" >&2
    exit 1
  fi
  START=$(datetime_to_unix "$START_DATETIME") || true
  END=$(datetime_to_unix "$END_DATETIME") || true
  if [[ -z "$START" || ! "$START" =~ ^[0-9]+$ ]]; then
    echo "Error: invalid --start-datetime '$START_DATETIME' (use YYYY-MM-DD HH:MM:SS)" >&2
    exit 1
  fi
  if [[ -z "$END" || ! "$END" =~ ^[0-9]+$ ]]; then
    echo "Error: invalid --end-datetime '$END_DATETIME' (use YYYY-MM-DD HH:MM:SS)" >&2
    exit 1
  fi
fi

# Compute start/end (Unix seconds)
if [[ -n "$START" && -n "$END" ]]; then
  if [[ "$START" == *[-:]* || "$END" == *[-:]* ]]; then
    echo "Error: --start and --end expect Unix timestamps (numbers). For absolute times use --start-datetime and --end-datetime (e.g. \"YYYY-MM-DD HH:MM:SS\")." >&2
    exit 1
  fi
  START_TS="$START"
  END_TS="$END"
else
  END_TS=$(date +%s)
  if [[ "$LAST" =~ ^([0-9]+)(s|m|h|d)$ ]]; then
    NUM="${BASH_REMATCH[1]}"
    UNIT="${BASH_REMATCH[2]}"
    case "$UNIT" in
      s) SEC=$NUM ;;
      m) SEC=$((NUM * 60)) ;;
      h) SEC=$((NUM * 3600)) ;;
      d) SEC=$((NUM * 86400)) ;;
    esac
    START_TS=$((END_TS - SEC))
  else
    echo "Error: --last must be like 1h, 30m, 90s" >&2
    exit 1
  fi
fi

# Step for query_range: 15s for range <= 2h, else 60s
RANGE_SEC=$((END_TS - START_TS))
if [[ $RANGE_SEC -le 7200 ]]; then
  STEP="15s"
else
  STEP="60s"
fi

QUERY_RANGE_URL="${PROMETHEUS_URL}/api/v1/query_range"

# Substitute $namespace in expression for Grafana template variable
subst_expr() {
  local expr="$1"
  echo "$expr" | sed "s/\\\$namespace/${NAMESPACE_REGEX}/g"
}

# Sanitize panel title for filename: only alphanumeric, underscore, hyphen
sanitize_filename() {
  echo "$1" | sed 's/[^a-zA-Z0-9_-]/_/g' | sed 's/__*/_/g' | sed 's/^_//;s/_$//'
}

# Fetch query_range and output CSV to stdout. Header: timestamp, <label columns>, value
# Timestamp is Unix seconds (no strftime; works with any jq). Prometheus matrix: .data.result[].values ([[ts,"val"],...])
prom_to_csv() {
  local json="$1"
  local panel_title="$2"
  if ! echo "$json" | "$JQ" -e '.status == "success" and .data.result != null' >/dev/null 2>&1; then
    return 1
  fi
  # Build CSV: collect all label keys across series, then output rows
  echo "$json" | "$JQ" -r '
    .data.result as $results
    | (if ($results | length) == 0 then []
      else ($results | map(.metric | keys) | add | unique) end) as $allKeys
    | (if $allKeys == null then [] else $allKeys end) as $keys
    | ["timestamp", ($keys | .[]), "value"],
      (
        $results[]
        | .metric as $m
        | .values[]?
        | .[0] as $ts
        | .[1] as $v
        | ([($ts | tonumber)] + (($keys // []) | map($m[.] // "")) + [$v])
        | @csv
      )
  '
}

exported=0
failed=0

for dash_file in "$DASHBOARDS_DIR"/*.json; do
  [[ -f "$dash_file" ]] || continue
  dash_name="$(basename "$dash_file" .json)"
  # Emit each data panel with its expr and the current row title (section) so filenames are unique.
  # Panels are in order; row panels (type "row") set the section; data panels get id, title, expr, row.
  panels_json=$("$JQ" -c '
    .panels
    | reduce .[] as $p ({row: "", out: []};
        if $p.type == "row" then .row = ($p.title // "")
        elif $p.targets != null and ($p.targets | length) > 0 then .out = .out + [{id: $p.id, title: $p.title, expr: $p.targets[0].expr, row: .row}]
        else . end)
    | .out[]
  ' "$dash_file" 2>/dev/null)
  if [[ -z "$panels_json" ]]; then
    continue
  fi
  while IFS= read -r panel; do
    title=$(echo "$panel" | "$JQ" -r '.title')
    expr=$(echo "$panel" | "$JQ" -r '.expr')
    row=$(echo "$panel" | "$JQ" -r '.row')
    expr=$(subst_expr "$expr")
    safe_title=$(sanitize_filename "$title")
    safe_dash=$(sanitize_filename "$dash_name")
    if [[ -n "$row" ]]; then
      safe_row=$(sanitize_filename "$row")
      out_file="${OUTPUT_DIR}/${safe_dash}_${safe_row}_${safe_title}.csv"
    else
      out_file="${OUTPUT_DIR}/${safe_dash}_${safe_title}.csv"
    fi
    encoded_query=$(printf '%s' "$expr" | "$JQ" -sRr @uri)
    url="${QUERY_RANGE_URL}?query=${encoded_query}&start=${START_TS}&end=${END_TS}&step=${STEP}"
    resp=$(curl -sS -w "\n%{http_code}" "$url" 2>/dev/null) || true
    http_code=$(echo "$resp" | tail -n1)
    body=$(echo "$resp" | sed '$d')
    if [[ "$http_code" != "200" ]]; then
      echo "Warning: $dash_name / $title -> HTTP $http_code" >&2
      ((failed++)) || true
      continue
    fi
    if ! csv=$(prom_to_csv "$body" "$title"); then
      echo "Warning: $dash_name / $title -> no data or error" >&2
      ((failed++)) || true
      continue
    fi
    echo "$csv" > "$out_file"
    echo "Wrote $out_file"
    ((exported++)) || true
  done <<< "$panels_json"
done

echo ""
echo "Exported $exported panel(s) to $OUTPUT_DIR"
if [[ $failed -gt 0 ]]; then
  echo "Failed/skipped: $failed" >&2
  exit 1
fi
