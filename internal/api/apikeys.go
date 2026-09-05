package api

import (
	"net/http"

	"github.com/akshar27/hookrelay/internal/apikey"
	"github.com/akshar27/hookrelay/internal/store/db"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func (s *Server) routeAPIKeys(r chi.Router) {
	r.Post("/", s.handleCreateAPIKey)
	r.Get("/", s.handleListAPIKeys)
	r.Delete("/{id}", s.handleDisableAPIKey)
}

type createAPIKeyReq struct {
	Name string `json:"name"`
}

type createAPIKeyResp struct {
	ID     uuid.UUID `json:"id"`
	Name   string    `json:"name"`
	Prefix string    `json:"prefix"`
	APIKey string    `json:"api_key"` // shown once
}

func (s *Server) handleCreateAPIKey(w http.ResponseWriter, r *http.Request) {
	var req createAPIKeyReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		writeErr(w, errf(http.StatusBadRequest, "hookrelay_validation", "name is required"))
		return
	}
	gen := apikey.Generate()
	row, err := s.store.Q.CreateAPIKey(r.Context(), db.CreateAPIKeyParams{
		Name:      req.Name,
		KeyHash:   gen.Hash,
		KeyPrefix: gen.Prefix,
	})
	if err != nil {
		obsErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, createAPIKeyResp{
		ID: row.ID, Name: row.Name, Prefix: row.KeyPrefix, APIKey: gen.Raw,
	})
}

func (s *Server) handleListAPIKeys(w http.ResponseWriter, r *http.Request) {
	rows, err := s.store.Q.ListAPIKeys(r.Context())
	if err != nil {
		obsErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"api_keys": rows})
}

func (s *Server) handleDisableAPIKey(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, errNotFound)
		return
	}
	if err := s.store.Q.DisableAPIKey(r.Context(), id); err != nil {
		obsErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
