package applications

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"hello/internal/features/auth"
	"hello/internal/features/profile"
	"hello/internal/features/staff"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
)

type wireSchema struct {
	Ref        string                `json:"$ref"`
	Type       any                   `json:"type"`
	Properties map[string]wireSchema `json:"properties"`
	Const      any                   `json:"const"`
}

// Check the exported wire fields, including fields promoted from embedded DTOs,
// and the contract's scalar representation at actual Go serialization boundaries.
func TestStaffBusinessWireContracts(t *testing.T) {
	var spec struct {
		Components struct{ Schemas map[string]wireSchema }
		Paths      map[string]map[string]struct {
			Responses map[string]struct {
				Content map[string]struct {
					Schema  wireSchema
					Example json.RawMessage
				}
			}
		}
	}
	data, err := os.ReadFile("../../../docs/contracts/staff-business.openapi.json")
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &spec))
	types := map[string]any{
		"Credentials": auth.LoginResponse{}, "Login": auth.LoginRequest{}, "Refresh": auth.RefreshTokenRequest{},
		"Staff": staff.StaffResponse{}, "StaffPage": staff.ListStaffResponse{}, "CreateStaff": staff.CreateStaffRequest{},
		"UpdateStaff": staff.UpdateStaffRequest{}, "SetStaffStatus": staff.UpdateStatusRequest{}, "ResetStaffPassword": staff.UpdatePasswordRequest{},
		"UpdateProfile": profile.UpdateProfileRequest{}, "ChangeProfilePassword": profile.UpdatePasswordRequest{},
		"User": scopedUserDTO{}, "Device": scopedDeviceDTO{}, "Segment": scopedSegmentDTO{}, "Member": scopedMemberDTO{},
		"CampaignSummary": campaignSummary{}, "CampaignDraft": campaignDraftResult{}, "CampaignInput": campaignDraftInput{},
		"Template": templateDTO{}, "TemplateInput": templateInput{}, "Upload": uploadBatchDTO{}, "UploadRow": uploadRowDTO{}, "Audit": projectAuditDTO{},
		"Delivery": deliveryDTO{}, "CampaignRead": campaignReadDTO{}, "DevicePage": readPage[scopedDeviceDTO]{}, "CampaignSummaryPage": readPage[campaignSummary]{},
	}
	types["UserPage"] = readPage[scopedUserDTO]{}
	types["SegmentPage"] = readPage[scopedSegmentDTO]{}
	types["MemberPage"] = readPage[scopedMemberDTO]{}
	types["TemplatePage"] = readPage[templateDTO]{}
	types["DeliveryPage"] = readPage[deliveryDTO]{}
	types["CampaignReadPage"] = readPage[campaignReadDTO]{}
	types["UploadPage"] = readPage[uploadBatchDTO]{}
	types["UploadRowPage"] = readPage[uploadRowDTO]{}
	types["AuditPage"] = readPage[projectAuditDTO]{}
	check := func(t *testing.T, name string, sample any) {
		t.Helper()
		schema, ok := spec.Components.Schemas[name]
		require.True(t, ok, name)
		var keys []string
		for _, f := range reflect.VisibleFields(reflect.TypeOf(sample)) {
			tag, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if tag == "" || tag == "-" {
				continue
			}
			keys = append(keys, tag)
			p, ok := schema.Properties[tag]
			require.True(t, ok, name+"."+tag)
			if p.Ref != "" {
				p = spec.Components.Schemas[strings.TrimPrefix(p.Ref, "#/components/schemas/")]
			}
			var wire []any
			switch v := p.Type.(type) {
			case string:
				wire = []any{v}
			case []any:
				wire = v
			}
			if _, ok := p.Const.(string); ok {
				wire = []any{"string"}
			}
			ft := f.Type
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
				// Input pointers can distinguish a missing required field.
				// An output pointer without omitempty serializes nil as null.
				if !strings.HasSuffix(name, "Input") {
					require.Contains(t, wire, "null", name+"."+tag)
				}
			}
			kind := ""
			switch ft.Kind() {
			case reflect.String:
				kind = "string"
			case reflect.Int32, reflect.Int64:
				kind = "integer"
			case reflect.Bool:
				kind = "boolean"
			case reflect.Slice:
				kind = "array"
			case reflect.Struct:
				if ft == reflect.TypeFor[pgtype.Text]() || ft == reflect.TypeFor[pgtype.Timestamptz]() {
					kind = "string"
				}
			}
			require.NotEmpty(t, kind, name+"."+tag)
			require.Contains(t, wire, kind, name+"."+tag)
		}
		require.Len(t, keys, len(schema.Properties), name)
	}
	for name, typ := range types {
		t.Run(name, func(t *testing.T) { check(t, name, typ) })
	}
	t.Run("profile matches staff projection", func(t *testing.T) { check(t, "Staff", profile.ProfileResponse{}) })
	var checked int
	for _, operations := range spec.Paths {
		for _, operation := range operations {
			for code, response := range operation.Responses {
				media := response.Content["application/json"]
				if !strings.HasPrefix(code, "2") || media.Example == nil {
					continue
				}
				name := strings.TrimSuffix(strings.TrimPrefix(media.Schema.Ref, "#/components/schemas/"), "Result")
				if name == "Campaign" {
					// The detail handler builds an anonymous response around raw
					// target JSON. The OpenAPI example validator checks its schema.
					continue
				}
				sample, ok := types[name]
				require.True(t, ok, name)
				var envelope struct{ Data json.RawMessage }
				require.NoError(t, json.Unmarshal(media.Example, &envelope))
				dst := reflect.New(reflect.TypeOf(sample)).Interface()
				require.NoError(t, json.Unmarshal(envelope.Data, dst), name)
				got, err := json.Marshal(dst)
				require.NoError(t, err)
				require.JSONEq(t, string(envelope.Data), string(got), name)
				checked++
			}
		}
	}
	require.NotZero(t, checked)
}
