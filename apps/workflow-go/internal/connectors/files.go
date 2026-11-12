package connectors

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/memory"
	"github.com/apache/iceberg-go"
	"github.com/apache/iceberg-go/catalog"
	icerest "github.com/apache/iceberg-go/catalog/rest"
	iceio "github.com/apache/iceberg-go/io"
	_ "github.com/apache/iceberg-go/io/gocloud"
	icetable "github.com/apache/iceberg-go/table"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

func (r *Runtime) registerFiles() {
	r.Sources["s3.fetch"] = r.s3Fetch
	r.Handlers["sink.s3"] = r.s3Sink
	r.Sources["sftp.fetch"] = r.sftpFetch
	r.Handlers["sink.sftp"] = r.sftpSink
	r.Sources["iceberg.fetch"] = r.icebergFetch
	r.Handlers["sink.iceberg"] = r.icebergSink
}
func (r *Runtime) s3Client(ctx context.Context, id string) (*s3.Client, error) {
	row, err := r.credential(ctx, id)
	if err != nil {
		return nil, err
	}
	cfg, _ := row["extra"].(map[string]interface{})
	secret, _ := row["secret_value"].(map[string]interface{})
	loaded, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(firstString(cfg["region"], "us-east-1")), awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(stringValue(secret["accessKeyId"]), stringValue(secret["secretAccessKey"]), "")))
	if err != nil {
		return nil, err
	}
	return s3.NewFromConfig(loaded, func(options *s3.Options) {
		options.UsePathStyle = cfg["forcePathStyle"] == true
		if endpoint := stringValue(cfg["endpoint"]); endpoint != "" {
			options.BaseEndpoint = aws.String(endpoint)
		}
	}), nil
}
func decodeRecords(body []byte, format string) ([]interface{}, error) {
	if format == "jsonl" {
		out := []interface{}{}
		scanner := bufio.NewScanner(bytes.NewReader(body))
		scanner.Buffer(make([]byte, 1024), 10<<20)
		for scanner.Scan() {
			var value interface{}
			if err := json.Unmarshal(scanner.Bytes(), &value); err != nil {
				return nil, err
			}
			out = append(out, value)
		}
		return out, scanner.Err()
	}
	var value interface{}
	if err := json.Unmarshal(body, &value); err != nil {
		return nil, err
	}
	if values, ok := value.([]interface{}); ok {
		return values, nil
	}
	return []interface{}{value}, nil
}
func encodeRecords(rows []interface{}, format string) ([]byte, error) {
	if format == "jsonl" {
		var body bytes.Buffer
		encoder := json.NewEncoder(&body)
		for _, row := range rows {
			if err := encoder.Encode(row); err != nil {
				return nil, err
			}
		}
		return body.Bytes(), nil
	}
	return json.Marshal(rows)
}
func (r *Runtime) s3Fetch(ctx context.Context, p SourceParams) (SourceResult, error) {
	client, err := r.s3Client(ctx, stringValue(p.Config["connectionId"]))
	if err != nil {
		return SourceResult{}, err
	}
	result, err := client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(stringValue(p.Config["bucket"])), Key: aws.String(stringValue(p.Config["key"]))})
	if err != nil {
		return SourceResult{}, err
	}
	defer result.Body.Close()
	const maxObjectBytes = 25 * 1024 * 1024
	if result.ContentLength != nil && *result.ContentLength > maxObjectBytes {
		return SourceResult{}, fmt.Errorf("s3 object exceeds the 25MB connector limit (%d bytes)", *result.ContentLength)
	}
	body, err := io.ReadAll(result.Body)
	if err != nil {
		return SourceResult{}, err
	}
	records, err := decodeRecords(body, firstString(p.Config["format"], "jsonl"))
	return SourceResult{Records: records, NextCursor: map[string]interface{}{"etag": stringValue(result.ETag)}, HasMore: false}, err
}
func (r *Runtime) s3Sink(ctx context.Context, input interface{}, cfg map[string]interface{}, handler HandlerContext) (interface{}, map[string]interface{}, error) {
	rows, err := records(input)
	if err != nil {
		return nil, nil, err
	}
	client, err := r.s3Client(ctx, stringValue(cfg["connectionId"]))
	if err != nil {
		return nil, nil, err
	}
	body, err := encodeRecords(rows, firstString(cfg["format"], "jsonl"))
	if err != nil {
		return nil, nil, err
	}
	key := strings.ReplaceAll(stringValue(cfg["key"]), "{executionId}", handler.ExecutionID)
	_, err = client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(stringValue(cfg["bucket"])), Key: aws.String(key), Body: bytes.NewReader(body)})
	return nil, nil, err
}

func (r *Runtime) sftpClient(ctx context.Context, id string) (*sftp.Client, func(), error) {
	row, err := r.credential(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	cfg, _ := row["extra"].(map[string]interface{})
	secret, _ := row["secret_value"].(map[string]interface{})
	auth := []ssh.AuthMethod{}
	if password := stringValue(secret["password"]); password != "" {
		auth = append(auth, ssh.Password(password))
	}
	if privateKey := stringValue(secret["privateKey"]); privateKey != "" {
		signer, err := ssh.ParsePrivateKey([]byte(privateKey))
		if err != nil {
			return nil, nil, err
		}
		auth = append(auth, ssh.PublicKeys(signer))
	}
	hostKeyCallback, err := fixedHostKey(stringValue(cfg["hostKey"]))
	if err != nil {
		return nil, nil, err
	}
	sshConfig := &ssh.ClientConfig{User: stringValue(cfg["user"]), Auth: auth, HostKeyCallback: hostKeyCallback, Timeout: 10 * time.Second}
	address := net.JoinHostPort(stringValue(cfg["host"]), fmt.Sprint(int(firstNumber(cfg["port"], 22))))
	connection, err := ssh.Dial("tcp", address, sshConfig)
	if err != nil {
		return nil, nil, err
	}
	client, err := sftp.NewClient(connection)
	if err != nil {
		connection.Close()
		return nil, nil, err
	}
	return client, func() { client.Close(); connection.Close() }, nil
}

func fixedHostKey(value string) (ssh.HostKeyCallback, error) {
	hostKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(value))
	if err != nil {
		return nil, fmt.Errorf("invalid SFTP host key: %w", err)
	}
	return ssh.FixedHostKey(hostKey), nil
}
func (r *Runtime) sftpFetch(ctx context.Context, p SourceParams) (SourceResult, error) {
	client, closeFn, err := r.sftpClient(ctx, stringValue(p.Config["connectionId"]))
	if err != nil {
		return SourceResult{}, err
	}
	defer closeFn()
	file, err := client.Open(stringValue(p.Config["path"]))
	if err != nil {
		return SourceResult{}, err
	}
	defer file.Close()
	body, err := io.ReadAll(file)
	if err != nil {
		return SourceResult{}, err
	}
	records, err := decodeRecords(body, firstString(p.Config["format"], "jsonl"))
	return SourceResult{Records: records, NextCursor: map[string]interface{}{}, HasMore: false}, err
}
func (r *Runtime) sftpSink(ctx context.Context, input interface{}, cfg map[string]interface{}, handler HandlerContext) (interface{}, map[string]interface{}, error) {
	rows, err := records(input)
	if err != nil {
		return nil, nil, err
	}
	client, closeFn, err := r.sftpClient(ctx, stringValue(cfg["connectionId"]))
	if err != nil {
		return nil, nil, err
	}
	defer closeFn()
	path := strings.ReplaceAll(stringValue(cfg["path"]), "{executionId}", handler.ExecutionID)
	file, err := client.Create(path)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()
	body, err := encodeRecords(rows, firstString(cfg["format"], "jsonl"))
	if err == nil {
		_, err = file.Write(body)
	}
	return nil, nil, err
}

func (r *Runtime) icebergTable(ctx context.Context, config map[string]interface{}) (*icetable.Table, error) {
	row, err := r.credential(ctx, stringValue(config["connectionId"]))
	if err != nil {
		return nil, err
	}
	cfg, _ := row["extra"].(map[string]interface{})
	secret, _ := row["secret_value"].(map[string]interface{})
	namespace := strings.Split(stringValue(config["namespace"]), ".")
	tableName := stringValue(config["table"])
	if len(namespace) == 0 || namespace[0] == "" || tableName == "" {
		return nil, fmt.Errorf("iceberg: namespace and table are required")
	}
	properties := iceberg.Properties{}
	if value := stringValue(secret["accessKeyId"]); value != "" {
		properties[iceio.S3AccessKeyID] = value
	}
	if value := stringValue(secret["secretAccessKey"]); value != "" {
		properties[iceio.S3SecretAccessKey] = value
	}
	if value := firstString(cfg["region"], "us-east-1"); value != "" {
		properties[iceio.S3Region] = value
	}
	if value := stringValue(cfg["endpoint"]); value != "" {
		properties[iceio.S3EndpointURL] = value
	}
	options := []icerest.Option{icerest.WithAdditionalProps(properties)}
	if value := stringValue(cfg["warehouse"]); value != "" {
		options = append(options, icerest.WithWarehouseLocation(value))
	}
	if value := stringValue(secret["token"]); value != "" {
		options = append(options, icerest.WithOAuthToken(value))
	}
	iceCatalog, err := icerest.NewCatalog(ctx, "dataflow", stringValue(cfg["url"]), options...)
	if err != nil {
		return nil, err
	}
	return iceCatalog.LoadTable(ctx, catalog.ToIdentifier(append(namespace, tableName)...))
}

func (r *Runtime) IcebergCurrentSnapshot(ctx context.Context, config map[string]interface{}) (string, error) {
	tbl, err := r.icebergTable(ctx, config)
	if err != nil { return "", err }
	if snapshot := tbl.CurrentSnapshot(); snapshot != nil { return strconv.FormatInt(snapshot.SnapshotID, 10), nil }
	return "", nil
}

func (r *Runtime) icebergFetch(ctx context.Context, p SourceParams) (SourceResult, error) {
	loaded, err := r.icebergTable(ctx, p.Config)
	if err != nil {
		return SourceResult{}, err
	}
	return icebergFetchTable(ctx, loaded, p)
}

// legacyIcebergPage pages through the table by row offset from the start of a
// full scan. Only a run that was mid-snapshot under the old cursor format
// (rowOffset without fileIndex) still uses it, so it finishes without
// re-reading or skipping rows.
func legacyIcebergPage(ctx context.Context, loaded *icetable.Table, p SourceParams) (SourceResult, error) {
	snapshot := loaded.CurrentSnapshot()
	if snapshot == nil {
		return SourceResult{Records: []interface{}{}, NextCursor: p.Cursor}, nil
	}
	snapshotID := strconv.FormatInt(snapshot.SnapshotID, 10)
	if stringValue(p.Cursor["snapshotId"]) == snapshotID {
		return SourceResult{Records: []interface{}{}, NextCursor: p.Cursor, HasMore: false}, nil
	}
	if stringValue(p.Cursor["snapshotId"]) != "" && snapshot.Summary != nil && snapshot.Summary.Operation != icetable.OpAppend {
		return SourceResult{}, fmt.Errorf("iceberg.fetch: incremental mode supports append snapshots; run a backfill after overwrite/delete snapshots")
	}
	totalRecords, _ := strconv.Atoi(snapshot.Summary.Properties["total-records"])
	start := int(firstNumber(p.Cursor["totalRecords"], 0))
	if stringValue(p.Cursor["pendingSnapshotId"]) == snapshotID {
		start = int(firstNumber(p.Cursor["rowOffset"], start))
	}
	pageSize := int(firstNumber(func() interface{} {
		if p.Ingestion != nil {
			return p.Ingestion.PageSize
		}
		return nil
	}(), p.Config["pageSize"], 1000))
	if pageSize < 1 {
		pageSize = 1
	}
	if pageSize > 10000 {
		pageSize = 10000
	}
	_, batches, err := loaded.Scan(icetable.WithLimit(int64(start + pageSize))).ToArrowRecords(ctx)
	if err != nil {
		return SourceResult{}, err
	}
	all := []interface{}{}
	for batch, batchErr := range batches {
		if batchErr != nil {
			return SourceResult{}, batchErr
		}
		encoded, marshalErr := array.RecordToStructArray(batch).MarshalJSON()
		batch.Release()
		if marshalErr != nil {
			return SourceResult{}, marshalErr
		}
		var values []interface{}
		if marshalErr = json.Unmarshal(encoded, &values); marshalErr != nil {
			return SourceResult{}, marshalErr
		}
		all = append(all, values...)
	}
	if start > len(all) {
		start = len(all)
	}
	end := start + pageSize
	if end > len(all) {
		end = len(all)
	}
	records := all[start:end]
	hasMore := start+len(records) < totalRecords
	next := map[string]interface{}{"snapshotId": snapshotID, "totalRecords": totalRecords}
	if hasMore {
		next = map[string]interface{}{"snapshotId": p.Cursor["snapshotId"], "pendingSnapshotId": snapshotID, "rowOffset": start + len(records), "totalRecords": totalRecords}
	}
	return SourceResult{Records: records, NextCursor: next, HasMore: hasMore}, nil
}

const (
	icebergExecutionID     = "dataflow.execution-id"
	icebergNodeID          = "dataflow.node-id"
	icebergPipelineVersion = "dataflow.pipeline-version"
	icebergRecordCount     = "dataflow.record-count"
)

func committedIcebergSnapshot(tbl *icetable.Table, executionID, nodeID string) (*icetable.Snapshot, bool) {
	return findCommittedIcebergSnapshot(tbl.Metadata().Snapshots(), executionID, nodeID)
}

func findCommittedIcebergSnapshot(snapshots []icetable.Snapshot, executionID, nodeID string) (*icetable.Snapshot, bool) {
	for i := len(snapshots) - 1; i >= 0; i-- {
		snapshot := &snapshots[i]
		if snapshot.Summary != nil && snapshot.Summary.Properties[icebergExecutionID] == executionID && snapshot.Summary.Properties[icebergNodeID] == nodeID {
			return snapshot, true
		}
	}
	return nil, false
}

func icebergArrowReader(rows []interface{}, schema *iceberg.Schema) (array.RecordReader, error) {
	arrowSchema, err := icetable.SchemaToArrowSchema(schema, nil, true, false)
	if err != nil {
		return nil, err
	}
	builder := array.NewRecordBuilder(memory.DefaultAllocator, arrowSchema)
	defer builder.Release()
	fields := make(map[string]bool, arrowSchema.NumFields())
	for _, field := range arrowSchema.Fields() {
		fields[field.Name] = field.Nullable
	}
	for rowIndex, value := range rows {
		row, ok := value.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("iceberg row %d must be an object", rowIndex)
		}
		for key := range row {
			if _, ok := fields[key]; !ok {
				return nil, fmt.Errorf("iceberg row %d: unknown field %q", rowIndex, key)
			}
		}
		for name, nullable := range fields {
			value, ok := row[name]
			if (!ok || value == nil) && !nullable {
				return nil, fmt.Errorf("iceberg row %d: required field %q is missing", rowIndex, name)
			}
		}
		encoded, err := json.Marshal(row)
		if err != nil {
			return nil, err
		}
		if err := builder.UnmarshalJSON(encoded); err != nil {
			return nil, fmt.Errorf("iceberg row %d: %w", rowIndex, err)
		}
	}
	record := builder.NewRecordBatch()
	defer record.Release()
	return array.NewRecordReader(arrowSchema, []arrow.RecordBatch{record})
}

func (r *Runtime) icebergSink(ctx context.Context, input interface{}, cfg map[string]interface{}, handler HandlerContext) (interface{}, map[string]interface{}, error) {
	rows, err := records(input)
	if err != nil {
		return nil, nil, err
	}
	tbl, err := r.icebergTable(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	return appendIcebergTable(ctx, tbl, rows, handler)
}

func appendIcebergTable(ctx context.Context, tbl *icetable.Table, rows []interface{}, handler HandlerContext) (interface{}, map[string]interface{}, error) {
	if snapshot, ok := committedIcebergSnapshot(tbl, handler.ExecutionID, handler.NodeID); ok {
		count, _ := strconv.Atoi(snapshot.Summary.Properties[icebergRecordCount])
		return nil, map[string]interface{}{"snapshotId": strconv.FormatInt(snapshot.SnapshotID, 10), "recordCount": count, "idempotent": true}, nil
	}
	reader, err := icebergArrowReader(rows, tbl.Schema())
	if err != nil {
		return nil, nil, err
	}
	defer reader.Release()
	updated, err := tbl.Append(ctx, reader, iceberg.Properties{
		icebergExecutionID:     handler.ExecutionID,
		icebergNodeID:          handler.NodeID,
		icebergPipelineVersion: strconv.Itoa(handler.PipelineVersion),
		icebergRecordCount:     strconv.Itoa(len(rows)),
	})
	if err != nil {
		return nil, nil, err
	}
	snapshot := updated.CurrentSnapshot()
	if snapshot == nil {
		return nil, nil, fmt.Errorf("iceberg append committed without a snapshot")
	}
	return nil, map[string]interface{}{"snapshotId": strconv.FormatInt(snapshot.SnapshotID, 10), "recordCount": len(rows)}, nil
}
