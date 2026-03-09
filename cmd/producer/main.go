package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	"github.com/ishanchopra/gpu-scheduler/internal/kafka"
	"github.com/ishanchopra/gpu-scheduler/internal/producer"
)

func main() {
	var brokers string
	var count int
	var interval time.Duration
	var configPath string
	var burst bool
	flag.StringVar(&brokers, "brokers", "localhost:9092", "Kafka broker list (comma-separated).")
	flag.IntVar(&count, "count", 10, "Number of workload messages to generate and send (ignored when -config is set).")
	flag.DurationVar(&interval, "interval", 0, "Delay between each message (0 = send as fast as possible; use with -count).")
	flag.StringVar(&configPath, "config", "", "Path to JSON file: single WorkloadRequest or array of WorkloadRequest (overrides -count).")
	flag.BoolVar(&burst, "burst", false, "Send all messages as fast as possible (no delay).")
	flag.Parse()

	brokerList := strings.Split(strings.TrimSpace(brokers), ",")
	writer := &kafkago.Writer{
		Addr:     kafkago.TCP(brokerList...),
		Topic:    kafka.TopicWorkloadSubmit,
		Balancer: &kafkago.Hash{},
	}
	defer writer.Close()

	ctx := context.Background()

	if configPath != "" {
		data, err := os.ReadFile(configPath)
		if err != nil {
			log.Fatalf("Read config %s: %v", configPath, err)
		}
		var requests []kafka.WorkloadRequest
		if err := json.Unmarshal(data, &requests); err != nil {
			var single kafka.WorkloadRequest
			if err2 := json.Unmarshal(data, &single); err2 != nil {
				log.Fatalf("Decode config: expected array or single WorkloadRequest: %v", err)
			}
			requests = []kafka.WorkloadRequest{single}
		}
		for i := range requests {
			if err := publishOne(ctx, writer, &requests[i]); err != nil {
				log.Fatalf("Publish request_id=%s: %v", requests[i].RequestID, err)
			}
			if !burst && i < len(requests)-1 && interval > 0 {
				time.Sleep(interval)
			}
		}
		return
	}

	gen := producer.NewGenerator(nil, time.Now().UnixNano())
	for i := 0; i < count; i++ {
		req := gen.Next()
		if err := publishOne(ctx, writer, req); err != nil {
			log.Fatalf("Publish request_id=%s: %v", req.RequestID, err)
		}
		if !burst && i < count-1 && interval > 0 {
			time.Sleep(interval)
		}
	}
}

func publishOne(ctx context.Context, w *kafkago.Writer, req *kafka.WorkloadRequest) error {
	value, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	key := []byte(req.RequestID)
	return w.WriteMessages(ctx, kafkago.Message{Key: key, Value: value})
}
