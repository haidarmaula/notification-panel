package httprequest

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStrictJSONObject(t *testing.T) {
	for _, tc := range []struct {
		name, body, media, encoding string
		status                      int
	}{
		{"valid", `{"name":"hello"}`, "application/json", "", 200},
		{"charset", `{"name":"hello"}`, "application/json; charset=UTF-8", "identity", 200},
		{"missing media", `{}`, "", "", 415},
		{"wrong media", `{}`, "text/plain", "", 415},
		{"other charset", `{}`, "application/json; charset=ascii", "", 415},
		{"other parameter", `{}`, "application/json; profile=x", "", 415},
		{"compressed", `{}`, "application/json", "gzip", 415},
		{"null", `null`, "application/json", "", 400},
		{"array", `[]`, "application/json", "", 400},
		{"two values", `{} {}`, "application/json", "", 400},
		{"duplicate", `{"name":"a","name":"b"}`, "application/json", "", 400},
		{"escaped duplicate", `{"name":"a","na\u006de":"b"}`, "application/json", "", 400},
		{"case alias", `{"Name":"a"}`, "application/json", "", 400},
		{"case collision", `{"name":"a","Name":"b"}`, "application/json", "", 400},
		{"unknown", `{"status":"ACTIVE"}`, "application/json", "", 400},
		{"bad utf8", "{\"name\":\"\xff\"}", "application/json", "", 400},
		{"bad type", `{"name":12}`, "application/json", "", 400},
		{"malformed", `{"name":`, "application/json", "", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/", strings.NewReader(tc.body))
			if tc.media != "" {
				r.Header.Set("Content-Type", tc.media)
			}
			if tc.encoding != "" {
				r.Header.Set("Content-Encoding", tc.encoding)
			}
			w := httptest.NewRecorder()
			var dst struct {
				Name *string `json:"name"`
			}
			ok := DecodeStrictJSONObject(w, r, &dst, DefaultJSONLimit, "name")
			if ok != (tc.status == 200) || w.Code != tc.status {
				t.Fatalf("ok=%v status=%d body=%s", ok, w.Code, w.Body.String())
			}
		})
	}
	for _, header := range []string{"Content-Type", "Content-Encoding"} {
		r := httptest.NewRequest("POST", "/", strings.NewReader(`{}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Add(header, "identity")
		r.Header.Add(header, "identity")
		w := httptest.NewRecorder()
		var dst struct{}
		if DecodeStrictJSONObject(w, r, &dst, 64) || w.Code != 415 {
			t.Fatalf("accepted duplicate %s", header)
		}
	}
}

func TestStrictBodyBudget(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		for _, extra := range []int{0, 1} {
			body := `{"name":"ok"}` + strings.Repeat(" ", int(DefaultJSONLimit)-len(`{"name":"ok"}`)+extra)
			r := httptest.NewRequest("POST", "/", strings.NewReader(body))
			if unknown {
				r.ContentLength = -1
			}
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			var dst struct {
				Name string `json:"name"`
			}
			ok := DecodeStrictJSONObject(w, r, &dst, DefaultJSONLimit, "name")
			if extra == 0 {
				if !ok {
					t.Fatalf("exact budget rejected: %s", w.Body)
				}
			} else if ok || w.Code != 413 {
				t.Fatalf("oversize accepted: unknown=%v status=%d", unknown, w.Code)
			}
		}
	}
}

func TestStrictNestedDuplicateAndDepth(t *testing.T) {
	for _, body := range []string{`{"name":{"a":1,"a":2}}`, `{"name":[{"a":1,"\u0061":2}]}`, `{"name":` + strings.Repeat("[", 65) + `0` + strings.Repeat("]", 65) + `}`} {
		if uniqueObject([]byte(body), []string{"name"}) {
			t.Fatal("accepted duplicate/deep object")
		}
	}
	if !uniqueObject([]byte(`{"name":[{"a":1},{"a":2}]}`), []string{"name"}) {
		t.Fatal("separate object keys incorrectly rejected")
	}
}

func TestRequireEmptyBody(t *testing.T) {
	for _, tc := range []struct {
		body string
		want int
	}{{"", 200}, {" ", 400}, {"{}", 400}, {strings.Repeat(" ", 65), 413}} {
		r := httptest.NewRequest("DELETE", "/", strings.NewReader(tc.body))
		r.ContentLength = -1
		w := httptest.NewRecorder()
		ok := RequireEmptyBody(w, r, 64)
		if ok != (tc.want == 200) || w.Code != tc.want {
			t.Fatalf("status=%d want=%d", w.Code, tc.want)
		}
	}
	r := httptest.NewRequest("DELETE", "/", nil)
	r.Body = nil
	if !RequireEmptyBody(httptest.NewRecorder(), r, 64) {
		t.Fatal("nil body rejected")
	}
}
