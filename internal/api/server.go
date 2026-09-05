// Package api holds the HTTP server: routing, middleware, and handlers.
package api

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/akshar27/hookrelay/internal/breaker"
	"github.com/akshar27/hookrelay/internal/config"
	"github.com/akshar27/hookrelay/internal/obs"
	"github.com/akshar27/hookrelay/internal/secretbox"
	"github.com/akshar27/hookrelay/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Server wires configuration, storage, and observability into an http.Handler.
type Server struct {
	cfg      config.Config
	store    *store.Store
	log      *slog.Logger
	metrics  *obs.Metrics
	secrets  *secretbox.Box
	notifier EventNotifier
	breakers *breaker.Registry
	reg      *prometheus.Registry
	handler  http.Handler
}

// New builds a Server and its route tree. notifier and breakers may be nil.
func New(cfg config.Config, st *store.Store, box *secretbox.Box, breakers *breaker.Registry, log *slog.Logger, notifier EventNotifier) (*Server, error) {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	s := &Server{
		cfg:      cfg,
		store:    st,
		log:      log,
		metrics:  obs.NewMetrics(reg),
		secrets:  box,
		notifier: notifier,
		breakers: breakers,
		reg:      reg,
	}
	s.handler = s.routes()
	return s, nil
}

func (s *Server) Handler() http.Handler { return s.handler }

func (s *Server) routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(s.requestLogger)
	r.Use(middleware.Timeout(30 * time.Second))

	// Open ops endpoints.
	r.Get("/healthz", s.handleHealthz)
	r.Get("/readyz", s.handleReadyz)
	r.Handle("/metrics", promhttp.HandlerFor(s.reg, promhttp.HandlerOpts{Registry: s.reg}))

	r.Route("/v1", func(r chi.Router) {
		r.Get("/ping", s.handlePing) // open smoke endpoint

		// Ingest: API-key auth.
		r.With(s.requireAPIKey).Post("/events", s.handleIngest)

		// Admin surface: static bearer token.
		r.Group(func(r chi.Router) {
			r.Use(s.requireAdmin)
			r.Route("/endpoints", s.routeEndpoints)
			r.Route("/api-keys", s.routeAPIKeys)
			r.Route("/deliveries", s.routeDeliveries)

			// events: the collection GET and item routes are admin; the
			// collection POST (ingest, above) is API-key. Registered flat so
			// the two POST /events handlers don't collide via a Mount.
			r.Get("/events", s.handleListEvents)
			r.Get("/events/{id}", s.handleGetEvent)
			r.Post("/events/{id}/replay", s.handleReplayEvent)
		})
	})

	r.NotFound(func(w http.ResponseWriter, _ *http.Request) { writeErr(w, errNotFound) })
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		writeErr(w, errf(http.StatusMethodNotAllowed, "hookrelay_method_not_allowed", "method not allowed"))
	})
	return r
}
