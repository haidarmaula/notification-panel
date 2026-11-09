package applications

import (
	"strings"
	"testing"
)

func TestValidNotificationContentUsesUTF8ByteLimits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		title string
		body  string
		want  bool
	}{
		{name: "title exact byte limit", title: strings.Repeat("a", maxNotificationTitleBytes), body: "body", want: true},
		{name: "title over byte limit", title: strings.Repeat("a", maxNotificationTitleBytes+1), body: "body"},
		{name: "multibyte title exact byte limit", title: strings.Repeat("界", maxNotificationTitleBytes/3), body: "body", want: true},
		{name: "multibyte title over byte limit", title: strings.Repeat("界", maxNotificationTitleBytes/3+1), body: "body"},
		{name: "body exact byte limit", title: "title", body: strings.Repeat("a", maxNotificationBodyBytes), want: true},
		{name: "body over byte limit", title: "title", body: strings.Repeat("a", maxNotificationBodyBytes+1)},
		{name: "multibyte body exact byte limit", title: "title", body: strings.Repeat("界", maxNotificationBodyBytes/3) + "a", want: true},
		{name: "multibyte body over byte limit", title: "title", body: strings.Repeat("界", maxNotificationBodyBytes/3) + "aa"},
		{name: "blank title", title: " \t", body: "body"},
		{name: "blank body", title: "title", body: " \t"},
		{name: "nul title", title: "ti\x00tle", body: "body"},
		{name: "nul body", title: "title", body: "bo\x00dy"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := validNotificationContent(tc.title, tc.body); got != tc.want {
				t.Fatalf("validNotificationContent() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCampaignDraftInputUsesNotificationContentBounds(t *testing.T) {
	t.Parallel()

	input := campaignDraftInput{
		Title:   strings.Repeat("界", maxNotificationTitleBytes/3),
		Body:    strings.Repeat("界", maxNotificationBodyBytes/3) + "a",
		Targets: []campaignTargetInput{{Type: "BROADCAST"}},
	}
	if _, _, ok := input.validate(); !ok {
		t.Fatal("campaign at exact UTF-8 byte limits was rejected")
	}

	input.Title += "界"
	if _, _, ok := input.validate(); ok {
		t.Fatal("campaign title over UTF-8 byte limit was accepted")
	}
}
