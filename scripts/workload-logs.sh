#!/usr/bin/env bash
# workload-logs.sh: List workload correlation IDs and fetch full journey logs for a given ID.
#
# Correlation ID = GPUWorkload name, or namespace/name (e.g. default/loadgen-1-123).
# Use these to trace a workload from submitter -> operator -> worker -> simulator.
#
# Usage:
#   ./scripts/workload-logs.sh list
#   ./scripts/workload-logs.sh logs <correlation-id>
#
# Examples:
#   make list-workload-ids
#   make workload-logs CORRELATION_ID=loadgen-1-2855833041059322389
#   make workload-logs CORRELATION_ID=default/loadgen-1-2855833041059322389

set -e

KUBECTL="${KUBECTL:-kubectl}"
OPERATOR_NS="${OPERATOR_NS:-gpu-scheduler-system}"
SYSTEM_NS="${SYSTEM_NS:-system}"

# Resolve correlation ID to namespace and name for grepping.
# If ID contains '/', use as namespace/name; else use default/ID.
resolve_id() {
  local id="$1"
  if [[ "$id" == */* ]]; then
    echo "$id"
  else
    echo "default/$id"
  fi
}

list_ids() {
  "$KUBECTL" get gpuworkloads -A -o custom-columns='NAMESPACE:.metadata.namespace,NAME:.metadata.name,PHASE:.status.phase,AGE:.metadata.creationTimestamp' 2>/dev/null
  echo ""
  echo "Correlation ID for 'logs' = NAMESPACE/NAME (e.g. default/loadgen-1-123) or NAME if namespace is default."
}

fetch_logs() {
  local id="$1"
  if [[ -z "$id" ]]; then
    echo "Usage: $0 logs <correlation-id>" >&2
    echo "  correlation-id: workload name (e.g. loadgen-1-123) or namespace/name (e.g. default/loadgen-1-123)" >&2
    exit 1
  fi

  local ns_name
  ns_name=$(resolve_id "$id")
  local namespace="${ns_name%%/*}"
  local name="${ns_name#*/}"
  local grep_pattern="$name"

  echo "=== Workload journey logs: $ns_name ==="
  echo ""

  echo "--- Submitter (created GPUWorkload) ---"
  "$KUBECTL" logs -n "$SYSTEM_NS" deployment/submitter --tail=5000 2>/dev/null | grep -- "$grep_pattern" || true
  echo ""

  echo "--- Operator (scheduler / reconciler) ---"
  "$KUBECTL" logs -n "$OPERATOR_NS" deployment/gpu-scheduler-controller-manager -c manager --tail=5000 2>/dev/null | grep -- "$grep_pattern" || true
  echo ""

  echo "--- Worker (claim, allocate, run) ---"
  "$KUBECTL" logs -n "$SYSTEM_NS" deployment/worker-deployment -c worker --tail=5000 2>/dev/null | grep -- "$grep_pattern" || true
  echo ""

  echo "--- Simulator (allocate, start, status) ---"
  "$KUBECTL" logs -n "$SYSTEM_NS" deployment/simulator --tail=5000 2>/dev/null | grep -E "(fleet|$grep_pattern|$ns_name)" || true
  echo ""

  echo "=== End of journey logs ==="
}

case "${1:-}" in
  list)
    list_ids
    ;;
  logs)
    fetch_logs "$2"
    ;;
  *)
    echo "Usage: $0 list | logs <correlation-id>" >&2
    echo "" >&2
    echo "  list              List all GPUWorkload correlation IDs (namespace, name, phase, age)." >&2
    echo "  logs <id>         Fetch logs for the full journey (submitter -> operator -> worker -> simulator)." >&2
    echo "                    <id> can be NAME or NAMESPACE/NAME (e.g. loadgen-1-123 or default/loadgen-1-123)." >&2
    exit 1
    ;;
esac
