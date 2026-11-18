package main

import (
	"strings"
	"testing"
)

const validPreparationSettings = `{"database_url":"postgres://executor@db/hello?sslmode=verify-full","application_id":"11111111-1111-4111-8111-111111111111","configuration_id":"22222222-2222-4222-8222-222222222222","environment":"staging","generation":1,"reservation_id":"33333333-3333-4333-8333-333333333333","setup_revision":3,"origin_epoch":"44444444-4444-4444-8444-444444444444","source_epoch":"55555555-5555-4555-8555-555555555555"}`

func TestRuntimePreparationSettingsAreClosedAndScoped(t *testing.T) {
	if _, err := parseRuntimePreparationSettings([]byte(validPreparationSettings)); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"command injection field": strings.TrimSuffix(validPreparationSettings, "}") + `,"command":"sh"}`,
		"mount field":             strings.TrimSuffix(validPreparationSettings, "}") + `,"mount":"/"}`,
		"duplicate":               strings.Replace(validPreparationSettings, `"generation":1`, `"generation":1,"generation":2`, 1),
		"null scope":              strings.Replace(validPreparationSettings, `"environment":"staging"`, `"environment":null`, 1),
		"noncanonical":            strings.Replace(validPreparationSettings, "11111111-1111-4111-8111-111111111111", "11111111111141118111111111111111", 1),
		"zero epoch":              strings.Replace(validPreparationSettings, "55555555-5555-4555-8555-555555555555", "00000000-0000-0000-0000-000000000000", 1),
		"trailing":                validPreparationSettings + `{}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseRuntimePreparationSettings([]byte(body)); err == nil {
				t.Fatal("unapproved preparation input accepted")
			}
		})
	}
}
