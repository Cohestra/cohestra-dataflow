package connectors

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
)

var kafkaTopic = regexp.MustCompile(`^[A-Za-z0-9._-]{1,249}$`)

func (r *Runtime) registerStreams() {
	r.Sources["kafka.fetch"] = r.kafkaFetch
	r.Handlers["sink.kafka"] = r.kafkaSink
}
func (r *Runtime) kafkaOptions(ctx context.Context, id string) ([]kgo.Opt, error) {
	row, err := r.credential(ctx, id)
	if err != nil {
		return nil, err
	}
	cfg, _ := row["extra"].(map[string]interface{})
	brokers := []string{}
	switch value := cfg["brokers"].(type) {
	case string:
		brokers = strings.Split(value, ",")
	case []interface{}:
		for _, broker := range value {
			brokers = append(brokers, stringValue(broker))
		}
	}
	if len(brokers) == 0 {
		return nil, fmt.Errorf("kafka connector requires brokers")
	}
	options := []kgo.Opt{kgo.SeedBrokers(brokers...), kgo.DialTimeout(10 * time.Second)}
	if truthy(cfg["tls"]) {
		options = append(options, kgo.DialTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12}))
	}
	return options, nil
}
func (r *Runtime) kafkaFetch(ctx context.Context, p SourceParams) (SourceResult, error) {
	topic := stringValue(p.Config["topic"])
	if !kafkaTopic.MatchString(topic) {
		return SourceResult{}, fmt.Errorf("invalid Kafka topic")
	}
	options, err := r.kafkaOptions(ctx, stringValue(p.Config["connectionId"]))
	if err != nil {
		return SourceResult{}, err
	}
	offsets := map[int32]interface{}{}
	if value, ok := p.Cursor["offsets"].(map[string]interface{}); ok {
		for partition, offset := range value {
			number, _ := strconv.ParseInt(stringValue(offset), 10, 64)
			id, _ := strconv.ParseInt(partition, 10, 32)
			offsets[int32(id)] = number
		}
	}
	partitions := map[string]map[int32]kgo.Offset{}
	if len(offsets) > 0 {
		partitions[topic] = map[int32]kgo.Offset{}
		for partition, value := range offsets {
			partitions[topic][partition] = kgo.NewOffset().At(value.(int64) + 1)
		}
		options = append(options, kgo.ConsumePartitions(partitions))
	} else {
		options = append(options, kgo.ConsumeTopics(topic))
		if p.Config["startPosition"] == "latest" {
			options = append(options, kgo.ConsumeResetOffset(kgo.NewOffset().AtEnd()))
		} else {
			options = append(options, kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
		}
	}
	client, err := kgo.NewClient(options...)
	if err != nil {
		return SourceResult{}, err
	}
	defer client.Close()
	page := int(firstNumber(p.Config["pageSize"], func() interface{} {
		if p.Ingestion != nil {
			return p.Ingestion.PageSize
		}
		return nil
	}(), 1000))
	readCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	fetches := client.PollRecords(readCtx, page)
	if fetches.Err() != nil && readCtx.Err() == nil {
		return SourceResult{}, fetches.Err()
	}
	records := []interface{}{}
	next := map[string]interface{}{"topic": topic, "offsets": map[string]interface{}{}}
	nextOffsets := next["offsets"].(map[string]interface{})
	var lagRecords int64
	fetches.EachPartition(func(partition kgo.FetchTopicPartition) {
		nextOffset := partition.HighWatermark
		if len(partition.Records) > 0 {
			nextOffset = partition.Records[len(partition.Records)-1].Offset + 1
		}
		if partition.HighWatermark > nextOffset {
			lagRecords += partition.HighWatermark - nextOffset
		}
	})
	fetches.EachRecord(func(record *kgo.Record) {
		var value interface{}
		if json.Unmarshal(record.Value, &value) != nil {
			value = string(record.Value)
		}
		if p.Config["includeMetadata"] == true {
			if object, ok := value.(map[string]interface{}); ok {
				object["_kafka"] = map[string]interface{}{"partition": record.Partition, "offset": record.Offset, "timestamp": record.Timestamp}
			}
		}
		records = append(records, value)
		nextOffsets[strconv.Itoa(int(record.Partition))] = strconv.FormatInt(record.Offset, 10)
	})
	return SourceResult{Records: records, NextCursor: next, HasMore: len(records) >= page, LagRecords: lagRecords}, nil
}
func (r *Runtime) kafkaSink(ctx context.Context, input interface{}, cfg map[string]interface{}, handler HandlerContext) (interface{}, map[string]interface{}, error) {
	topic := stringValue(cfg["topic"])
	if !kafkaTopic.MatchString(topic) {
		return nil, nil, fmt.Errorf("invalid Kafka topic")
	}
	rows, err := recordsMaps(input)
	if err != nil {
		return nil, nil, err
	}
	options, err := r.kafkaOptions(ctx, stringValue(cfg["connectionId"]))
	if err != nil {
		return nil, nil, err
	}
	client, err := kgo.NewClient(options...)
	if err != nil {
		return nil, nil, err
	}
	defer client.Close()
	// franz-go produces idempotently by default, which removes duplicates from
	// its own network retries. A Temporal retry of this step re-sends the batch,
	// so each record also carries a key that is stable across those retries.
	records := kafkaSinkRecords(topic, stringValue(cfg["keyField"]), rows, handler)
	if err = client.ProduceSync(ctx, records...).FirstErr(); err != nil {
		return nil, nil, err
	}
	return nil, nil, nil
}

const kafkaIdempotencyHeader = "dataflow-idempotency-key"

func kafkaSinkRecords(topic, keyField string, rows []map[string]interface{}, handler HandlerContext) []*kgo.Record {
	base := idempotencyKey(handler, 0)
	records := make([]*kgo.Record, 0, len(rows))
	for i, row := range rows {
		body, _ := json.Marshal(row)
		record := &kgo.Record{Topic: topic, Value: body, Headers: []kgo.RecordHeader{{Key: kafkaIdempotencyHeader, Value: []byte(base + ":" + strconv.Itoa(i))}}}
		if keyField != "" {
			record.Key = []byte(stringValue(row[keyField]))
		}
		records = append(records, record)
	}
	return records
}
