package middleware

import (
	"net/http"
	"strings"
)

var corsMethods = map[string]struct{}{
	"DELETE":  {},
	"GET":     {},
	"HEAD":    {},
	"PATCH":   {},
	"POST":    {},
	"PUT":     {},
	"OPTIONS": {},
}

var corsHeaders = map[string]struct{}{
	"authorization":   {},
	"content-type":    {},
	"if-match":        {},
	"if-none-match":   {},
	"idempotency-key": {},
	"x-api-key":       {},
}

// NewCORSMiddleware allows browser requests from the configured exact origins.
// It does not authenticate requests; API middleware remains responsible for
// authenticating every request, including requests that do not use a browser.
func NewCORSMiddleware(origins []string) Middleware {
	allowedOrigins := make(map[string]struct{}, len(origins))
	for _, origin := range origins {
		allowedOrigins[origin] = struct{}{}
	}

	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin == "" {
				next(w, r)
				return
			}
			if _, ok := allowedOrigins[origin]; !ok {
				if isCORSPreflight(r) {
					http.Error(w, "origin is not allowed", http.StatusForbidden)
					return
				}
				next(w, r)
				return
			}

			w.Header().Add("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Origin", origin)
			if isCORSPreflight(r) {
				w.Header().Add("Vary", "Access-Control-Request-Method")
				w.Header().Add("Vary", "Access-Control-Request-Headers")
				if !allowedCORSMethod(r.Header.Get("Access-Control-Request-Method")) ||
					!allowedCORSHeaders(r.Header.Get("Access-Control-Request-Headers")) {
					http.Error(w, "preflight request is not allowed", http.StatusForbidden)
					return
				}
				w.Header().Set("Access-Control-Allow-Methods", "DELETE, GET, HEAD, OPTIONS, PATCH, POST, PUT")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, If-Match, If-None-Match, Idempotency-Key, X-API-Key")
				w.Header().Set("Access-Control-Max-Age", "600")
				w.WriteHeader(http.StatusNoContent)
				return
			}

			w.Header().Set("Access-Control-Expose-Headers", "ETag")
			next(w, r)
		}
	}
}

func isCORSPreflight(r *http.Request) bool {
	return r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != ""
}

func allowedCORSMethod(method string) bool {
	_, ok := corsMethods[strings.ToUpper(strings.TrimSpace(method))]
	return ok
}

func allowedCORSHeaders(headers string) bool {
	for _, header := range strings.Split(headers, ",") {
		if strings.TrimSpace(header) == "" {
			continue
		}
		if _, ok := corsHeaders[strings.ToLower(strings.TrimSpace(header))]; !ok {
			return false
		}
	}
	return true
}
