# Contributor workflow (Kubebuilder)

This guide is for humans contributing to `gpu-scheduler`. It summarizes the practical Kubebuilder workflow used in this repo.

## Quick start

```bash
# Show available tasks
make help

# Run controller locally (includes generation + checks)
make run

# Run unit/integration tests (envtest)
make test
```

## Most common command workflows

### Fast local development

```bash
make run
```

Runs generation/checks and starts the controller locally.

### Validate code quality and tests

```bash
make test
```

Verifies generated profile data is current, regenerates manifests/code, runs fmt/vet, sets up envtest assets, and executes non-e2e tests.

### Run e2e tests (Kind)

```bash
make test-e2e
```

Creates/reuses the e2e Kind cluster, runs the e2e suite, and cleans up the cluster after completion.

Override e2e cluster name:

```bash
make test-e2e KIND_CLUSTER=my-e2e-cluster
```

Notes:

- E2E tests are intended for an isolated Kind cluster
- Avoid running e2e against shared dev/prod clusters

**Full-pipeline e2e** (producer → Kafka → submitter → operator → worker → simulator) runs only when requested:

```bash
make test-e2e-full
```

To also save output to a parseable log file (console + file): `make test-e2e-full-log` (writes to `e2e-full.log` by default), or override the path: `make test-e2e-full-log E2E_FULL_LOG=path/to/my.log`. You can get the same effect manually with: `make test-e2e-full 2>&1 | tee e2e-full.log`.

This runs only the "Full pipeline" describe with a longer timeout. To run it as part of the full suite (after Manager tests): `E2E_FULL_PIPELINE=true make test-e2e`.

Full-pipeline e2e requires fleet registration with the simulator to succeed (no best-effort). The test sets `STRICT_REMOTE_FLEET=true` on the operator and waits for the simulator to be ready before applying the pool, so reconciliation retries until the remote simulator has the fleet.

### Build and deploy controller image

```bash
export IMG=<registry>/<repo>:<tag>
make docker-build docker-push IMG=$IMG
make deploy IMG=$IMG
kubectl apply -k config/samples/
```

Debug:

```bash
kubectl logs -n <project>-system deployment/<project>-controller-manager -c manager -f
```

### Producer (workload config)

The producer (`make run-producer`, `cmd/producer`) publishes WorkloadRequest messages to the Kafka topic used by the submitter. For port-forward and run steps, see [deploy/README.md](../deploy/README.md).

**Producer config file:** The `-config` flag takes a path to a JSON file. The file may be a single **WorkloadRequest** object or an **array** of WorkloadRequest objects. Schema matches the Kafka message format: `request_id`, `tenant`, `priority`, `gpuCount`, `gpuMemoryMiB`, `tokens`, `modelProfile`, `kind`, and the per-kind payload (e.g. `training` with `globalBatchSize`, `microBatchSize`, `gradAccumSteps`, `sequenceLength`). See `internal/kafka/workload_request.go` and `config/demo/contention-10-workloads.json` for reference. When `-config` is set, `-count` and generator options are ignored.

### Demo capture script

`scripts/demo-capture.sh [OUTPUT_FILE] [WORKLOAD_CONFIG_JSON]` polls GPUWorkloads, events, and operator/submitter/worker/simulator logs, appends them to an output file every 30s, and exits when all expected workloads are in a terminal phase (Succeeded/Failed) or after 1 hour. With `WORKLOAD_CONFIG_JSON` (same format as producer `-config`), the script checks exactly those workloads by `request_id`; otherwise it requires at least `EXPECTED_COUNT` in the namespace and all terminal. Requires `jq` when the config JSON is used. Default output: `demo-capture-<timestamp>.log`. Overrides (env): `WORKLOAD_NS`, `EXPECTED_COUNT`, `POLL_INTERVAL`, `MAX_DURATION_SEC`. See script header for usage.

### Baseline demo: deploy stack, Grafana, and producer

To run a **baseline demo** (metrics + dashboards + 10 workloads) and capture results:

**Prerequisites:** Docker, [Kind](https://kind.sigs.k8s.io/), [Helm](https://helm.sh/), `kubectl`, and (for the producer) Go.

1. **Deploy pipeline and monitoring** (from repo root):
   ```bash
   make stack-up
   ```
   This brings up Kind, builds/loads images, deploys Kafka, KEDA, simulator, operator, submitter, worker-deployment, applies the default sample GPUNodePool, and installs Prometheus + Grafana (kube-prometheus-stack with `deploy/monitoring/values.yaml`). Override pool with `make stack-up APPLY_POOL=path/to/pool.yaml` or skip with `make stack-up APPLY_POOL=`. If pods show `ImagePullBackOff`, run `make stack-load-images` and restart deployments (see [deploy/README.md](../deploy/README.md)).

2. **Port-forward Grafana and Kafka** (separate terminals or background):
   - **Grafana:** `kubectl port-forward -n monitoring svc/kube-prometheus-stack-grafana 3000:80` → http://localhost:3000, login `admin` / `admin` (from `deploy/monitoring/values.yaml`).
   - **Kafka (for producer):** `kubectl port-forward -n kafka pod/kafka-dual-role-0 9092:9094` so the broker advertises localhost:9092. If the pod name differs, use `kubectl get pods -n kafka` and forward that pod’s 9094 to host 9092.

3. **Configure Grafana:** Add Prometheus datasource URL `http://kube-prometheus-stack-prometheus.monitoring.svc.cluster.local:9090`. Import dashboards from `config/grafana/dashboards/gpu-scheduler-pipeline.json` and `gpu-scheduler-resources.json`. For the resources dashboard, set the namespace variable to the namespaces where pipeline pods run (e.g. `system`, `gpu-scheduler-system`).

4. **Run producer and capture:** From repo root:
   ```bash
   make run-producer ARGS="-brokers=localhost:9092 -count=10 -interval=500ms"
   kubectl get gpuworkloads -A -w   # optional: watch phases
   EXPECTED_COUNT=10 ./scripts/demo-capture.sh demo-run.log
   ```

5. **Teardown:** Stop port-forwards, then `make stack-down-fast` to delete the Kind cluster (no separate monitoring teardown).

If the pipeline dashboard stays empty, ensure Prometheus is scraping the operator (ServiceMonitor), submitter, simulator, and worker (see `deploy/monitoring/values.yaml`). If the resources dashboard stays empty, ensure the stack scrapes kubelet/cAdvisor and the namespace variable includes pipeline pod namespaces. If worker KEDA scaling does not trigger, patch the ScaledObject `serverAddress` to your Prometheus service (e.g. `kubectl -n system edit scaledobject worker-deployment`).

## Project layout

Single-group Kubebuilder layout (this repo):

- `cmd/main.go`: manager entrypoint. **Must be generated via Kubebuilder** (scaffold then merge project wiring). Retain all `// +kubebuilder:scaffold:imports`, `// +kubebuilder:scaffold:scheme`, and `// +kubebuilder:scaffold:builder` markers; do not replace with a fully hand-written main.
- `api/<version>/*_types.go`: API schemas + kubebuilder markers
- `internal/controller/*`: reconciliation logic
- `internal/webhook/*`: validation/defaulting webhooks
- `config/samples/*`: sample resources

Generated outputs:

- `config/crd/bases/*` (CRDs)
- `config/rbac/role.yaml` (RBAC)
- `api/<version>/zz_generated.*` (deepcopy/codegen)

## `config/` folder guide

`config/` contains Kubernetes manifests and Kustomize overlays used by `make install`, `make deploy`, and sample application flows.

- `config/default/`: Main deployment composition; wires together CRDs, RBAC, manager deployment, metrics service, and optional feature toggles (webhook/cert-manager/prometheus/network-policy).
- `config/manager/`: Controller-manager runtime manifests (Deployment and namespace scaffolding).
- `config/crd/`: CRD manifests and CRD-specific kustomize config; defines/registers custom APIs like `GPUWorkload`.
- `config/rbac/`: ServiceAccount, manager RBAC, leader-election RBAC, metrics auth RBAC, and helper admin/editor/viewer roles for CRDs.
- `config/samples/`: Example Custom Resources for quick testing (`kubectl apply -k config/samples`).
- `config/prometheus/`: Optional `ServiceMonitor` resources/patches for Prometheus scraping of controller metrics.
- `config/network-policy/`: Optional NetworkPolicy resources to restrict access (for example metrics ingress).

Typical usage:

- `make install` applies CRDs from `config/crd`.
- `make deploy` builds/applies `config/default` (which includes manager + RBAC + metrics service and references CRDs).
- `kubectl apply -k config/samples` creates example CR instances.

## Scaffold new APIs and webhooks

Always use Kubebuilder CLI for scaffolding:

```bash
# Create a new API + controller
kubebuilder create api --group <group> --version <version> --kind <Kind>

# Add validation/defaulting webhooks
kubebuilder create webhook --group <group> --version <version> --kind <Kind> \
  --defaulting --programmatic-validation
```

For multi-version conversion:

```bash
kubebuilder create webhook --group <group> --version v1 --kind <Kind> \
  --conversion --spoke v2
```

## Regeneration rules

After editing API types or kubebuilder markers in `*_types.go`:

```bash
make manifests
make generate
```

After editing Go implementation files:

```bash
make lint-fix
make test
```

## Build-time vs runtime (important)

`make` targets are a development/CI step, not something the in-cluster operator executes.

- Build-time (your machine or CI): generate code/manifests and build/deploy artifacts
- Runtime (in cluster): the controller watches CRs and reconciles continuously

End-to-end flow:

1. Edit API/controller code
2. Run generation/checks (`make manifests`, `make generate`, `make test`)
3. Install/deploy manifests (`make install` / `make deploy`)
4. Create CR instances (`kubectl apply -f ...` or another client)
5. Operator reconcile loop reacts and updates `.status`

The operator pod does not run `make manifests` or `make generate` in real time.

## Controller implementation guidelines

- Keep reconciliation idempotent
- Re-fetch before updates to reduce conflicts
- Use owner references for dependent resources
- Use finalizers for external cleanup
- Prefer `metav1.Condition` for status conditions
- Watch secondary resources with `.Owns()` / `.Watches()` where appropriate

## Logging style

Use Kubernetes-style logs:

- Start with capital letter
- No trailing period
- Past tense and object-specific messages
- Keep balanced key/value fields

Example:

```go
log.Info("Created Deployment", "name", deploy.Name)
log.Error(err, "Failed to create Pod", "name", podName)
```

## Make target reference

### Development

- `make refresh-profiles`: Regenerate `internal/sim/hardware_profiles.json` from config.
- `make refresh-profiles-check`: Verify generated profile registry is up to date.
- `make refresh-profiles-snapshot`: Fetch and snapshot upstream profile data.
- `make sim-generate`: Refresh profiles and run `go generate ./...`.
- `make kind-up`: Create/reuse a local Kind cluster.

### Code generation and checks

- `make manifests`: Generate CRDs, RBAC, and webhook manifests.
- `make generate`: Generate deepcopy and related code.
- `make fmt`: Run `go fmt ./...`.
- `make vet`: Run `go vet ./...`.
- `make lint`: Run golangci-lint.
- `make lint-fix`: Run golangci-lint with autofixes.
- `make lint-config`: Verify linter configuration.

### Build and run

- `make build`: Build manager binary to `bin/manager`.
- `make run`: Run controller from host.
- `make docker-build`: Build container image (`IMG`).
- `make docker-push`: Push container image (`IMG`).
- `make docker-buildx`: Build/push multi-arch image (`PLATFORMS`).
- `make build-installer`: Generate `dist/install.yaml`.

### Cluster lifecycle

- `make install`: Install CRDs into current cluster context.
- `make uninstall`: Remove CRDs from current cluster context.
- `make deploy`: Deploy controller manifests.
- `make undeploy`: Remove deployed controller manifests.

### Tests

- `make setup-test-e2e`: Ensure e2e Kind cluster exists.
- `make test`: Run unit/integration tests (envtest).
- `make test-e2e`: Run e2e tests.
- `make cleanup-test-e2e`: Delete e2e Kind cluster.

### Tool bootstrap

- `make kustomize`: Install `kustomize` into `bin/` if needed.
- `make controller-gen`: Install `controller-gen` into `bin/` if needed.
- `make envtest`: Install `setup-envtest` helper into `bin/` if needed.
- `make setup-envtest`: Download Kubernetes envtest binaries.
- `make golangci-lint`: Install golangci-lint into `bin/` if needed.

## Important Make variables

- `IMG` (default: `controller:latest`): image tag used by build/deploy targets.
- `CONTAINER_TOOL` (default: `docker`): container tool command.
- `PLATFORMS`: target architectures for `docker-buildx`.
- `KIND_CLUSTER` (default: `gpu-scheduler-test-e2e`): e2e Kind cluster name.
- `ignore-not-found` (default: `false`): delete behavior for uninstall/undeploy targets.

## Distribution options

### Option 1: Single YAML installer

```bash
make build-installer IMG=<registry>/<project>:<tag>
```

Produces `dist/install.yaml` for `kubectl apply -f ...` installs.

### Option 2: Helm chart scaffolding

```bash
kubebuilder edit --plugins=helm/v2-alpha
```

Use when you want Helm-native installs for users.

## Reference docs

- Kubebuilder Book: [https://book.kubebuilder.io](https://book.kubebuilder.io)
- controller-runtime FAQ: [https://github.com/kubernetes-sigs/controller-runtime/blob/main/FAQ.md](https://github.com/kubernetes-sigs/controller-runtime/blob/main/FAQ.md)
- Kubernetes API conventions: [https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md)
- Kubebuilder markers reference: [https://book.kubebuilder.io/reference/markers.html](https://book.kubebuilder.io/reference/markers.html)

