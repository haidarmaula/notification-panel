//go:build ignore

package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

func main() {
	if len(os.Args) != 2 && len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: fixture-onboarding-mobile <app-uuid> [campaign-id binding-id]")
		os.Exit(2)
	}
	appID, err := uuid.Parse(os.Args[1])
	if err != nil || appID == uuid.Nil || !strings.EqualFold(os.Args[1], appID.String()) {
		fmt.Fprintln(os.Stderr, "expected an application UUID")
		os.Exit(2)
	}
	if len(os.Args) == 4 {
		if err := recordReceipt(appID, os.Args[2], os.Args[3]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	installationID, err := enroll(appID)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(installationID)
}

func enroll(appID uuid.UUID) (uuid.UUID, error) {
	transport, err := fixtureTransport()
	if err != nil {
		return uuid.Nil, err
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		Timeout:   10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	const subject = "fixture-device"
	now := time.Now()
	expires := now.Add(time.Minute).Unix()
	credential, err := signFixtureToken(3, jwt.MapClaims{
		"iss": "fixture-identity", "aud": "fixture-mobile", "sub": subject,
		"purpose": "mobile", "exp": expires,
	})
	if err != nil {
		return uuid.Nil, errors.New("sign fixture mobile credential")
	}
	nonce, err := uuid.NewRandom()
	if err != nil {
		return uuid.Nil, errors.New("generate fixture origin nonce")
	}
	digest := sha256.Sum256([]byte(credential))
	origin, err := signFixtureToken(4, jwt.MapClaims{
		"iss": "fixture-origin", "aud": "fixture-installation", "sub": subject,
		"purpose": "mobile_installation_enrollment", "namespace": "fixture-users", "app_id": appID.String(),
		"credential_digest": base64.RawURLEncoding.EncodeToString(digest[:]),
		"jti":               nonce.String(), "iat": now.Unix(), "exp": expires,
	})
	if err != nil {
		return uuid.Nil, errors.New("sign fixture enrollment origin")
	}

	body, err := json.Marshal(map[string]string{
		"platform": "ANDROID", "address_type": "FCM_REGISTRATION_TOKEN",
		"address": "execution-address-0", "origin": origin,
	})
	if err != nil {
		return uuid.Nil, errors.New("encode fixture enrollment request")
	}
	endpoint := "https://127.0.0.1:8080/api/v1/apps/" + appID.String() + "/mobile/installations"
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return uuid.Nil, errors.New("create fixture enrollment request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+credential)
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-For", "127.0.0.1")
	resp, err := client.Do(req)
	if err != nil {
		return uuid.Nil, errors.New("send fixture enrollment request")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return uuid.Nil, fmt.Errorf("fixture enrollment returned HTTP %d, want 201", resp.StatusCode)
	}
	var result struct {
		Data struct {
			InstallationID uuid.UUID `json:"installation_id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<10)).Decode(&result); err != nil {
		return uuid.Nil, errors.New("decode fixture enrollment response")
	}
	if result.Data.InstallationID == uuid.Nil {
		return uuid.Nil, errors.New("fixture enrollment response is missing an installation UUID")
	}
	return result.Data.InstallationID, nil
}

func recordReceipt(appID uuid.UUID, campaign, binding string) error {
	var payload struct {
		Message struct {
			Data map[string]string `json:"data"`
		} `json:"message"`
	}
	if err := json.NewDecoder(io.LimitReader(os.Stdin, 16<<10)).Decode(&payload); err != nil {
		return errors.New("decode synthetic push payload")
	}
	data := payload.Message.Data
	delivery, err := uuid.Parse(data["delivery_id"])
	if err != nil || delivery == uuid.Nil || data["application_id"] != appID.String() ||
		data["notification_id"] != campaign || data["registration_id"] != binding || data["receipt_token"] == "" {
		return errors.New("synthetic push did not match the enrolled installation and campaign")
	}
	eventID, err := uuid.NewRandom()
	if err != nil {
		return errors.New("generate fixture receipt event ID")
	}
	credential, err := signFixtureToken(3, jwt.MapClaims{
		"iss": "fixture-identity", "aud": "fixture-mobile", "sub": "fixture-device",
		"purpose": "mobile", "exp": time.Now().Add(time.Minute).Unix(),
	})
	if err != nil {
		return errors.New("sign fixture mobile credential")
	}
	transport, err := fixtureTransport()
	if err != nil {
		return err
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	body, err := json.Marshal(map[string]string{
		"delivery_id": delivery.String(), "client_event_id": eventID.String(),
		"event": "DELIVERED", "receipt_token": data["receipt_token"],
	})
	if err != nil {
		return errors.New("encode fixture receipt")
	}
	url := "https://127.0.0.1:8080/api/v1/apps/" + appID.String() + "/mobile/notifications/" + campaign + "/receipt"
	for range 2 {
		req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return errors.New("create fixture receipt request")
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+credential)
		req.Header.Set("X-Forwarded-Proto", "https")
		req.Header.Set("X-Forwarded-For", "127.0.0.1")
		resp, err := client.Do(req)
		if err != nil {
			return errors.New("send fixture receipt request")
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("fixture receipt returned HTTP %d, want 200", resp.StatusCode)
		}
	}
	return nil
}

func signFixtureToken(seed byte, claims jwt.MapClaims) (string, error) {
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{seed}, ed25519.SeedSize))
	return jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims).SignedString(key)
}

func fixtureTransport() (*http.Transport, error) {
	const dir = "/etc/hello/"
	ca, err := os.ReadFile(dir + "fixture-ca.crt")
	if err != nil {
		return nil, errors.New("read fixture CA certificate")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return nil, errors.New("parse fixture CA certificate")
	}
	cert, err := tls.LoadX509KeyPair(dir+"fixture-client.crt", dir+"fixture-client.key")
	if err != nil {
		return nil, errors.New("load fixture client certificate and key")
	}
	return &http.Transport{TLSClientConfig: &tls.Config{
		RootCAs:      roots,
		Certificates: []tls.Certificate{cert},
		ServerName:   "localhost",
		MinVersion:   tls.VersionTLS13,
	}}, nil
}
