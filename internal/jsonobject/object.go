// Package jsonobject validates bounded operational JSON without exposing input in errors.
package jsonobject

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"
)

// ErrInvalid contains no decoder values, credentials or payload details.
var ErrInvalid = errors.New("invalid JSON object")

// Read returns at most 64 KiB of UTF-8 input. Decode separately enforces the schema.
func Read(reader io.Reader) ([]byte, error) {
	if reader == nil {
		return nil, ErrInvalid
	}
	data, err := io.ReadAll(io.LimitReader(reader, 64*1024+1))
	if err != nil || len(data) > 64*1024 || !utf8.Valid(data) {
		return nil, ErrInvalid
	}
	return data, nil
}

// Decode requires each known field exactly once and rejects nulls, case
// aliases and trailing values. Nested objects need a separate Decode call with
// their own schema to enforce the same constraints.
func Decode(data []byte, fields map[string]any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return ErrInvalid
	}
	seen := make(map[string]bool, len(fields))
	for decoder.More() {
		token, err := decoder.Token()
		name, ok := token.(string)
		if err != nil || !ok || seen[name] {
			return ErrInvalid
		}
		target, ok := fields[name]
		if !ok {
			return ErrInvalid
		}
		var raw json.RawMessage
		if decoder.Decode(&raw) != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, target) != nil {
			return ErrInvalid
		}
		seen[name] = true
	}
	if end, err := decoder.Token(); err != nil || end != json.Delim('}') || len(seen) != len(fields) {
		return ErrInvalid
	}
	var extra json.RawMessage
	if decoder.Decode(&extra) != io.EOF {
		return ErrInvalid
	}
	return nil
}
