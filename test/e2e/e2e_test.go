//go:build e2e
// +build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/ishanchopra/gpu-scheduler/test/utils"
)

// namespace where the project is deployed in
const namespace = "gpu-scheduler-system"

// serviceAccountName created for the project
const serviceAccountName = "gpu-scheduler-controller-manager"

// metricsServiceName is the name of the metrics service of the project
const metricsServiceName = "gpu-scheduler-controller-manager-metrics-service"

// metricsRoleBindingName is the name of the RBAC that will be created to allow get the metrics data
const metricsRoleBindingName = "gpu-scheduler-metrics-binding"

var _ = Describe("Manager", Ordered, func() {
	var controllerPodName string

	// Before running the tests, set up the environment by creating the namespace,
	// enforce the restricted security policy to the namespace, installing CRDs,
	// and deploying the controller.
	BeforeAll(func() {
		By("creating manager namespace")
		cmd := exec.Command("kubectl", "create", "ns", namespace)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to create namespace")

		By("labeling the namespace to enforce the restricted security policy")
		cmd = exec.Command("kubectl", "label", "--overwrite", "ns", namespace,
			"pod-security.kubernetes.io/enforce=restricted")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to label namespace with restricted policy")

		By("installing CRDs")
		cmd = exec.Command("make", "install")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to install CRDs")

		By("deploying the controller-manager")
		cmd = exec.Command("make", "deploy", fmt.Sprintf("IMG=%s", managerImage))
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to deploy the controller-manager")
	})

	// After all tests have been executed, clean up by deleting CRs (so the controller can remove
	// finalizers while still running), then undeploying the controller, uninstalling CRDs,
	// and deleting the namespace. Deleting CRs before undeploy prevents CRD deletion from hanging
	// because CRs with finalizers would otherwise block CRD removal.
	AfterAll(func() {
		By("cleaning up the curl pod for metrics")
		cmd := exec.Command("kubectl", "delete", "pod", "curl-metrics", "-n", namespace)
		_, _ = utils.Run(cmd)

		By("deleting custom resources so controller can remove finalizers before undeploy")
		for _, ns := range []string{"default", "system"} {
			for _, kind := range []string{"gpuworkloads", "gpunodepools", "tenantquotas"} {
				cmd = exec.Command("kubectl", "delete", kind, "--all", "-n", ns, "--ignore-not-found=true", "--wait=false")
				_, _ = utils.Run(cmd)
			}
		}
		By("waiting for custom resources to be gone so CRD deletion can complete")
		Eventually(func(g Gomega) {
			for _, ns := range []string{"default", "system"} {
				cmd = exec.Command("kubectl", "get", "gpuworkloads,gpunodepools,tenantquotas", "-n", ns, "--no-headers", "-o", "name")
				out, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(strings.TrimSpace(out)).To(BeEmpty(), "expected no CRs in namespace %s", ns)
			}
		}, 90*time.Second, 3*time.Second).Should(Succeed())

		By("undeploying the controller-manager")
		cmd = exec.Command("make", "undeploy")
		_, _ = utils.Run(cmd)

		By("uninstalling CRDs")
		cmd = exec.Command("make", "uninstall")
		_, _ = utils.Run(cmd)

		By("removing manager namespace")
		cmd = exec.Command("kubectl", "delete", "ns", namespace)
		_, _ = utils.Run(cmd)
	})

	// After each test, check for failures and collect logs, events,
	// and pod descriptions for debugging.
	AfterEach(func() {
		specReport := CurrentSpecReport()
		if specReport.Failed() && controllerPodName != "" {
			By("Fetching controller manager pod logs")
			cmd := exec.Command("kubectl", "logs", controllerPodName, "-n", namespace)
			controllerLogs, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Controller logs:\n %s", controllerLogs)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get Controller logs: %s", err)
			}

			By("Fetching Kubernetes events")
			cmd = exec.Command("kubectl", "get", "events", "-n", namespace, "--sort-by=.lastTimestamp")
			eventsOutput, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Kubernetes events:\n%s", eventsOutput)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get Kubernetes events: %s", err)
			}

			By("Fetching curl-metrics logs")
			cmd = exec.Command("kubectl", "logs", "curl-metrics", "-n", namespace)
			metricsOutput, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Metrics logs:\n %s", metricsOutput)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get curl-metrics logs: %s", err)
			}

			if controllerPodName != "" {
				By("Fetching controller manager pod description")
				cmd = exec.Command("kubectl", "describe", "pod", controllerPodName, "-n", namespace)
				podDescription, err := utils.Run(cmd)
				if err == nil {
					fmt.Println("Pod description:\n", podDescription)
				} else {
					fmt.Println("Failed to describe controller pod")
				}
			}
		}
	})

	SetDefaultEventuallyTimeout(2 * time.Minute)
	SetDefaultEventuallyPollingInterval(time.Second)

	Context("Manager", func() {
		It("should run successfully", func() {
			By("validating that the controller-manager pod is running as expected")
			verifyControllerUp := func(g Gomega) {
				// Get the name of the controller-manager pod
				cmd := exec.Command("kubectl", "get",
					"pods", "-l", "control-plane=controller-manager",
					"-o", "go-template={{ range .items }}"+
						"{{ if not .metadata.deletionTimestamp }}"+
						"{{ .metadata.name }}"+
						"{{ \"\\n\" }}{{ end }}{{ end }}",
					"-n", namespace,
				)

				podOutput, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to retrieve controller-manager pod information")
				podNames := utils.GetNonEmptyLines(podOutput)
				g.Expect(podNames).To(HaveLen(1), "expected 1 controller pod running")
				controllerPodName = podNames[0]
				g.Expect(controllerPodName).To(ContainSubstring("controller-manager"))

				// Validate the pod's status
				cmd = exec.Command("kubectl", "get",
					"pods", controllerPodName, "-o", "jsonpath={.status.phase}",
					"-n", namespace,
				)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Running"), "Incorrect controller-manager pod status")
			}
			Eventually(verifyControllerUp).Should(Succeed())
		})

		It("should ensure the metrics endpoint is serving metrics", func() {
			By("creating or updating ClusterRoleBinding for the service account to allow access to metrics")
			cmd := exec.Command("kubectl", "create", "clusterrolebinding", metricsRoleBindingName,
				"--clusterrole=gpu-scheduler-metrics-reader",
				fmt.Sprintf("--serviceaccount=%s:%s", namespace, serviceAccountName),
				"--dry-run=client", "-o", "yaml",
			)
			out, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to generate ClusterRoleBinding YAML")
			cmd = exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(out)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to apply ClusterRoleBinding")

			By("validating that the metrics service is available")
			cmd = exec.Command("kubectl", "get", "service", metricsServiceName, "-n", namespace)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Metrics service should exist")

			By("getting the service account token")
			token, err := serviceAccountToken()
			Expect(err).NotTo(HaveOccurred())
			Expect(token).NotTo(BeEmpty())

			By("ensuring the controller pod is ready")
			verifyControllerPodReady := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pod", controllerPodName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"), "Controller pod not ready")
			}
			Eventually(verifyControllerPodReady, 3*time.Minute, time.Second).Should(Succeed())

			By("verifying that the controller manager is serving the metrics server")
			verifyMetricsServerStarted := func(g Gomega) {
				cmd := exec.Command("kubectl", "logs", controllerPodName, "-n", namespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(ContainSubstring("Serving metrics server"),
					"Metrics server not yet started")
			}
			Eventually(verifyMetricsServerStarted, 3*time.Minute, time.Second).Should(Succeed())

			// +kubebuilder:scaffold:e2e-metrics-webhooks-readiness

			By("creating the curl-metrics pod to access the metrics endpoint")
			cmd = exec.Command("kubectl", "run", "curl-metrics", "--restart=Never",
				"--namespace", namespace,
				"--image=curlimages/curl:latest",
				"--overrides",
				fmt.Sprintf(`{
					"spec": {
						"containers": [{
							"name": "curl",
							"image": "curlimages/curl:latest",
							"command": ["/bin/sh", "-c"],
							"args": [
								"for i in $(seq 1 30); do curl -v -k -H 'Authorization: Bearer %s' https://%s.%s.svc.cluster.local:8443/metrics && exit 0 || sleep 2; done; exit 1"
							],
							"securityContext": {
								"readOnlyRootFilesystem": true,
								"allowPrivilegeEscalation": false,
								"capabilities": {
									"drop": ["ALL"]
								},
								"runAsNonRoot": true,
								"runAsUser": 1000,
								"seccompProfile": {
									"type": "RuntimeDefault"
								}
							}
						}],
						"serviceAccountName": "%s"
					}
				}`, token, metricsServiceName, namespace, serviceAccountName))
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create curl-metrics pod")

			By("waiting for the curl-metrics pod to complete.")
			verifyCurlUp := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pods", "curl-metrics",
					"-o", "jsonpath={.status.phase}",
					"-n", namespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Succeeded"), "curl pod in wrong status")
			}
			Eventually(verifyCurlUp, 5*time.Minute).Should(Succeed())

			By("getting the metrics by checking curl-metrics logs")
			verifyMetricsAvailable := func(g Gomega) {
				metricsOutput, err := getMetricsOutput()
				g.Expect(err).NotTo(HaveOccurred(), "Failed to retrieve logs from curl pod")
				g.Expect(metricsOutput).NotTo(BeEmpty())
				g.Expect(metricsOutput).To(ContainSubstring("< HTTP/1.1 200 OK"))
			}
			Eventually(verifyMetricsAvailable, 2*time.Minute).Should(Succeed())
		})

		// +kubebuilder:scaffold:e2e-webhooks-checks

		Context("Workload lifecycle", func() {
			const workloadNS = "default"
			var fixtureDir string

			BeforeAll(func() {
				projectDir, err := utils.GetProjectDir()
				Expect(err).NotTo(HaveOccurred())
				fixtureDir = filepath.Join(projectDir, "test", "e2e", "fixtures")

				By("applying sample GPUNodePool to register fleet")
				poolPath := filepath.Join(fixtureDir, "gpunodepool.yaml")
				cmd := exec.Command("kubectl", "apply", "-f", poolPath)
				_, err = utils.Run(cmd)
				Expect(err).NotTo(HaveOccurred(), "Failed to apply GPUNodePool")

				By("waiting for GPUNodePool to be reconciled (fleet registered)")
				Eventually(func(g Gomega) {
					cmd := exec.Command("kubectl", "get", "gpunodepool", "e2e-pool", "-n", workloadNS,
						"-o", "jsonpath={.status.totalDevices}")
					out, err := utils.Run(cmd)
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(out).To(Equal("8"), "fleet should have 8 devices (2 nodes * 4)")
				}, 2*time.Minute, 2*time.Second).Should(Succeed())

				By("applying GPUWorkloads")
				workloadsPath := filepath.Join(fixtureDir, "gpuworkloads.yaml")
				cmd = exec.Command("kubectl", "apply", "-f", workloadsPath)
				_, err = utils.Run(cmd)
				Expect(err).NotTo(HaveOccurred(), "Failed to apply GPUWorkloads")
			})

			AfterAll(func() {
				workloadsPath := filepath.Join(fixtureDir, "gpuworkloads.yaml")
				_, _ = utils.Run(exec.Command("kubectl", "delete", "-f", workloadsPath, "--ignore-not-found", "--timeout=30s"))
				poolPath := filepath.Join(fixtureDir, "gpunodepool.yaml")
				_, _ = utils.Run(exec.Command("kubectl", "delete", "-f", poolPath, "--ignore-not-found", "--timeout=30s"))
			})

			It("sets all workloads to Queued and emits Queued events", func() {
				By("waiting for all three workloads to be processed (phase at least Queued; may have progressed to Scheduled/Running/Succeeded)")
				validPhases := map[string]bool{"Queued": true, "Scheduled": true, "Running": true, "Succeeded": true}
				for _, name := range []string{"e2e-workload-1", "e2e-workload-2", "e2e-workload-3"} {
					Eventually(func(g Gomega) {
						cmd := exec.Command("kubectl", "get", "gpuworkload", name, "-n", workloadNS,
							"-o", "jsonpath={.status.phase}")
						out, err := utils.Run(cmd)
						g.Expect(err).NotTo(HaveOccurred())
						phase := strings.TrimSpace(out)
						g.Expect(validPhases[phase]).To(BeTrue(), "expected phase Queued/Scheduled/Running/Succeeded, got %q", phase)
					}, 2*time.Minute, 2*time.Second).Should(Succeed())
				}

				By("verifying Queued events exist for the workloads")
				cmd := exec.Command("kubectl", "get", "events", "-n", workloadNS,
					"--field-selector", "reason=Queued", "-o", "jsonpath={range .items[*]}{.involvedObject.name}{' '}{end}")
				out, err := utils.Run(cmd)
				Expect(err).NotTo(HaveOccurred())
				Expect(out).To(ContainSubstring("e2e-workload-1"))
				Expect(out).To(ContainSubstring("e2e-workload-2"))
				Expect(out).To(ContainSubstring("e2e-workload-3"))
			})

			It("admits at least one workload and emits Admitted event", func() {
				By("waiting for at least one workload to be admitted (phase Scheduled, Running, or Succeeded)")
				Eventually(func(g Gomega) {
					cmd := exec.Command("kubectl", "get", "gpuworkloads", "-n", workloadNS,
						"-o", "jsonpath={range .items[*]}{.status.phase}{'\\n'}{end}")
					out, err := utils.Run(cmd)
					g.Expect(err).NotTo(HaveOccurred())
					phases := strings.Split(strings.TrimSpace(out), "\n")
					admitted := false
					for _, p := range phases {
						if p == "Scheduled" || p == "Running" || p == "Succeeded" {
							admitted = true
							break
						}
					}
					g.Expect(admitted).To(BeTrue(), "expected at least one workload admitted, got phases: %v", phases)
				}, 3*time.Minute, 2*time.Second).Should(Succeed())

				By("verifying Admitted event exists for at least one workload")
				cmd := exec.Command("kubectl", "get", "events", "-n", workloadNS,
					"--field-selector", "reason=Admitted", "-o", "jsonpath={range .items[*]}{.involvedObject.name}{' '}{end}")
				out, err := utils.Run(cmd)
				Expect(err).NotTo(HaveOccurred())
				Expect(out).NotTo(BeEmpty())
				admittedNames := strings.TrimSpace(out)
				hasAdmitted := strings.Contains(admittedNames, "e2e-workload-1") ||
					strings.Contains(admittedNames, "e2e-workload-2") ||
					strings.Contains(admittedNames, "e2e-workload-3")
				Expect(hasAdmitted).To(BeTrue(), "expected Admitted event for an e2e-workload, got: %s", admittedNames)
			})

			It("runs at least one workload to completion (Succeeded)", func() {
				By("waiting for at least one workload to reach Succeeded")
				Eventually(func(g Gomega) {
					cmd := exec.Command("kubectl", "get", "gpuworkloads", "-n", workloadNS,
						"-o", "jsonpath={range .items[*]}{.metadata.name}{':'}{.status.phase}{'\\n'}{end}")
					out, err := utils.Run(cmd)
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(out).To(ContainSubstring("Succeeded"),
						"expected at least one workload Succeeded, got:\n%s", out)
				}, 5*time.Minute, 5*time.Second).Should(Succeed())
			})

			It("eventually runs all workloads to completion (all Succeeded)", func() {
				By("waiting for all three workloads to reach Succeeded")
				for _, name := range []string{"e2e-workload-1", "e2e-workload-2", "e2e-workload-3"} {
					Eventually(func(g Gomega) {
						cmd := exec.Command("kubectl", "get", "gpuworkload", name, "-n", workloadNS,
							"-o", "jsonpath={.status.phase}")
						out, err := utils.Run(cmd)
						g.Expect(err).NotTo(HaveOccurred())
						g.Expect(strings.TrimSpace(out)).To(Equal("Succeeded"),
							"expected workload %q to reach Succeeded", name)
					}, 8*time.Minute, 10*time.Second).Should(Succeed())
				}
			})
		})
	})

	// Full pipeline runs only when E2E_FULL_PIPELINE=true. It brings up producer -> Kafka -> submitter ->
	// operator -> worker -> simulator and asserts workloads reach Succeeded.
	Describe("Full pipeline", Ordered, func() {
		const (
			fullPipelineNS     = "gpu-scheduler-system"
			fullPipelineWorker = "default"
			producerJobName    = "e2e-producer"
			producerCount      = 3
		)
		var projectDir string

		BeforeAll(func() {
			if os.Getenv("E2E_FULL_PIPELINE") != "true" {
				Skip("Full pipeline e2e is skipped unless E2E_FULL_PIPELINE=true")
			}

			var err error
			projectDir, err = utils.GetProjectDir()
			Expect(err).NotTo(HaveOccurred(), "Failed to get project dir")

			kindCluster := os.Getenv("KIND_CLUSTER")
			if kindCluster == "" {
				kindCluster = "gpu-scheduler-test-e2e"
			}
			envWithKind := append(os.Environ(), "KIND_CLUSTER_LOCAL="+kindCluster)
			kindBin := "kind"
			if b := os.Getenv("KIND"); b != "" {
				kindBin = b
			}

			By("creating manager namespace and installing operator")
			cmd := exec.Command("kubectl", "create", "ns", fullPipelineNS)
			_, _ = utils.Run(cmd)
			cmd = exec.Command("kubectl", "label", "--overwrite", "ns", fullPipelineNS,
				"pod-security.kubernetes.io/enforce=restricted")
			_, _ = utils.Run(cmd)
			cmd = exec.Command("make", "-C", projectDir, "install")
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to install CRDs")
			cmd = exec.Command("make", "-C", projectDir, "deploy", fmt.Sprintf("IMG=%s", managerImage))
			cmd.Env = envWithKind
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to deploy controller-manager")

			By("building and loading simulator, submitter, worker, producer images")
			for _, target := range []string{"docker-build-simulator", "docker-build-submitter", "docker-build-worker-deployment", "docker-build-producer"} {
				cmd = exec.Command("make", "-C", projectDir, target)
				_, err = utils.Run(cmd)
				Expect(err).NotTo(HaveOccurred(), "Failed to build %s", target)
			}
			for _, img := range []string{"gpu-scheduler-simulator:latest", "gpu-scheduler-submitter:latest", "gpu-scheduler-worker-deployment:latest", "gpu-scheduler-producer:latest"} {
				cmd = exec.Command(kindBin, "load", "docker-image", img, "--name", kindCluster)
				_, err = utils.Run(cmd)
				Expect(err).NotTo(HaveOccurred(), "Failed to load image %s into Kind", img)
			}

			By("deploying Kafka and KEDA")
			cmd = exec.Command("make", "-C", projectDir, "kafka-up")
			cmd.Env = envWithKind
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to bring up Kafka")
			cmd = exec.Command("make", "-C", projectDir, "keda-up")
			cmd.Env = envWithKind
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to bring up KEDA")

			By("deploying simulator and waiting for it to be ready (required for strict fleet registration)")
			cmd = exec.Command("kubectl", "apply", "-k", filepath.Join(projectDir, "deploy/simulator"))
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to apply simulator")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "rollout", "status", "deployment/simulator", "-n", "system", "--timeout=120s")
				_, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
			}, 3*time.Minute, 5*time.Second).Should(Succeed())

			By("patching operator for worker-pool, simulator URL, and STRICT_REMOTE_FLEET (fleet registration must succeed)")
			cmd = exec.Command("kubectl", "set", "env", "deployment/gpu-scheduler-controller-manager",
				"-n", fullPipelineNS, "USE_WORKER_POOL=true", "STRICT_REMOTE_FLEET=true", "--containers=manager")
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to set operator env")
			cmd = exec.Command("kubectl", "patch", "deployment", "gpu-scheduler-controller-manager", "-n", fullPipelineNS,
				"--type=json", "-p", `[{"op": "add", "path": "/spec/template/spec/containers/0/args/-", "value": "--use-worker-pool"}, {"op": "add", "path": "/spec/template/spec/containers/0/args/-", "value": "--simulator-url=http://simulator.system.svc.cluster.local:8080"}]`)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to patch operator with simulator URL")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "rollout", "status", "deployment/gpu-scheduler-controller-manager", "-n", fullPipelineNS, "--timeout=120s")
				_, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
			}, 3*time.Minute, 5*time.Second).Should(Succeed())

			By("deploying submitter and worker")
			cmd = exec.Command("kubectl", "apply", "-k", filepath.Join(projectDir, "deploy/submitter"))
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to apply submitter")
			cmd = exec.Command("kubectl", "apply", "-k", filepath.Join(projectDir, "deploy/worker-deployment"))
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to apply worker-deployment")

			By("applying GPUNodePool and waiting for fleet")
			cmd = exec.Command("kubectl", "apply", "-f", filepath.Join(projectDir, "test/e2e/fixtures/gpunodepool.yaml"))
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to apply GPUNodePool")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "gpunodepool", "e2e-pool", "-n", fullPipelineWorker,
					"-o", "jsonpath={.status.totalDevices}")
				out, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(strings.TrimSpace(out)).To(Equal("8"))
			}, 3*time.Minute, 5*time.Second).Should(Succeed())

			By("creating producer Job (delete any existing Job so this run produces fresh messages)")
			_, _ = utils.Run(exec.Command("kubectl", "delete", "job", producerJobName, "-n", "system", "--ignore-not-found", "--wait", "--timeout=60s"))
			producerJobYaml := fmt.Sprintf(`apiVersion: batch/v1
kind: Job
metadata:
  name: %s
  namespace: system
spec:
  template:
    spec:
      restartPolicy: Never
      containers:
        - name: producer
          image: gpu-scheduler-producer:latest
          imagePullPolicy: IfNotPresent
          args:
            - --brokers=kafka-kafka-bootstrap.kafka.svc.cluster.local:9092
            - --count=%d
            - --burst
  backoffLimit: 2
`, producerJobName, producerCount)
			producerJobFile := filepath.Join(projectDir, "test/e2e/fixtures/producer-job.yaml")
			Expect(os.WriteFile(producerJobFile, []byte(producerJobYaml), 0o600)).To(Succeed())
			DeferCleanup(func() { _ = os.Remove(producerJobFile) })
			cmd = exec.Command("kubectl", "apply", "-f", producerJobFile)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create producer Job")
		})

		AfterEach(func() {
			specReport := CurrentSpecReport()
			if !specReport.Failed() {
				return
			}
			By("Full pipeline failure: dumping controller-manager logs")
			cmd := exec.Command("kubectl", "get", "pods", "-n", fullPipelineNS,
				"-l", "control-plane=controller-manager", "-o", "jsonpath={.items[0].metadata.name}")
			if podName, err := utils.Run(cmd); err == nil && strings.TrimSpace(podName) != "" {
				cmd = exec.Command("kubectl", "logs", strings.TrimSpace(podName), "-n", fullPipelineNS)
				if out, err := utils.Run(cmd); err == nil {
					_, _ = fmt.Fprintf(GinkgoWriter, "Controller-manager logs:\n%s\n", out)
				}
			}
			By("Full pipeline failure: dumping GPUWorkloads and events in default")
			cmd = exec.Command("kubectl", "get", "gpuworkloads", "-n", fullPipelineWorker, "-o", "wide")
			if out, err := utils.Run(cmd); err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "GPUWorkloads:\n%s\n", out)
			}
			cmd = exec.Command("kubectl", "get", "events", "-n", fullPipelineWorker, "--sort-by=.lastTimestamp")
			if out, err := utils.Run(cmd); err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Events (default ns):\n%s\n", out)
			}
		})

		It("produces messages and workloads reach Succeeded", func() {
			By("waiting for producer Job to complete")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "job", producerJobName, "-n", "system",
					"-o", "jsonpath={.status.succeeded}")
				out, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(strings.TrimSpace(out)).To(Equal("1"))
			}, 3*time.Minute, 5*time.Second).Should(Succeed())

			By("waiting for all produced workloads to reach Succeeded (full pipeline: producer -> Kafka -> submitter -> operator -> worker -> simulator)")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "gpuworkloads", "-n", fullPipelineWorker,
					"-o", "jsonpath={range .items[*]}{.metadata.name}{':'}{.status.phase}{'\\n'}{end}")
				out, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				lines := strings.Split(strings.TrimSpace(out), "\n")
				succeeded := 0
				var phases []string
				for _, line := range lines {
					line = strings.TrimSpace(line)
					if line == "" {
						continue
					}
					parts := strings.SplitN(line, ":", 2)
					if len(parts) == 2 && parts[1] == "Succeeded" {
						succeeded++
					}
					if len(parts) == 2 {
						phases = append(phases, parts[1])
					}
				}
				g.Expect(succeeded).To(BeNumerically(">=", producerCount),
					"expected at least %d Succeeded (total workloads: %d), got phases: %v", producerCount, len(phases), phases)
			}, 12*time.Minute, 10*time.Second).Should(Succeed())
		})
	})
})

// serviceAccountToken returns a token for the specified service account in the given namespace.
// It uses the Kubernetes TokenRequest API to generate a token by directly sending a request
// and parsing the resulting token from the API response.
func serviceAccountToken() (string, error) {
	const tokenRequestRawString = `{
		"apiVersion": "authentication.k8s.io/v1",
		"kind": "TokenRequest"
	}`

	// Temporary file to store the token request
	secretName := fmt.Sprintf("%s-token-request", serviceAccountName)
	tokenRequestFile := filepath.Join("/tmp", secretName)
	err := os.WriteFile(tokenRequestFile, []byte(tokenRequestRawString), os.FileMode(0o644))
	if err != nil {
		return "", err
	}

	var out string
	verifyTokenCreation := func(g Gomega) {
		// Execute kubectl command to create the token
		cmd := exec.Command("kubectl", "create", "--raw", fmt.Sprintf(
			"/api/v1/namespaces/%s/serviceaccounts/%s/token",
			namespace,
			serviceAccountName,
		), "-f", tokenRequestFile)

		output, err := cmd.CombinedOutput()
		g.Expect(err).NotTo(HaveOccurred())

		// Parse the JSON output to extract the token
		var token tokenRequest
		err = json.Unmarshal(output, &token)
		g.Expect(err).NotTo(HaveOccurred())

		out = token.Status.Token
	}
	Eventually(verifyTokenCreation).Should(Succeed())

	return out, err
}

// getMetricsOutput retrieves and returns the logs from the curl pod used to access the metrics endpoint.
func getMetricsOutput() (string, error) {
	By("getting the curl-metrics logs")
	cmd := exec.Command("kubectl", "logs", "curl-metrics", "-n", namespace)
	return utils.Run(cmd)
}

// tokenRequest is a simplified representation of the Kubernetes TokenRequest API response,
// containing only the token field that we need to extract.
type tokenRequest struct {
	Status struct {
		Token string `json:"token"`
	} `json:"status"`
}
