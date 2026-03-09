# Image URL to use all building/pushing image targets
IMG ?= controller:latest

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# CONTAINER_TOOL defines the container tool to be used for building images.
# Be aware that the target commands are only tested with Docker which is
# scaffolded by default. However, you might want to replace it to use other
# tools. (i.e. podman)
CONTAINER_TOOL ?= docker

# Setting SHELL to bash allows bash commands to be executed by recipes.
# Options are set to exit when a recipe line exits non-zero or a piped command fails.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

.PHONY: all
all: build

##@ General

# The help target prints out all targets with their descriptions organized
# beneath their categories. The categories are represented by '##@' and the
# target descriptions by '##'. The awk command is responsible for reading the
# entire set of makefiles included in this invocation, looking for lines of the
# file as xyz: ## something, and then pretty-format the target and help. Then,
# if there's a line with ##@ something, that gets pretty-printed as a category.
# More info on the usage of ANSI control characters for terminal formatting:
# https://en.wikipedia.org/wiki/ANSI_escape_code#SGR_parameters
# More info on the awk command:
# http://linuxcommand.org/lc3_adv_awk.php

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: refresh-profiles
refresh-profiles: ## Refresh internal hardware profile registry.
	go run ./tools/refresh_profiles -in configs/hardware_profiles.yaml -out internal/sim/hardware_profiles.json

.PHONY: refresh-profiles-check
refresh-profiles-check: ## Verify hardware profile registry is up-to-date.
	go run ./tools/refresh_profiles -in configs/hardware_profiles.yaml -out internal/sim/hardware_profiles.json -check

.PHONY: refresh-profiles-snapshot
refresh-profiles-snapshot: ## Fetch and snapshot upstream hardware profile data sources.
	go run ./tools/refresh_profiles -in configs/hardware_profiles.yaml -fetch-sources

.PHONY: sim-generate
sim-generate: refresh-profiles ## Run go:generate after refreshing profiles.
	go generate ./...

# Local Kind cluster for deployment and demos (section 10). E2e tests use a separate cluster (KIND_CLUSTER).
KIND_CLUSTER_LOCAL ?= gpu-scheduler

.PHONY: kind-up
kind-up: ## Create (or reuse) a local Kind cluster for deployment and demos.
	@command -v $(KIND) >/dev/null 2>&1 || { \
		echo "Kind is not installed. Install from https://kind.sigs.k8s.io/docs/user/quick-start/"; \
		exit 1; \
	}
	@case "$$($(KIND) get clusters 2>/dev/null)" in \
		*"$(KIND_CLUSTER_LOCAL)"*) \
			echo "Kind cluster '$(KIND_CLUSTER_LOCAL)' already exists. Skipping creation." ;; \
		*) \
			echo "Creating Kind cluster '$(KIND_CLUSTER_LOCAL)'..."; \
			$(KIND) create cluster --name $(KIND_CLUSTER_LOCAL) ;; \
	esac

.PHONY: kind-down
kind-down: ## Delete the local Kind cluster used for deployment and demos.
	@$(KIND) delete cluster --name $(KIND_CLUSTER_LOCAL) 2>/dev/null || true

# Kafka (Strimzi) for local Kind. Requires: kind-up, Helm, kubectl context = Kind.
KAFKA_NS ?= kafka
STRIMZI_HELM_REPO ?= oci://quay.io/strimzi-helm/strimzi-kafka-operator

.PHONY: kafka-up
kafka-up: ## Deploy Strimzi operator and a single-node Kafka cluster in Kind (topic gpu.workloads.submit created).
	@command -v helm >/dev/null 2>&1 || { echo "Helm is not installed. Install from https://helm.sh/docs/intro/install/"; exit 1; }
	@$(KUBECTL) create namespace $(KAFKA_NS) --dry-run=client -o yaml | $(KUBECTL) apply -f -
	@helm upgrade --install strimzi-cluster-operator $(STRIMZI_HELM_REPO) \
		--namespace $(KAFKA_NS) \
		--wait --timeout 5m
	@echo "Waiting for Strimzi Cluster Operator to be ready..."
	@$(KUBECTL) wait --for=condition=Available deployment/strimzi-cluster-operator -n $(KAFKA_NS) --timeout=120s 2>/dev/null || true
	@$(KUBECTL) apply -k deploy/kafka
	@echo "Waiting for Kafka cluster to be ready (may take a few minutes)..."
	@$(KUBECTL) wait --for=condition=Ready kafka/kafka -n $(KAFKA_NS) --timeout=600s

.PHONY: kafka-down
kafka-down: ## Remove Strimzi Kafka and operator from the cluster.
	@$(KUBECTL) delete -k deploy/kafka --ignore-not-found --timeout=120s 2>/dev/null || true
	@helm uninstall strimzi-cluster-operator -n $(KAFKA_NS) 2>/dev/null || true
	@$(KUBECTL) delete namespace $(KAFKA_NS) --timeout=120s 2>/dev/null || true

# KEDA for submitter (and worker-deployment) autoscaling. Required by submitter-up.
KEDA_NS ?= keda
KEDA_HELM_REPO ?= https://kedacore.github.io/charts
KEDA_VALUES ?= deploy/keda/values.yaml

.PHONY: keda-up
keda-up: kind-up ## Install KEDA in the cluster (required for submitter ScaledObject).
	@command -v helm >/dev/null 2>&1 || { echo "Helm is not installed. Install from https://helm.sh/docs/intro/install/"; exit 1; }
	@helm repo add kedacore $(KEDA_HELM_REPO) 2>/dev/null || true
	@helm repo update
	@$(KUBECTL) create namespace $(KEDA_NS) --dry-run=client -o yaml | $(KUBECTL) apply -f -
	@helm upgrade --install keda kedacore/keda --namespace $(KEDA_NS) -f $(KEDA_VALUES) --wait --timeout 5m
	@echo "Waiting for KEDA operator to be ready..."
	@$(KUBECTL) wait --for=condition=Available deployment/keda -n $(KEDA_NS) --timeout=120s 2>/dev/null || true

.PHONY: keda-down
keda-down: ## Remove KEDA from the cluster.
	@helm uninstall keda -n $(KEDA_NS) 2>/dev/null || true
	@$(KUBECTL) delete namespace $(KEDA_NS) --ignore-not-found --timeout=120s 2>/dev/null || true

.PHONY: simulator-up
simulator-up: kind-up docker-build-simulator ## Deploy simulator service to Kind (build image, load, apply).
	$(KIND) load docker-image $(SIMULATOR_IMG) --name $(KIND_CLUSTER_LOCAL)
	$(KUBECTL) apply -k deploy/simulator

.PHONY: simulator-down
simulator-down: ## Remove simulator service from the cluster (leaves system namespace intact).
	@$(KUBECTL) delete deployment simulator -n system --ignore-not-found --timeout=60s 2>/dev/null || true
	@$(KUBECTL) delete service simulator -n system --ignore-not-found --timeout=60s 2>/dev/null || true

.PHONY: operator-up
operator-up: kind-up docker-build kustomize ## Deploy operator (CRDs + controller) to Kind. Builds and loads $(IMG) first.
	$(KIND) load docker-image $(IMG) --name $(KIND_CLUSTER_LOCAL)
	cd config/manager && "$(CURDIR)/$(KUSTOMIZE)" edit set image controller=$(IMG)
	$(KUSTOMIZE) build config/default | $(KUBECTL) apply -f -

.PHONY: operator-down
operator-down: kustomize ## Remove operator (CRDs + controller) from the cluster.
	$(KUSTOMIZE) build config/default | $(KUBECTL) delete --ignore-not-found=true --timeout=120s -f - 2>/dev/null || true

.PHONY: manifests
manifests: controller-gen ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	"$(CONTROLLER_GEN)" rbac:roleName=manager-role crd:allowDangerousTypes=true webhook paths="./api/...;./internal/controller/..." output:crd:artifacts:config=config/crd/bases

.PHONY: generate
generate: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	"$(CONTROLLER_GEN)" object:headerFile="hack/boilerplate.go.txt" paths="./api/..."

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: test
test: refresh-profiles-check manifests generate fmt vet setup-envtest ## Run tests.
	KUBEBUILDER_ASSETS="$(abspath $(strip $(shell "$(ENVTEST)" use $(ENVTEST_K8S_VERSION) --bin-dir "$(LOCALBIN)" -p path)))" go test $$(go list ./... | grep -v /e2e)

# Packages that have at least one test file (avoids covdata tool missing with auto-downloaded Go toolchain).
TEST_PKGS ?= $(shell go list -f '{{if or (len .TestGoFiles) (len .XTestGoFiles)}}{{.ImportPath}}{{end}}' ./... | grep -v /e2e)

.PHONY: test-cover
test-cover: refresh-profiles-check manifests generate fmt vet setup-envtest ## Run tests with coverage (only packages that have tests).
	KUBEBUILDER_ASSETS="$(abspath $(strip $(shell "$(ENVTEST)" use $(ENVTEST_K8S_VERSION) --bin-dir "$(LOCALBIN)" -p path)))" go test $(TEST_PKGS) -coverprofile cover.out

# TODO(user): To use a different vendor for e2e tests, modify the setup under 'tests/e2e'.
# The default setup assumes Kind is pre-installed and builds/loads the Manager Docker image locally.
# CertManager is installed by default; skip with:
# - CERT_MANAGER_INSTALL_SKIP=true
KIND_CLUSTER ?= gpu-scheduler-test-e2e

.PHONY: setup-test-e2e
setup-test-e2e: ## Set up a Kind cluster for e2e tests if it does not exist
	@command -v $(KIND) >/dev/null 2>&1 || { \
		echo "Kind is not installed. Please install Kind manually."; \
		exit 1; \
	}
	@case "$$($(KIND) get clusters)" in \
		*"$(KIND_CLUSTER)"*) \
			echo "Kind cluster '$(KIND_CLUSTER)' already exists. Skipping creation." ;; \
		*) \
			echo "Creating Kind cluster '$(KIND_CLUSTER)'..."; \
			$(KIND) create cluster --name $(KIND_CLUSTER) ;; \
	esac

# E2E test timeout: docker-build (and load image, cert-manager, deploy, specs) can exceed default 10m on first run.
E2E_TIMEOUT ?= 25m
.PHONY: test-e2e
test-e2e: setup-test-e2e manifests generate fmt vet ## Run the e2e tests. Expected an isolated environment using Kind.
	KIND=$(KIND) KIND_CLUSTER=$(KIND_CLUSTER) go test -tags=e2e ./test/e2e/ -v -ginkgo.v -timeout $(E2E_TIMEOUT)
	$(MAKE) cleanup-test-e2e

# Full-pipeline e2e: producer -> Kafka -> submitter -> operator -> worker -> simulator. Longer timeout (Kafka + workloads).
E2E_FULL_PIPELINE_TIMEOUT ?= 60m
.PHONY: test-e2e-full
test-e2e-full: setup-test-e2e manifests generate fmt vet ## Run only the full-pipeline e2e test (E2E_FULL_PIPELINE=true, focus Full pipeline).
	KIND=$(KIND) KIND_CLUSTER=$(KIND_CLUSTER) E2E_FULL_PIPELINE=true go test -tags=e2e ./test/e2e/ -v -ginkgo.v \
		-ginkgo.focus "Full pipeline" -timeout $(E2E_FULL_PIPELINE_TIMEOUT)
	$(MAKE) cleanup-test-e2e

# Log file for test-e2e-full when using test-e2e-full-log. Default: e2e-full.log (overridable).
E2E_FULL_LOG ?= e2e-full.log
.PHONY: test-e2e-full-log
test-e2e-full-log: setup-test-e2e manifests generate fmt vet ## Run full-pipeline e2e and tee output to console and $(E2E_FULL_LOG).
	@set -o pipefail; KIND=$(KIND) KIND_CLUSTER=$(KIND_CLUSTER) E2E_FULL_PIPELINE=true go test -tags=e2e ./test/e2e/ -v -ginkgo.v \
		-ginkgo.focus "Full pipeline" -timeout $(E2E_FULL_PIPELINE_TIMEOUT) 2>&1 | tee "$(E2E_FULL_LOG)"; \
	rc=$$?; $(MAKE) cleanup-test-e2e; exit $$rc

.PHONY: cleanup-test-e2e
cleanup-test-e2e: ## Tear down the Kind cluster used for e2e tests
	@$(KIND) delete cluster --name $(KIND_CLUSTER)

.PHONY: lint
lint: golangci-lint ## Run golangci-lint linter
	"$(GOLANGCI_LINT)" run

.PHONY: lint-fix
lint-fix: golangci-lint ## Run golangci-lint linter and perform fixes
	"$(GOLANGCI_LINT)" run --fix

.PHONY: lint-config
lint-config: golangci-lint ## Verify golangci-lint linter configuration
	"$(GOLANGCI_LINT)" config verify

##@ Build

.PHONY: build
build: manifests generate fmt vet ## Build manager binary.
	go build -o bin/manager cmd/main.go

.PHONY: build-submitter
build-submitter: manifests generate fmt vet ## Build submitter binary (Kafka consumer that creates GPUWorkload CRs).
	go build -o bin/submitter cmd/submitter/main.go

.PHONY: run
run: manifests generate fmt vet ## Run a controller from your host.
	go run ./cmd/main.go

.PHONY: run-submitter
run-submitter: manifests generate fmt vet ## Run the submitter consumer from your host.
	go run ./cmd/submitter/main.go

.PHONY: build-producer
build-producer: manifests generate fmt vet ## Build producer binary (loadgen that publishes WorkloadRequest to Kafka).
	go build -o bin/producer cmd/producer/main.go

.PHONY: run-producer
run-producer: manifests generate fmt vet ## Run the producer/loadgen from your host. Pass flags via ARGS, e.g. make run-producer ARGS="-brokers=localhost:9092 -config=docs/fixtures/contention-10-workloads.json -burst"
	go run ./cmd/producer/main.go $(ARGS)

.PHONY: build-worker
build-worker: manifests generate fmt vet ## Build worker binary (Job container: calls simulator and exits on completion).
	go build -o bin/worker cmd/worker/main.go

.PHONY: build-worker-deployment
build-worker-deployment: manifests generate fmt vet ## Build worker-deployment binary (scale-focused loop: poll Scheduled, claim, run sim).
	go build -o bin/worker-deployment cmd/worker-deployment/main.go

.PHONY: build-simulator
build-simulator: manifests generate fmt vet ## Build simulator binary (HTTP server for fleet/allocate/start/status).
	go build -o bin/simulator cmd/simulator/main.go

SUBMITTER_IMG ?= gpu-scheduler-submitter:latest
.PHONY: docker-build-submitter
docker-build-submitter: manifests generate fmt vet ## Build docker image for the submitter consumer.
	$(CONTAINER_TOOL) build -f Dockerfile.submitter -t $(SUBMITTER_IMG) .

PRODUCER_IMG ?= gpu-scheduler-producer:latest
.PHONY: docker-build-producer
docker-build-producer: manifests generate fmt vet ## Build docker image for the producer (e2e full pipeline).
	$(CONTAINER_TOOL) build -f Dockerfile.producer -t $(PRODUCER_IMG) .

.PHONY: submitter-up
submitter-up: kind-up keda-up docker-build-submitter ## Deploy submitter consumer to Kind (installs KEDA if needed, build image, load, apply).
	$(KIND) load docker-image $(SUBMITTER_IMG) --name $(KIND_CLUSTER_LOCAL)
	$(KUBECTL) apply -k deploy/submitter

.PHONY: submitter-down
submitter-down: ## Remove submitter consumer from the cluster.
	@$(KUBECTL) delete -k deploy/submitter --ignore-not-found --timeout=60s 2>/dev/null || true

# Worker deployment (scale-focused): long-lived pods that claim Phase=Scheduled workloads and run simulator.
# Requires: KEDA, operator with USE_WORKER_POOL=true, simulator service. KEDA scales on Prometheus gpu_scheduler_scheduled_workloads.
WORKER_DEPLOYMENT_IMG ?= gpu-scheduler-worker-deployment:latest
WORKER_DEPLOYMENT_NS ?= gpu-scheduler-system
WORKER_DEPLOYMENT_NAME ?= gpu-scheduler-controller-manager

.PHONY: docker-build-worker-deployment
docker-build-worker-deployment: manifests generate fmt vet ## Build docker image for the worker-deployment (scale-focused workers).
	$(CONTAINER_TOOL) build -f Dockerfile.worker-deployment -t $(WORKER_DEPLOYMENT_IMG) .

# Simulator URL for the operator so it registers the fleet with the remote simulator (workers call that service).
SIMULATOR_URL ?= http://simulator.system.svc.cluster.local:8080
.PHONY: worker-up
worker-up: kind-up keda-up operator-up simulator-up docker-build-worker-deployment ## Deploy worker-deployment to Kind (enables operator worker-pool mode, build image, load, apply).
	$(KUBECTL) set env deployment/$(WORKER_DEPLOYMENT_NAME) -n $(WORKER_DEPLOYMENT_NS) USE_WORKER_POOL=true SIMULATOR_URL="$(SIMULATOR_URL)" --containers=manager 2>/dev/null || true
	$(KUBECTL) rollout restart deployment/$(WORKER_DEPLOYMENT_NAME) -n $(WORKER_DEPLOYMENT_NS)
	$(KIND) load docker-image $(WORKER_DEPLOYMENT_IMG) --name $(KIND_CLUSTER_LOCAL)
	$(KUBECTL) apply -k deploy/worker-deployment

.PHONY: worker-down
worker-down: ## Remove worker-deployment from the cluster and disable operator worker-pool mode.
	@$(KUBECTL) delete -k deploy/worker-deployment --ignore-not-found --timeout=60s 2>/dev/null || true
	@$(KUBECTL) set env deployment/$(WORKER_DEPLOYMENT_NAME) -n $(WORKER_DEPLOYMENT_NS) USE_WORKER_POOL- --containers=manager 2>/dev/null || true

# GPUNodePool to apply after operator and simulator are up (register fleet with simulator).
# Override with make stack-up APPLY_POOL=path/to/pool.yaml; set APPLY_POOL= to skip applying any pool.
APPLY_POOL ?= config/samples/scheduler_v1alpha1_gpunodepool.yaml

.PHONY: apply-pool
apply-pool: ## Apply GPUNodePool manifest from APPLY_POOL (default: sample pool). Set APPLY_POOL= to skip.
	@[ -n '$(APPLY_POOL)' ] && $(KUBECTL) apply -f $(APPLY_POOL) || true

.PHONY: apply-sample-pool
apply-sample-pool: apply-pool ## Apply the sample GPUNodePool (convenience for APPLY_POOL default).

# Full end-to-end pipeline: Kind, Kafka, monitoring (Prometheus CRDs first so ServiceMonitor exists),
# then KEDA, simulator, operator, submitter, workers, and optional GPUNodePool (APPLY_POOL).
.PHONY: stack-up
stack-up: kind-up stack-load-images kafka-up monitoring-up submitter-up worker-up apply-pool ## Bring up pipeline + Prometheus/Grafana + pool (APPLY_POOL). Monitoring before operator so ServiceMonitor CRD exists.

MONITORING_NS ?= monitoring
PROMETHEUS_HELM_REPO ?= https://prometheus-community.github.io/helm-charts
KUBE_PROMETHEUS_STACK_RELEASE ?= kube-prometheus-stack
MONITORING_VALUES ?= deploy/monitoring/values.yaml

.PHONY: monitoring-up
monitoring-up: kind-up ## Install Prometheus and Grafana (kube-prometheus-stack) with scrape configs for pipeline metrics.
	@command -v helm >/dev/null 2>&1 || { echo "Helm is not installed. Install from https://helm.sh/docs/intro/install/"; exit 1; }
	@helm repo add prometheus-community $(PROMETHEUS_HELM_REPO) 2>/dev/null || true
	@helm repo update
	@$(KUBECTL) create namespace $(MONITORING_NS) --dry-run=client -o yaml | $(KUBECTL) apply -f -
	@helm upgrade --install $(KUBE_PROMETHEUS_STACK_RELEASE) prometheus-community/kube-prometheus-stack \
		--namespace $(MONITORING_NS) \
		--create-namespace \
		-f $(MONITORING_VALUES) \
		--wait --timeout 10m
	@echo "Monitoring stack ready. Port-forward Grafana: kubectl port-forward -n $(MONITORING_NS) svc/$(KUBE_PROMETHEUS_STACK_RELEASE)-grafana 3000:80"

.PHONY: stack-load-images
stack-load-images: kind-up docker-build docker-build-simulator docker-build-submitter docker-build-worker-deployment ## Build all stack images and load them into Kind (fixes ImagePullBackOff / "failing to pull image").
	$(KIND) load docker-image $(IMG) --name $(KIND_CLUSTER_LOCAL)
	$(KIND) load docker-image $(SIMULATOR_IMG) --name $(KIND_CLUSTER_LOCAL)
	$(KIND) load docker-image $(SUBMITTER_IMG) --name $(KIND_CLUSTER_LOCAL)
	$(KIND) load docker-image $(WORKER_DEPLOYMENT_IMG) --name $(KIND_CLUSTER_LOCAL)
	@echo "Images loaded. Restart pods if they were failing: kubectl rollout restart deployment -n gpu-scheduler-system gpu-scheduler-controller-manager; kubectl rollout restart deployment -n system submitter simulator worker-deployment"

.PHONY: stack-down
stack-down: worker-down submitter-down operator-down simulator-down keda-down kafka-down kind-down ## Tear down the full e2e pipeline and the Kind cluster.

.PHONY: stack-down-fast
stack-down-fast: ## Delete the Kind cluster immediately (skip per-component teardown). Use when stack-down is slow or stuck.
	@$(KIND) delete cluster --name $(KIND_CLUSTER_LOCAL) 2>/dev/null || true

# Workload correlation IDs and journey logs (submitter -> operator -> worker -> simulator).
# Correlation ID = GPUWorkload name or namespace/name (e.g. default/loadgen-1-123).
.PHONY: list-workload-ids
list-workload-ids: ## List all GPUWorkload correlation IDs (namespace, name, phase, age).
	@$(CURDIR)/scripts/workload-logs.sh list

.PHONY: workload-logs
workload-logs: ## Fetch full journey logs for one workload. Usage: make workload-logs CORRELATION_ID=default/loadgen-1-123
	@if [ -z "$(CORRELATION_ID)" ]; then \
		echo "Usage: make workload-logs CORRELATION_ID=<name or namespace/name>"; \
		echo "  Example: make workload-logs CORRELATION_ID=loadgen-1-2855833041059322389"; \
		echo "  Run 'make list-workload-ids' to see correlation IDs."; \
		exit 1; \
	fi
	$(CURDIR)/scripts/workload-logs.sh logs "$(CORRELATION_ID)"

# If you wish to build the manager image targeting other platforms you can use the --platform flag.
# (i.e. docker build --platform linux/arm64). However, you must enable docker buildKit for it.
# More info: https://docs.docker.com/develop/develop-images/build_enhancements/
.PHONY: docker-build
docker-build: ## Build docker image with the manager.
	$(CONTAINER_TOOL) build -t ${IMG} .

SIMULATOR_IMG ?= gpu-scheduler-simulator:latest
.PHONY: docker-build-simulator
docker-build-simulator: manifests generate fmt vet ## Build docker image for the simulator service.
	$(CONTAINER_TOOL) build -f Dockerfile.simulator -t $(SIMULATOR_IMG) .

.PHONY: docker-push
docker-push: ## Push docker image with the manager.
	$(CONTAINER_TOOL) push ${IMG}

# PLATFORMS defines the target platforms for the manager image be built to provide support to multiple
# architectures. (i.e. make docker-buildx IMG=myregistry/mypoperator:0.0.1). To use this option you need to:
# - be able to use docker buildx. More info: https://docs.docker.com/build/buildx/
# - have enabled BuildKit. More info: https://docs.docker.com/develop/develop-images/build_enhancements/
# - be able to push the image to your registry (i.e. if you do not set a valid value via IMG=<myregistry/image:<tag>> then the export will fail)
# To adequately provide solutions that are compatible with multiple platforms, you should consider using this option.
PLATFORMS ?= linux/arm64,linux/amd64,linux/s390x,linux/ppc64le
.PHONY: docker-buildx
docker-buildx: ## Build and push docker image for the manager for cross-platform support
	# copy existing Dockerfile and insert --platform=${BUILDPLATFORM} into Dockerfile.cross, and preserve the original Dockerfile
	sed -e '1 s/\(^FROM\)/FROM --platform=\$$\{BUILDPLATFORM\}/; t' -e ' 1,// s//FROM --platform=\$$\{BUILDPLATFORM\}/' Dockerfile > Dockerfile.cross
	- $(CONTAINER_TOOL) buildx create --name gpu-scheduler-builder
	$(CONTAINER_TOOL) buildx use gpu-scheduler-builder
	- $(CONTAINER_TOOL) buildx build --push --platform=$(PLATFORMS) --tag ${IMG} -f Dockerfile.cross .
	- $(CONTAINER_TOOL) buildx rm gpu-scheduler-builder
	rm Dockerfile.cross

.PHONY: build-installer
build-installer: manifests generate kustomize ## Generate a consolidated YAML with CRDs and deployment.
	mkdir -p dist
	cd config/manager && "$(CURDIR)/$(KUSTOMIZE)" edit set image controller=${IMG}
	"$(KUSTOMIZE)" build config/default > dist/install.yaml

##@ Deployment

ifndef ignore-not-found
  ignore-not-found = false
endif

.PHONY: install
install: manifests kustomize ## Install CRDs into the K8s cluster specified in ~/.kube/config.
	@out="$$( "$(KUSTOMIZE)" build config/crd 2>/dev/null || true )"; \
	if [ -n "$$out" ]; then echo "$$out" | "$(KUBECTL)" apply -f -; else echo "No CRDs to install; skipping."; fi

.PHONY: uninstall
uninstall: manifests kustomize ## Uninstall CRDs from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	@out="$$( "$(KUSTOMIZE)" build config/crd 2>/dev/null || true )"; \
	if [ -n "$$out" ]; then echo "$$out" | "$(KUBECTL)" delete --ignore-not-found=$(ignore-not-found) -f -; else echo "No CRDs to delete; skipping."; fi

.PHONY: deploy
deploy: manifests kustomize ## Deploy controller to the K8s cluster specified in ~/.kube/config.
	cd config/manager && "$(CURDIR)/$(KUSTOMIZE)" edit set image controller=${IMG}
	"$(KUSTOMIZE)" build config/default | "$(KUBECTL)" apply -f -

.PHONY: undeploy
undeploy: kustomize ## Undeploy controller from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	"$(KUSTOMIZE)" build config/default | "$(KUBECTL)" delete --ignore-not-found=$(ignore-not-found) -f -

##@ Dependencies

## Location to install dependencies to
#
# NOTE: This repo path may include spaces (e.g. "Gpu Scheduler"). Make treats
# spaces as separators in target/prerequisite lists, so using an absolute path
# here breaks dependency targets. Use a relative path instead.
LOCALBIN ?= bin
$(LOCALBIN):
	mkdir -p "$(LOCALBIN)"

## Tool Binaries
KUBECTL ?= kubectl
KIND ?= kind
KUSTOMIZE ?= $(LOCALBIN)/kustomize
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
ENVTEST ?= $(LOCALBIN)/setup-envtest
GOLANGCI_LINT = $(LOCALBIN)/golangci-lint

## Tool Versions
KUSTOMIZE_VERSION ?= v5.8.1
CONTROLLER_TOOLS_VERSION ?= v0.20.1

#ENVTEST_VERSION is the version of controller-runtime release branch to fetch the envtest setup script (i.e. release-0.20)
ENVTEST_VERSION ?= $(shell v='$(call gomodver,sigs.k8s.io/controller-runtime)'; \
  [ -n "$$v" ] || { echo "Set ENVTEST_VERSION manually (controller-runtime replace has no tag)" >&2; exit 1; }; \
  printf '%s\n' "$$v" | sed -E 's/^v?([0-9]+)\.([0-9]+).*/release-\1.\2/')

#ENVTEST_K8S_VERSION is the version of Kubernetes to use for setting up ENVTEST binaries (i.e. 1.31)
ENVTEST_K8S_VERSION ?= $(shell v='$(call gomodver,k8s.io/api)'; \
  [ -n "$$v" ] || { echo "Set ENVTEST_K8S_VERSION manually (k8s.io/api replace has no tag)" >&2; exit 1; }; \
  printf '%s\n' "$$v" | sed -E 's/^v?[0-9]+\.([0-9]+).*/1.\1/')

GOLANGCI_LINT_VERSION ?= v2.8.0
.PHONY: kustomize
kustomize: $(KUSTOMIZE) ## Download kustomize locally if necessary.
$(KUSTOMIZE): $(LOCALBIN)
	$(call go-install-tool,$(KUSTOMIZE),sigs.k8s.io/kustomize/kustomize/v5,$(KUSTOMIZE_VERSION))

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary.
$(CONTROLLER_GEN): $(LOCALBIN)
	$(call go-install-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen,$(CONTROLLER_TOOLS_VERSION))

.PHONY: setup-envtest
setup-envtest: envtest ## Download the binaries required for ENVTEST in the local bin directory.
	@echo "Setting up envtest binaries for Kubernetes version $(ENVTEST_K8S_VERSION)..."
	@"$(ENVTEST)" use $(ENVTEST_K8S_VERSION) --bin-dir "$(LOCALBIN)" -p path || { \
		echo "Error: Failed to set up envtest binaries for version $(ENVTEST_K8S_VERSION)."; \
		exit 1; \
	}

.PHONY: envtest
envtest: $(ENVTEST) ## Download setup-envtest locally if necessary.
$(ENVTEST): $(LOCALBIN)
	$(call go-install-tool,$(ENVTEST),sigs.k8s.io/controller-runtime/tools/setup-envtest,$(ENVTEST_VERSION))

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT) ## Download golangci-lint locally if necessary.
$(GOLANGCI_LINT): $(LOCALBIN)
	$(call go-install-tool,$(GOLANGCI_LINT),github.com/golangci/golangci-lint/v2/cmd/golangci-lint,$(GOLANGCI_LINT_VERSION))
	@test -f .custom-gcl.yml && { \
		echo "Building custom golangci-lint with plugins..." && \
		$(GOLANGCI_LINT) custom --destination $(LOCALBIN) --name golangci-lint-custom && \
		mv -f $(LOCALBIN)/golangci-lint-custom $(GOLANGCI_LINT); \
	} || true

# go-install-tool will 'go install' any package with custom target and name of binary, if it doesn't exist
# $1 - target path with name of binary
# $2 - package url which can be installed
# $3 - specific version of package
define go-install-tool
@[ -f "$(1)-$(3)" ] && [ "$$(readlink -- "$(1)" 2>/dev/null)" = "$(1)-$(3)" ] || { \
set -e; \
package=$(2)@$(3) ;\
echo "Downloading $${package}" ;\
rm -f "$(1)" ;\
GOBIN="$(abspath $(LOCALBIN))" go install $${package} ;\
mv "$(LOCALBIN)/$$(basename "$(1)")" "$(1)-$(3)" ;\
} ;\
ln -sf "$$(realpath "$(1)-$(3)")" "$(1)"
endef

define gomodver
$(shell go list -m -f '{{if .Replace}}{{.Replace.Version}}{{else}}{{.Version}}{{end}}' $(1) 2>/dev/null)
endef
