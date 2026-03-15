# Contributor Guide

Development workflow, project layout, and Make target reference for the KGPU Scheduler.

---

## Quick Start

```bash
make help          # Show all available targets
make run           # Run controller locally (includes codegen + checks)
make test          # Run unit/integration tests (envtest)
make test-e2e      # Run e2e tests on an isolated Kind cluster
```

---

## Development Workflow

### Local Development

```bash
make run
```

Runs code generation, lint checks, and starts the controller locally against your current kubeconfig context.

### Run Tests

```bash
make test                    # Unit/integration tests (envtest: real API server + etcd)
make test-e2e                # E2E tests on an isolated Kind cluster
make test-e2e KIND_CLUSTER=my-cluster   # Override cluster name
```

E2E tests use an isolated Kind cluster. Do not run against shared dev/prod clusters.

**Full-pipeline e2e** (producer → Kafka → submitter → operator → worker → simulator):

```bash
make test-e2e-full           # Run full-pipeline tests only
make test-e2e-full-log       # Console + file output (e2e-full.log)
```

To include full-pipeline tests in the standard suite: `E2E_FULL_PIPELINE=true make test-e2e`.

### Build and Deploy

```bash
export IMG=<registry>/<repo>:<tag>
make docker-build docker-push IMG=$IMG
make deploy IMG=$IMG
kubectl apply -k config/samples/
```

Debug logs:

```bash
kubectl logs -n <project>-system deployment/<project>-controller-manager -c manager -f
```

---

## Running Demos

### Deploy Full Stack

```bash
make stack-up
```

Brings up Kind, builds/loads images, deploys Kafka, KEDA, simulator, operator, submitter, workers, applies the default GPUNodePool, and installs Prometheus + Grafana.

Override the pool: `make stack-up APPLY_POOL=path/to/pool.yaml`. Skip pool: `make stack-up APPLY_POOL=`.

### Port-Forward Services

```bash
# Grafana (http://localhost:3001, login: admin/admin)
kubectl port-forward -n monitoring svc/kube-prometheus-stack-grafana 3001:80

# Kafka (for producer on host)
kubectl port-forward -n kafka pod/kafka-dual-role-0 9092:9094

# Prometheus (for CSV export)
kubectl port-forward -n monitoring svc/kube-prometheus-stack-prometheus 9090:9090
```

### Configure Grafana

1. Add Prometheus datasource: `http://kube-prometheus-stack-prometheus.monitoring.svc.cluster.local:9090`
2. Import dashboards from `config/grafana/dashboards/`:
   - `gpu-scheduler-pipeline.json` — queue depth, admission rate, completion rate, E2E latency
   - `gpu-scheduler-resources.json` — per-component CPU/memory (set namespace variable to pipeline pod namespaces)

### Run Producer

```bash
# 10 workloads from fixture
make run-producer ARGS="-brokers=localhost:9092 -config=config/demo/contention-10-workloads.json -burst"

# Watch lifecycle
kubectl get gpuworkloads -w
```

The `-config` flag takes a JSON file (single object or array of `WorkloadRequest`). See `config/demo/` for fixtures and `internal/kafka/workload_request.go` for the schema.

### Capture Demo Output

```bash
./scripts/demo-capture.sh <output.log> <workload-config.json>
```

Polls GPUWorkloads, events, and component logs until all expected workloads reach a terminal phase. Env overrides: `WORKLOAD_NS`, `EXPECTED_COUNT`, `POLL_INTERVAL`, `MAX_DURATION_SEC`.

### Teardown

```bash
make stack-down-fast     # Delete Kind cluster (fastest)
```

---

## Project Layout

```
cmd/main.go                        Manager entrypoint (controller-runtime scaffolded)
api/<version>/*_types.go           CRD schemas + kubebuilder markers
internal/controller/               Reconciliation logic + scheduler loop
internal/sim/                      Simulated accelerator fleet
internal/kafka/                    Kafka producer/consumer
internal/worker/                   Worker deployment + discovery
config/crd/bases/                  Generated CRDs (DO NOT EDIT)
config/rbac/role.yaml              Generated RBAC (DO NOT EDIT)
config/samples/                    Sample CRs
config/demo/                       Demo workload fixtures
deploy/                            Kubernetes manifests for pipeline components
scripts/                           Demo capture, CSV export, preemption automation
```

**`config/` subdirectories:**

| Directory | Purpose |
|-----------|---------|
| `config/default/` | Main deployment composition (CRDs + RBAC + manager + metrics) |
| `config/manager/` | Controller-manager Deployment and namespace |
| `config/crd/` | CRD manifests and kustomize config |
| `config/rbac/` | ServiceAccount, RBAC roles, leader-election roles |
| `config/samples/` | Example CRs for quick testing |
| `config/prometheus/` | ServiceMonitor for Prometheus scraping |
| `config/network-policy/` | Optional NetworkPolicy for metrics ingress |

---

## Scaffolding

Always use the Kubebuilder CLI:

```bash
# New API + controller
kubebuilder create api --group <group> --version <version> --kind <Kind>

# Validation/defaulting webhooks
kubebuilder create webhook --group <group> --version <version> --kind <Kind> \
  --defaulting --programmatic-validation

# Multi-version conversion
kubebuilder create webhook --group <group> --version v1 --kind <Kind> \
  --conversion --spoke v2
```

The manager entrypoint (`cmd/main.go`) must be generated via Kubebuilder. Retain all `// +kubebuilder:scaffold:*` markers.

---

## Code Generation

After editing `*_types.go` or kubebuilder markers:

```bash
make manifests     # Regenerate CRDs, RBAC, webhook manifests
make generate      # Regenerate DeepCopy and related code
```

After editing Go implementation files:

```bash
make lint-fix      # Auto-fix code style
make test          # Run tests
```

---

## Controller Guidelines

- Keep reconciliation **idempotent**
- **Re-fetch** before updates to reduce optimistic concurrency conflicts
- Use **owner references** for dependent resources (automatic GC)
- Use **finalizers** for external cleanup
- Prefer **`metav1.Condition`** for status conditions
- Watch secondary resources with `.Owns()` / `.Watches()`

### Logging Style

Follow [Kubernetes logging conventions](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-instrumentation/logging.md):

- Start with a capital letter, no trailing period
- Past tense, object-specific messages
- Balanced key/value pairs

```go
log.Info("Created Deployment", "name", deploy.Name)
log.Error(err, "Failed to create Pod", "name", podName)
```

---

## Make Target Reference

### Development

| Target | Description |
|--------|-------------|
| `make refresh-profiles` | Regenerate hardware profiles JSON |
| `make refresh-profiles-check` | Verify profile registry is current |
| `make sim-generate` | Refresh profiles + `go generate` |
| `make kind-up` | Create/reuse a local Kind cluster |

### Code Generation & Quality

| Target | Description |
|--------|-------------|
| `make manifests` | Generate CRDs, RBAC, webhooks |
| `make generate` | Generate DeepCopy code |
| `make fmt` | `go fmt ./...` |
| `make vet` | `go vet ./...` |
| `make lint` | Run golangci-lint |
| `make lint-fix` | Run golangci-lint with autofixes |

### Build & Run

| Target | Description |
|--------|-------------|
| `make build` | Build manager binary to `bin/manager` |
| `make run` | Run controller from host |
| `make docker-build` | Build container image (`IMG`) |
| `make docker-push` | Push container image (`IMG`) |
| `make docker-buildx` | Multi-arch build/push (`PLATFORMS`) |
| `make build-installer` | Generate `dist/install.yaml` |

### Cluster Lifecycle

| Target | Description |
|--------|-------------|
| `make install` | Install CRDs into current cluster |
| `make uninstall` | Remove CRDs |
| `make deploy` | Deploy controller manifests |
| `make undeploy` | Remove controller manifests |
| `make stack-up` | Full pipeline deployment (Kind + all components) |
| `make stack-down-fast` | Delete Kind cluster |

### Tests

| Target | Description |
|--------|-------------|
| `make test` | Unit/integration tests (envtest) |
| `make test-e2e` | E2E tests (Kind) |
| `make test-e2e-full` | Full-pipeline E2E |
| `make setup-test-e2e` | Ensure E2E Kind cluster exists |
| `make cleanup-test-e2e` | Delete E2E Kind cluster |

### Tool Bootstrap

| Target | Description |
|--------|-------------|
| `make kustomize` | Install kustomize |
| `make controller-gen` | Install controller-gen |
| `make envtest` | Install setup-envtest |
| `make golangci-lint` | Install golangci-lint |

---

## Key Make Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `IMG` | `controller:latest` | Image tag for build/deploy |
| `CONTAINER_TOOL` | `docker` | Container tool command |
| `PLATFORMS` | — | Target architectures for buildx |
| `KIND_CLUSTER` | `gpu-scheduler-test-e2e` | E2E Kind cluster name |

---

## Distribution

### YAML Bundle

```bash
make build-installer IMG=<registry>/<project>:<tag>
# Produces dist/install.yaml
kubectl apply -f dist/install.yaml
```

### Helm Chart

```bash
kubebuilder edit --plugins=helm/v2-alpha
```

---

## References

- [Kubebuilder Book](https://book.kubebuilder.io)
- [controller-runtime FAQ](https://github.com/kubernetes-sigs/controller-runtime/blob/main/FAQ.md)
- [Kubernetes API Conventions](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md)
- [Kubebuilder Markers](https://book.kubebuilder.io/reference/markers.html)
- [Kubernetes Logging Conventions](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-instrumentation/logging.md)
