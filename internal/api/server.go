// Package api holds the HTTP server: routing, middleware, and handlers.
package api

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/akshar27/hookrelay/internal/breaker"
	"github.com/akshar27/hookrelay/internal/config"
	"github.com/akshar27/hookrelay/internal/dashboard"
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
	cfg       config.Config
	store     *store.Store
	log       *slog.Logger
	metrics   *obs.Metrics
	secrets   *secretbox.Box
	notifier  EventNotifier
	breakers  *breaker.Registry
	dashboard http.Handler
	reg       *prometheus.Registry
	handler   http.Handler
}

// Deps are the collaborators a Server needs. notifier and breakers may be nil.
type Deps struct {
	Store    *store.Store
	Secrets  *secretbox.Box
	Breakers *breaker.Registry
	Notifier EventNotifier
	Metrics  *obs.Metrics
	Registry *prometheus.Registry
	Log      *slog.Logger
}

// New builds a Server and its route tree.
func New(cfg config.Config, d Deps) (*Server, error) {
	reg := d.Registry
	if reg == nil {
		reg = prometheus.NewRegistry()
	}
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	metrics := d.Metrics
	if metrics == nil {
		metrics = obs.NewMetrics(reg)
	}

	s := &Server{
		cfg:      cfg,
		store:    d.Store,
		log:      d.Log,
		metrics:  metrics,
		secrets:  d.Secrets,
		notifier: d.Notifier,
		breakers: d.Breakers,
		reg:      reg,
	}
	if cfg.AdminToken != "" && d.Store != nil {
		s.dashboard = dashboard.New(d.Store, d.Breakers, d.Secrets, cfg.AdminToken).Router()
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

	// Anything not an API/ops route falls through to the operator dashboard
	// (its own router handles "/", "/deliveries", "/assets/*", … and its 404).
	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		if s.dashboard != nil && !strings.HasPrefix(req.URL.Path, "/v1/") {
			s.dashboard.ServeHTTP(w, req)
			return
		}
		writeErr(w, errNotFound)
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		writeErr(w, errf(http.StatusMethodNotAllowed, "hookrelay_method_not_allowed", "method not allowed"))
	})
	return r
}
