//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	broker "github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/require"
	"hello/internal/dispatch"
	"hello/internal/kafka"
	"hello/internal/requeue"
)

func TestReplayCommandVerifiedTLSAndMTLS(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	for _, setting := range os.Environ() {
		name, _, _ := strings.Cut(setting, "=")
		if strings.HasPrefix(name, "PG") || name == "SSL_CERT_FILE" || name == "SSL_CERT_DIR" {
			t.Setenv(name, "")
		}
	}
	directory := os.Getenv("HELLO_INTEGRATION_CERTS_DIR")
	require.NotEmpty(t, directory, "use the isolated fixture runner")
	read := func(name string) string {
		data, err := os.ReadFile(filepath.Join(directory, name))
		require.NoError(t, err)
		return string(data)
	}
	f := newReplayRegistrationFixture(t, ctx)
	transport := &broker.Transport{DialTimeout: time.Second}
	defer transport.CloseIdleConnections()
	client := &broker.Client{Addr: broker.TCP("127.0.0.1:19092"), Timeout: 3 * time.Second, Transport: transport}
	topic := fmt.Sprintf("requeue-tls-%d", time.Now().UnixNano())
	created, err := client.CreateTopics(ctx, &broker.CreateTopicsRequest{Topics: []broker.TopicConfig{{Topic: topic, NumPartitions: 1, ReplicationFactor: 1}}})
	require.NoError(t, err)
	require.NoError(t, created.Errors[topic])
	defer func() {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer stop()
		deleted, err := client.DeleteTopics(cleanup, &broker.DeleteTopicsRequest{Topics: []string{topic}})
		require.NoError(t, err)
		require.NoError(t, deleted.Errors[topic])
	}()
	var metadata *broker.MetadataResponse
	require.Eventually(t, func() bool {
		var err error
		metadata, err = client.Metadata(ctx, &broker.MetadataRequest{Topics: []string{topic}})
		return err == nil && len(metadata.Topics) == 1 && metadata.Topics[0].Error == nil && len(metadata.Topics[0].Partitions) == 1 && metadata.Topics[0].Partitions[0].Leader.Host != ""
	}, 5*time.Second, 100*time.Millisecond)
	require.NoError(t, f.pool.QueryRow(ctx, `INSERT INTO notification_log_sources(id,topic,cluster_identity,topic_incarnation,active) VALUES(gen_random_uuid(),$1,$2,'fixture-incarnation',true) RETURNING id`, topic, metadata.ClusterID).Scan(&f.source))
	event := f.acceptedEvent(t, ctx)
	payload, err := json.Marshal(event)
	require.NoError(t, err)
	quarantine, err := dispatch.NewQuarantine(f.pool)
	require.NoError(t, err)
	require.NoError(t, quarantine.Store(ctx, dispatch.SourceMessage{LogSourceID: f.source, Topic: topic, Offset: 42, Key: []byte("notification_" + strconv.FormatInt(event.NotificationID, 10)), Payload: payload}, dispatch.AdmissionExhausted, 3))
	request := f.input(t, ctx)
	credential, _ := f.credential(t, ctx, request)
	// The server-side role setting selects this isolated schema. Production input
	// has no schema/PGOPTIONS override, and no production guard is disabled here.
	role := fmt.Sprintf("requeue_role_%d", time.Now().UnixNano())
	roleSQL := pgx.Identifier{role}.Sanitize()
	_, err = f.pool.Exec(ctx, "CREATE ROLE "+roleSQL+" LOGIN SUPERUSER PASSWORD 'integration_only'")
	require.NoError(t, err)
	schema := strings.Split(f.pool.Config().ConnConfig.RuntimeParams["search_path"], ",")[0]
	_, err = f.pool.Exec(ctx, "ALTER ROLE "+roleSQL+" SET search_path TO "+pgx.Identifier{schema}.Sanitize()+",pg_catalog")
	require.NoError(t, err)
	defer func() {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer stop()
		_, err := f.pool.Exec(cleanup, "DROP ROLE "+roleSQL)
		require.NoError(t, err)
	}()
	uri := "postgresql://" + role + ":integration_only@127.0.0.1:15432/notification_integration?sslmode=verify-full&connect_timeout=3&sslrootcert=" + url.QueryEscape(filepath.Join(directory, "ca.crt"))
	config := map[string]any{
		"database_url": uri, "issuer": "fixture-operator", "audience": "fixture-replay", "deployment": "fixture-deployment",
		"verification_key": base64.StdEncoding.EncodeToString(f.key.Public().(ed25519.PublicKey)),
		"log_source_id":    f.source.String(), "cluster_identity": metadata.ClusterID, "topic_name": topic, "topic_incarnation": "fixture-incarnation", "partition_count": 1,
		"kafka": map[string]any{"brokers": []string{"127.0.0.1:19095"}, "authentication": "MTLS", "ca_pem": read("ca.crt"), "client_certificate_pem": read("client.crt"), "client_key_pem": read("client.key"), "sasl_username": "", "sasl_password": ""},
	}
	input, err := json.Marshal(map[string]any{"application_id": request.ApplicationID.String(), "request_id": request.ID.String(), "change_reference": request.ChangeReference, "credential": credential, "positions": []map[string]any{{"log_source_id": f.source.String(), "partition": 0, "offset": 42}}})
	require.NoError(t, err)
	load := func() *requeue.Configuration {
		data, err := json.Marshal(config)
		require.NoError(t, err)
		cfg, err := requeue.LoadConfiguration(bytes.NewReader(data))
		require.NoError(t, err)
		return cfg
	}
	for _, name := range []string{"incarnation mismatch", "partition mismatch", "untrusted broker certificate", "untrusted DB certificate"} {
		t.Run(name, func(t *testing.T) {
			restore := func() {}
			switch name {
			case "incarnation mismatch":
				config["topic_incarnation"] = "wrong"
				restore = func() { config["topic_incarnation"] = "fixture-incarnation" }
			case "partition mismatch":
				config["partition_count"] = 2
				restore = func() { config["partition_count"] = 1 }
			case "untrusted broker certificate":
				config["kafka"].(map[string]any)["ca_pem"] = ""
				restore = func() { config["kafka"].(map[string]any)["ca_pem"] = read("ca.crt") }
			case "untrusted DB certificate":
				config["database_url"] = strings.Split(uri, "&sslrootcert=")[0]
				restore = func() { config["database_url"] = uri }
			}
			defer restore()
			var out, diag bytes.Buffer
			require.Equal(t, 1, requeue.Run(ctx, []string{"--execute"}, bytes.NewReader(input), &out, &diag, load().Open))
			require.Empty(t, out.String())
			require.NotContains(t, diag.String(), credential)
			var count int
			require.NoError(t, f.pool.QueryRow(ctx, "SELECT count(*) FROM notification_replay_requests").Scan(&count))
			require.Zero(t, count, "startup rejection reserves no request")
		})
	}
	cfg := load()
	var out, diag bytes.Buffer
	require.Equal(t, 0, requeue.Run(ctx, nil, bytes.NewReader(input), &out, &diag, cfg.Open), diag.String())
	require.Contains(t, out.String(), `"disposition":"eligible"`)
	var count int
	require.NoError(t, f.pool.QueryRow(ctx, "SELECT count(*) FROM notification_replay_requests").Scan(&count))
	require.Zero(t, count)
	out.Reset()
	diag.Reset()
	require.Equal(t, 0, requeue.Run(ctx, []string{"--execute"}, bytes.NewReader(input), &out, &diag, cfg.Open), diag.String())
	require.Contains(t, out.String(), `"disposition":"published"`)
	require.NotContains(t, out.String(), credential)
	out.Reset()
	diag.Reset()
	require.Equal(t, 0, requeue.Run(ctx, []string{"--execute"}, bytes.NewReader(input), &out, &diag, cfg.Open), diag.String())
	require.NoError(t, f.pool.QueryRow(ctx, "SELECT count(*) FROM notification_replay_attempts").Scan(&count))
	require.Equal(t, 1, count)
	reader := broker.NewReader(broker.ReaderConfig{Brokers: []string{"127.0.0.1:19092"}, Topic: topic, Partition: 0, MinBytes: 1, MaxBytes: 1 << 20})
	defer reader.Close()
	message, err := reader.ReadMessage(ctx)
	require.NoError(t, err)
	decoded, err := kafka.DecodeScopedSend(message.Key, message.Value)
	require.NoError(t, err)
	require.Equal(t, event, decoded)
	connection, err := broker.DialLeader(ctx, "tcp", "127.0.0.1:19092", topic, 0)
	require.NoError(t, err)
	defer connection.Close()
	require.NoError(t, connection.SetReadDeadline(time.Now().Add(2*time.Second)))
	last, err := connection.ReadLastOffset()
	require.NoError(t, err)
	require.EqualValues(t, 1, last, "same request cannot publish another message")
	require.Eventually(t, func() bool {
		var count int
		err := f.pool.QueryRow(ctx, "SELECT count(*) FROM pg_stat_activity WHERE usename=$1", role).Scan(&count)
		return err == nil && count == 0
	}, time.Second, 10*time.Millisecond, "session pools must be closed before command return")
}
