package worker

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kafkago "github.com/segmentio/kafka-go"

	internalkafka "github.com/ishanchopra/gpu-scheduler/internal/kafka"

	schedulerv1alpha1 "github.com/ishanchopra/gpu-scheduler/api/v1alpha1"
)

// RunDeploymentLoopQueue runs a queue-based claim loop backed by Kafka. It consumes
// identifiers of Scheduled workloads from the claimable topic, fetches each workload
// from the API, claims it, and runs it via the simulator. When idle, it blocks on
// Kafka rather than polling or maintaining an informer cache.
func RunDeploymentLoopQueue(ctx context.Context, cfg DeploymentLoopConfig, reader *kafkago.Reader) {
	if cfg.MaxConcurrentWorkloads <= 0 {
		cfg.MaxConcurrentWorkloads = DefaultMaxConcurrentWorkloads
	}
	log.Printf("Worker %s started, queue-based discovery for namespace %s", cfg.WorkerID, cfg.Namespace)
	defer func() {
		if reader != nil {
			_ = reader.Close()
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		msg, err := reader.ReadMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("Kafka ReadMessage error: %v", err)
			time.Sleep(2 * time.Second)
			continue
		}

		var payload internalkafka.ClaimableWorkloadMessage
		if err := json.Unmarshal(msg.Value, &payload); err != nil {
			log.Printf("Failed to unmarshal claimable workload message: %v", err)
			continue
		}
		ns := payload.Namespace
		if ns == "" {
			ns = cfg.Namespace
		}
		name := strings.TrimSpace(payload.Name)
		if ns == "" || name == "" {
			log.Printf("Invalid claimable workload message (empty namespace or name)")
			continue
		}

		workload := &schedulerv1alpha1.GPUWorkload{}
		if err := cfg.Client.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, workload); err != nil {
			log.Printf("Get workload %s/%s from queue: %v", ns, name, err)
			continue
		}
		if workload.Status.Phase != schedulerv1alpha1.GPUWorkloadPhaseScheduled {
			continue
		}
		if _, claimed := workload.Annotations[AnnotationClaimedBy]; claimed {
			continue
		}

		if err := claimWorkload(ctx, cfg, workload); err != nil {
			if client.IgnoreNotFound(err) != nil {
				RecordError(cfg.WorkerID, "claim_failed")
				log.Printf("Claim workload from queue: %v", err)
			}
			continue
		}

		runOneWorkload(ctx, cfg, workload)
	}
}
