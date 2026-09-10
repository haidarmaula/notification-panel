package middleware

import "net/http"

// Middleware is the canonical shape of every middleware function in this
// package.
//
// A Middleware takes the next handler in the chain and returns a new handler
// that will eventually call it. Because the returned value is itself an
// http.HandlerFunc, middlewares compose by nesting, and a chain of any
// length remains a single http.HandlerFunc that can be registered directly
// on a ServeMux pattern:
//
//	mux.HandleFunc("GET /users", chain(handler.List))
//
// # Relationship to net/http conventions
//
// The http.HandlerFunc-in / http.HandlerFunc-out shape is a deliberate
// deviation from the more common func(http.Handler) http.Handler form used
// by most third-party middleware libraries. The reason is that every handler
// in this codebase is registered with ServeMux pattern syntax and is a plain
// http.HandlerFunc, so keeping the chain in the HandlerFunc domain avoids
// forcing an http.HandlerFunc(h).ServeHTTP adapter at every call site.
// Logging is the single exception, because it wraps the mux as a whole
// rather than an individual route — see its documentation for why.
//
// # Context
//
// Middlewares communicate with downstream handlers through request context
// values, not through shared state. The keys are declared as private types
// (contextKey, auditContextKey) so that unrelated middlewares cannot collide.
// See those types for the mechanism.
type Middleware func(http.HandlerFunc) http.HandlerFunc

// Chain composes a sequence of middlewares into a single Middleware.
//
// # Ordering
//
// The first middleware in the variadic argument list is the outermost
// wrapper and runs first on each request. Concretely:
//
//	Chain(A, B, C)(handler)
//
// produces a handler equivalent to A(B(C(handler))), and a request will be
// processed in the order A → B → C → handler, with each stage able to
// short-circuit by writing a response and returning without calling next.
// This is the reading order of the argument list, even though the
// implementation iterates backwards to achieve it — the reverse loop exists
// because each iteration wraps the accumulated chain, so starting from the
// rightmost and working leftwards leaves the leftmost as the outer wrapper.
//
// # Empty and single-element chains
//
// Chain() with no arguments returns a Middleware that, when applied,
// returns its input unchanged — a no-op pass-through. This is useful for
// building chains conditionally without branching at the call site. Chain(A)
// is equivalent to A.
//
// # Reuse
//
// The Middleware returned by Chain is itself stateless: it captures only the
// slice of middlewares and can be applied to any number of handlers, reused
// across requests, and stored as a long-lived value in a module. Applying it
// twice to the same handler produces two independent, equivalent chains.
//
// # Concurrency
//
// Chain is safe for concurrent use. It does not mutate its inputs, and the
// handler it returns does not share mutable state across requests.
func Chain(middlewares ...Middleware) Middleware {
	return func(next http.HandlerFunc) http.HandlerFunc {
		for i := len(middlewares) - 1; i >= 0; i-- {
			next = middlewares[i](next)
		}
		return next
	}
}
