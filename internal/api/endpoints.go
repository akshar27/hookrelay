package api

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/akshar27/hookrelay/internal/filter"
	"github.com/akshar27/hookrelay/internal/ssrf"
	"github.com/akshar27/hookrelay/internal/store/db"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Server) routeEndpoints(r chi.Router) {
	r.Post("/", s.handleCreateEndpoint)
	r.Get("/", s.handleListEndpoints)
	r.Get("/{id}", s.handleGetEndpoint)
	r.Patch("/{id}", s.handleUpdateEndpoint)
	r.Delete("/{id}", s.handleDeleteEndpoint)
	r.Post("/{id}/rotate-secret", s.handleRotateSecret)
	r.Post("/{id}/reset-breaker", s.handleResetBreaker)
}

type endpointView struct {
	ID           uuid.UUID       `json:"id"`
	Name         string          `json:"name"`
	URL          string          `json:"url"`
	Filter       json.RawMessage `json:"filter"`
	Status       string          `json:"status"`
	RateLimitRPS int32           `json:"rate_limit_rps"`
	TimeoutMS    int32           `json:"timeout_ms"`
	MaxAttempts  int32           `json:"max_attempts"`
	BreakerState string          `json:"breaker_state"`
	AllowPrivate bool            `json:"allow_private"`
	CreatedAt    time.Time       `json:"created_at"`
	Secret       string          `json:"secret,omitempty"` // only on create / rotate
	Health       *endpointHealth `json:"health,omitempty"` // only on GET /{id}
}

func viewOf(e db.Endpoint) endpointView {
	return endpointView{
		ID: e.ID, Name: e.Name, URL: e.Url, Filter: e.Filter, Status: e.Status,
		RateLimitRPS: e.RateLimitRps, TimeoutMS: e.TimeoutMs, MaxAttempts: e.MaxAttempts,
		BreakerState: e.BreakerState, AllowPrivate: e.AllowPrivate, CreatedAt: e.CreatedAt,
	}
}

type createEndpointReq struct {
	Name             string          `json:"name"`
	URL              string          `json:"url"`
	Filter           json.RawMessage `json:"filter"`
	RateLimitRPS     *int32          `json:"rate_limit_rps"`
	TimeoutMS        *int32          `json:"timeout_ms"`
	MaxAttempts      *int32          `json:"max_attempts"`
	Max4xxAttempts   *int32          `json:"max_4xx_attempts"`
	BreakerThreshold *int32          `json:"breaker_threshold"`
	BreakerCooldownS *int32          `json:"breaker_cooldown_s"`
	AllowPrivate     bool            `json:"allow_private"`
}

func (s *Server) handleCreateEndpoint(w http.ResponseWriter, r *http.Request) {
	var req createEndpointReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		validationErr(w, "name is required")
		return
	}
	if _, err := ssrf.ValidateURL(req.URL, s.cfg.AllowInsecureEndpoints, req.AllowPrivate); err != nil {
		writeErr(w, errf(http.StatusUnprocessableEntity, "hookrelay_ssrf_blocked", err.Error()))
		return
	}
	f, err := filter.Parse(req.Filter)
	if err != nil {
		validationErr(w, err.Error())
		return
	}
	filterJSON, _ := json.Marshal(f)

	rawSecret := newSigningSecret()
	sealed, err := s.secrets.Seal([]byte(rawSecret))
	if err != nil {
		obsErr(w, r, err)
		return
	}

	row, err := s.store.Q.CreateEndpoint(r.Context(), db.CreateEndpointParams{
		Name:             req.Name,
		Url:              req.URL,
		SecretEnc:        sealed,
		Filter:           filterJSON,
		RateLimitRps:     orDefault(req.RateLimitRPS, 0),
		TimeoutMs:        orDefault(req.TimeoutMS, 10000),
		MaxAttempts:      orDefault(req.MaxAttempts, 12),
		Max4xxAttempts:   orDefault(req.Max4xxAttempts, 3),
		BreakerThreshold: orDefault(req.BreakerThreshold, 5),
		BreakerCooldownS: orDefault(req.BreakerCooldownS, 60),
		AllowPrivate:     req.AllowPrivate,
	})
	if err != nil {
		obsErr(w, r, err)
		return
	}
	v := viewOf(row)
	v.Secret = rawSecret
	writeJSON(w, http.StatusCreated, v)
}

func (s *Server) handleListEndpoints(w http.ResponseWriter, r *http.Request) {
	rows, err := s.store.Q.ListEndpoints(r.Context())
	if err != nil {
		obsErr(w, r, err)
		return
	}
	out := make([]endpointView, len(rows))
	for i, e := range rows {
		out[i] = viewOf(e)
	}
	writeJSON(w, http.StatusOK, map[string]any{"endpoints": out})
}

func (s *Server) handleGetEndpoint(w http.ResponseWriter, r *http.Request) {
	e, ok := s.loadEndpoint(w, r)
	if !ok {
		return
	}
	v := viewOf(e)
	if health, err := s.store.Q.EndpointHealth(r.Context(), e.ID); err == nil {
		rate := 1.0
		if health.Attempts24h > 0 {
			rate = float64(health.Successes24h) / float64(health.Attempts24h)
		}
		v.Health = &endpointHealth{
			BreakerState: health.BreakerState,
			SuccessRate:  rate,
			Attempts24h:  health.Attempts24h,
			P95MS:        health.P95Ms24h,
		}
	}
	writeJSON(w, http.StatusOK, v)
}

type endpointHealth struct {
	BreakerState string  `json:"breaker_state"`
	SuccessRate  float64 `json:"success_rate_24h"`
	Attempts24h  int64   `json:"attempts_24h"`
	P95MS        int32   `json:"p95_ms_24h"`
}

type updateEndpointReq struct {
	Name         *string         `json:"name"`
	URL          *string         `json:"url"`
	Filter       json.RawMessage `json:"filter"`
	Status       *string         `json:"status"`
	RateLimitRPS *int32          `json:"rate_limit_rps"`
	TimeoutMS    *int32          `json:"timeout_ms"`
	MaxAttempts  *int32          `json:"max_attempts"`
}

func (s *Server) handleUpdateEndpoint(w http.ResponseWriter, r *http.Request) {
	e, ok := s.loadEndpoint(w, r)
	if !ok {
		return
	}
	var req updateEndpointReq
	if !decodeJSON(w, r, &req) {
		return
	}
	params := db.UpdateEndpointParams{ID: e.ID}
	params.Name = req.Name
	if req.URL != nil {
		if _, err := ssrf.ValidateURL(*req.URL, s.cfg.AllowInsecureEndpoints, e.AllowPrivate); err != nil {
			writeErr(w, errf(http.StatusUnprocessableEntity, "hookrelay_ssrf_blocked", err.Error()))
			return
		}
		params.Url = req.URL
	}
	if len(req.Filter) > 0 {
		f, err := filter.Parse(req.Filter)
		if err != nil {
			validationErr(w, err.Error())
			return
		}
		fj, _ := json.Marshal(f)
		params.Filter = fj
	}
	if req.Status != nil {
		switch *req.Status {
		case "enabled", "disabled", "paused":
			params.Status = req.Status
		default:
			validationErr(w, "status must be enabled | disabled | paused")
			return
		}
	}
	params.RateLimitRps = req.RateLimitRPS
	params.TimeoutMs = req.TimeoutMS
	params.MaxAttempts = req.MaxAttempts

	row, err := s.store.Q.UpdateEndpoint(r.Context(), params)
	if err != nil {
		obsErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, viewOf(row))
}

func (s *Server) handleDeleteEndpoint(w http.ResponseWriter, r *http.Request) {
	e, ok := s.loadEndpoint(w, r)
	if !ok {
		return
	}
	if err := s.store.Q.DeleteEndpoint(r.Context(), e.ID); err != nil {
		obsErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRotateSecret(w http.ResponseWriter, r *http.Request) {
	e, ok := s.loadEndpoint(w, r)
	if !ok {
		return
	}
	rawSecret := newSigningSecret()
	sealed, err := s.secrets.Seal([]byte(rawSecret))
	if err != nil {
		obsErr(w, r, err)
		return
	}
	row, err := s.store.Q.RotateEndpointSecret(r.Context(), db.RotateEndpointSecretParams{
		ID: e.ID, SecretEnc: sealed,
	})
	if err != nil {
		obsErr(w, r, err)
		return
	}
	v := viewOf(row)
	v.Secret = rawSecret
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleResetBreaker(w http.ResponseWriter, r *http.Request) {
	e, ok := s.loadEndpoint(w, r)
	if !ok {
		return
	}
	if err := s.store.Q.ResetBreaker(r.Context(), e.ID); err != nil {
		obsErr(w, r, err)
		return
	}
	if s.breakers != nil {
		s.breakers.RecordSuccess(e.ID) // force the in-process breaker closed too
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) loadEndpoint(w http.ResponseWriter, r *http.Request) (db.Endpoint, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, errNotFound)
		return db.Endpoint{}, false
	}
	e, err := s.store.Q.GetEndpoint(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, errNotFound)
		return db.Endpoint{}, false
	}
	if err != nil {
		obsErr(w, r, err)
		return db.Endpoint{}, false
	}
	return e, true
}

func newSigningSecret() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return "whsec_" + base64.RawURLEncoding.EncodeToString(b)
}

func orDefault(p *int32, def int32) int32 {
	if p != nil {
		return *p
	}
	return def
}

func validationErr(w http.ResponseWriter, msg string) {
	writeErr(w, errf(http.StatusBadRequest, "hookrelay_validation", msg))
}
