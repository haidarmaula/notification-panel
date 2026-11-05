package requeue

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/dispatch"
	"hello/internal/jsonobject"
)

var errInput = errors.New("invalid replay input")

// decodeInput accepts one bounded UTF-8 object with exact field names. It never
// returns decoder details, which may contain credentials or other private input.
func decodeInput(reader io.Reader) (dispatch.ReplayRequest, string, error) {
	var none dispatch.ReplayRequest
	if reader == nil {
		return none, "", errInput
	}
	data, err := jsonobject.Read(reader)
	if err != nil {
		return none, "", errInput
	}
	var application, request, reference, credential string
	var positions []json.RawMessage
	if err := jsonobject.Decode(data, map[string]any{
		"application_id": &application, "request_id": &request, "change_reference": &reference,
		"credential": &credential, "positions": &positions,
	}); err != nil {
		return none, "", errInput
	}
	if len(credential) == 0 || len(credential) > 8192 || strings.ContainsFunc(credential, unicode.IsSpace) || strings.ContainsFunc(credential, unicode.IsControl) {
		return none, "", errInput
	}
	app, err := parseUUID(application)
	if err != nil {
		return none, "", errInput
	}
	id, err := parseUUID(request)
	if err != nil {
		return none, "", errInput
	}
	if len(positions) < 1 || len(positions) > 100 {
		return none, "", errInput
	}
	selection := make([]dispatch.ReplayPosition, 0, len(positions))
	for _, raw := range positions {
		var source string
		var partition int32
		var offset int64
		if err := jsonobject.Decode(raw, map[string]any{"log_source_id": &source, "partition": &partition, "offset": &offset}); err != nil {
			return none, "", errInput
		}
		sourceID, err := parseUUID(source)
		if err != nil {
			return none, "", errInput
		}
		selection = append(selection, dispatch.ReplayPosition{LogSourceID: sourceID, Partition: partition, Offset: offset})
	}
	result := dispatch.ReplayRequest{ApplicationID: app, ID: id, ChangeReference: reference, Positions: selection}
	if err := result.Validate(); err != nil {
		return none, "", errInput
	}
	return result, credential, nil
}

func parseUUID(value string) (pgtype.UUID, error) {
	var id pgtype.UUID
	if len(value) != 36 || id.Scan(value) != nil || !id.Valid || id.Bytes == [16]byte{} || id.String() != value {
		return pgtype.UUID{}, errInput
	}
	return id, nil
}
