// Package middleware provides the composable HTTP middleware chain shared by
// every endpoint of the Notification Panel API.
//
// # Composition model
//
// Middleware is expressed as a function that takes an http.HandlerFunc and
// returns an http.HandlerFunc:
//
//	mw := middleware.NewAPIKeyMiddleware(cfg.APIKey)
//	mux.HandleFunc("GET /users", mw.Use(handler.List))
//
// This differs from the more common func(http.Handler) http.Handler shape
// used by most third-party middleware libraries. The choice here is
// deliberate: every handler in this codebase is registered with ServeMux's
// pattern syntax and is a plain http.HandlerFunc, so wrapping at the
// HandlerFunc level removes the need for the http.HandlerFunc(h).ServeHTTP
// boilerplate that the alternative form would force on every call site.
//
// # Chaining
//
// Because the returned value is itself an http.HandlerFunc, wrappers compose
// by nesting, with the outermost applied first:
//
//	use := func(h http.HandlerFunc) http.HandlerFunc {
//	    return apiKeyMW.Use(jwtMW.Use(superAdminMW.Use(h)))
//	}
//
// On each request the chain runs top-down — API key validation, then JWT
// parsing, then role check — and each stage is expected to short-circuit on
// failure by writing a response and returning without calling next.
//
// # Context values
//
// Middleware communicates with downstream handlers exclusively through
// request context values. Two key types are used to prevent accidental
// collisions between unrelated namespaces (see contextKey and
// auditContextKey). Handlers should read context values via the helper
// functions exposed by the packages that own them (for example,
// audit.GetStaffID) rather than by direct map access.
//
// # Ordering
//
// The API key middleware is expected to run before JWT validation, on the
// assumption that the API key is a coarse, cheap, shared gate that keeps
// unauthenticated traffic from reaching the more expensive token-parsing
// step. Reversing this order is not incorrect but changes the failure
// profile under load.
package middleware

import (
	"net/http"

	"hello/pkg/response"
)

// APIKeyMiddleware validates the shared X-API-Key header that every API
// request must carry.
//
// The API key is a coarse-grained, shared secret held by the admin web
// portal (and any other first-party client) rather than by individual users.
// It is not a substitute for JWT authentication: JWT establishes which staff
// user is acting, while the API key establishes that the caller is a
// recognised first-party client at all. Both are expected to be present on
// every endpoint except login and refresh.
//
// The middleware is stateless after construction and safe for concurrent
// use across goroutines. It performs no logging, no rate limiting, and no
// key rotation; those concerns, if they are added later, belong in
// dedicated middleware rather than here.
type APIKeyMiddleware struct {
	apiKey string
}

// NewAPIKeyMiddleware constructs an APIKeyMiddleware that will accept exactly
// the supplied apiKey value.
//
// apiKey is typically sourced from cfg.APIKey, which the config loader
// guarantees is non-empty. An empty apiKey is not rejected here — the
// middleware will still be constructed — but the Use wrapper treats an
// empty configured key as a failure case and rejects every request, since
// it can never match a non-empty header and an empty header is rejected
// outright. This is deliberate: a misconfigured API key fails closed rather
// than open.
func NewAPIKeyMiddleware(apiKey string) *APIKeyMiddleware {
	return &APIKeyMiddleware{
		apiKey: apiKey,
	}
}

// Use wraps next with API key validation.
//
// On every request the middleware reads the X-API-Key header and compares it
// against the configured key. If the header is missing, empty, or does not
// match, the request is rejected with a 401 response and next is not
// invoked. On success, the request is passed through unchanged — no context
// value is set, because downstream code does not need to know which key was
// used (there is only one).
//
// # Comparison
//
// The comparison is a plain string inequality, not a constant-time
// comparison. This is a known limitation: an attacker able to issue a large
// number of requests and measure response latency could in principle recover
// the key byte-by-byte through a timing side channel. In practice, network
// jitter makes this attack difficult against a remote endpoint, and the key
// is only one of several required credentials, but callers deploying this
// service on a low-latency network should consider switching to
// crypto/subtle.ConstantTimeCompare. This is listed as a known gap in the
// project README.
//
// # Failure response
//
// Rejections are returned as a JSON envelope with status 401 and the message
// "invalid api key". The same response is used for a missing header and for
// a mismatched value, so an attacker cannot distinguish "no key supplied"
// from "wrong key supplied" — this is intentional.
//
// # Concurrency
//
// The returned handler is safe for concurrent use. The middleware reads only
// its own immutable configuration and the per-request header map, both of
// which are concurrency-safe for reads.
func (m *APIKeyMiddleware) Use(next http.HandlerFunc) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providedAPIKey := r.Header.Get("X-API-Key")

		if providedAPIKey == "" || providedAPIKey != m.apiKey {
			response.JSON(w, http.StatusUnauthorized, nil, "invalid api key")
			return
		}

		next.ServeHTTP(w, r)
	})
}
