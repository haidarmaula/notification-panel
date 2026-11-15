package dashboard

import (
	"net/http"
	"regexp"
	"strings"
)

var onboardingPath = regexp.MustCompile(`^/admin/apps/([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})/onboarding(?:/(.*))?$`)
var onboardingOperationID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// onboardingRoute admits only implemented setup endpoints. Lifecycle and release
// commands remain unavailable until their executors and authority are installed.
func onboardingRoute(method, path string) bool {
	match := onboardingPath.FindStringSubmatch(path)
	if match == nil || strings.HasSuffix(path, "/onboarding/") {
		return false
	}
	switch match[2] {
	case "", "enrollment-profiles", "approvals":
		return method == http.MethodGet
	case "enrollment", "fcm":
		return method == http.MethodPut
	case "fcm/verify", "dispatch/hold":
		return method == http.MethodPost
	default:
		id, ok := strings.CutPrefix(match[2], "operations/")
		return ok && method == http.MethodGet && onboardingOperationID.MatchString(id)
	}
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
