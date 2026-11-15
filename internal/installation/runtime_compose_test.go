package installation

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"hello/internal/database/sqlc"
)

func runtimeProjectFixture(t *testing.T, id, name string) map[string]any {
	t.Helper()
	identityKey, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	originKey, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	return map[string]any{
		"app_id": id, "issuer": "approved-broker", "audience": name,
		"purpose": "mobile", "namespace": name,
		"verification_key": identityKey, "origin_issuer": "approved-origin",
		"origin_audience": name + "-origin", "origin_key": originKey,
		"routes": []map[string]string{{"platform": "ANDROID", "address_type": FCMRegistrationToken,
			"provider_namespace": name, "provider_route_key": "android"}},
	}
}

func runtimeJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return data
}

func runtimeCompositionFixture(t *testing.T) ([]byte, map[string]any) {
	t.Helper()
	old := runtimeProjectFixture(t, "10000000-0000-4000-8000-000000000001", "first")
	next := runtimeProjectFixture(t, "10000000-0000-4000-8000-000000000002", "second")
	return runtimeJSON(t, map[string]any{
		"current_key_version": 1, "fingerprint_key": bytes.Repeat([]byte{1}, 32),
		"encryption_keys": []map[string]any{{"version": 1, "key": bytes.Repeat([]byte{2}, 32)}},
		"projects":        []any{old},
	}), next
}

func TestAppendRuntimeProjectPreservesExistingPolicy(t *testing.T) {
	base, next := runtimeCompositionFixture(t)
	result, err := AppendRuntimeProject(bytes.NewReader(base), runtimeJSON(t, next), sqlc.New(nil), &lifecycleDeadlineStore{})
	require.NoError(t, err)
	var before, after map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(base, &before))
	require.NoError(t, json.Unmarshal(result, &after))
	for key, original := range before {
		if key != "projects" {
			require.JSONEq(t, string(original), string(after[key]), key)
		}
	}
	var oldProjects, newProjects []json.RawMessage
	require.NoError(t, json.Unmarshal(before["projects"], &oldProjects))
	require.NoError(t, json.Unmarshal(after["projects"], &newProjects))
	require.Len(t, newProjects, 2)
	require.JSONEq(t, string(oldProjects[0]), string(newProjects[0]))
	require.JSONEq(t, string(runtimeJSON(t, next)), string(newProjects[1]))
	_, err = LoadRuntime(bytes.NewReader(result), sqlc.New(nil), &lifecycleDeadlineStore{})
	require.NoError(t, err)
}

func TestAppendRuntimeProjectRejectsReplacementAndCrossAppIdentity(t *testing.T) {
	for _, field := range []string{"app_id", "audience", "namespace", "origin_audience"} {
		t.Run(field, func(t *testing.T) {
			base, next := runtimeCompositionFixture(t)
			switch field {
			case "app_id":
				next[field] = "10000000-0000-4000-8000-000000000001"
			case "origin_audience":
				next[field] = "first-origin"
			default:
				next[field] = "first"
			}
			result, err := AppendRuntimeProject(bytes.NewReader(base), runtimeJSON(t, next), sqlc.New(nil), &lifecycleDeadlineStore{})
			require.ErrorIs(t, err, ErrConfiguration)
			require.Nil(t, result)
		})
	}
}

func TestAppendRuntimeProjectUsesServerValidation(t *testing.T) {
	base, next := runtimeCompositionFixture(t)
	valid := runtimeJSON(t, next)
	for name, candidate := range map[string][]byte{
		"unknown field": bytes.Replace(valid, []byte(`"purpose"`), []byte(`"unsupported"`), 1),
		"duplicate":     append([]byte(`{"app_id":"invalid",`), valid[1:]...),
		"null":          []byte(`null`),
		"trailing":      append(bytes.Clone(valid), []byte(` {}`)...),
		"oversize":      []byte(strings.Repeat(" ", (64*1024)+1)),
	} {
		t.Run(name, func(t *testing.T) {
			result, err := AppendRuntimeProject(bytes.NewReader(base), candidate, sqlc.New(nil), &lifecycleDeadlineStore{})
			require.ErrorIs(t, err, ErrConfiguration)
			require.Nil(t, result)
		})
	}
	for _, invalid := range [][]byte{[]byte(`{}`), append(bytes.Clone(base), []byte(` {}`)...), []byte(strings.Repeat(" ", (64*1024)+1))} {
		result, err := AppendRuntimeProject(bytes.NewReader(invalid), valid, sqlc.New(nil), &lifecycleDeadlineStore{})
		require.ErrorIs(t, err, ErrConfiguration)
		require.Nil(t, result)
	}
}

func TestAppendRuntimeProjectRejectsOversizeGeneration(t *testing.T) {
	base, next := runtimeCompositionFixture(t)
	var file map[string]any
	require.NoError(t, json.Unmarshal(base, &file))
	projects := file["projects"].([]any)
	template := projects[0].(map[string]any)
	last := base
	for i := 2; i < 256; i++ {
		project := make(map[string]any, len(template))
		for key, value := range template {
			project[key] = value
		}
		project["app_id"] = fmt.Sprintf("20000000-0000-4000-8000-%012d", i)
		project["audience"] = fmt.Sprintf("app-%d", i)
		project["namespace"] = fmt.Sprintf("app-%d", i)
		project["origin_audience"] = fmt.Sprintf("origin-%d", i)
		projects = append(projects, project)
		file["projects"] = projects
		encoded := runtimeJSON(t, file)
		if len(encoded) > 64*1024 {
			break
		}
		last = encoded
	}
	// A long, valid issuer makes the candidate larger than the final existing
	// project's remaining space while each input still fits the file budget.
	next["issuer"] = strings.Repeat("i", 255)
	next["origin_issuer"] = strings.Repeat("o", 255)
	require.LessOrEqual(t, len(last), 64*1024)
	_, err := LoadRuntime(bytes.NewReader(last), sqlc.New(nil), &lifecycleDeadlineStore{})
	require.NoError(t, err)
	result, err := AppendRuntimeProject(bytes.NewReader(last), runtimeJSON(t, next), sqlc.New(nil), &lifecycleDeadlineStore{})
	require.ErrorIs(t, err, ErrConfiguration)
	require.Nil(t, result)
}
