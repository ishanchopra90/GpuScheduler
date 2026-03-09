#!/usr/bin/env bash
# demo-capture.sh: Poll GPUWorkloads, events, and component logs; dump to an output file.
# Exits when all expected workloads are in a terminal phase (Succeeded/Failed) or after 1 hour.
#
# Usage:
#   ./scripts/demo-capture.sh [OUTPUT_FILE] [WORKLOAD_CONFIG_JSON]
#   ./scripts/demo-capture.sh demo-run.log
#   ./scripts/demo-capture.sh demo-run.log config/demo/contention-10-workloads.json
#
# If WORKLOAD_CONFIG_JSON is provided (same format as producer -config), the script checks
# exactly those workloads (by request_id = CR name). Otherwise checks "at least EXPECTED_COUNT
# and all in namespace terminal". Requires jq when WORKLOAD_CONFIG_JSON is used.
#
# Prerequisites: stack up, apply-sample-pool, producer has been run.
# Run from repo root. Uses kubectl default context.

set -e

KUBECTL="${KUBECTL:-kubectl}"
OPERATOR_NS="${OPERATOR_NS:-gpu-scheduler-system}"
SYSTEM_NS="${SYSTEM_NS:-system}"
WORKLOAD_NS="${WORKLOAD_NS:-default}"
EXPECTED_COUNT="${EXPECTED_COUNT:-10}"
POLL_INTERVAL="${POLL_INTERVAL:-30}"
MAX_DURATION_SEC="${MAX_DURATION_SEC:-3600}"

OUTPUT_FILE="${1:-demo-capture-$(date +%Y%m%d-%H%M%S).log}"
WORKLOAD_CONFIG_JSON="${2:-}"
# If a config path was given but the file does not exist, try with .json appended (common typo).
if [[ -n "$WORKLOAD_CONFIG_JSON" ]] && [[ ! -f "$WORKLOAD_CONFIG_JSON" ]] && [[ -f "${WORKLOAD_CONFIG_JSON}.json" ]]; then
  WORKLOAD_CONFIG_JSON="${WORKLOAD_CONFIG_JSON}.json"
fi
if [[ -n "$WORKLOAD_CONFIG_JSON" ]] && [[ ! -f "$WORKLOAD_CONFIG_JSON" ]]; then
  echo "Error: workload config file not found: $WORKLOAD_CONFIG_JSON" >&2
  exit 1
fi
START_TIME="$(date +%s)"

log() {
  echo "[$(date -u +%Y-%m-%dT%H:%M:%SZ)] $*" | tee -a "$OUTPUT_FILE"
}

section() {
  echo "" >> "$OUTPUT_FILE"
  echo "========== $* ==========" >> "$OUTPUT_FILE"
  echo "" >> "$OUTPUT_FILE"
}

# Build list of expected workload names from config JSON (request_id per item). One per line.
get_expected_names() {
  if [[ -z "$WORKLOAD_CONFIG_JSON" ]] || [[ ! -f "$WORKLOAD_CONFIG_JSON" ]]; then
    return
  fi
  jq -r 'if type == "array" then .[].request_id else .request_id end' "$WORKLOAD_CONFIG_JSON" 2>/dev/null
}

# Report "done" when all expected workloads are Succeeded or Failed.
# - With WORKLOAD_CONFIG_JSON: check exactly those names (from request_id in the JSON).
# - Without config: we don't know names, so we use a heuristic: "done" when (1) there are
#   at least EXPECTED_COUNT GPUWorkloads in the namespace (so we don't stop after only 3
#   have been created and finished), and (2) every GPUWorkload in the namespace has phase
#   Succeeded or Failed (no one still Queued/Scheduled/Running).
all_terminal() {
  if [[ -n "$WORKLOAD_CONFIG_JSON" ]] && [[ -f "$WORKLOAD_CONFIG_JSON" ]]; then
    local name phase count=0
    while IFS= read -r name; do
      [[ -z "$name" ]] && continue
      count=$((count + 1))
      phase=$("$KUBECTL" get gpuworkload "$name" -n "$WORKLOAD_NS" -o jsonpath='{.status.phase}' 2>/dev/null || true)
      if [[ "$phase" != "Succeeded" ]] && [[ "$phase" != "Failed" ]]; then
        return 1
      fi
    done < <(get_expected_names)
    [[ $count -gt 0 ]]
    return
  fi
  # Fallback: no config, so we don't know which names to expect.
  local count
  count=$("$KUBECTL" get gpuworkloads -n "$WORKLOAD_NS" --no-headers 2>/dev/null | wc -l | tr -d ' ')
  if [[ "$count" -lt "$EXPECTED_COUNT" ]]; then
    return 1
  fi
  local non_terminal
  non_terminal=$("$KUBECTL" get gpuworkloads -n "$WORKLOAD_NS" -o jsonpath='{.items[*].status.phase}' 2>/dev/null | tr ' ' '\n' | grep -v -E '^(Succeeded|Failed)$' | wc -l | tr -d ' ')
  [[ "$non_terminal" -eq 0 ]]
}

dump_workloads() {
  section "GPUWorkloads (namespace=$WORKLOAD_NS)"
  "$KUBECTL" get gpuworkloads -n "$WORKLOAD_NS" -o wide >> "$OUTPUT_FILE" 2>&1 || true
}

dump_events() {
  section "Events (involvedObject.kind=GPUWorkload)"
  "$KUBECTL" get events -A --field-selector involvedObject.kind=GPUWorkload --sort-by='.lastTimestamp' -o wide >> "$OUTPUT_FILE" 2>&1 || true
}

dump_logs() {
  section "Operator (manager) logs (tail=300)"
  "$KUBECTL" logs -n "$OPERATOR_NS" deployment/gpu-scheduler-controller-manager -c manager --tail=300 >> "$OUTPUT_FILE" 2>&1 || true

  section "Submitter logs (tail=200)"
  "$KUBECTL" logs -n "$SYSTEM_NS" deployment/submitter --tail=200 >> "$OUTPUT_FILE" 2>&1 || true

  section "Worker-deployment logs (tail=200)"
  "$KUBECTL" logs -n "$SYSTEM_NS" deployment/worker-deployment -c worker --tail=200 >> "$OUTPUT_FILE" 2>&1 || true

  section "Simulator logs (tail=200)"
  "$KUBECTL" logs -n "$SYSTEM_NS" deployment/simulator --tail=200 >> "$OUTPUT_FILE" 2>&1 || true
}

elapsed_sec() {
  echo $(($(date +%s) - START_TIME))
}

expected_desc="$EXPECTED_COUNT workloads"
if [[ -n "$WORKLOAD_CONFIG_JSON" ]] && [[ -f "$WORKLOAD_CONFIG_JSON" ]]; then
  expected_count=$(get_expected_names | grep -c . || true)
  expected_desc="$expected_count workloads from $WORKLOAD_CONFIG_JSON"
fi
log "Starting demo capture: output=$OUTPUT_FILE expected=$expected_desc namespace=$WORKLOAD_NS poll_interval=${POLL_INTERVAL}s max_duration=${MAX_DURATION_SEC}s"
log "Waiting for expected workloads in $WORKLOAD_NS to reach Succeeded or Failed, or until ${MAX_DURATION_SEC}s elapsed."

poll_count=0
while true; do
  poll_count=$((poll_count + 1))
  now=$(date +%s)
  elapsed=$((now - START_TIME))
  if [[ $elapsed -ge $MAX_DURATION_SEC ]]; then
    log "Max duration ${MAX_DURATION_SEC}s reached; stopping."
    break
  fi
  if all_terminal; then
    log "All expected workloads are in a terminal phase (Succeeded/Failed)."
    break
  fi
  log "Poll #$poll_count (elapsed ${elapsed}s): capturing workloads, events, and logs..."
  dump_workloads
  dump_events
  dump_logs
  sleep "$POLL_INTERVAL"
done

section "FINAL: GPUWorkloads"
"$KUBECTL" get gpuworkloads -n "$WORKLOAD_NS" -o wide >> "$OUTPUT_FILE" 2>&1 || true
section "FINAL: Events (GPUWorkload)"
"$KUBECTL" get events -A --field-selector involvedObject.kind=GPUWorkload --sort-by='.lastTimestamp' -o wide >> "$OUTPUT_FILE" 2>&1 || true
section "FINAL: Operator logs (tail=500)"
"$KUBECTL" logs -n "$OPERATOR_NS" deployment/gpu-scheduler-controller-manager -c manager --tail=500 >> "$OUTPUT_FILE" 2>&1 || true
section "FINAL: Submitter logs (tail=300)"
"$KUBECTL" logs -n "$SYSTEM_NS" deployment/submitter --tail=300 >> "$OUTPUT_FILE" 2>&1 || true
section "FINAL: Worker-deployment logs (tail=300)"
"$KUBECTL" logs -n "$SYSTEM_NS" deployment/worker-deployment -c worker --tail=300 >> "$OUTPUT_FILE" 2>&1 || true
section "FINAL: Simulator logs (tail=300)"
"$KUBECTL" logs -n "$SYSTEM_NS" deployment/simulator --tail=300 >> "$OUTPUT_FILE" 2>&1 || true

total_sec=$(elapsed_sec)
log "Demo capture complete. Total duration: ${total_sec}s ($(($total_sec / 60))m $(($total_sec % 60))s). Output: $OUTPUT_FILE"
