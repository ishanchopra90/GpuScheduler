package controller

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	internalkafka "github.com/ishanchopra/gpu-scheduler/internal/kafka"
)

// ClaimableProducer publishes identifiers of newly Scheduled GPUWorkloads so workers
// can discover claimable work via a queue instead of an informer cache.
type ClaimableProducer interface {
	Produce(ctx context.Context, namespace, name string) error
	Close() error
}

// KafkaClaimableProducer implements ClaimableProducer using kafka-go.
type KafkaClaimableProducer struct {
	writer *kafkago.Writer
}

// NewKafkaClaimableProducer constructs a KafkaClaimableProducer for the given brokers and topic.
// Brokers is a comma-separated list (e.g. "broker:9092,broker:9093").
func NewKafkaClaimableProducer(brokersCSV, topic string) *KafkaClaimableProducer {
	brokersCSV = strings.TrimSpace(brokersCSV)
	if brokersCSV == "" {
		return nil
	}
	brokers := strings.Split(brokersCSV, ",")
	for i := range brokers {
		brokers[i] = strings.TrimSpace(brokers[i])
	}
	if topic == "" {
		topic = internalkafka.TopicWorkloadClaimable
	}
	w := &kafkago.Writer{
		Addr:         kafkago.TCP(brokers...),
		Topic:        topic,
		Balancer:     &kafkago.LeastBytes{},
		RequiredAcks: kafkago.RequireOne,
		Async:        false,
		BatchTimeout: 100 * time.Millisecond,
	}
	return &KafkaClaimableProducer{writer: w}
}

// Produce publishes a single claimable workload identifier. Best-effort: logs on error
// and returns it so callers can decide whether to retry.
func (p *KafkaClaimableProducer) Produce(ctx context.Context, namespace, name string) error {
	if p == nil || p.writer == nil {
		return nil
	}
	payload := internalkafka.ClaimableWorkloadMessage{
		Namespace: namespace,
		Name:      name,
	}
	data, err := json.Marshal(&payload)
	if err != nil {
		return err
	}
	msg := kafkago.Message{
		Key:   []byte(namespace + "/" + name),
		Value: data,
	}
	if err := p.writer.WriteMessages(ctx, msg); err != nil {
		log.Printf("Failed to produce claimable workload %s/%s: %v", namespace, name, err)
		return err
	}
	return nil
}

// Close closes the underlying writer.
func (p *KafkaClaimableProducer) Close() error {
	if p == nil || p.writer == nil {
		return nil
	}
	return p.writer.Close()
}
