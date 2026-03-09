package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	schedulerv1alpha1 "github.com/ishanchopra/gpu-scheduler/api/v1alpha1"
	"github.com/ishanchopra/gpu-scheduler/internal/worker"
)

var (
	scheme = runtime.NewScheme()
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(schedulerv1alpha1.AddToScheme(scheme))
}

func main() {
	simulatorURL := strings.TrimSpace(os.Getenv("SIMULATOR_URL"))
	if simulatorURL == "" {
		simulatorURL = "http://simulator:8080"
	}
	namespace := strings.TrimSpace(os.Getenv("NAMESPACE"))
	if namespace == "" {
		namespace = "default"
	}
	workerID := strings.TrimSpace(os.Getenv("WORKER_ID"))
	if workerID == "" {
		workerID, _ = os.Hostname()
	}
	if workerID == "" {
		workerID = "worker-unknown"
	}
	logCompletionTimestamps := false
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("LOG_COMPLETION_TIMESTAMPS"))); v == "true" || v == "1" {
		logCompletionTimestamps = true
	}

	metricsAddr := ":8080"
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	srv := &http.Server{Addr: metricsAddr, Handler: mux}
	go func() {
		_ = srv.ListenAndServe()
	}()
	defer srv.Shutdown(context.Background())

	cfg, err := config.GetConfig()
	if err != nil {
		os.Exit(1)
	}
	k8sClient, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		os.Exit(1)
	}

	simClient := worker.NewSimClient(simulatorURL)
	pollInterval := 5 * time.Second
	if v := os.Getenv("POLL_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			pollInterval = d
		}
	}
	maxConcurrent := 1
	if v := os.Getenv("MAX_CONCURRENT_WORKLOADS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxConcurrent = n
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	cfgLoop := worker.DeploymentLoopConfig{
		Client:                  k8sClient,
		SimClient:               simClient,
		Namespace:               namespace,
		WorkerID:                workerID,
		PollInterval:            pollInterval,
		MaxConcurrentWorkloads:  maxConcurrent,
		LogCompletionTimestamps: logCompletionTimestamps,
	}
	worker.RunDeploymentLoop(ctx, cfgLoop)
}
