package submitter

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/ishanchopra/gpu-scheduler/internal/kafka"
	kafkago "github.com/segmentio/kafka-go"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Consumer reads WorkloadRequest messages from Kafka, validates them, creates GPUWorkload CRs
// idempotently, and sends invalid messages to the DLQ. Offsets are committed only after success.
// Scale ingestion by running more submitter replicas (consumer group size) and more partitions.
type Consumer struct {
	Client    client.Client
	Namespace string
	Brokers   []string
	GroupID   string
	DLQTopic  string
	log       *slog.Logger
}

// Run starts the consumer group loop. It blocks until ctx is cancelled.
func (c *Consumer) Run(ctx context.Context) error {
	if c.log == nil {
		c.log = slog.Default()
	}

	reader := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers: c.Brokers,
		Topic:   kafka.TopicWorkloadSubmit,
		GroupID: c.GroupID,
	})
	defer func() { _ = reader.Close() }()

	dlqWriter := &kafkago.Writer{
		Addr:     kafkago.TCP(c.Brokers...),
		Topic:    c.DLQTopic,
		Balancer: &kafkago.LeastBytes{},
	}
	defer func() { _ = dlqWriter.Close() }()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		msg, err := reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			c.log.Error("Failed to fetch message", "error", err)
			continue
		}
		if err := c.processMessage(ctx, dlqWriter, msg); err != nil {
			c.log.Error("Failed to process message", "error", err, "partition", msg.Partition, "offset", msg.Offset)
			// Do not commit; will be retried
			continue
		}
		if err := reader.CommitMessages(ctx, msg); err != nil {
			c.log.Error("Failed to commit offset", "error", err, "partition", msg.Partition, "offset", msg.Offset)
			continue
		}
		MessagesConsumedTotal.Inc()
	}
}

func (c *Consumer) processMessage(ctx context.Context, dlqWriter *kafkago.Writer, msg kafkago.Message) error {
	var req kafka.WorkloadRequest
	if err := json.Unmarshal(msg.Value, &req); err != nil {
		c.log.Info("Invalid JSON, sending to DLQ", "error", err, "offset", msg.Offset)
		return c.sendToDLQ(ctx, dlqWriter, msg.Value, "invalid_json", err.Error())
	}
	if err := Validate(&req); err != nil {
		c.log.Info("Validation failed, sending to DLQ", "error", err, "request_id", req.RequestID)
		return c.sendToDLQ(ctx, dlqWriter, msg.Value, "validation_failed", err.Error())
	}
	cr := ToGPUWorkload(&req, c.Namespace)
	if cr == nil {
		c.log.Error("Mapper returned nil GPUWorkload", "request_id", req.RequestID)
		return c.sendToDLQ(ctx, dlqWriter, msg.Value, "mapper_error", "CR name derived to empty")
	}
	err := c.Client.Create(ctx, cr)
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			c.log.Debug("GPUWorkload already exists, treating as success", "name", cr.Name, "request_id", req.RequestID)
			return nil
		}
		return err
	}
	WorkloadCreationsTotal.Inc()
	c.log.Info("Created GPUWorkload", "name", cr.Name, "request_id", req.RequestID)
	return nil
}

func (c *Consumer) sendToDLQ(ctx context.Context, w *kafkago.Writer, value []byte, reason, detail string) error {
	msg := kafkago.Message{
		Value: value,
		Headers: []kafkago.Header{
			{Key: "dlq_reason", Value: []byte(reason)},
			{Key: "dlq_detail", Value: []byte(detail)},
		},
	}
	return w.WriteMessages(ctx, msg)
}
