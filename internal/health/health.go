// Package health serves liveness and readiness endpoints.
package health

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Check reports whether one dependency is usable.
type Check struct {
	Name string
	Fn   func(ctx context.Context) error
}

// Handler serves /healthz (the process is up) and /readyz (every check
// passes).
func Handler(checks ...Check) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		var failed []string
		for _, c := range checks {
			if err := c.Fn(ctx); err != nil {
				failed = append(failed, c.Name+": "+err.Error())
			}
		}
		if len(failed) > 0 {
			http.Error(w, strings.Join(failed, "\n"), http.StatusServiceUnavailable)
			return
		}
		_, _ = fmt.Fprintln(w, "ok")
	})
	return mux
}
