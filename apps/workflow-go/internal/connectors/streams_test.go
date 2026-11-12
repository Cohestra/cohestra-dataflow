package connectors

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"
)

func TestKafkaSASLMechanism(t *testing.T) {
	for _, name := range []string{"", "none", " NONE "} {
		mechanism, err := KafkaSASLMechanism(name, false, "", "")
		require.NoError(t, err)
		require.Nil(t, mechanism)
	}
	for name, want := range map[string]string{"plain": "PLAIN", "scram-sha-256": "SCRAM-SHA-256", "SCRAM-SHA-512": "SCRAM-SHA-512"} {
		mechanism, err := KafkaSASLMechanism(name, true, "user", "secret")
		require.NoError(t, err, name)
		require.Equal(t, want, mechanism.Name())
	}
	// SCRAM never sends the password, so it is allowed without TLS.
	_, err := KafkaSASLMechanism("scram-sha-256", false, "user", "secret")
	require.NoError(t, err)
	for name, test := range map[string]struct {
		mechanism, username, password string
		tls                           bool
		message                       string
	}{
		"PLAIN over plaintext": {"plain", "user", "secret", false, "enable TLS"},
		"missing password":     {"scram-sha-512", "user", "", true, "username and password"},
		"missing username":     {"plain", "", "secret", true, "username and password"},
		"unknown mechanism":    {"gssapi", "user", "secret", true, "unsupported"},
		"misspelled mechanism": {"scram-sha256", "user", "secret", true, "unsupported"},
	} {
		_, err := KafkaSASLMechanism(test.mechanism, test.tls, test.username, test.password)
		require.Error(t, err, name)
		require.Contains(t, err.Error(), test.message, name)
	}
	_, err = kafkaClientOptions(map[string]interface{}{"brokers": " , "}, nil)
	require.ErrorContains(t, err, "requires brokers")
}

// TestKafkaSCRAMRoundTrip runs against a broker that requires SASL/SCRAM-SHA-256,
// e.g. Redpanda with enable_sasl (see the PR description for the setup).
func TestKafkaSCRAMRoundTrip(t *testing.T) {
	brokers := os.Getenv("KAFKA_SASL_TEST_BROKERS")
	if brokers == "" {
		t.Skip("KAFKA_SASL_TEST_BROKERS names a broker that requires SASL/SCRAM-SHA-256")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cfg := map[string]interface{}{"brokers": brokers, "saslMechanism": "scram-sha-256", "clientId": "dataflow-test"}
	login := map[string]interface{}{"username": os.Getenv("KAFKA_SASL_TEST_USER"), "password": os.Getenv("KAFKA_SASL_TEST_PASSWORD")}

	// Without the login (the old behaviour) the broker refuses the client.
	options, err := kafkaClientOptions(map[string]interface{}{"brokers": brokers}, nil)
	require.NoError(t, err)
	anonymous, err := kgo.NewClient(options...)
	require.NoError(t, err)
	require.Error(t, anonymous.Ping(ctx), "broker accepted a client without SASL")
	anonymous.Close()

	wrong, err := kafkaClientOptions(cfg, map[string]interface{}{"username": login["username"], "password": "not-the-password"})
	require.NoError(t, err)
	impostor, err := kgo.NewClient(wrong...)
	require.NoError(t, err)
	require.Error(t, impostor.Ping(ctx), "broker accepted a wrong password")
	impostor.Close()

	options, err = kafkaClientOptions(cfg, login)
	require.NoError(t, err)
	topic := "dataflow-sasl-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	producer, err := kgo.NewClient(append(options, kgo.AllowAutoTopicCreation())...)
	require.NoError(t, err)
	defer producer.Close()
	require.NoError(t, producer.ProduceSync(ctx, &kgo.Record{Topic: topic, Value: []byte(`{"ok":true}`)}).FirstErr())

	consumer, err := kgo.NewClient(append(options, kgo.ConsumeTopics(topic), kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))...)
	require.NoError(t, err)
	defer consumer.Close()
	fetches := consumer.PollFetches(ctx)
	require.Empty(t, fetches.Errors())
	var values []string
	fetches.EachRecord(func(record *kgo.Record) { values = append(values, string(record.Value)) })
	require.Equal(t, []string{`{"ok":true}`}, values)
}
