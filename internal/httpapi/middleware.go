package httpapi

import (
	"log/slog"
	"net/http"
)

// handlerFunc is like http.HandlerFunc but can return an error, which adapt
// turns into a logged 500 response - this keeps individual handlers from
// needing to repeat error-response boilerplate for upstream/programming
// errors, while still writing their own responses for expected outcomes
// (200s, 400s, redirects).
type handlerFunc func(w http.ResponseWriter, r *http.Request) error

// adapt wraps a handlerFunc into a standard http.HandlerFunc, recovering
// panics and turning returned errors into a 500 response, both logged with
// structured fields.
func adapt(h handlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("panic handling request", "panic", rec, "path", r.URL.Path)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		}()

		if err := h(w, r); err != nil {
			slog.Error("error handling request", "error", err, "path", r.URL.Path)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
	}
}
