package httprequest

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"unicode/utf8"

	"hello/pkg/response"
)

// DecodeStrictJSONObject is opt-in for new APIs: UTF-8 JSON object, known fields,
// exact top-level field names supplied by the caller and no duplicate keys (including escaped spellings), one complete value. Existing
// DecodeJSON callers retain their compatibility semantics. Nesting is bounded
// during duplicate detection; current registry DTOs contain only scalar fields.
func DecodeStrictJSONObject(w http.ResponseWriter, r *http.Request, dst any, limit int64, fields ...string) bool {
	if !jsonMediaType(r) {
		response.JSON(w, http.StatusUnsupportedMediaType, nil, "unsupported media type")
		return false
	}
	data, ok := readBody(w, r, limit)
	if !ok {
		return false
	}
	if !utf8.Valid(data) || !uniqueObject(data, fields) {
		response.JSON(w, http.StatusBadRequest, nil, "invalid request body")
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid request body")
		return false
	}
	return true
}
func jsonMediaType(r *http.Request) bool {
	values := r.Header.Values("Content-Type")
	if len(values) != 1 {
		return false
	}
	media, params, err := mime.ParseMediaType(values[0])
	if err != nil || !strings.EqualFold(media, "application/json") {
		return false
	}
	for k, v := range params {
		if k != "charset" || !strings.EqualFold(v, "utf-8") {
			return false
		}
	}
	enc := r.Header.Values("Content-Encoding")
	return len(enc) == 0 || len(enc) == 1 && strings.EqualFold(strings.Trim(enc[0], " \t"), "identity")
}
func uniqueObject(data []byte, fields []string) bool {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	first, err := d.Token()
	if err != nil || first != json.Delim('{') {
		return false
	}
	if err := scanContainer(d, '{', 1, fields); err != nil {
		return false
	}
	_, err = d.Token()
	return errors.Is(err, io.EOF)
}
func scanContainer(d *json.Decoder, kind json.Delim, depth int, fields []string) error {
	if depth > 64 {
		return errors.New("JSON nesting limit")
	}
	seen := map[string]struct{}{}
	for d.More() {
		if kind == '{' {
			token, err := d.Token()
			if err != nil {
				return err
			}
			key, ok := token.(string)
			if !ok {
				return errors.New("invalid object key")
			}
			if _, ok := seen[key]; ok {
				return errors.New("duplicate object key")
			}
			if depth == 1 {
				allowed := false
				for _, field := range fields {
					if key == field {
						allowed = true
						break
					}
				}
				if !allowed {
					return errors.New("unknown object key")
				}
			}
			seen[key] = struct{}{}
		}
		token, err := d.Token()
		if err != nil {
			return err
		}
		if delim, ok := token.(json.Delim); ok {
			if delim != '{' && delim != '[' {
				return errors.New("invalid value")
			}
			if err := scanContainer(d, delim, depth+1, nil); err != nil {
				return err
			}
		}
	}
	close, err := d.Token()
	if err != nil {
		return err
	}
	expected := json.Delim('}')
	if kind == '[' {
		expected = ']'
	}
	if close != expected {
		return errors.New("invalid closing delimiter")
	}
	return nil
}

// RequireEmptyBody uses the same byte budget for DELETE, without JSON/media
// requirements. Nonempty whitespace is still a body and is rejected.
func RequireEmptyBody(w http.ResponseWriter, r *http.Request, limit int64) bool {
	if r.Body == nil {
		r.Body = http.NoBody
	}
	data, ok := readBody(w, r, limit)
	if !ok {
		return false
	}
	if len(data) != 0 {
		response.JSON(w, http.StatusBadRequest, nil, "invalid request body")
		return false
	}
	return true
}
