//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"net/http"
	"testing"
	"time"

	firebase "firebase.google.com/go/v4"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/option"
	"hello/internal/installation"
	"hello/internal/pushpayload"
	"hello/internal/receipt"
)

func TestScopedFCMPayloadAgainstSimulation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: localTransport{transport}, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("fixture redirects forbidden") }}
	app, err := firebase.NewApp(ctx, &firebase.Config{ProjectID: "integration-fixture"}, option.WithoutAuthentication(), option.WithHTTPClient(client), option.WithEndpoint(simulationURL))
	require.NoError(t, err)
	messaging, err := app.Messaging(ctx)
	require.NoError(t, err)
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{42}, 32))
	signer, err := receipt.NewSigner("fixture", map[string]ed25519.PrivateKey{"fixture-key": key})
	require.NoError(t, err)
	verifier, err := receipt.NewVerifier("fixture", map[string]ed25519.PublicKey{"fixture-key": key.Public().(ed25519.PublicKey)})
	require.NoError(t, err)
	id := func(b byte) pgtype.UUID { return pgtype.UUID{Bytes: [16]byte{b}, Valid: true} }
	now := time.Now().UTC()
	scope := receipt.Scope{ApplicationID: id(1), OriginEpoch: id(2), NotificationID: 3, DeliveryID: id(4), BindingID: 5, InstallationID: id(6), OwnerEpoch: 7, ExpiresUnix: now.Add(24 * time.Hour).Unix()}
	capability, err := signer.Sign("fixture-key", scope)
	require.NoError(t, err)
	for _, platform := range []string{"ANDROID", "IOS"} {
		for _, addressType := range []string{installation.FCMFID, installation.FCMRegistrationToken} {
			t.Run(platform+"/"+addressType, func(t *testing.T) {
				builder, err := pushpayload.New(pushpayload.Route{ApplicationID: scope.ApplicationID, Environment: "fixture", Namespace: "firebase-fixture", Key: "route", Platform: platform, AddressType: addressType})
				require.NoError(t, err)
				message, err := builder.Build(now, pushpayload.Request{Scope: scope, Capability: capability, Destination: pushpayload.Destination{ApplicationID: scope.ApplicationID, BindingID: scope.BindingID, InstallationID: scope.InstallationID, OwnerEpoch: scope.OwnerEpoch, Namespace: "firebase-fixture", RouteKey: "route", Platform: platform, AddressType: addressType, Address: "fixture-accepted"}, Deadline: now.Add(time.Hour), Title: "Fixture", Body: "Scoped payload", Data: map[string]string{"fixture": "integration"}})
				require.NoError(t, err)
				name, err := messaging.Send(ctx, message)
				require.NoError(t, err)
				require.Contains(t, name, "projects/integration-fixture/messages/accepted-")
				require.NoError(t, verifier.Verify(message.Data["receipt_token"], scope, now))
			})
		}
	}
}
