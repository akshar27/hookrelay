// Package dashboard is the server-rendered operator UI: HTMX + html/template,
// all assets embedded so it ships in the single binary.
package dashboard

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"html/template"
	"io/fs"
	"net/http"
	"time"

	"github.com/akshar27/hookrelay/internal/breaker"
	"github.com/akshar27/hookrelay/internal/secretbox"
	"github.com/akshar27/hookrelay/internal/store"
	"github.com/akshar27/hookrelay/internal/store/db"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed assets/*
var assetsFS embed.FS

// Handler holds the dependencies and the parsed templates.
type Handler struct {
	store      *store.Store
	breakers   *breaker.Registry
	secrets    *secretbox.Box
	adminToken string
	tpl        map[string]*template.Template
}

// New builds the dashboard handler. adminToken guards every page via HTTP Basic
// auth (username is ignored).
func New(st *store.Store, breakers *breaker.Registry, secrets *secretbox.Box, adminToken string) *Handler {
	funcs := template.FuncMap{
		"slice8": func(id uuid.UUID) string { return id.String()[:8] },
	}
	pages := map[string][]string{
		"index":      {"layout.html", "partials.html", "index.html"},
		"deliveries": {"layout.html", "partials.html", "deliveries.html"},
		"delivery":   {"layout.html", "partials.html", "delivery.html"},
		"endpoint":   {"layout.html", "partials.html", "endpoint.html"},
		"events":     {"layout.html", "partials.html", "events.html"},
		"event":      {"layout.html", "partials.html", "event.html"},
	}
	tpl := make(map[string]*template.Template, len(pages))
	for name, files := range pages {
		paths := make([]string, len(files))
		for i, f := range files {
			paths[i] = "templates/" + f
		}
		tpl[name] = template.Must(template.New("layout").Funcs(funcs).ParseFS(templatesFS, paths...))
	}
	return &Handler{store: st, breakers: breakers, secrets: secrets, adminToken: adminToken, tpl: tpl}
}

// Router returns the dashboard's http.Handler.
func (h *Handler) Router() http.Handler {
	r := chi.NewRouter()

	sub, _ := fs.Sub(assetsFS, "assets")
	r.Handle("/assets/*", http.StripPrefix("/assets/", http.FileServer(http.FS(sub))))

	r.Group(func(r chi.Router) {
		r.Use(h.basicAuth)
		r.Get("/", h.index)
		r.Get("/deliveries", h.deliveries)
		r.Get("/deliveries/{id}", h.delivery)
		r.Post("/deliveries/{id}/replay", h.replayDelivery)
		r.Get("/events", h.events)
		r.Get("/events/{id}", h.event)
		r.Post("/events/{id}/replay", h.replayEvent)
		r.Get("/endpoints/{id}", h.endpoint)
		r.Patch("/endpoints/{id}", h.patchEndpoint)
		r.Post("/endpoints/{id}/reset-breaker", h.resetBreaker)
		r.Post("/endpoints/{id}/rotate-secret", h.rotateSecret)
	})
	return r
}

const sessionCookie = "hookrelay_dash"

func (h *Handler) authorized(r *http.Request) bool {
	if _, pass, ok := r.BasicAuth(); ok &&
		subtle.ConstantTimeCompare([]byte(pass), []byte(h.adminToken)) == 1 {
		return true
	}
	if c, err := r.Cookie(sessionCookie); err == nil &&
		subtle.ConstantTimeCompare([]byte(c.Value), []byte(h.adminToken)) == 1 {
		return true
	}
	return false
}

func (h *Handler) basicAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// One-time bootstrap: ?token=<admin> sets a session cookie and redirects.
		if t := r.URL.Query().Get("token"); t != "" &&
			subtle.ConstantTimeCompare([]byte(t), []byte(h.adminToken)) == 1 {
			http.SetCookie(w, &http.Cookie{
				Name: sessionCookie, Value: t, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
			})
			q := r.URL.Query()
			q.Del("token")
			r.URL.RawQuery = q.Encode()
			http.Redirect(w, r, r.URL.String(), http.StatusSeeOther)
			return
		}
		if !h.authorized(r) {
			w.Header().Set("WWW-Authenticate", `Basic realm="HookRelay"`)
			http.Error(w, "unauthorized — sign in with the admin token (Basic auth, or ?token=… once)", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) render(w http.ResponseWriter, page string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tpl[page].ExecuteTemplate(w, "layout", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func ctxOf(r *http.Request) context.Context {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	_ = cancel
	return ctx
}

func fmtTime(t time.Time) string { return t.Local().Format("Jan 2 15:04:05") }

func statusCounts(rows []db.CountDeliveriesByStatusRow) []db.CountDeliveriesByStatusRow {
	order := map[string]int{"pending": 0, "delivering": 1, "failed": 2, "blocked": 3, "succeeded": 4, "dead": 5}
	present := map[string]bool{}
	for _, r := range rows {
		present[r.Status] = true
	}
	for s := range order {
		if !present[s] {
			rows = append(rows, db.CountDeliveriesByStatusRow{Status: s, N: 0})
		}
	}
	// simple insertion sort by the fixed order
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0 && order[rows[j-1].Status] > order[rows[j].Status]; j-- {
			rows[j-1], rows[j] = rows[j], rows[j-1]
		}
	}
	return rows
}

func jsonCompact(raw json.RawMessage) string {
	var buf []byte
	if b, err := json.MarshalIndent(json.RawMessage(raw), "", "  "); err == nil {
		buf = b
	} else {
		buf = raw
	}
	return string(buf)
}
