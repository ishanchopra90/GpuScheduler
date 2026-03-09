# Deploy components

## Full stack (one command)

To bring up the entire end-to-end pipeline (Kind, Kafka, KEDA, simulator, operator, submitter, worker-deployment):

```bash
make stack-up
```

To tear it all down, including the Kind cluster:

```bash
make stack-down
```

After the stack is up, apply a sample `GPUNodePool` so the scheduler has fleet capacity (operator registers it with the simulator):

```bash
make apply-sample-pool
```

**If pods show "trying and failing to pull image" or ImagePullBackOff:** `stack-up` builds and loads all images into Kind first. If you see this (e.g. after applying manifests by hand), run `make stack-load-images`, then restart the deployments:  
`kubectl rollout restart deployment -n gpu-scheduler-system gpu-scheduler-controller-manager` and  
`kubectl rollout restart deployment -n system submitter simulator worker-deployment`

### Checking logs and events

**Operator (scheduler loop, reconcilers, fleet registration):**
```bash
kubectl logs -n gpu-scheduler-system deployment/gpu-scheduler-controller-manager -c manager -f
```

**Submitter (Kafka consumer, GPUWorkload creation):**
```bash
kubectl logs -n system deployment/submitter -f
```

**Worker-deployment (claim Scheduled workloads, run simulator):**
```bash
kubectl logs -n system deployment/worker-deployment -c worker -f
# Or a specific pod:
kubectl logs -n system -l app.kubernetes.io/name=gpu-scheduler-worker-deployment -c worker -f
```
If you see no logs: first check that pods exist (`kubectl get pods -n system -l app.kubernetes.io/name=gpu-scheduler-worker-deployment`). With KEDA `minReplicaCount: 1` there should be at least one pod. The worker only logs when it starts, when it claims/runs a workload, or on errors—so empty logs are normal when there are no `Phase=Scheduled` workloads. Rebuild and redeploy the worker image after code changes so the startup log appears.

**Simulator (fleet, allocate, start, status):**
```bash
kubectl logs -n system deployment/simulator -f
```

**Workload and pool status:**
```bash
kubectl get gpuworkloads -A
kubectl get gpunodepools -A
kubectl get gpuworkloads -A -w   # watch phase transitions
```

**Details and events for a workload:**
```bash
kubectl describe gpuworkload -n default <name>
kubectl get events -n default --sort-by='.lastTimestamp'
kubectl get events -A --field-selector involvedObject.kind=GPUWorkload
```
In worker-pool mode, only **Queued** and **Admitted** are emitted as Kubernetes events. After admission, worker pods update the workload **Phase** (Running → Succeeded/Failed) but do not record events. To see progress after Admitted, watch phases with `kubectl get gpuworkloads -A -w` or check `kubectl get gpuworkloads -A` for the Phase column.

**Workload journey logs (submitter → operator → worker → simulator):** To list all workload correlation IDs and fetch logs for the full journey in one go:

```bash
make list-workload-ids
make workload-logs CORRELATION_ID=default/loadgen-1-2855833041059322389
```

Use the workload name or `namespace/name` as `CORRELATION_ID`. Logs are grepped from submitter, operator, worker, and simulator.

**Workers report "fleet is not registered":** The operator must register the pool with the remote simulator (the one workers call). Ensure the operator has `SIMULATOR_URL` set—`make worker-up` sets it and restarts the manager. If the operator was deployed without it, set the env and restart, then re-apply the pool:

```bash
kubectl set env deployment/gpu-scheduler-controller-manager -n gpu-scheduler-system SIMULATOR_URL=http://simulator.system.svc.cluster.local:8080 --containers=manager
kubectl rollout restart deployment/gpu-scheduler-controller-manager -n gpu-scheduler-system
make apply-sample-pool
```

The simulator keeps fleet state in memory, so if the simulator pod restarts after the pool was applied, trigger a re-register by touching the pool: `kubectl annotate gpunodepool -n default gpunodepool-sample gpu-scheduler.ishanchopra.dev/synced-at="$(date +%s)" --overwrite`. Check operator logs for "Registered GPUNodePool fleet in simulator"; workers should then succeed on the next allocate.

### Submitting workloads (producer)

The pipeline is **producer → Kafka topic `gpu.workloads.submit` → submitter (creates GPUWorkload CRs) → scheduler → workers**. To feed the queue from your machine, Kafka must be reachable. With the stack in Kind, port-forward the Kafka bootstrap service, then run the producer:

**1. Port-forward Kafka (in a separate terminal):**  
Use the **host** listener (container port 9094) so Kafka advertises `localhost:9092` in metadata; otherwise the producer would try to connect to in-cluster broker hostnames and fail from your machine. Forward the broker pod’s port 9094 to host 9092:
```bash
kubectl port-forward -n kafka pod/kafka-dual-role-0 9092:9094
```
If the pod name differs (e.g. after scaling), use the bootstrap service by port number: `kubectl port-forward -n kafka svc/kafka-kafka-bootstrap 9092:9094` and ensure the Kafka pod has rolled out with the new listener.

**2. Run the producer (from repo root):**
```bash
make run-producer
# Or with options (pass flags via ARGS), e.g. 20 messages, burst mode:
make run-producer ARGS="-brokers=localhost:9092 -count=20 -burst"
# Or from a config JSON, burst:
make run-producer ARGS="-brokers=localhost:9092 -config=config/demo/contention-10-workloads.json -burst"
# Or paced: 5 messages, 500ms between each:
make run-producer ARGS="-brokers=localhost:9092 -count=5 -interval=500ms"
```

The producer sends `WorkloadRequest` messages to the topic; the submitter consumes them and creates `GPUWorkload` CRs in the `default` namespace. Then watch workloads with `kubectl get gpuworkloads -A -w` and the logs above.

## Local cluster (Kind)

For local deployment and demos, create a Kind cluster from the repo root:

```bash
make kind-up
```

This creates a cluster named `gpu-scheduler` (or reuse if it already exists). Tear down with:

```bash
make kind-down
```

Ensure `kubectl` context is set to the Kind cluster (e.g. `kubectl config use-context kind-gpu-scheduler`).

## Kafka (Strimzi)

With a Kind cluster running, deploy Strimzi and a single-node Kafka cluster (including topic `gpu.workloads.submit`):

```bash
make kafka-up
```

This installs the Strimzi Cluster Operator and a KRaft Kafka cluster in the `kafka` namespace. The cluster includes **Kafka Exporter**, which exposes consumer group lag as Prometheus metrics (e.g. `kafka_consumer_group_lag`). Configure Prometheus to scrape the Kafka Exporter service (e.g. `kafka-kafka-exporter.kafka.svc.cluster.local:9404`) so KEDA can scale the submitter from lag. In-cluster bootstrap address for submitter/KEDA/producer:

- **Bootstrap servers**: `kafka-kafka-bootstrap.kafka.svc.cluster.local:9092`

Override `bootstrapServers` in the submitter deployment and KEDA ScaledObject when deploying in-cluster (e.g. patch to `kafka-bootstrap.kafka.svc.cluster.local:9092`). Tear down with:

```bash
make kafka-down
```

## Simulator service

The simulator exposes the HTTP API used by the operator and worker-deployment (fleet registration, allocate, start, run status). Deploy it to Kind:

```bash
make simulator-up
```

This builds the simulator image, loads it into the Kind cluster, and applies `deploy/simulator` (Deployment + Service in namespace `system`). The Service is named `simulator` so `SIMULATOR_URL=http://simulator:8080` works from pods in the same namespace. Tear down with:

```bash
make simulator-down
```

## Operator (CRDs + controller)

Deploy the operator (CRDs and controller manager) to Kind:

```bash
make operator-up
```

This ensures the Kind cluster exists, builds the manager image (`controller:latest` by default), loads it into Kind, and applies `config/default` (CRDs, RBAC, controller deployment in namespace `gpu-scheduler-system`). Tear down with:

```bash
make operator-down
```

## Prerequisites

- Kubernetes cluster with CRDs and operator deployed (e.g. `kubectl apply -k config/default`).
- For worker-deployment: **Prometheus** scraping the operator metrics endpoint so `gpu_scheduler_scheduled_workloads` is available.

## KEDA (autoscaling)

Required for the submitter ScaledObject. `make submitter-up` installs KEDA automatically. You can also install or remove it explicitly:

- **Install**: `make keda-up`
- **Remove**: `make keda-down`

## Submitter (Kafka consumer)

Deployment + RBAC + KEDA ScaledObject (scales on Kafka consumer group lag for topic `gpu.workloads.submit`). Kafka Exporter is deployed with the Kafka cluster (see Kafka section) and exposes lag metrics; KEDA is wired to scale the submitter based on that lag metric. `submitter-up` installs KEDA if not already present.

- **Apply to Kind**: `make submitter-up`
- **Tear down**: `make submitter-down`
- **Direct apply** (KEDA must be installed first): `kubectl apply -k deploy/submitter`
- **Override** (e.g. brokers, image): patch or use kustomize `replacements`/`patches` for `deploy/submitter/deployment.yaml` and `keda-scaledobject.yaml`.

Ensure the `system` namespace exists (created by `config/manager`) and Kafka topic `gpu.workloads.submit` exists.

## Worker deployment (scale-focused workers)

Long-lived worker pods that poll for `GPUWorkload` in `Phase=Scheduled`, claim one, call the simulator (Allocate/Start), and update status to Succeeded/Failed. Wired to **KEDA** like the submitter: KEDA ScaledObject scales on **Prometheus metric** `gpu_scheduler_scheduled_workloads` (backlog of Scheduled workloads). When you run `worker-up`, the operator is configured with `USE_WORKER_POOL=true` so the reconciler leaves admitted workloads in `Phase=Scheduled` for workers to pick up (no in-process execution, no Job per workload).

- **Apply to Kind**: `make worker-up`
- **Tear down**: `make worker-down`
- **Direct apply** (KEDA and operator must be present; set `USE_WORKER_POOL=true` on the manager deployment): `kubectl apply -k deploy/worker-deployment`
- **Override**: `serverAddress` in `keda-scaledobject.yaml` to your Prometheus query URL; set `SIMULATOR_URL` and `NAMESPACE` in the Deployment to match your simulator and workload namespace.

KEDA computes desired replicas from `ceil(metric / threshold)`; `threshold: "2"` means roughly two Scheduled workloads per pod. Scale-down uses a stabilization window to avoid flapping. For autoscaling to work, Prometheus must scrape the operator metrics endpoint so `gpu_scheduler_scheduled_workloads` is available.

## Grafana dashboards

Two dashboards are provided to keep application metrics and resource usage separate (avoids overloading Grafana):

| Dashboard | File | Contents |
|-----------|------|----------|
| **Pipeline metrics** | `config/grafana/dashboards/gpu-scheduler-pipeline.json` | Operator (queue, admission, utilization, latency), submitter (consumed/creations), simulator (devices, run duration, allocation failures). Uses `gpu_scheduler_*` and `sim_*` metrics. |
| **CPU & memory utilization** | `config/grafana/dashboards/gpu-scheduler-resources.json` | Container memory (working set) and CPU usage by component: operator (manager), submitter, simulator, worker. Uses `container_memory_working_set_bytes` and `container_cpu_usage_seconds_total`; requires Prometheus scraping kubelet/cAdvisor (or equivalent). |

**Import in Grafana:** Dashboards → New → Import → Upload JSON (or paste from the file). Set the Prometheus datasource when prompted. For the resources dashboard, the **namespace** variable is filled from Prometheus (namespaces where pipeline containers run); default is `system`; add `gpu-scheduler-system` if the operator runs there.
