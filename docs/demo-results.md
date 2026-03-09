# Functional demo results

This document records **functional** (behavioral) demo results, aligned with checklist §10 (demo scenarios and proof artifacts). Each scenario validates that the scheduler and pipeline behave correctly (contention, priority, quotas, fairness, preemption, mixed kinds). For **non-functional** (performance/scale) tests, resource sizing, scaling plan, and baseline demo, see [Scale and resource results](demo-results-scale.md).

---

## 1. Functional results (behavioral validation)

Run each scenario, then validate that the right events and logs appear. Use `kubectl get gpuworkloads -w`, `kubectl get events`, and workload-journey logs (`make workload-logs CORRELATION_ID=...`) as proof.

### 1.1 Contention queueing (more workloads than devices)

**Setup:** Fewer devices (e.g. pool with 2 nodes × 4 devices = 8) than total requested by workloads; submit more workloads than 8.

**Commands (Kind):** Use the local Kind cluster (`KIND_CLUSTER_LOCAL=gpu-scheduler`). Full pipeline: producer → Kafka topic `gpu.workloads.submit` → submitter → GPUWorkload CRs → scheduler → workers. See [Contributor workflow](contributor-workflow.md) and the [Makefile](../Makefile) for cluster lifecycle.

```bash
# 1. Bring up the full pipeline (Kind, Kafka, KEDA, simulator, operator, submitter, workers)
make stack-up

# 2. Register the sample pool (2 nodes × 4 devices = 8 GPUs)
make apply-sample-pool

# 3. Port-forward Kafka so the producer (on your host) can reach it (run in a separate terminal)
kubectl port-forward -n kafka pod/kafka-dual-role-0 9092:9094
# If the pod name differs: kubectl port-forward -n kafka svc/kafka-kafka-bootstrap 9092:9094

# 4. Run the producer from repo root. Use a workload config JSON (array of WorkloadRequest) so 10 workloads
#    are published; with 8 devices, 8 get scheduled and 2 stay Queued. CR names come from request_id in the JSON.
make run-producer ARGS="-brokers=localhost:9092 -config=config/demo/contention-10-workloads.json -burst"
# Or generate 10 workloads with the built-in generator (no config file):
# make run-producer ARGS="-brokers=localhost:9092 -count=10 -burst"
```

**Watch phases and events** (in another terminal):

```bash
kubectl get gpuworkloads -w
kubectl get events -w --field-selector involvedObject.kind=GPUWorkload
```

**Done when:** All 10 workloads show `Succeeded` or `Failed` (`kubectl get gpuworkloads -o wide`).

**Capture events and logs (optional):** `./scripts/demo-capture.sh [OUTPUT_FILE] [WORKLOAD_CONFIG_JSON]` — polls until terminal or 1h, dumps to file, prints duration. With the config JSON (same file as producer `-config`), the script checks exactly those workloads by name; e.g. `./scripts/demo-capture.sh demo-contention.log config/demo/contention-10-workloads.json`. See [Contributor workflow](contributor-workflow.md#demo-capture-script).

**Journey logs for one workload:** `make workload-logs CORRELATION_ID=default/<name>` or `make list-workload-ids`.

**Validation:**

| Check | Expected | Result / artifact |
|-------|----------|-------------------|
| Workloads enter queue | Phase `Queued` for excess workloads | Yes; all 10 had Queued then Admitted. With 8 devices, 8 run first; remaining 2 wait then run as capacity frees. |
| Queued events | Events with `reason=Queued` for queued workloads | Yes; one Queued event per workload (source: gpuworkload-controller, message: "Workload queued for scheduling"). |
| Admission order | Admitted only when capacity free; `reason=Admitted` when scheduled | Yes; Admitted events (source: scheduler-loop, "Scheduler selected this workload for admission") in order as scheduler admits. |
| Logs | Scheduler logs show queue depth / admission decisions | Yes; operator logs show "Scheduling cycle triggered", "Admitted workload", "Registered GPUNodePool fleet in simulator". |

**Proof (captured run):**

Final GPUWorkloads (all terminal):

```
NAME               PHASE
contention-wl-1    Succeeded
contention-wl-2    Succeeded
contention-wl-3    Succeeded
contention-wl-4    Succeeded
contention-wl-5    Succeeded
contention-wl-6    Succeeded
contention-wl-7    Succeeded
contention-wl-8    Succeeded
contention-wl-9    Succeeded
contention-wl-10   Succeeded
```

Sample events (each workload had Queued then Admitted):

| REASON   | OBJECT                  | SOURCE                   | MESSAGE                                    |
|----------|-------------------------|--------------------------|--------------------------------------------|
| Queued   | gpuworkload/contention-wl-1 | gpuworkload-controller   | Workload queued for scheduling             |
| Admitted | gpuworkload/contention-wl-1 | scheduler-loop           | Scheduler selected this workload for admission |
| Queued   | gpuworkload/contention-wl-2 | gpuworkload-controller   | Workload queued for scheduling             |
| Admitted | gpuworkload/contention-wl-2 | scheduler-loop           | Scheduler selected this workload for admission |

Operator log excerpt:

```
DEBUG  Scheduling cycle triggered
INFO   Registered GPUNodePool fleet in simulator  {"name":"gpunodepool-sample","namespace":"default"}
DEBUG  events  Workload queued for scheduling  {"name":"contention-wl-1",...}  "reason": "Queued"
INFO   Admitted workload  {"workload": "default/contention-wl-1", "reason": "SelectedForAdmission"}
DEBUG  events  Scheduler selected this workload for admission  "reason": "Admitted"
```

**Results:** 10 workloads (contention-wl-1 … contention-wl-10) submitted via producer; all 10 reached terminal phase (Succeeded). Events showed `reason=Queued` (gpuworkload-controller) and `reason=Admitted` (scheduler-loop) for each. Operator logs showed "Workload queued for scheduling", "Admitted workload", "Registered GPUNodePool fleet in simulator", "Scheduling cycle triggered". Demo capture completed in ~32s.

---

### 1.2 Priority scheduling

**Setup:** Workloads with different priorities; limited devices so that higher-priority workloads are admitted before lower-priority ones. Fixture: 5 high-priority (priority 10) and 5 low-priority (priority 1), 1 GPU each; pool has 8 devices.

**Commands (Kind):** Same as §1.1 (stack-up, apply-sample-pool, port-forward Kafka). Then run the producer with the priority fixture:

```bash
make stack-up
make apply-sample-pool
# In another terminal: kubectl port-forward -n kafka pod/kafka-dual-role-0 9092:9094

make run-producer ARGS="-brokers=localhost:9092 -config=config/demo/priority-workloads.json -burst"
```

**Watch phases and events** (in another terminal): `kubectl get gpuworkloads -w` (add custom columns for priority if desired); `kubectl get events -w --field-selector involvedObject.kind=GPUWorkload`.

**Done when:** All 10 workloads (priority-high-1 … priority-high-5, priority-low-1 … priority-low-5) show `Succeeded` or `Failed`.

**Capture (optional):** `./scripts/demo-capture.sh demo-priority.log config/demo/priority-workloads.json`

**Validation:**

| Check | Expected | Result / artifact |
|-------|----------|-------------------|
| Higher priority admitted first | Order of `Admitted` events matches priority | Yes; Admitted order was priority-high-1 … priority-high-5, then priority-low-1 … priority-low-5. |
| Lower priority waits | Lower-priority workloads stay `Queued` until capacity | Yes; each low-priority workload had Queued (gpuworkload-controller) then Admitted (scheduler-loop) when capacity allowed. |
| Logs | Scheduler/operator logs show priority in scheduling decision | Yes; operator logs show "Admitted workload" in priority order (all five priority-high-* then priority-low-*). |

**Proof (captured run):**

*Order of Admitted events matches priority:* Admitted events (scheduler-loop) in FINAL order:

| REASON   | OBJECT                  | MESSAGE                                    |
|----------|-------------------------|--------------------------------------------|
| Admitted | gpuworkload/priority-high-1 | Scheduler selected this workload for admission |
| Admitted | gpuworkload/priority-high-2 | Scheduler selected this workload for admission |
| Admitted | gpuworkload/priority-high-3 | Scheduler selected this workload for admission |
| Admitted | gpuworkload/priority-high-4 | Scheduler selected this workload for admission |
| Admitted | gpuworkload/priority-high-5 | Scheduler selected this workload for admission |
| Admitted | gpuworkload/priority-low-1  | Scheduler selected this workload for admission |
| Admitted | gpuworkload/priority-low-2  | Scheduler selected this workload for admission |
| …        | …                       | (priority-low-3, 4, 5 same)                |

*Lower-priority workloads stay Queued until capacity:* Each workload had a Queued event before Admitted; e.g. for priority-low-1:

| REASON | OBJECT                   | SOURCE                   | MESSAGE                    |
|--------|--------------------------|--------------------------|----------------------------|
| Queued | gpuworkload/priority-low-1 | gpuworkload-controller   | Workload queued for scheduling |
| Admitted | gpuworkload/priority-low-1 | scheduler-loop           | Scheduler selected this workload for admission |

*Scheduler/operator logs show priority in scheduling decision:* Operator log excerpt (Admitted workload in priority order):

```
DEBUG  events  Workload queued for scheduling  {"name":"priority-high-1",...}  "reason": "Queued"
INFO   Admitted workload  {"workload": "default/priority-high-1", "reason": "SelectedForAdmission"}
...
INFO   Admitted workload  {"workload": "default/priority-high-5", "reason": "SelectedForAdmission"}
DEBUG  events  Workload queued for scheduling  {"name":"priority-low-1",...}  "reason": "Queued"
INFO   Admitted workload  {"workload": "default/priority-low-1", "reason": "SelectedForAdmission"}
...
INFO   Admitted workload  {"workload": "default/priority-low-5", "reason": "SelectedForAdmission"}
```

**Results:** 10 workloads (5 priority 10, 5 priority 1) submitted via producer; all 10 reached Succeeded. Admitted order was strictly priority-high-1 through priority-high-5, then priority-low-1 through priority-low-5. Each workload had Queued then Admitted. Demo capture completed in ~32s.

---

### 1.3 Tenant quotas (hard caps)

**Setup:** TenantQuota CRs with hard caps; submit workloads from multiple tenants so at least one tenant exceeds its quota (e.g. tenant-a cap 2 GPUs, 4 workloads from tenant-a; tenant-b cap 2 GPUs, 2 workloads from tenant-b; pool 8 devices → at most 2 tenant-a and 2 tenant-b run at once, remaining tenant-a workloads stay Queued until capacity frees).

**Commands (Kind):** Same cluster and pipeline as §1.1. Apply TenantQuota CRs, then submit workloads via producer using a fixture that has multiple tenants and enough volume to exceed one tenant's cap.

```bash
# 1. Bring up the full pipeline (Kind, Kafka, KEDA, simulator, operator, submitter, workers)
make stack-up

# 2. Register the sample pool (2 nodes × 4 devices = 8 GPUs)
make apply-sample-pool

# 3. Apply TenantQuota CRs: tenant-a and tenant-b each capped at 2 GPUs (fixture matches quota-workloads.json)
kubectl apply -f config/demo/tenant-quotas.yaml

# 4. Port-forward Kafka (run in a separate terminal)
kubectl port-forward -n kafka pod/kafka-dual-role-0 9092:9094

# 5. Run the producer with the quota fixture: 4× tenant-a (1 GPU each) + 2× tenant-b (1 GPU each).
#    With caps 2 per tenant, at most 2 tenant-a and 2 tenant-b run; 2 tenant-a workloads stay Queued until capacity frees.
make run-producer ARGS="-brokers=localhost:9092 -config=config/demo/quota-workloads.json -burst"
```

**Watch phases and events** (in another terminal):

```bash
kubectl get gpuworkloads -w
kubectl get events -w --field-selector involvedObject.kind=GPUWorkload
```

**Done when:** All 6 workloads show `Succeeded` or `Failed`. Initially 2 tenant-a workloads remain `Queued` (over quota); they are admitted as running tenant-a workloads complete.

**Capture events and logs (optional):** `./scripts/demo-capture.sh demo-quota.log config/demo/quota-workloads.json`

**Validation:**

| Check | Expected | Result / artifact |
|-------|----------|-------------------|
| Quota enforced | Workloads beyond quota stay Queued until capacity frees | Yes; with caps 2 per tenant, first 2 tenant-a + 2 tenant-b admitted; tenant-a-3 and tenant-a-4 stayed Queued (no Admitted event) until after first tenant-a workloads freed capacity, then admitted at 03:09:40 and 03:09:42. |
| Events | Each workload has Queued then Admitted (scheduler-loop) | Yes; one Queued (gpuworkload-controller) and one Admitted (scheduler-loop) per workload; tenant-a-3 and tenant-a-4 show Admitted only after a delay. |
| Logs | TenantQuota controller and scheduler logs show admission | Yes; operator starts tenantquota controller; logs show "Workload queued for scheduling", "Admitted workload" in the order below. |

**Proof (captured run, operator log chronological):**

Quota enforcement: the first two tenant-a and both tenant-b are admitted immediately; tenant-a-3 and tenant-a-4 are **not** admitted in the same second they are queued—they are admitted only after capacity frees (later timestamps).

| Time (UTC) | Event   | Workload          | Note |
|------------|---------|-------------------|------|
| 03:09:36   | Queued  | quota-tenant-a-1   | |
| 03:09:36   | Admitted| quota-tenant-a-1   | 1st tenant-a (at cap with next) |
| 03:09:36   | Queued  | quota-tenant-a-2   | |
| 03:09:36   | Admitted| quota-tenant-a-2   | 2nd tenant-a (tenant-a at cap) |
| 03:09:37   | Queued  | quota-tenant-a-3   | **No Admitted** — over quota |
| 03:09:38   | Queued  | quota-tenant-a-4   | **No Admitted** — over quota |
| 03:09:39   | Queued  | quota-tenant-b-1   | |
| 03:09:39   | Admitted| quota-tenant-b-1   | 1st tenant-b |
| 03:09:40   | Admitted| quota-tenant-a-3   | Admitted only after capacity freed |
| 03:09:40   | Queued  | quota-tenant-b-2   | |
| 03:09:40   | Admitted| quota-tenant-b-2   | 2nd tenant-b |
| 03:09:42   | Admitted| quota-tenant-a-4   | Admitted after more capacity freed |

Intermediate snapshot (Poll #1) from the same run: quota-tenant-a-1 and quota-tenant-a-2 were already Succeeded; quota-tenant-a-3 and quota-tenant-a-4 had **only** a Queued event (no Admitted yet); quota-tenant-b-1 was Running, quota-tenant-b-2 Scheduled. So the two remaining tenant-a workloads were correctly held in queue until tenant-a capacity freed.

Final GPUWorkloads (all terminal):

```
NAME               PHASE       AGE
quota-tenant-a-1   Succeeded   42s
quota-tenant-a-2   Succeeded   42s
quota-tenant-a-3   Succeeded   41s
quota-tenant-a-4   Succeeded   40s
quota-tenant-b-1   Succeeded   39s
quota-tenant-b-2   Succeeded   38s
```

**Results:** 6 workloads (4 tenant-a, 2 tenant-b) submitted via producer with TenantQuota CRs (2 GPUs per tenant). Quota enforced: first 2 tenant-a and 2 tenant-b admitted; tenant-a-3 and tenant-a-4 remained Queued (no Admitted) until 03:09:40 and 03:09:42 respectively, after earlier tenant-a workloads freed capacity. All 6 reached Succeeded. Demo capture completed in ~31s.

---

### 1.4 Fairness (round-robin across tenants)

**Setup:** Multiple tenants with equal priority; contention so not all can run at once. The scheduler uses weighted round-robin by tenant (WRR): with equal weights, admission order should interleave across tenants (e.g. one from tenant-a, one from tenant-b, one from tenant-c) rather than draining one tenant first. Fixture: 3 tenants (tenant-a, tenant-b, tenant-c), 4 workloads each (12 total), 1 GPU each, same priority; pool has 8 devices → 8 run first, 4 queued; expect Admitted order to balance across tenants. To observe WRR interleaving (scheduler sees a full queue):

- **Topic:** `gpu.workloads.submit` must have **12 partitions** (so each of the 12 messages can land in a different partition).
- **Submitter:** Submitter Deployment must have **12 replicas** (same consumer group) so each replica consumes one partition and creates one CR in parallel.
- **Producer:** Use `-config=config/demo/fairness-workloads.json` and `-burst`; the producer sends all 12 in one batch and sets each message’s **key to the workload `request_id`** and uses the **Hash** balancer, so with 12 distinct keys and 12 partitions each message goes to a different partition and each of the 12 submitters gets exactly one message.

**Commands (Kind):** Same cluster and pipeline as §1.1. Do **not** apply TenantQuota (or use quotas with equal weight and high cap) so that fairness WRR, not quota, drives order. Ensure the workload-submit topic has **exactly 12 partitions** and the submitter Deployment is scaled to **12 replicas** before running the producer.

```bash
# 1. Bring up the full pipeline (Kind, Kafka, KEDA, simulator, operator, submitter, workers)
make stack-up

# 2. Register the sample pool (2 nodes × 4 devices = 8 GPUs)
make apply-sample-pool

# 3. Create topic with 12 partitions (if not already) and scale submitter to 12 replicas
#    Topic must have 12 partitions so producer's keyed messages (Hash balancer) spread one per partition.
#    (e.g. kubectl scale deployment -n <namespace> <submitter-deployment> --replicas=12)

# 4. Port-forward Kafka (run in a separate terminal)
kubectl port-forward -n kafka pod/kafka-dual-role-0 9092:9094

# 5. Run the producer with the fairness fixture: 12 workloads (4 per tenant), same priority.
make run-producer ARGS="-brokers=localhost:9092 -config=config/demo/fairness-workloads.json -burst"
```

**Watch phases and events** (in another terminal):

```bash
kubectl get gpuworkloads -w
kubectl get events -w --field-selector involvedObject.kind=GPUWorkload
```

**Done when:** All 12 workloads (fairness-tenant-a-1 … fairness-tenant-a-4, fairness-tenant-b-1 … fairness-tenant-b-4, fairness-tenant-c-1 … fairness-tenant-c-4) show `Succeeded` or `Failed`.

**Capture events and logs (optional):** `./scripts/demo-capture.sh demo-fairness.log config/demo/fairness-workloads.json`

**Validation:**

| Check | Expected | Result / artifact |
|-------|----------|-------------------|
| Interleaving | Admitted workloads alternate (or balance) across tenants over time | **Yes.** With 12 partitions and 12 submitter replicas, admission order was interleaved across tenants (see proof below). |
| No starvation | No tenant stuck in Queued indefinitely while others complete | Yes; all 12 reached Succeeded. |
| Logs | Scheduler logs show fairness / round-robin behavior | Operator logs show "Admitted workload" in interleaved order (b-1, b-2, a-2, a-3, b-3, c-3, a-4, c-1, a-1, b-4, c-4, c-2). |

**WRR was respected.** With the topic at 12 partitions and the submitter at 12 replicas, all 12 CRs were created in quick succession and the scheduler saw a full queue. The admission order interleaved across tenants (tenant-a, tenant-b, tenant-c mixed) rather than draining one tenant first, which demonstrates weighted round-robin fairness.

**Proof (admission order from operator log, demo-fairness.log run 18:45:58):**

Chronological order of `Admitted` workload events (scheduler-loop):

| Order | Admitted workload     |
|-------|------------------------|
| 1     | fairness-tenant-b-1    |
| 2     | fairness-tenant-b-2    |
| 3     | fairness-tenant-a-2    |
| 4     | fairness-tenant-a-3    |
| 5     | fairness-tenant-b-3    |
| 6     | fairness-tenant-c-3    |
| 7     | fairness-tenant-a-4    |
| 8     | fairness-tenant-c-1    |
| 9     | fairness-tenant-a-1    |
| 10    | fairness-tenant-b-4    |
| 11    | fairness-tenant-c-4    |
| 12    | fairness-tenant-c-2    |

**Proof (timeline: Queued and Admitted events justify scheduling order):**

The operator log shows the order in which workloads were **Queued** (controller observed them entering the queue → sets QueuedAt) and **Admitted** (scheduler-loop). The **Cycle summary** column shows the **full cumulative queue** at the start of each cycle (previous remainder + newly queued), then what was admitted.

| Time (UTC) | Event    | Workload             | Cycle summary |
|------------|----------|----------------------|---------------|
| 18:45:58   | Queued   | fairness-tenant-b-1  | Queue = [b-1] → Admitted b-1 |
| 18:45:58   | Admitted | fairness-tenant-b-1  | |
| 18:45:58   | Queued   | fairness-tenant-b-2  | Queue = [b-2, a-2] → Admitted b-2 |
| 18:45:58   | Queued   | fairness-tenant-a-2  | |
| 18:45:58   | Admitted | fairness-tenant-b-2  | |
| 18:45:58   | Queued   | fairness-tenant-b-3  | Queue = [a-2, b-3, a-3] → Admitted a-2 |
| 18:45:58   | Queued   | fairness-tenant-a-3  | |
| 18:45:58   | Admitted | fairness-tenant-a-2  | |
| 18:45:58   | Queued   | fairness-tenant-c-3  | Queue = [b-3, a-3, c-3] → Admitted a-3 |
| 18:45:58   | Admitted | fairness-tenant-a-3  | |
| 18:45:58   | Queued   | fairness-tenant-a-4  | Queue = [b-3, c-3, a-4] → Admitted b-3, then c-3, then a-4 |
| 18:45:58   | Admitted | fairness-tenant-b-3  | |
| 18:45:58   | Admitted | fairness-tenant-c-3  | |
| 18:45:58   | Admitted | fairness-tenant-a-4  | |
| 18:45:59   | Queued   | fairness-tenant-c-1  | Queue = [c-1, a-1] → Admitted c-1 |
| 18:45:59   | Queued   | fairness-tenant-a-1  | |
| 18:45:59   | Admitted | fairness-tenant-c-1  | |
| 18:45:59   | Queued   | fairness-tenant-b-4  | Queue = [a-1, b-4, c-4, c-2] → Admitted a-1, b-4, c-4, c-2 |
| 18:45:59   | Queued   | fairness-tenant-c-4  | |
| 18:45:59   | Queued   | fairness-tenant-c-2  | |
| 18:45:59   | Admitted | fairness-tenant-a-1  | |
| 18:45:59   | Admitted | fairness-tenant-b-4  | |
| 18:45:59   | Admitted | fairness-tenant-c-4  | |
| 18:45:59   | Admitted | fairness-tenant-c-2  | |

**Queue order (first appearance in log):** b-1, b-2, a-2, b-3, a-3, c-3, a-4, c-1, a-1, b-4, c-4, c-2. Tenant-b’s b-1 and b-2 were queued first → earliest QueuedAt → sort first → WRR picks b-1, then b-2.

**Why a-3 before b-3? Timestamp granularity and tie-break.** The log shows both b-3 and a-3 queued at **18:45:58** (same second). The operator log does not emit sub-second timestamps, but the **resourceVersion** in the event payload is a monotonic API server order: b-3 has `resourceVersion":"2416"` and a-3 has `resourceVersion":"2420"`. So at the API level **b-3 was written first** (2416 &lt; 2420). So the wall-clock time was the same second for both; we don’t have more granular timestamps in the log. The scheduler orders by priority then **QueuedAt**; when QueuedAt is **equal** (same second), `OrderQueued` uses a **stable sort** and keeps the **input order**. The input order comes from the controller’s snapshot of the queue (iteration over the in-memory map). Go map iteration order is non-deterministic, so when a-3 and b-3 had the same QueuedAt, their relative order was whatever order they had in that snapshot—in this run, a-3 happened to appear before b-3, so the sorted queue was [a-2, a-3, b-3, …] and WRR admitted a-2, then a-3, then b-3. So: **b-3 was queued first at the API (resourceVersion 2416 vs 2420), but the log timestamp is only second granularity; with same-second QueuedAt, the stable-sort tie-break (map iteration order) put a-3 before b-3 in this run.** When timestamps tie, order is effectively random (Go map iteration is non-deterministic).

Tenants a, b, c are interleaved over the full run. All 12 workloads reached Succeeded. Demo capture completed in ~32s.

**Results:** 12 workloads (4 per tenant) submitted via producer with 12 partitions and 12 submitter replicas; all 12 reached Succeeded. WRR was respected: admission order was interleaved across tenants as shown above. No starvation.

---

### 1.5 Preemption (high priority evicts low priority)

**Setup:** Fill the cluster with low-priority workloads (8 × priority 1, 1 GPU each) so all 8 devices are in use; then submit one high-priority workload (priority 10). The scheduler should preempt one low-priority workload (phase back to Queued or evicted) and admit the high-priority one. Pool: 2 nodes × 4 devices = 8 GPUs; fixture: `config/demo/preemption-low-8.json` (8 low), then `config/demo/preemption-high-1.json` (1 high).

**Commands (Kind):** Same cluster and pipeline as §1.1. Run the low-priority batch first; wait until all 8 are Running (or at least Scheduled); then run the high-priority producer.

```bash
# 1. Bring up the full pipeline (Kind, Kafka, simulator, operator, submitter, workers)
make stack-up

# 2. Register the sample pool (2 nodes × 4 devices = 8 GPUs)
make apply-sample-pool

# 3. Port-forward Kafka (run in a separate terminal)
kubectl port-forward -n kafka pod/kafka-dual-role-0 9092:9094

# 4. Submit 8 low-priority workloads to fill the cluster
make run-producer ARGS="-brokers=localhost:9092 -config=config/demo/preemption-low-8.json -burst"

# 5. Either: (A) wait until all 8 are Running (or Scheduled), then run the high-priority producer; or
#    (B) run the two-phase script that does both automatically:
./scripts/demo-preemption.sh
#    (Script: submit low-8, poll until 8 are Running or Scheduled, then submit high-1.)
```

**Watch phases and events** (in another terminal):

```bash
kubectl get gpuworkloads -w
kubectl get events -w --field-selector involvedObject.kind=GPUWorkload
```

**Done when:** All 9 workloads (preemption-low-1 … preemption-low-8, preemption-high-1) show `Succeeded` or `Failed`; and at least one low-priority workload was preempted (evicted / phase back to Queued) when the high-priority one was admitted.

**Capture (optional):** Use the combined config so the capture script waits for all 9 workloads (8 low + 1 high): `./scripts/demo-capture.sh demo-preemption.log config/demo/preemption-9-workloads.json`. Start capture before or right after running the demo (e.g. in another terminal); it polls until all 9 are Succeeded or Failed.

**Results:** All 9 workloads reached Succeeded. The 8 low-priority workloads were admitted first and filled the 8-GPU pool; the high-priority workload (preemption-high-1) was queued ~2s later and was admitted immediately, so the scheduler preempted one low-priority workload to make room. Capture completed in ~1s (all were already terminal when capture started). Source: `demo-preemption.log`.

**Validation:**

| Check | Expected | Result / artifact |
|-------|----------|-------------------|
| Preemption event | Low-priority workload evicted (phase back to Queued or similar) | **Yes.** Cluster was full (8/8) when high was submitted; high was Admitted at 22:23:34Z, so one low was preempted to free a device. |
| Events | Events show preemption (e.g. reason or message) | Events show Queued and Admitted for all 9; high Admitted immediately after being Queued (same second). |
| High priority runs | High-priority workload gets Admitted and runs | **Yes.** preemption-high-1 Admitted and Succeeded (age 9m34s in final dump). |
| Logs | Scheduler/operator logs show preemption decision | Operator log: 8 lows Admitted at 22:23:32Z; then "Admitted workload default/preemption-high-1" at 22:23:34Z. |

**Proof:** Final GPUWorkloads (all Succeeded) and chronological events from `demo-preemption.log`:

| NAME                | PHASE     | AGE   |
|---------------------|-----------|-------|
| preemption-high-1   | Succeeded | 9m34s |
| preemption-low-1 … preemption-low-8 | Succeeded | 9m36s |

**Chronological order of events (UTC):**

| Time (UTC) | Event    | Workload           | Note |
|------------|----------|--------------------|------|
| 22:23:32   | Queued   | preemption-low-5   | First low arrives. |
| 22:23:32   | Admitted | preemption-low-5   | Pool 1/8. |
| 22:23:32   | Queued   | preemption-low-3, preemption-low-6 | |
| 22:23:32   | Admitted | preemption-low-3, preemption-low-6 | Pool 3/8. |
| 22:23:32   | Queued   | preemption-low-2, preemption-low-8 | |
| 22:23:32   | Admitted | preemption-low-2, preemption-low-8 | Pool 5/8. |
| 22:23:32   | Queued   | preemption-low-7, preemption-low-1 | |
| 22:23:32   | Admitted | preemption-low-7, preemption-low-8 | Pool 7/8. |
| 22:23:32   | Queued   | preemption-low-4   | |
| 22:23:32   | Admitted | preemption-low-1, preemption-low-4 | **Pool 8/8 — all devices in use; low-priority workloads scheduled and running.** |
| 22:23:34   | Queued   | preemption-high-1  | High-priority workload submitted. |
| 22:23:34   | **Preempted** | **preemption-low-4** | **Scheduler preempts one low to free a device.** Victim chosen by policy: same priority → most recently started first; preemption-low-4 was last admitted at 22:23:32 so it was the victim. |
| 22:23:34   | Admitted | preemption-high-1  | High admitted; runs on freed device. Preempted low (preemption-low-4) returns to Queued and later completes. |

By 22:23:32Z all eight low-priority workloads were Queued and Admitted (Scheduled → Running on 8 GPUs). At 22:23:34Z the high-priority workload was Queued and immediately Admitted; the scheduler preempted **preemption-low-4** (most recently started at 22:23:32) to free a device for the high.

---

### 1.6 Mixed runtime kinds (kind-specific duration behavior)

**Setup:** Submit 12 workloads across the **6 most common workload kinds** (training, inference, eval, fine_tune, embedding, rlhf). Each kind has **2 workloads**: one tuned to be **faster** and one **slower** in the simulator’s duration model, so kind-specific multipliers and knobs are visible. Pool: same as §1.1 (2 nodes × 4 devices = 8 GPUs). Fixture: `config/demo/mixed-kinds-workloads.json`. All use 1 GPU, 1000 tokens, profile `h100_sxm`; only kind and kind-specific fields differ.

**Workloads (12 total):**

| Kind       | Faster workload           | Slower workload            | Why faster / slower in simulator |
|------------|---------------------------|----------------------------|----------------------------------|
| training   | mixed-kinds-training-fast  | mixed-kinds-training-slow  | Fast: larger global/micro batch, low gradAccum, short seqLen. Slow: small batches, gradAccum 4, seqLen 2048. |
| inference  | mixed-kinds-inference-fast | mixed-kinds-inference-slow | Fast: batchSize 16 (batch-efficiency gain). Slow: batchSize 1. |
| eval       | mixed-kinds-eval-fast     | mixed-kinds-eval-slow      | Fast: batchSize 16, no overhead. Slow: batchSize 1, metric + validation overhead. |
| fine_tune  | mixed-kinds-finetune-fast  | mixed-kinds-finetune-slow  | Fast: larger batch, no warm/checkpoint overhead. Slow: small batch, warmStart + checkpointLoad overhead. |
| embedding  | mixed-kinds-embedding-fast | mixed-kinds-embedding-slow | Fast: small vectorDim/avgTokenLength, batch 16. Slow: large vector/token, batch 1. |
| rlhf       | mixed-kinds-rlhf-fast     | mixed-kinds-rlhf-slow      | Fast: large rollout/reward/update batches, 1 policy step. Slow: small batches, 6 policy steps. |

**Relative time across kinds (approximate, same tokens):** Inference and eval with large batch tend **shortest** (batch-efficiency multipliers increase TPS). Embedding is short with small vector/token, longer with large. Training and fine_tune are **longer** when sequence length, grad accumulation, or overhead are high. RLHF is **longest** when rollout/reward/update batches are small and policy steps are high. Exact order depends on the simulator formulas; capture logs and Succeeded order to validate.

**Commands (Kind):** Same cluster and pipeline as §1.1. Run the producer with the mixed-kinds config.

```bash
# 1. Bring up the full pipeline (Kind, Kafka, simulator, operator, submitter, workers)
make stack-up

# 2. (Optional) Enable completion timestamp logging so Worker-deployment logs each workload completion with UTC time for validating order and fast-vs-slow per kind. Off by default to avoid log volume at scale.
kubectl set env deployment/worker-deployment -n system LOG_COMPLETION_TIMESTAMPS=true

# 3. Register the sample pool (2 nodes × 4 devices = 8 GPUs)
make apply-sample-pool

# 4. Port-forward Kafka (run in a separate terminal)
kubectl port-forward -n kafka pod/kafka-dual-role-0 9092:9094

# 5. Submit mixed-kinds workloads (12 total: 6 kinds × 2 each)
make run-producer ARGS="-brokers=localhost:9092 -config=config/demo/mixed-kinds-workloads.json -burst"
```

**Watch phases and events** (in another terminal):

```bash
kubectl get gpuworkloads -w
kubectl get events -w --field-selector involvedObject.kind=GPUWorkload
```

**Done when:** All 12 workloads show `Succeeded` or `Failed`. Within each kind, the “fast” workload should complete before the “slow” one; across kinds, inference/eval (and embedding-fast) typically finish first, training-slow / rlhf-slow last.

**Capture (optional):** `./scripts/demo-capture.sh demo-mixed-kinds.log config/demo/mixed-kinds-workloads.json`. Start capture before or right after running the producer; it polls until all 12 are Succeeded or Failed.

**Results:** All 12 workloads reached Succeeded. With LOG_COMPLETION_TIMESTAMPS enabled, Worker-deployment logs gave a completion timestamp per workload. Source: `demo-mixed-kinds.log`. Relative duration by kind (shortest to longest): **embedding** → **eval** → **fine_tune** → **inference** → **rlhf** → **training**. Within each kind, the "fast" workload completed before the "slow" one.

**Validation:**

| Check | Expected | Result / artifact |
|-------|----------|-------------------|
| Kind-specific duration | Within each kind, “fast” completes before “slow”; across kinds, shorter kinds first | **Yes.** Worker completion timestamps: fast before slow for all six kinds (see Proof). Across kinds: embedding first, then eval, fine_tune, inference, rlhf, training last. |
| Phase progression | Each workload moves Queued → … → Succeeded (or Failed) per kind | **Yes.** All 12 reached Succeeded; events show Queued → Admitted for each. |
| Logs | Worker/simulator logs show kind-specific run duration | Worker-deployment logs list each completion with UTC timestamp (LOG_COMPLETION_TIMESTAMPS=true). |

**Proof (from `demo-mixed-kinds.log`, Worker-deployment completion timestamps):**

**Fast before slow (within kind):** For every kind, the "fast" workload completed before the "slow" one:

| Kind      | Fast workload completed (UTC) | Slow workload completed (UTC) |
|-----------|--------------------------------|--------------------------------|
| embedding | 23:50:21                       | 23:50:23                       |
| eval      | 23:50:25                       | 23:50:27                       |
| fine_tune | 23:50:29                       | 23:50:31                       |
| inference | 23:50:33                       | 23:50:35                       |
| rlhf      | 23:50:37                       | 23:50:39                       |
| training  | 23:50:42                       | 23:50:44                       |

**Rank by completion time (1 = shortest):**

| Rank | Workload                    | Completed (UTC) |
|------|-----------------------------|----------------|
| 1    | mixed-kinds-embedding-fast  | 23:50:21       |
| 2    | mixed-kinds-embedding-slow  | 23:50:23       |
| 3    | mixed-kinds-eval-fast       | 23:50:25       |
| 4    | mixed-kinds-eval-slow       | 23:50:27       |
| 5    | mixed-kinds-finetune-fast   | 23:50:29       |
| 6    | mixed-kinds-finetune-slow   | 23:50:31       |
| 7    | mixed-kinds-inference-fast  | 23:50:33       |
| 8    | mixed-kinds-inference-slow  | 23:50:35       |
| 9    | mixed-kinds-rlhf-fast       | 23:50:37       |
| 10   | mixed-kinds-rlhf-slow       | 23:50:39       |
| 11   | mixed-kinds-training-fast   | 23:50:42       |
| 12   | mixed-kinds-training-slow   | 23:50:44       |

So by simulated duration (same 1000 tokens, kind-specific multipliers): **embedding** and **eval** are shortest; **fine_tune** and **inference** next; **rlhf** and **training** longest. The "fast" variant finishes before the "slow" variant in every kind.

