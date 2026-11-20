package dashboard

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

var onboardingPath = regexp.MustCompile(`^/admin/apps/([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})/onboarding(?:/(.*))?$`)
var onboardingOperationID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// onboardingRoute admits the implemented setup and queued release endpoints.
func onboardingRoute(method, path string) bool {
	match := onboardingPath.FindStringSubmatch(path)
	if match == nil || strings.HasSuffix(path, "/onboarding/") {
		return false
	}
	switch match[2] {
	case "", "enrollment-profiles", "approvals", "test-installations":
		return method == http.MethodGet
	case "enrollment", "fcm", "test-cohort":
		return method == http.MethodPut
	case "fcm/verify", "dispatch/hold", "dispatch/release", "activate":
		return method == http.MethodPost
	default:
		id, ok := strings.CutPrefix(match[2], "operations/")
		id = strings.TrimSuffix(id, "/targets")
		return ok && method == http.MethodGet && onboardingOperationID.MatchString(id)
	}
}

func onboardingQueryAllowed(path, query string) bool {
	if query == "" {
		return true
	}
	if !strings.HasSuffix(path, "/onboarding/test-installations") {
		return false
	}
	values, err := url.ParseQuery(query)
	return err == nil && len(values) == 1 && len(values["after"]) == 1 && onboardingOperationID.MatchString(values.Get("after"))
}

// Only same-application operation locations are rewritten to the BFF namespace.
func onboardingOperationLocation(path, location string) string {
	match := onboardingPath.FindStringSubmatch(path)
	if match == nil {
		return ""
	}
	prefix := "/api/v1/admin/apps/" + match[1] + "/onboarding/operations/"
	id, ok := strings.CutPrefix(location, prefix)
	if !ok || !onboardingOperationID.MatchString(id) {
		return ""
	}
	return "/bff/api" + strings.TrimPrefix(location, "/api/v1")
}
