#!/usr/bin/env bash
# demo-preemption.sh: Run preemption demo in one shot.
# (1) Submit 8 low-priority workloads (preemption-low-8.json).
# (2) Poll until 8 GPUWorkloads are Running or Scheduled.
# (3) Submit 1 high-priority workload (preemption-high-1.json).
#
# Usage:
#   ./scripts/demo-preemption.sh
#   WORKLOAD_NS=default BROKERS=localhost:9092 ./scripts/demo-preemption.sh
#
# Prerequisites: stack up, apply-sample-pool, Kafka port-forward (e.g. kubectl port-forward -n kafka pod/kafka-dual-role-0 9092:9094).
# Run from repo root.

set -e

KUBECTL="${KUBECTL:-kubectl}"
WORKLOAD_NS="${WORKLOAD_NS:-default}"
BROKERS="${BROKERS:-localhost:9092}"
LOW_CONFIG="${LOW_CONFIG:-config/demo/preemption-low-8.json}"
HIGH_CONFIG="${HIGH_CONFIG:-config/demo/preemption-high-1.json}"
POLL_INTERVAL="${POLL_INTERVAL:-1}"
MAX_WAIT_SEC="${MAX_WAIT_SEC:-120}"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

log() {
  echo "[$(date -u +%Y-%m-%dT%H:%M:%SZ)] $*"
}

# Count GPUWorkloads in phase Running or Scheduled in WORKLOAD_NS.
count_running_or_scheduled() {
  local phases
  phases=$("$KUBECTL" get gpuworkloads -n "$WORKLOAD_NS" -o jsonpath='{.items[*].status.phase}' 2>/dev/null || true)
  if [[ -z "$phases" ]]; then
    echo 0
    return
  fi
  echo "$phases" | tr ' ' '\n' | grep -E '^(Running|Scheduled)$' | wc -l | tr -d ' '
}

cd "$REPO_ROOT"

log "Phase 1: submitting 8 low-priority workloads (config=$LOW_CONFIG)"
go run ./cmd/producer/main.go -brokers="$BROKERS" -config="$LOW_CONFIG" -burst

log "Phase 2: waiting until 8 GPUWorkloads are Running or Scheduled (poll every ${POLL_INTERVAL}s, max ${MAX_WAIT_SEC}s)"
start=$(date +%s)
while true; do
  count=$(count_running_or_scheduled)
  elapsed=$(($(date +%s) - start))
  log "  Running or Scheduled: $count / 8 (elapsed ${elapsed}s)"
  if [[ "$count" -ge 8 ]]; then
    break
  fi
  if [[ "$elapsed" -ge "$MAX_WAIT_SEC" ]]; then
    log "ERROR: timed out after ${MAX_WAIT_SEC}s with only $count Running or Scheduled"
    exit 1
  fi
  sleep "$POLL_INTERVAL"
done

log "Phase 3: submitting 1 high-priority workload (config=$HIGH_CONFIG)"
go run ./cmd/producer/main.go -brokers="$BROKERS" -config="$HIGH_CONFIG" -burst

log "Done. Watch with: kubectl get gpuworkloads -n $WORKLOAD_NS -w"
