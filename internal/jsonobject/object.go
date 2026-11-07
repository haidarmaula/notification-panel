// Package jsonobject validates bounded operational JSON without exposing input in errors.
package jsonobject

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
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

// UniqueLowercaseFields rejects duplicate or case-aliased policy names anywhere
// in a nested document. Typed decoding subsequently validates values and schema.
func UniqueLowercaseFields(data []byte) bool {
	if !utf8.Valid(data) {
		return false
	}
	d := json.NewDecoder(bytes.NewReader(data))
	var value func(int) bool
	value = func(depth int) bool {
		if depth > 16 {
			return false
		}
		token, err := d.Token()
		if err != nil {
			return false
		}
		switch token {
		case json.Delim('{'):
			names := make(map[string]bool)
			for d.More() {
				key, err := d.Token()
				name, ok := key.(string)
				if err != nil || !ok || names[name] || name != strings.ToLower(name) {
					return false
				}
				names[name] = true
				if !value(depth + 1) {
					return false
				}
			}
			end, err := d.Token()
			return err == nil && end == json.Delim('}')
		case json.Delim('['):
			for d.More() {
				if !value(depth + 1) {
					return false
				}
			}
			end, err := d.Token()
			return err == nil && end == json.Delim(']')
		default:
			return true
		}
	}
	if !value(0) {
		return false
	}
	_, err := d.Token()
	return err == io.EOF
}
