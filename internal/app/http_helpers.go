package app

import (
	"fmt"
	"net/http"
	"strings"
)

// methodNotAllowed writes a consistent 405 response with the route-specific Allow header.
func methodNotAllowed(w http.ResponseWriter, allowedMethods ...string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if len(allowedMethods) > 0 {
		w.Header().Set("Allow", strings.Join(allowedMethods, ", "))
	}
	w.WriteHeader(http.StatusMethodNotAllowed)
	fmt.Fprintln(w, "method not allowed")
}

func flashFromQuery(r *http.Request) string {
	return strings.TrimSpace(r.URL.Query().Get("flash"))
}
