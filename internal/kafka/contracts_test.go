package kafka

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type eventSchema struct {
	Key                  string                     `json:"x-kafka-key"`
	Properties           map[string]json.RawMessage `json:"properties"`
	Required             []string                   `json:"required"`
	AdditionalProperties bool                       `json:"additionalProperties"`
	Examples             []json.RawMessage          `json:"examples"`
}

func checkEventSchema(t *testing.T, file string, model any) eventSchema {
	t.Helper()
	data, err := os.ReadFile("../../docs/contracts/" + file)
	require.NoError(t, err)
	var schema eventSchema
	require.NoError(t, json.Unmarshal(data, &schema))
	require.False(t, schema.AdditionalProperties)
	fields := reflect.TypeOf(model)
	var required []string
	for i := range fields.NumField() {
		tag, ok := fields.Field(i).Tag.Lookup("json")
		require.True(t, ok)
		name, opts, _ := strings.Cut(tag, ",")
		require.Contains(t, schema.Properties, name)
		if opts != "omitempty" {
			required = append(required, name)
		}
	}
	require.Len(t, schema.Properties, fields.NumField())
	sort.Strings(required)
	sort.Strings(schema.Required)
	require.Equal(t, required, schema.Required)
	require.NotEmpty(t, schema.Examples)
	return schema
}

func TestDocumentedKafkaRecords(t *testing.T) {
	send := checkEventSchema(t, "send-requested-v1.schema.json", ScopedSendRequested{})
	for _, example := range send.Examples {
		key := strings.ReplaceAll(send.Key, "<notification_id>", "42")
		e, err := DecodeScopedSend([]byte(key), example)
		require.NoError(t, err)
		require.EqualValues(t, 42, e.NotificationID)
	}
	delivery := checkEventSchema(t, "delivery-updated-v2.schema.json", ScopedDeliveryUpdated{})
	for _, example := range delivery.Examples {
		e, err := DecodeScopedDelivery(example)
		require.NoError(t, err)
		require.Equal(t, strings.ReplaceAll(delivery.Key, "<notification_id>", "42"), string(DeliveryUpdateKey(e.NotificationID)))
	}
}
