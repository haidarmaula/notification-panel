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
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

const fixtureStaffPassword = "isolated-release-fixture-only"

func main() {
	if len(os.Args) >= 2 && slices.Contains([]string{"staff-hash", "staff-login", "register", "upload", "enrollment", "verify", "activate", "test-cohort", "release", "test-installations", "test-cohort-targets", "pilot-campaign"}, os.Args[1]) {
		if err := staffRequest(os.Args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
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

func sendPilotCampaign(args []string, do func(string, string, map[string]string, any, int, any) (http.Header, error)) error {
	if len(args) != 4 {
		return errors.New("expected app, staff, enrolled user and send key")
	}
	app, err := uuid.Parse(args[0])
	if err != nil || app == uuid.Nil || app.String() != args[0] {
		return errors.New("invalid fixture campaign application")
	}
	for _, input := range args[1:3] {
		value, err := strconv.ParseInt(input, 10, 64)
		if err != nil || value <= 0 || strconv.FormatInt(value, 10) != input {
			return errors.New("invalid fixture campaign staff or user")
		}
	}
	if key, err := uuid.Parse(args[3]); err != nil || key == uuid.Nil || key.String() != args[3] {
		return errors.New("invalid fixture campaign send key")
	}

	token, err := io.ReadAll(io.LimitReader(os.Stdin, 8192))
	if err != nil || len(token) == 0 {
		return errors.New("missing fixture access token on stdin")
	}
	headers := map[string]string{"Authorization": "Bearer " + strings.TrimSpace(string(token)), "If-None-Match": "*"}
	if _, err := do(http.MethodPut, "/api/v1/admin/apps/"+app.String()+"/staff-grants/"+args[1], headers,
		map[string]bool{"is_active": true}, http.StatusCreated, nil); err != nil {
		return err
	}
	delete(headers, "If-None-Match")

	path := "/api/v1/apps/" + app.String() + "/campaigns"
	type campaignResponse struct {
		Data struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"data"`
	}
	var created campaignResponse
	draft := map[string]any{
		"title": "Fixture pilot", "body": "Synthetic enrollment test",
		"targets": []map[string]string{{"type": "INDIVIDUAL", "id": args[2]}},
	}
	if _, err := do(http.MethodPost, path, headers, draft, http.StatusCreated, &created); err != nil {
		return err
	}
	id, err := strconv.ParseInt(created.Data.ID, 10, 64)
	if err != nil || id <= 0 || created.Data.Status != "DRAFT" {
		return errors.New("fixture draft did not return a campaign ID")
	}

	headers["Idempotency-Key"] = args[3]
	for _, replayed := range []string{"false", "true"} {
		var accepted campaignResponse
		resultHeaders, err := do(http.MethodPost, path+"/"+created.Data.ID+"/send", headers, nil, http.StatusAccepted, &accepted)
		if err != nil {
			return err
		}
		if accepted.Data.ID != created.Data.ID || accepted.Data.Status != "QUEUED" ||
			resultHeaders.Get("Idempotency-Replayed") != replayed {
			return errors.New("pilot campaign did not accept and replay the same operation")
		}
	}
	fmt.Println(created.Data.ID)
	return nil
}

func staffRequest(args []string) error {
	if args[0] == "staff-hash" {
		digest, err := bcrypt.GenerateFromPassword([]byte(fixtureStaffPassword), bcrypt.DefaultCost)
		if err == nil {
			fmt.Println(string(digest))
		}
		return err
	}
	transport, err := fixtureTransport()
	if err != nil {
		return err
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	do := func(method, path string, headers map[string]string, body any, status int, output any) (http.Header, error) {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		var reader io.Reader
		if body != nil {
			reader = bytes.NewReader(encoded)
		}
		req, err := http.NewRequest(method, "https://127.0.0.1:8080"+path, reader)
		if err != nil {
			return nil, err
		}
		req.Header.Set("X-API-Key", "fixture-api-key-not-for-deployment")
		req.Header.Set("X-Forwarded-Proto", "https")
		req.Header.Set("X-Forwarded-For", "127.0.0.1")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		for key, value := range headers {
			req.Header.Set(key, value)
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != status {
			return nil, fmt.Errorf("%s %s: got HTTP %d, want %d", method, path, resp.StatusCode, status)
		}
		if output != nil {
			if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(output); err != nil {
				return nil, err
			}
		}
		return resp.Header, nil
	}
	if args[0] == "pilot-campaign" {
		return sendPilotCampaign(args[1:], do)
	}
	if args[0] == "staff-login" {
		var login struct {
			Data struct {
				AccessToken string `json:"access_token"`
			} `json:"data"`
		}
		_, err := do(http.MethodPost, "/api/v1/auth/login", nil, map[string]string{
			"email": "activation@fixture.invalid", "password": fixtureStaffPassword,
		}, http.StatusOK, &login)
		if err != nil {
			return err
		}
		if login.Data.AccessToken == "" {
			return errors.New("missing fixture access token")
		}
		fmt.Println(login.Data.AccessToken)
		return nil
	}
	if args[0] == "test-installations" || args[0] == "test-cohort-targets" {
		token, err := io.ReadAll(io.LimitReader(os.Stdin, 8192))
		if err != nil || len(token) == 0 || len(args) < 3 {
			return errors.New("invalid fixture read arguments")
		}
		path := "/api/v1/admin/apps/" + args[1] + "/onboarding"
		headers := map[string]string{"Authorization": "Bearer " + strings.TrimSpace(string(token))}
		if args[0] == "test-installations" {
			var result struct {
				Data struct {
					Installations []struct {
						ID string `json:"installation_id"`
					} `json:"installations"`
				} `json:"data"`
			}
			_, err := do(http.MethodGet, path+"/test-installations", headers, nil, http.StatusOK, &result)
			if err != nil {
				return err
			}
			for _, item := range result.Data.Installations {
				if item.ID == args[2] {
					fmt.Println(item.ID)
					return nil
				}
			}
			return errors.New("expected scoped installation missing")
		}
		if len(args) != 4 {
			return errors.New("invalid target arguments")
		}
		var result struct {
			Data []struct {
				ID      string  `json:"installation_id"`
				Outcome *string `json:"outcome"`
			} `json:"data"`
		}
		_, err = do(http.MethodGet, path+"/operations/"+args[2]+"/targets", headers, nil, http.StatusOK, &result)
		if err != nil {
			return err
		}
		for _, item := range result.Data {
			if item.ID == args[3] && item.Outcome != nil {
				fmt.Println(*item.Outcome)
				return nil
			}
		}
		fmt.Println("PENDING")
		return nil
	}
	if args[0] == "register" || args[0] == "upload" {
		token, err := io.ReadAll(io.LimitReader(os.Stdin, 8192))
		if err != nil || len(token) == 0 {
			return errors.New("missing fixture access token on stdin")
		}
		headers := map[string]string{"Authorization": "Bearer " + strings.TrimSpace(string(token))}
		var resource struct {
			Data struct {
				ID    uuid.UUID `json:"id"`
				State string    `json:"state"`
			} `json:"data"`
		}
		switch {
		case args[0] == "register" && len(args) == 1:
			_, err = do(http.MethodPost, "/api/v1/admin/apps", headers,
				map[string]string{"key": "preflight-fixture", "name": "Preflight Fixture"}, http.StatusCreated, &resource)
		case args[0] == "upload" && len(args) == 3:
			path := "/api/v1/admin/apps/" + args[1] + "/onboarding"
			current, readErr := do(http.MethodGet, path, headers, nil, http.StatusOK, nil)
			if readErr != nil {
				return readErr
			}
			account, readErr := os.ReadFile("/fixtures/service-account.json")
			if readErr != nil {
				return readErr
			}
			var identity struct {
				Project string `json:"project_id"`
				Email   string `json:"client_email"`
			}
			if json.Unmarshal(account, &identity) != nil || identity.Project != "integration-fixture" ||
				identity.Email != "fixture@integration-fixture.iam.gserviceaccount.com" {
				return errors.New("refusing a non-fixture Firebase credential")
			}
			headers["If-Match"], headers["Idempotency-Key"] = current.Get("ETag"), args[2]
			_, err = do(http.MethodPut, path+"/fcm", headers, map[string]any{
				"environment": "staging", "target_project_id": identity.Project,
				"service_account": json.RawMessage(account),
			}, http.StatusCreated, &resource)
			if err == nil && resource.Data.State != "STORED" {
				return errors.New("fixture credential was not stored")
			}
		default:
			return errors.New("invalid fixture bootstrap operation arguments")
		}
		if err != nil {
			return err
		}
		if resource.Data.ID == uuid.Nil {
			return errors.New("missing created resource ID")
		}
		fmt.Println(resource.Data.ID)
		return nil
	}
	var method, suffix, kind, key, replayed, expectedStatus string
	var payload any
	switch {
	case args[0] == "release" && len(args) == 8:
		method, suffix, kind = http.MethodPost, "/dispatch/release", "RELEASE_"+args[2]
		payload = map[string]string{"mode": args[2], "cohort_id": args[3], "evidence_id": args[4]}
		key, expectedStatus, replayed = args[5], args[6], args[7]
	case args[0] == "enrollment" && len(args) == 3:
		method, suffix = http.MethodPut, "/enrollment"
		payload = map[string]any{"profile_id": "50000000-0000-4000-8000-000000000001",
			"platforms": []map[string]string{{"platform": "ANDROID", "application_identifier": "invalid.fixture.onboarding"}}}
		key, expectedStatus = args[2], "200"
	case args[0] == "verify" && len(args) == 4:
		method, suffix, kind = http.MethodPost, "/fcm/verify", "VERIFY"
		payload = map[string]string{"configuration_id": args[2]}
		key, expectedStatus, replayed = args[3], "202", "false"
	case args[0] == "activate" && len(args) == 6:
		method, suffix, kind = http.MethodPost, "/activate", "ACTIVATE"
		payload = map[string]string{"configuration_id": args[2]}
		key, expectedStatus, replayed = args[3], args[4], args[5]
	case args[0] == "test-cohort" && len(args) == 7:
		method, suffix, kind = http.MethodPut, "/test-cohort", "TEST_COHORT"
		payload = map[string]any{"evidence_id": args[2], "installation_ids": strings.Split(args[3], ","), "reason": "fixture cohort"}
		key, expectedStatus, replayed = args[4], args[5], args[6]
	default:
		return errors.New("invalid fixture onboarding operation arguments")
	}
	status, err := strconv.Atoi(expectedStatus)
	if err != nil {
		return err
	}
	token, err := io.ReadAll(io.LimitReader(os.Stdin, 8192))
	if err != nil || len(token) == 0 {
		return errors.New("missing fixture access token on stdin")
	}
	path := "/api/v1/admin/apps/" + args[1] + "/onboarding"
	headers := map[string]string{"Authorization": "Bearer " + strings.TrimSpace(string(token))}
	responseHeaders, err := do(http.MethodGet, path, headers, nil, http.StatusOK, nil)
	if err != nil {
		return err
	}
	headers["If-Match"] = responseHeaders.Get("ETag")
	headers["Idempotency-Key"] = key
	var operation struct {
		Data struct {
			ID    uuid.UUID `json:"id"`
			Kind  string    `json:"kind"`
			State string    `json:"state"`
		} `json:"data"`
	}
	responseHeaders, err = do(method, path+suffix, headers, payload, status, &operation)
	if err != nil || status != http.StatusAccepted {
		return err
	}
	if operation.Data.ID == uuid.Nil || operation.Data.Kind != kind || operation.Data.State != "PENDING" ||
		responseHeaders.Get("Location") != path+"/operations/"+operation.Data.ID.String() || responseHeaders.Get("Idempotency-Replayed") != replayed {
		return errors.New("invalid accepted onboarding operation response")
	}
	if kind == "VERIFY" {
		location := responseHeaders.Get("Location")
		for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(250 * time.Millisecond) {
			if _, err := do(http.MethodGet, location, headers, nil, http.StatusOK, &operation); err != nil {
				return err
			}
			switch operation.Data.State {
			case "SUCCEEDED":
				fmt.Println(operation.Data.ID)
				return nil
			case "PENDING", "RUNNING":
			default:
				return fmt.Errorf("fixture OAuth verification ended in %s", operation.Data.State)
			}
		}
		return errors.New("fixture OAuth verification did not finish")
	}
	fmt.Println(operation.Data.ID)
	return nil
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
