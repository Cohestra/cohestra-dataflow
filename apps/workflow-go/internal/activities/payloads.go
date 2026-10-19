package activities

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/dataflow-poc/workflow-go/internal/connectors"
	"github.com/dataflow-poc/workflow-go/internal/database"
	"github.com/dataflow-poc/workflow-go/internal/model"
	"github.com/dataflow-poc/workflow-go/internal/objectstore"
)

const inlineMax = 4 * 1024

// PayloadTooLargeError is the non-retryable error type for reads over MaxPayloadBytes.
const PayloadTooLargeError = "PayloadTooLargeError"

type Payloads struct {
	DB          *database.DB
	Store       *objectstore.Store
	PlatformKey []byte
	// MaxPayloadBytes caps any single payload read; <= 0 means unlimited.
	MaxPayloadBytes int64
}

func (p *Payloads) Write(ctx context.Context, data interface{}, tenantID, executionID, nodeID string, dek []byte) (*model.DataRef, error) {
	if data == nil {
		data = nil
	}
	body, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	key := dek
	if len(key) == 0 {
		key = p.PlatformKey
	}
	recordCount := 0
	if records, ok := data.([]interface{}); ok {
		recordCount = len(records)
	}
	ref := &model.DataRef{TenantID: tenantID, SizeBytes: len(body), RecordCount: recordCount}
	if len(body) <= inlineMax {
		ref.Type = "inline"
		if len(key) > 0 {
			ref.Key, ref.IV, err = EncryptPayload(body, key)
			ref.Encrypted = err == nil
		} else {
			ref.Key = base64.StdEncoding.EncodeToString(body)
		}
		return ref, err
	}
	if p.Store != nil {
		if len(key) == 0 {
			return nil, errors.New("PAYLOAD_S3_BUCKET requires TEMPORAL_PAYLOAD_ENCRYPTION_KEY or an execution DEK")
		}
		ciphertext, iv, err := EncryptPayload(body, key)
		if err != nil {
			return nil, err
		}
		objectKey := objectstore.Key(tenantID, executionID, nodeID)
		if err := p.Store.Put(ctx, objectKey, ciphertext); err != nil {
			return nil, err
		}
		ref.Type, ref.Key, ref.Bucket, ref.IV, ref.Encrypted = "s3", objectKey, p.Store.Bucket(), iv, true
		return ref, nil
	}
	payload := interface{}(json.RawMessage(body))
	encrypted, iv := false, ""
	if len(key) > 0 {
		ciphertext, valueIV, err := EncryptPayload(body, key)
		if err != nil {
			return nil, err
		}
		// The jsonb column needs valid JSON: store the ciphertext as a JSON
		// string (Read unmarshals it back with json.Unmarshal into a string).
		// Passing the bare base64 string fails with SQLSTATE 22P02.
		quoted, err := json.Marshal(ciphertext)
		if err != nil {
			return nil, err
		}
		payload, iv, encrypted = interface{}(json.RawMessage(quoted)), valueIV, true
	}
	err = p.DB.Pool.QueryRow(ctx, `INSERT INTO node_payloads
      (tenant_id,execution_id,node_id,payload,encrypted,encryption_iv)
      VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`, tenantID, executionID, nodeID, payload, encrypted, nullString(iv)).Scan(&ref.Key)
	ref.Type, ref.Encrypted, ref.IV = "pg", encrypted, iv
	return ref, err
}

func (p *Payloads) Read(ctx context.Context, ref *model.DataRef, dek []byte) (interface{}, error) {
	if ref == nil {
		return nil, nil
	}
	if p.MaxPayloadBytes > 0 && int64(ref.SizeBytes) > p.MaxPayloadBytes {
		// Retrying cannot shrink the payload, so fail once with the fix.
		return nil, &connectors.PermanentError{Type: PayloadTooLargeError, Err: fmt.Errorf("upstream output is %d bytes, over the MAX_PAYLOAD_BYTES limit of %d; raise MAX_PAYLOAD_BYTES on the activity worker or lower the source pageSize", ref.SizeBytes, p.MaxPayloadBytes)}
	}
	key := dek
	if len(key) == 0 {
		key = p.PlatformKey
	}
	var body []byte
	var err error
	switch ref.Type {
	case "inline":
		if ref.Encrypted {
			if len(key) == 0 || ref.IV == "" {
				return nil, errors.New("encrypted inline DataRef is missing its encryption key or IV")
			}
			body, err = DecryptPayload(ref.Key, ref.IV, key)
		} else {
			body, err = base64.StdEncoding.DecodeString(ref.Key)
		}
	case "s3":
		if !ref.Encrypted || len(key) == 0 || ref.IV == "" || p.Store == nil {
			return nil, fmt.Errorf("encrypted DataRef %s is missing its object store, encryption key, or IV", ref.Key)
		}
		var ciphertext string
		ciphertext, err = p.Store.Get(ctx, ref.Bucket, ref.Key, ciphertextSizeCap(p.MaxPayloadBytes))
		if err == nil {
			body, err = DecryptPayload(ciphertext, ref.IV, key)
		}
	case "pg":
		var payload json.RawMessage
		var encrypted bool
		var iv *string
		err = p.DB.Pool.QueryRow(ctx, `SELECT payload,encrypted,encryption_iv FROM node_payloads WHERE id=$1`, ref.Key).Scan(&payload, &encrypted, &iv)
		if err == nil && encrypted {
			if len(key) == 0 || iv == nil {
				return nil, fmt.Errorf("encrypted DataRef %s is missing its encryption key or IV", ref.Key)
			}
			var ciphertext string
			if unmarshalErr := json.Unmarshal(payload, &ciphertext); unmarshalErr != nil {
				return nil, unmarshalErr
			}
			body, err = DecryptPayload(ciphertext, *iv, key)
		} else {
			body = payload
		}
	default:
		return nil, fmt.Errorf("unsupported DataRef type %q", ref.Type)
	}
	if err != nil {
		return nil, err
	}
	var value interface{}
	if err := json.Unmarshal(body, &value); err != nil {
		return nil, err
	}
	return value, nil
}

// ciphertextSizeCap converts a plaintext byte limit into the corresponding
// download cap for the AES-GCM-sealed, base64.RawURLEncoding-at-rest object:
// the sealed body is plaintext+gcmTagSize bytes, then base64-inflated. Store.Get
// only sees ciphertext on the wire, so it must be given this larger bound —
// the plaintext bound is already enforced separately via ref.SizeBytes in Read.
const gcmTagSize = 16

func ciphertextSizeCap(maxPlaintextBytes int64) int64 {
	if maxPlaintextBytes <= 0 {
		return 0
	}
	return int64(base64.RawURLEncoding.EncodedLen(int(maxPlaintextBytes) + gcmTagSize))
}

func nullString(value string) interface{} {
	if value == "" {
		return nil
	}
	return value
}
