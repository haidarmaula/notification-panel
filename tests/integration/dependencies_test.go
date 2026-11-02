//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/messaging"
	"github.com/jackc/pgx/v5/pgtype"
	broker "github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/option"

	"hello/internal/database"
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
	"hello/internal/kafka"
)

const databaseURL = "postgres://integration:integration_only@127.0.0.1:15432/notification_integration?sslmode=disable"
const simulationURL = "http://127.0.0.1:14010/fcm/v1"

func TestMain(m *testing.M) {
	if os.Getenv("HELLO_INTEGRATION") != "1" {
		fmt.Fprintln(os.Stderr, "Run bash scripts/integration.sh; explicit HELLO_INTEGRATION=1 is required")
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func TestPostgresRepository(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := database.New(ctx, databaseURL)
	require.NoError(t, err)
	defer pool.Close()
	var roles int
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM roles").Scan(&roles))
	require.Equal(t, 4, roles)
	repo := repository.NewUserRepository(sqlc.New(pool))
	created, err := repo.Create(ctx, sqlc.CreateUserParams{
		ExternalID: "integration-user", Name: pgtype.Text{String: "Fixture", Valid: true}, Status: "ACTIVE",
	})
	require.NoError(t, err)
	loaded, err := repo.FindByExternalID(ctx, "integration-user")
	require.NoError(t, err)
	require.Equal(t, created.ID, loaded.ID)
	require.Equal(t, "ACTIVE", loaded.Status)
}

func TestKafkaProducerRoundTrip(t *testing.T) {
	for _, topic := range []string{"notification.send.requested", "notification.delivery.updated"} {
		t.Run(topic, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			startOffset := lastTopicOffset(t, ctx, topic)
			producer := kafka.NewProducer("127.0.0.1:19092", topic)
			defer producer.Close()
			wantKey := "notification_42"
			var wantPayload []byte
			if topic == "notification.send.requested" {
				event := kafka.NotificationSendRequested{NotificationID: 42, RequestedAt: time.Now().UTC()}
				var err error
				wantPayload, err = json.Marshal(event)
				require.NoError(t, err)
				err = producer.PublishSendRequested(ctx, event)
				require.NoError(t, err)
			} else {
				wantKey = "delivery_42"
				event := kafka.DeliveryUpdated{NotificationID: 42, UserID: 7, Status: "SENT", Provider: "fixture", OccurredAt: time.Now().UTC()}
				var err error
				wantPayload, err = json.Marshal(event)
				require.NoError(t, err)
				err = producer.PublishDeliveryUpdated(ctx, event)
				require.NoError(t, err)
			}
			reader := broker.NewReader(broker.ReaderConfig{
				Brokers: []string{"127.0.0.1:19092"}, Topic: topic, Partition: 0,
				MinBytes: 1, MaxBytes: 1_000_000, MaxWait: 100 * time.Millisecond,
			})
			defer reader.Close()
			require.NoError(t, reader.SetOffset(startOffset))
			msg, err := reader.ReadMessage(ctx)
			require.NoError(t, err)
			require.Equal(t, wantKey, string(msg.Key))
			require.JSONEq(t, string(wantPayload), string(msg.Value))
		})
	}
}

type localTransport struct{ base *http.Transport }

func (rt localTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "http" || req.URL.Host != "127.0.0.1:14010" || !strings.HasPrefix(req.URL.Path, "/fcm/") {
		return nil, errors.New("integration transport rejects non-fixture request")
	}
	return rt.base.RoundTrip(req)
}

func TestFirebaseSDKAgainstSimulation(t *testing.T) {
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	httpClient := &http.Client{
		Transport:     localTransport{transport},
		CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("fixture redirects forbidden") },
	}
	app, err := firebase.NewApp(context.Background(), &firebase.Config{ProjectID: "integration-fixture"},
		option.WithoutAuthentication(), option.WithHTTPClient(httpClient), option.WithEndpoint(simulationURL))
	require.NoError(t, err)
	client, err := app.Messaging(context.Background())
	require.NoError(t, err)
	for _, tc := range []struct {
		address string
		check   func(error) bool
	}{
		{"fixture-accepted", nil},
		{"fixture-unregistered", messaging.IsRegistrationTokenNotRegistered},
		{"fixture-quota", messaging.IsQuotaExceeded},
		{"fixture-unavailable", messaging.IsUnavailable},
	} {
		t.Run(tc.address, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			id, err := client.Send(ctx, &messaging.Message{Fid: tc.address, Data: map[string]string{"fixture": "integration"}})
			if tc.check == nil {
				require.NoError(t, err)
				require.True(t, strings.HasPrefix(id, "projects/integration-fixture/messages/accepted-"))
			} else {
				require.Error(t, err)
				require.True(t, tc.check(err), "unexpected SDK error: %v", err)
			}
		})
	}
	t.Run("caller deadline", func(t *testing.T) {
		started := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		_, err := client.Send(ctx, &messaging.Message{Fid: "fixture-timeout"})
		require.Error(t, err)
		require.ErrorIs(t, ctx.Err(), context.DeadlineExceeded)
		require.Less(t, time.Since(started), time.Second, "caller must not wait for the delayed response")
	})
	t.Run("external URL rejected", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodPost, "https://fcm.googleapis.com/v1/projects/real/messages:send", nil)
		require.NoError(t, err)
		_, err = httpClient.Do(req)
		require.ErrorContains(t, err, "rejects non-fixture")
	})
}

// A shared fixture broker can already contain API acceptance events. Each
// round-trip asserts its own publication range rather than assuming offset 0.
func lastTopicOffset(t *testing.T, ctx context.Context, topic string) int64 {
	t.Helper()
	conn, err := broker.DialLeader(ctx, "tcp", "127.0.0.1:19092", topic, 0)
	require.NoError(t, err)
	defer conn.Close()
	require.NoError(t, conn.SetDeadline(time.Now().Add(5*time.Second)))
	offset, err := conn.ReadLastOffset()
	require.NoError(t, err)
	return offset
}
