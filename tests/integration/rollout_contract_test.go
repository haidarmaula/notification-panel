//go:build integration

package integration

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

type rolloutMedia struct {
	Content map[string]struct {
		Example json.RawMessage `json:"example"`
	} `json:"content"`
}
type rolloutOperation struct {
	Request   rolloutMedia            `json:"requestBody"`
	Responses map[string]rolloutMedia `json:"responses"`
}

// Use the published consumer examples with the fixture's real identity and clock.
func rolloutExample(t *testing.T, method, path, status string) json.RawMessage {
	t.Helper()
	data, err := os.ReadFile("../../docs/contracts/mobile-delivery.openapi.json")
	require.NoError(t, err)
	var doc struct {
		Paths map[string]map[string]rolloutOperation `json:"paths"`
	}
	require.NoError(t, json.Unmarshal(data, &doc))
	op := doc.Paths["/api/v1/apps/{app_id}"+path][method]
	media := op.Request
	if status != "" {
		media = op.Responses[status]
	}
	example := media.Content["application/json"].Example
	require.NotEmpty(t, example)
	return example
}
