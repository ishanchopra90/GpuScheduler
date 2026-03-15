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
	"k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	kafkago "github.com/segmentio/kafka-go"

	schedulerv1alpha1 "github.com/ishanchopra/gpu-scheduler/api/v1alpha1"
	internalkafka "github.com/ishanchopra/gpu-scheduler/internal/kafka"
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

	worker.RegisterMetrics()

	metricsAddr := ":8080"
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	srv := &http.Server{Addr: metricsAddr, Handler: mux}
	go func() {
		_ = srv.ListenAndServe()
	}()
	defer func() { _ = srv.Shutdown(context.Background()) }()

	restCfg, err := config.GetConfig()
	if err != nil {
		os.Exit(1)
	}
	k8sClient, err := client.New(restCfg, client.Options{Scheme: scheme})
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
	useQueue := false
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("USE_QUEUE"))); v == "true" || v == "1" {
		useQueue = true
	}
	useWatch := true
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("USE_WATCH"))); v == "false" || v == "0" {
		useWatch = false
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

	if useQueue {
		brokers := strings.TrimSpace(os.Getenv("KAFKA_BROKERS"))
		if brokers == "" {
			os.Exit(1)
		}
		brokerList := strings.Split(brokers, ",")
		for i := range brokerList {
			brokerList[i] = strings.TrimSpace(brokerList[i])
		}
		topic := strings.TrimSpace(os.Getenv("CLAIMABLE_TOPIC"))
		if topic == "" {
			topic = internalkafka.TopicWorkloadClaimable
		}
		reader := kafkago.NewReader(kafkago.ReaderConfig{
			Brokers: brokerList,
			Topic:   topic,
			GroupID: "gpu-scheduler-worker",
		})
		worker.RunDeploymentLoopQueue(ctx, cfgLoop, reader)
	} else if useWatch {
		inf, err := worker.NewGPUWorkloadInformer(ctx, restCfg, namespace)
		if err != nil {
			panic(err)
		}
		go inf.Informer.Run(ctx.Done())
		if !cache.WaitForCacheSync(ctx.Done(), inf.Informer.HasSynced) {
			panic("informer cache sync failed")
		}
		worker.RunDeploymentLoopWatch(ctx, cfgLoop, inf)
	} else {
		worker.RunDeploymentLoop(ctx, cfgLoop)
	}
}
