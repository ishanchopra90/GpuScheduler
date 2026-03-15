# Functional Validation

End-to-end behavioral validation of the scheduler and pipeline. Each scenario runs the full pipeline (producer → Kafka → submitter → operator → workers → simulator) on a local Kind cluster and validates that scheduling policies are enforced correctly.

For scale and performance results, see [Scale Analysis](demo-results-scale.md).

---

## Summary

| Scenario | Workloads | What It Validates | Result |
|----------|-----------|-------------------|--------|
| [Contention queueing](#1-contention-queueing) | 10 (8 GPU pool) | Excess workloads queue and drain as capacity frees | All 10 Succeeded |
| [Priority scheduling](#2-priority-scheduling) | 10 (5 high, 5 low) | Higher priority admitted first | Strict priority order confirmed |
| [Tenant quotas](#3-tenant-quotas) | 6 (2 tenants, cap 2 each) | Hard caps enforced, excess stays queued | Quotas held; delayed admission observed |
| [Fairness (WRR)](#4-fairness-weighted-round-robin) | 12 (3 tenants × 4) | Admission interleaves across tenants | WRR interleaving confirmed |
| [Preemption](#5-preemption) | 9 (8 low + 1 high) | High-priority evicts running low-priority | Victim preempted, high ran immediately |
| [Mixed workload kinds](#6-mixed-workload-kinds) | 12 (6 kinds × 2) | Kind-specific duration modeling | Correct relative completion order |

All scenarios use `make stack-up` for deployment. Demo fixtures are in `config/demo/`.

---

## 1. Contention Queueing

**Goal:** Verify that when more workloads are submitted than available devices, excess workloads queue correctly and drain as capacity frees.

**Setup:** Pool with 8 GPUs (2 nodes × 4 devices). Submit 10 workloads (1 GPU each).

```bash
make run-producer ARGS="-brokers=localhost:9092 -config=config/demo/contention-10-workloads.json -burst"
```

**Result:** All 10 workloads reached `Succeeded`. With 8 devices, 8 were admitted immediately; the remaining 2 stayed `Queued` until running workloads completed, then were admitted and ran.

| Validation | Expected | Observed |
|------------|----------|----------|
| Excess workloads queue | Phase=Queued for workloads beyond capacity | Yes — all had Queued events before Admitted |
| Admission on capacity free | Queued workloads admitted as running ones complete | Yes — remaining 2 admitted after first completions |
| Events | `reason=Queued` (controller) and `reason=Admitted` (scheduler-loop) per workload | Yes |

<details>
<summary>Event log excerpt</summary>

| Reason | Object | Source | Message |
|--------|--------|--------|---------|
| Queued | gpuworkload/contention-wl-1 | gpuworkload-controller | Workload queued for scheduling |
| Admitted | gpuworkload/contention-wl-1 | scheduler-loop | Scheduler selected this workload for admission |
| Queued | gpuworkload/contention-wl-2 | gpuworkload-controller | Workload queued for scheduling |
| Admitted | gpuworkload/contention-wl-2 | scheduler-loop | Scheduler selected this workload for admission |

</details>

---

## 2. Priority Scheduling

**Goal:** Verify that higher-priority workloads are admitted before lower-priority ones when capacity is contended.

**Setup:** 5 high-priority (priority 10) and 5 low-priority (priority 1) workloads, 1 GPU each. Pool has 8 devices.

```bash
make run-producer ARGS="-brokers=localhost:9092 -config=config/demo/priority-workloads.json -burst"
```

**Result:** All 10 reached `Succeeded`. Admission order was strictly `priority-high-1` through `priority-high-5`, then `priority-low-1` through `priority-low-5`.

| Validation | Expected | Observed |
|------------|----------|----------|
| Higher priority admitted first | Admitted events follow priority order | Yes — all 5 high before any low |
| Lower priority waits | Low-priority stays Queued until capacity | Yes — each low had Queued then Admitted when capacity allowed |

<details>
<summary>Admission order</summary>

| Order | Admitted Workload |
|-------|-------------------|
| 1 | priority-high-1 |
| 2 | priority-high-2 |
| 3 | priority-high-3 |
| 4 | priority-high-4 |
| 5 | priority-high-5 |
| 6 | priority-low-1 |
| 7 | priority-low-2 |
| 8 | priority-low-3 |
| 9 | priority-low-4 |
| 10 | priority-low-5 |

</details>

---

## 3. Tenant Quotas

**Goal:** Verify that `TenantQuota` CRDs enforce hard per-tenant GPU caps, holding excess workloads in `Queued` until the tenant's running count drops.

**Setup:** `tenant-a` capped at 2 GPUs, `tenant-b` capped at 2 GPUs. Submit 4 workloads from `tenant-a` and 2 from `tenant-b` (1 GPU each). Pool has 8 devices.

```bash
kubectl apply -f config/demo/tenant-quotas.yaml
make run-producer ARGS="-brokers=localhost:9092 -config=config/demo/quota-workloads.json -burst"
```

**Result:** All 6 reached `Succeeded`. First 2 `tenant-a` and both `tenant-b` workloads admitted immediately. `tenant-a-3` and `tenant-a-4` stayed `Queued` (over quota) and were admitted only after earlier `tenant-a` workloads completed.

| Validation | Expected | Observed |
|------------|----------|----------|
| Quota enforced | Workloads beyond cap stay Queued | Yes — `tenant-a-3` and `tenant-a-4` held until capacity freed |
| Delayed admission | Over-quota workloads admitted only after capacity frees | Yes — admitted at 03:09:40 and 03:09:42 (4–6s after initial batch) |

<details>
<summary>Chronological admission timeline</summary>

| Time (UTC) | Event | Workload | Note |
|------------|-------|----------|------|
| 03:09:36 | Admitted | quota-tenant-a-1 | 1st tenant-a |
| 03:09:36 | Admitted | quota-tenant-a-2 | 2nd tenant-a (at cap) |
| 03:09:37 | Queued | quota-tenant-a-3 | **Over quota — no Admitted** |
| 03:09:38 | Queued | quota-tenant-a-4 | **Over quota — no Admitted** |
| 03:09:39 | Admitted | quota-tenant-b-1 | 1st tenant-b |
| 03:09:40 | **Admitted** | **quota-tenant-a-3** | Capacity freed |
| 03:09:40 | Admitted | quota-tenant-b-2 | 2nd tenant-b |
| 03:09:42 | **Admitted** | **quota-tenant-a-4** | More capacity freed |

</details>

---

## 4. Fairness (Weighted Round-Robin)

**Goal:** Verify that the scheduler interleaves admission across tenants (weighted round-robin) rather than draining one tenant first.

**Setup:** 3 tenants (`tenant-a`, `tenant-b`, `tenant-c`), 4 workloads each (12 total), same priority, 1 GPU each. Pool has 8 devices. Topic set to 12 partitions with 12 submitter replicas so all CRs are created simultaneously.

```bash
make run-producer ARGS="-brokers=localhost:9092 -config=config/demo/fairness-workloads.json -burst"
```

**Result:** All 12 reached `Succeeded`. Admission order interleaved across all three tenants rather than draining one first, confirming WRR fairness.

| Validation | Expected | Observed |
|------------|----------|----------|
| Interleaving | Admission alternates across tenants | Yes — b-1, b-2, a-2, a-3, b-3, c-3, a-4, c-1, a-1, b-4, c-4, c-2 |
| No starvation | All tenants' workloads eventually complete | Yes — all 12 Succeeded |

<details>
<summary>Full admission order with cycle analysis</summary>

| Order | Admitted Workload | Tenant |
|-------|-------------------|--------|
| 1 | fairness-tenant-b-1 | b |
| 2 | fairness-tenant-b-2 | b |
| 3 | fairness-tenant-a-2 | a |
| 4 | fairness-tenant-a-3 | a |
| 5 | fairness-tenant-b-3 | b |
| 6 | fairness-tenant-c-3 | c |
| 7 | fairness-tenant-a-4 | a |
| 8 | fairness-tenant-c-1 | c |
| 9 | fairness-tenant-a-1 | a |
| 10 | fairness-tenant-b-4 | b |
| 11 | fairness-tenant-c-4 | c |
| 12 | fairness-tenant-c-2 | c |

**Distribution:** tenant-a = 4, tenant-b = 4, tenant-c = 4. No tenant was starved; admission was interleaved as workloads entered the queue.

**Note on tie-breaking:** When multiple workloads have the same QueuedAt timestamp (same-second granularity), `OrderQueued` uses a stable sort. Tie-break order depends on Go map iteration (non-deterministic), so same-second ordering may vary across runs.

</details>

---

## 5. Preemption

**Goal:** Verify that when the fleet is full with low-priority workloads, submitting a high-priority workload triggers preemption of a running low-priority workload.

**Setup:** Fill the 8-GPU pool with 8 low-priority workloads (priority 1), then submit 1 high-priority workload (priority 10).

```bash
# Phase 1: Fill cluster with low-priority work
make run-producer ARGS="-brokers=localhost:9092 -config=config/demo/preemption-low-8.json -burst"

# Phase 2: Submit high-priority workload (after lows are running)
make run-producer ARGS="-brokers=localhost:9092 -config=config/demo/preemption-high-1.json -burst"

# Or use the automated two-phase script:
./scripts/demo-preemption.sh
```

**Result:** All 9 workloads reached `Succeeded`. The 8 low-priority workloads filled the pool by 22:23:32Z. At 22:23:34Z, the high-priority workload was queued and immediately admitted; `preemption-low-4` (most recently started) was preempted to free a device.

| Validation | Expected | Observed |
|------------|----------|----------|
| Preemption occurs | Low-priority evicted when high-priority needs capacity | Yes — `preemption-low-4` preempted at 22:23:34Z |
| High priority runs immediately | High admitted same second it's queued | Yes — Queued and Admitted both at 22:23:34Z |
| Victim selection | Most recently started low-priority workload | Yes — `preemption-low-4` was last admitted |

<details>
<summary>Event timeline</summary>

| Time (UTC) | Event | Workload | Note |
|------------|-------|----------|------|
| 22:23:32 | Admitted | preemption-low-1 through low-8 | Pool 8/8 — all devices in use |
| 22:23:34 | Queued | preemption-high-1 | High-priority submitted |
| 22:23:34 | **Preempted** | **preemption-low-4** | Victim: most recently started |
| 22:23:34 | Admitted | preemption-high-1 | Runs on freed device |

The preempted workload (`preemption-low-4`) was re-queued and eventually completed as `Succeeded`.

</details>

---

## 6. Mixed Workload Kinds

**Goal:** Verify that the simulator's kind-specific duration model produces different run times for different workload kinds, and that "fast" variants complete before "slow" variants within each kind.

**Setup:** 12 workloads across 6 kinds (training, inference, eval, fine_tune, embedding, RLHF), each with a fast and slow variant. All use 1 GPU, 1000 tokens, profile `h100_sxm`; only kind-specific parameters differ.

```bash
kubectl set env deployment/worker-deployment -n system LOG_COMPLETION_TIMESTAMPS=true
make run-producer ARGS="-brokers=localhost:9092 -config=config/demo/mixed-kinds-workloads.json -burst"
```

**Result:** All 12 reached `Succeeded`. Within every kind, the fast variant completed before the slow variant. Across kinds, the ordering matched expected simulator behavior.

| Validation | Expected | Observed |
|------------|----------|----------|
| Fast before slow (per kind) | Within each kind, fast completes first | Yes — confirmed for all 6 kinds |
| Kind-specific ordering | Different kinds produce different durations | Yes — embedding fastest, training slowest |

**Completion order (fastest to slowest):**

| Rank | Kind | Variant | Completed (UTC) |
|------|------|---------|-----------------|
| 1 | embedding | fast | 23:50:21 |
| 2 | embedding | slow | 23:50:23 |
| 3 | eval | fast | 23:50:25 |
| 4 | eval | slow | 23:50:27 |
| 5 | fine_tune | fast | 23:50:29 |
| 6 | fine_tune | slow | 23:50:31 |
| 7 | inference | fast | 23:50:33 |
| 8 | inference | slow | 23:50:35 |
| 9 | rlhf | fast | 23:50:37 |
| 10 | rlhf | slow | 23:50:39 |
| 11 | training | fast | 23:50:42 |
| 12 | training | slow | 23:50:44 |

**Kind duration ranking** (same tokens, kind-specific multipliers): **embedding** and **eval** shortest → **fine_tune** and **inference** → **rlhf** and **training** longest.

---

## How to Reproduce

All scenarios follow the same pattern:

```bash
make stack-up                    # Deploy pipeline
make apply-sample-pool           # Register GPU pool
# Port-forward Kafka in a separate terminal:
kubectl port-forward -n kafka pod/kafka-dual-role-0 9092:9094

make run-producer ARGS="-brokers=localhost:9092 -config=<fixture> -burst"
kubectl get gpuworkloads -w      # Watch lifecycle
./scripts/demo-capture.sh <output.log> <fixture>   # Optional: capture events

make stack-down-fast             # Tear down
```

Workload fixtures are in `config/demo/`. Journey logs for individual workloads: `make workload-logs CORRELATION_ID=default/<name>`.
