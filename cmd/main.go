package main

import (
	"crypto/tls"
	"flag"
	"os"
	"strconv"
	"strings"

	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	schedulerv1alpha1 "github.com/ishanchopra/gpu-scheduler/api/v1alpha1"
	"github.com/ishanchopra/gpu-scheduler/internal/controller"
	"github.com/ishanchopra/gpu-scheduler/internal/metrics"
	"github.com/ishanchopra/gpu-scheduler/internal/sim"
	"github.com/ishanchopra/gpu-scheduler/internal/worker"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(schedulerv1alpha1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

func main() {
	var metricsAddr string
	var metricsCertPath, metricsCertName, metricsCertKey string
	var webhookCertPath, webhookCertName, webhookCertKey string
	var enableLeaderElection bool
	var probeAddr string
	var secureMetrics bool
	var enableHTTP2 bool
	flag.StringVar(&metricsAddr, "metrics-bind-address", "0",
		"The address the metrics endpoint binds to. Use :8443 for HTTPS or :8080 for HTTP, or 0 to disable.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false, "Enable leader election for controller manager.")
	flag.BoolVar(&secureMetrics, "metrics-secure", true, "If set, the metrics endpoint is served securely via HTTPS.")
	flag.StringVar(&webhookCertPath, "webhook-cert-path", "", "The directory that contains the webhook certificate.")
	flag.StringVar(&webhookCertName, "webhook-cert-name", "tls.crt", "The name of the webhook certificate file.")
	flag.StringVar(&webhookCertKey, "webhook-cert-key", "tls.key", "The name of the webhook key file.")
	flag.StringVar(&metricsCertPath, "metrics-cert-path", "",
		"The directory that contains the metrics server certificate.")
	flag.StringVar(&metricsCertName, "metrics-cert-name", "tls.crt", "The name of the metrics server certificate file.")
	flag.StringVar(&metricsCertKey, "metrics-cert-key", "tls.key", "The name of the metrics server key file.")
	flag.BoolVar(&enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers.")
	opts := zap.Options{Development: true}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	tlsOpts := []func(*tls.Config){}
	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, func(c *tls.Config) { c.NextProtos = []string{"http/1.1"} })
	}

	webhookServerOptions := webhook.Options{TLSOpts: tlsOpts}
	if webhookCertPath != "" {
		webhookServerOptions.CertDir = webhookCertPath
		webhookServerOptions.CertName = webhookCertName
		webhookServerOptions.KeyName = webhookCertKey
	}
	webhookServer := webhook.NewServer(webhookServerOptions)

	metricsServerOptions := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: secureMetrics,
		TLSOpts:       tlsOpts,
	}
	if secureMetrics {
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}
	if metricsCertPath != "" {
		metricsServerOptions.CertDir = metricsCertPath
		metricsServerOptions.CertName = metricsCertName
		metricsServerOptions.KeyName = metricsCertKey
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsServerOptions,
		WebhookServer:          webhookServer,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "gpu-scheduler.ishanchopra.dev",
	})
	if err != nil {
		setupLog.Error(err, "Failed to create manager")
		os.Exit(1)
	}

	// +kubebuilder:scaffold:builder

	localSim := sim.NewSimulator()
	simulatorURL := strings.TrimSpace(os.Getenv("SIMULATOR_URL"))
	useWorkerPool := strings.ToLower(strings.TrimSpace(os.Getenv("USE_WORKER_POOL"))) == "true"

	claimableBrokers := strings.TrimSpace(os.Getenv("KAFKA_BROKERS"))
	claimableTopic := strings.TrimSpace(os.Getenv("CLAIMABLE_TOPIC"))

	maxAdmissionsPerCycle := 1
	if v := strings.TrimSpace(os.Getenv("MAX_ADMISSIONS_PER_CYCLE")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 {
			maxAdmissionsPerCycle = n
		}
	}

	var claimableProducer controller.ClaimableProducer
	if claimableBrokers != "" {
		claimableProducer = controller.NewKafkaClaimableProducer(claimableBrokers, claimableTopic)
		defer func() {
			if claimableProducer != nil {
				_ = claimableProducer.Close()
			}
		}()
	}

	fleetRegistrar := controller.NewMultiFleetRegistrar(localSim, simulatorURL, true)

	if err = (&controller.GPUNodePoolReconciler{
		Client:         mgr.GetClient(),
		Scheme:         mgr.GetScheme(),
		FleetRegistrar: fleetRegistrar,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "gpunodepool")
		os.Exit(1)
	}

	if err = (&controller.TenantQuotaReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "tenantquota")
		os.Exit(1)
	}

	if err = (&controller.GPUWorkloadReconciler{
		Client:           mgr.GetClient(),
		Scheme:           mgr.GetScheme(),
		RuntimeSimulator: localSim,
		//nolint:staticcheck // SA1019: migrate to GetEventRecorder when switching to new events API
		Recorder:          mgr.GetEventRecorderFor("gpuworkload-controller"),
		UseWorkerPool:     useWorkerPool,
		SimulatorURL:      simulatorURL,
		WorkerJobTemplate: worker.DefaultWorkerJobTemplate(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "gpuworkload")
		os.Exit(1)
	}

	schedulerLoop := &controller.SchedulerLoop{
		Client:    mgr.GetClient(),
		Scheme:    mgr.GetScheme(),
		Cache:     mgr.GetCache(),
		Simulator: localSim,
		//nolint:staticcheck // SA1019: migrate to GetEventRecorder when switching to new events API
		Recorder:              mgr.GetEventRecorderFor("scheduler-loop"),
		MaxAdmissionsPerCycle: maxAdmissionsPerCycle,
		MinCycleInterval:      0,
		ClaimableProducer:     claimableProducer,
	}
	if err = mgr.Add(schedulerLoop); err != nil {
		setupLog.Error(err, "Failed to add SchedulerLoop runnable")
		os.Exit(1)
	}

	backlogMetrics := &metrics.BacklogMetrics{
		Client:       mgr.GetClient(),
		PollInterval: 0,
	}
	if err = mgr.Add(backlogMetrics); err != nil {
		setupLog.Error(err, "Failed to add BacklogMetrics runnable")
		os.Exit(1)
	}

	if err = mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "Failed to set up health check")
		os.Exit(1)
	}
	if err = mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "Failed to set up readyz check")
		os.Exit(1)
	}

	setupLog.Info("Starting manager")
	if err = mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "Failed to run manager")
		os.Exit(1)
	}
}
