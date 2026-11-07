package jsonobject

import "testing"

func TestUniquePolicyFields(t *testing.T) {
	for _, raw := range []string{`{"projects":[{"receipts":{},"receipts":{}}]}`, `{"projects":[{"Receipts":{}}]}`, `{} {}`, `{"projects":`} {
		if UniqueLowercaseFields([]byte(raw)) {
			t.Fatalf("accepted %q", raw)
		}
	}
	if !UniqueLowercaseFields([]byte(`{"projects":[{"key":"UPPERCASE value"},{"key":"second"}]}`)) {
		t.Fatal("valid fields rejected")
	}
}
