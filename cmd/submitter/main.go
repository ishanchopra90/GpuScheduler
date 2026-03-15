package main

import (
	"context"
	"flag"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	schedulerv1alpha1 "github.com/ishanchopra/gpu-scheduler/api/v1alpha1"
	"github.com/ishanchopra/gpu-scheduler/internal/kafka"
	"github.com/ishanchopra/gpu-scheduler/internal/submitter"
)

var (
	scheme = runtime.NewScheme()
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(schedulerv1alpha1.AddToScheme(scheme))
}

func main() {
	var brokers string
	var groupID string
	var namespace string
	var metricsAddr string
	flag.StringVar(&brokers, "brokers", "localhost:9092", "Kafka broker list (comma-separated).")
	flag.StringVar(&groupID, "group-id", "gpu-scheduler-submitter", "Kafka consumer group ID.")
	flag.StringVar(&namespace, "namespace", "default", "Namespace for created GPUWorkload CRs.")
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metrics endpoint binds to.")
	flag.Parse()

	submitter.RegisterMetrics()

	if metricsAddr != "" && metricsAddr != "0" {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler())
		srv := &http.Server{Addr: metricsAddr, Handler: mux}
		go func() {
			_ = srv.ListenAndServe()
		}()
		defer func() { _ = srv.Shutdown(context.Background()) }()
	}

	cfg, err := config.GetConfig()
	if err != nil {
		os.Exit(1)
	}
	k8sClient, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		os.Exit(1)
	}

	consumer := &submitter.Consumer{
		Client:    k8sClient,
		Namespace: namespace,
		Brokers:   strings.Split(strings.TrimSpace(brokers), ","),
		GroupID:   groupID,
		DLQTopic:  kafka.TopicWorkloadSubmitDLQ,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()
	if err := consumer.Run(ctx); err != nil && ctx.Err() == nil {
		os.Exit(1)
	}
}
