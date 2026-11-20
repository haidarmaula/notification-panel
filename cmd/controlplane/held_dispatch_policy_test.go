package main

import (
	"strings"
	"testing"
)

func TestHeldDispatchPolicyRequiresProfileAndExplicitTTLs(t *testing.T) {
	data := `{"database_url":"postgres://fixture@db","application_id":"10000000-0000-4000-8000-000000000001",
	"environment":"staging","profile_revision":2,"push_ttl_seconds":120,"key_ttl_seconds":300}`
	got, err := parseHeldDispatchPolicy([]byte(data))
	if err != nil || !got.applicationID.Valid || got.profileRevision != 2 || got.pushTTL != 120 || got.keyTTL != 300 {
		t.Fatalf("valid held policy rejected: %v", err)
	}
	for name, invalid := range map[string]string{
		"zero TTL":        strings.Replace(data, ":120", ":0", 1),
		"excessive TTL":   strings.Replace(data, ":300", ":604801", 1),
		"missing TTL":     strings.Replace(data, `,"key_ttl_seconds":300`, "", 1),
		"dispatch mode":   strings.Replace(data, ":300}", `:300,"dispatch_mode":"FULL"}`, 1),
		"unknown profile": strings.Replace(data, ":2,", ":0,", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseHeldDispatchPolicy([]byte(invalid)); err == nil {
				t.Fatal("invalid held policy was accepted")
			}
		})
	}
}
