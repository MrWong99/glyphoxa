// Package health provides HTTP health and readiness check handlers.
//
// The package exposes two endpoints:
//
//   - /healthz — liveness probe; always returns 200 OK.
//   - /readyz  — readiness probe; returns 200 only when all registered
//     [Checker] functions pass.
//
// Responses are JSON objects with a top-level "status" field ("ok" or "fail")
// and a "checks" map containing the result of each named checker.
package health

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// checkTimeout is the maximum time a single readiness check may take before
// the context is cancelled.
const checkTimeout = 5 * time.Second

// Checker is a named health check function. The Check function should return
// nil when the dependency is healthy and a non-nil error describing the
// failure otherwise.
type Checker struct {
	// Name is a short, human-readable label for this check (e.g. "database",
	// "providers"). It appears as a key in the JSON response.
	Name string

	// Check probes the dependency. It must respect context cancellation.
	Check func(ctx context.Context) error
}

// result is the JSON response body for health endpoints.
type result struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks,omitempty"`
}

// Handler serves /healthz and /readyz endpoints. It is safe for concurrent
// use; the checker list is fixed at construction time.
type Handler struct {
	checkers []Checker
}

// New creates a [Handler] that evaluates the given checkers on each /readyz
// request. The checkers are evaluated sequentially in the order provided.
func New(checkers ...Checker) *Handler {
	c := make([]Checker, len(checkers))
	copy(c, checkers)
	return &Handler{checkers: c}
}

// Healthz is a liveness probe that always returns 200 OK. A running process
// that can serve HTTP is considered alive.
func (h *Handler) Healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, result{Status: "ok"})
}

// Readyz is a readiness probe that returns 200 only when every registered
// [Checker] passes. Each checker is given a context with a [checkTimeout]
// deadline derived from the request context.
func (h *Handler) Readyz(w http.ResponseWriter, r *http.Request) {
	checks := make(map[string]string, len(h.checkers))
	allOK := true

	for _, c := range h.checkers {
		ctx, cancel := context.WithTimeout(r.Context(), checkTimeout)
		err := c.Check(ctx)
		cancel()

		if err != nil {
			checks[c.Name] = "fail: " + err.Error()
			allOK = false
		} else {
			checks[c.Name] = "ok"
		}
	}

	res := result{
		Status: "ok",
		Checks: checks,
	}
	status := http.StatusOK
	if !allOK {
		res.Status = "fail"
		status = http.StatusServiceUnavailable
	}

	writeJSON(w, status, res)
}

// Register adds the /healthz and /readyz routes to mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", h.Healthz)
	mux.HandleFunc("GET /readyz", h.Readyz)
}

// writeJSON encodes v as JSON and writes it with the given status code. On
// encoding failure it falls back to a plain-text 500 response.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		http.Error(w, `{"status":"error"}`, http.StatusInternalServerError)
	}
}
