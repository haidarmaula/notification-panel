package httprequest

import (
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type observedBody struct {
	io.Reader
	read   int
	closed bool
}

func (b *observedBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	b.read += n
	return n, err
}
func (b *observedBody) Close() error { b.closed = true; return nil }

type failedReader struct{}

func (failedReader) Read([]byte) (int, error) { return 0, errors.New("private body credential detail") }

func TestDecodeJSONWithMediaTypeAcceptsUTF8JSON(t *testing.T) {
	for _, mediaType := range []string{"application/json", "application/json; charset=utf-8"} {
		t.Run(mediaType, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/", strings.NewReader(`{"value":"accepted"}`))
			r.Header.Set("Content-Type", mediaType)
			w := httptest.NewRecorder()
			var dst struct{ Value string }
			require.True(t, DecodeJSONWithMediaType(w, r, &dst, 64))
			require.Equal(t, 200, w.Code)
			require.Equal(t, "accepted", dst.Value)
		})
	}
}

func TestJSONSizeBoundaries(t *testing.T) {
	const limit int64 = 64
	prefix := `{"value":"é","unknown":true}`
	for _, tc := range []struct {
		name   string
		size   int64
		length int64
		want   int
	}{
		{"exact known", limit, limit, 200}, {"exact unknown", limit, -1, 200},
		{"over known", limit + 1, limit + 1, 413}, {"over unknown", limit + 1, -1, 413},
		{"inaccurate smaller length", limit + 1, 1, 413},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := prefix + strings.Repeat(" ", int(tc.size)-len(prefix))
			body := &observedBody{Reader: strings.NewReader(input)}
			r := httptest.NewRequest("POST", "/", nil)
			r.Body = body
			r.ContentLength = tc.length
			if tc.length == -1 {
				r.TransferEncoding = []string{"chunked"}
			}
			w := httptest.NewRecorder()
			var dst struct{ Value string }
			ok := DecodeJSON(w, r, &dst, limit)
			require.Equal(t, tc.want == 200, ok)
			require.Equal(t, tc.want, w.Code)
			require.True(t, body.closed)
			require.LessOrEqual(t, body.read, int(limit+1))
			if ok {
				require.Equal(t, "é", dst.Value)
			} else {
				require.Empty(t, dst.Value, "oversized input must not be decoded")
				require.JSONEq(t, `{"success":false,"message":"request body too large"}`, w.Body.String())
				if tc.length > limit {
					require.Zero(t, body.read, "known large body should reject before reading")
				}
			}
		})
	}
}

func TestJSONInvalidAndCompatibleInputs(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		want        int
	}{
		{"empty", "", 400}, {"whitespace", "   ", 400}, {"malformed", `{"value":`, 400},
		{"second value", `{"value":"first"}{"value":"second"}`, 400},
		{"trailing garbage", `{"value":"first"}private`, 400},
		{"unknown field", `{"value":"first","extra":true}`, 200},
		{"duplicate key", `{"value":"first","value":"last"}`, 200},
		{"null", `null`, 200}, {"oversized malformed", strings.Repeat("x", 65), 413},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/", strings.NewReader(tc.input))
			r.ContentLength = -1
			w := httptest.NewRecorder()
			var dst struct{ Value string }
			require.Equal(t, tc.want == 200, DecodeJSON(w, r, &dst, 64))
			require.Equal(t, tc.want, w.Code)
			if tc.want == 400 {
				require.JSONEq(t, `{"success":false,"message":"invalid request body"}`, w.Body.String())
			}
			if tc.name == "duplicate key" {
				require.Equal(t, "last", dst.Value)
			}
		})
	}
	r := httptest.NewRequest("POST", "/", nil)
	r.Body = &observedBody{Reader: failedReader{}}
	r.ContentLength = -1
	w := httptest.NewRecorder()
	var dst any
	require.False(t, DecodeJSON(w, r, &dst, 64))
	require.Equal(t, 400, w.Code)
	require.NotContains(t, w.Body.String(), "private")
	r.Body = nil
	w = httptest.NewRecorder()
	require.False(t, DecodeJSON(w, r, &dst, 64))
	require.Equal(t, 400, w.Code)
	require.Panics(t, func() { DecodeJSON(w, r, &dst, 0) })
}
