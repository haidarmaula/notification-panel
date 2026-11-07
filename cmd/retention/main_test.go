package main

import (
	"strings"
	"testing"
)

func TestRetentionCommandScope(t *testing.T) {
	for _, args := range [][]string{{}, {"apply"}, {"apply", "0"}, {"apply", "1001"}, {"apply", "+1"}, {"delete", "1"}, {"preview", "1", "extra"}} {
		if _, _, ok := parseStep(args); ok {
			t.Fatal("invalid command accepted")
		}
	}
	step, apply, ok := parseStep([]string{"preview", "1"})
	if !ok || apply || step != 1 {
		t.Fatal("invalid preview")
	}
	const id = "00000000-0000-4000-8000-000000000001"
	config := `{"application_id":"ID","grant_id":"ID","origin_epoch":"ID","source_epoch":"ID","record_class":"origin_nonce","database_url":"postgres://retention:fixture@db:5432/hello?sslmode=verify-full"}`
	config = strings.ReplaceAll(config, "ID", id)
	cfg, b, err := policy([]byte(config))
	if err != nil || cfg.MaxConns != 1 || b.Apply || b.RecordClass != "origin_nonce" {
		t.Fatal("valid bounded role policy rejected")
	}
	for _, invalid := range []string{strings.Replace(config, "origin_nonce", "audit", 1), strings.Replace(config, "verify-full", "disable", 1),
		strings.Replace(config, id, "00000000-0000-0000-0000-000000000000", 1), strings.Replace(config, "{", `{"force":true,`, 1)} {
		if _, _, err := policy([]byte(invalid)); err == nil {
			t.Fatal("unsafe policy accepted")
		}
	}
}
