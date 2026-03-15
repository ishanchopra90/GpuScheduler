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
	"golang.org/x/sync/errgroup"

	"github.com/ishanchopra/gpu-scheduler/internal/kafka"
	"github.com/ishanchopra/gpu-scheduler/internal/producer"
)

func main() {
	var brokers string
	var count int
	var interval time.Duration
	var configPath string
	var burst bool
	var concurrency int
	var partitions int
	var seedConfig string
	var seedCount int
	flag.StringVar(&brokers, "brokers", "localhost:9092", "Kafka broker list (comma-separated).")
	flag.IntVar(&count, "count", 10, "Number of workload messages to generate and send (ignored when -config is set).")
	flag.DurationVar(&interval, "interval", 0,
		"Delay between each message (0 = send as fast as possible; use with -count).")
	flag.StringVar(&configPath, "config", "",
		"Path to JSON file: single WorkloadRequest or array of WorkloadRequest (overrides -count).")
	flag.BoolVar(&burst, "burst", false, "Send all messages as fast as possible (no delay).")
	flag.IntVar(&concurrency, "concurrency", 1,
		"Number of concurrent goroutines to send messages; 1 keeps serial behavior.")
	flag.IntVar(&partitions, "partitions", 14, "Number of Kafka partitions to target explicitly when concurrency > 1.")
	flag.StringVar(&seedConfig, "seed-config", "",
		"Path to JSON file containing a seed WorkloadRequest (single object or array); "+
			"used with -seed-count to generate N in-memory workloads.")
	flag.IntVar(&seedCount, "seed-count", 0,
		"Number of workloads to generate from the seed in -seed-config (ignored when 0).")
	flag.Parse()

	if seedConfig != "" && configPath != "" {
		log.Fatalf("flags -config and -seed-config cannot be used together; choose one source of workloads")
	}
	if seedConfig != "" && seedCount <= 0 {
		log.Fatalf("when -seed-config is set, -seed-count must be > 0")
	}

	if concurrency < 1 {
		concurrency = 1
	}
	if partitions < 1 {
		partitions = 1
	}
	producerWorkers := 1
	if concurrency > 1 {
		producerWorkers = min(concurrency, partitions)
	}
	if producerWorkers > 1 && !burst && interval > 0 {
		log.Fatalf("interval-based throttling is not supported when -concurrency>1; use -burst for concurrent mode")
	}

	brokerList := strings.Split(strings.TrimSpace(brokers), ",")
	writer := &kafkago.Writer{
		Addr:     kafkago.TCP(brokerList...),
		Topic:    kafka.TopicWorkloadSubmit,
		Balancer: &kafkago.Hash{},
	}
	defer func() { _ = writer.Close() }()

	ctx := context.Background()

	var requests []kafka.WorkloadRequest
	switch {
	case seedConfig != "" && seedCount > 0:
		data, err := os.ReadFile(seedConfig)
		if err != nil {
			log.Fatalf("Read seed-config %s: %v", seedConfig, err)
		}
		var seedSlice []kafka.WorkloadRequest
		if err := json.Unmarshal(data, &seedSlice); err != nil {
			// Try single object as fallback.
			var single kafka.WorkloadRequest
			if err2 := json.Unmarshal(data, &single); err2 != nil {
				log.Fatalf("Decode seed-config: expected array or single WorkloadRequest: %v", err)
			}
			seedSlice = []kafka.WorkloadRequest{single}
		}
		if len(seedSlice) == 0 {
			log.Fatalf("seed-config %s decoded successfully but contained no WorkloadRequest entries", seedConfig)
		}
		seed := seedSlice[0]
		requests = make([]kafka.WorkloadRequest, 0, seedCount)
		for i := 0; i < seedCount; i++ {
			req := seed
			req.RequestID = fmt.Sprintf("scale-wl-%d", i+1)
			requests = append(requests, req)
		}
	case configPath != "":
		data, err := os.ReadFile(configPath)
		if err != nil {
			log.Fatalf("Read config %s: %v", configPath, err)
		}
		if err := json.Unmarshal(data, &requests); err != nil {
			var single kafka.WorkloadRequest
			if err2 := json.Unmarshal(data, &single); err2 != nil {
				log.Fatalf("Decode config: expected array or single WorkloadRequest: %v", err)
			}
			requests = []kafka.WorkloadRequest{single}
		}
	default:
		gen := producer.NewGenerator(nil, time.Now().UnixNano())
		for i := 0; i < count; i++ {
			req := gen.Next()
			requests = append(requests, *req)
		}
	}

	var err error
	if producerWorkers <= 1 {
		err = sendSerial(ctx, writer, requests, burst, interval)
	} else {
		err = sendConcurrent(ctx, writer, requests, producerWorkers, partitions)
	}
	if err != nil {
		log.Fatalf("Producer failed: %v", err)
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

func publishOneToPartition(ctx context.Context, w *kafkago.Writer, req *kafka.WorkloadRequest, partition int) error {
	value, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	key := []byte(req.RequestID)
	msg := kafkago.Message{
		Key:       key,
		Value:     value,
		Partition: partition,
	}
	return w.WriteMessages(ctx, msg)
}

func sendSerial(
	ctx context.Context, writer *kafkago.Writer, requests []kafka.WorkloadRequest, burst bool, interval time.Duration,
) error {
	for i := range requests {
		if err := publishOne(ctx, writer, &requests[i]); err != nil {
			return fmt.Errorf("publish request_id=%s: %w", requests[i].RequestID, err)
		}
		if !burst && i < len(requests)-1 && interval > 0 {
			time.Sleep(interval)
		}
	}
	return nil
}

//nolint:unparam // partitions is used by publishOneToPartition for routing.
func sendConcurrent(
	ctx context.Context, writer *kafkago.Writer, requests []kafka.WorkloadRequest, producerWorkers, partitions int,
) error {
	if len(requests) == 0 {
		return nil
	}
	// producerWorkers is min(concurrency, partitions); each worker is bound to partition [0, producerWorkers).
	g, ctx := errgroup.WithContext(ctx)
	for gIndex := range producerWorkers {
		partition := gIndex
		g.Go(func() error {
			for i := gIndex; i < len(requests); i += producerWorkers {
				if err := publishOneToPartition(ctx, writer, &requests[i], partition); err != nil {
					return fmt.Errorf("publish request_id=%s (partition=%d): %w", requests[i].RequestID, partition, err)
				}
			}
			return nil
		})
	}
	return g.Wait()
}
