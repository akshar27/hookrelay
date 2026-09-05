package api

import (
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	"github.com/akshar27/hookrelay/internal/obs"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// requestLogger attaches a request-scoped slog logger (with the chi request id)
// to the context and logs one line per request on completion.
func (s *Server) requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		reqID := middleware.GetReqID(r.Context())
		log := s.log.With("request_id", reqID, "method", r.Method, "path", r.URL.Path)
		ctx := obs.WithLogger(r.Context(), log)

		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r.WithContext(ctx))

		route := routePattern(r)
		dur := time.Since(start)
		s.metrics.HTTPRequests.WithLabelValues(route, r.Method, statusClass(ww.Status())).Inc()
		s.metrics.HTTPLatency.WithLabelValues(route, r.Method).Observe(dur.Seconds())
		log.Info("request", "status", ww.Status(), "bytes", ww.BytesWritten(), "duration_ms", dur.Milliseconds())
	})
}

// requireAdmin gates a route on the static admin bearer token.
func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := bearer(r)
		if !ok || subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.AdminToken)) != 1 {
			writeErr(w, errUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func bearer(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(strings.ToLower(h), "bearer ") {
		return "", false
	}
	v := strings.TrimSpace(h[len("Bearer "):])
	return v, v != ""
}

func routePattern(r *http.Request) string {
	if rc := chi.RouteContext(r.Context()); rc != nil && rc.RoutePattern() != "" {
		return rc.RoutePattern()
	}
	return "unknown"
}

func statusClass(code int) string {
	switch {
	case code >= 500:
		return "5xx"
	case code >= 400:
		return "4xx"
	case code >= 300:
		return "3xx"
	case code >= 200:
		return "2xx"
	default:
		return "1xx"
	}
}
