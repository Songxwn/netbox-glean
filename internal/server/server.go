package server

import (
	"crypto/subtle"
	"embed"
	"io/fs"
	"log/slog"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/Songxwn/netbox-glean/internal/jobs"
	"github.com/Songxwn/netbox-glean/internal/store"
)

//go:embed web
var webFiles embed.FS

// Options configures the HTTP server.
type Options struct {
	Store       *store.Store
	Jobs        *jobs.Manager
	Version     string
	Commit      string
	Date        string
	WebUsername string
	WebPassword string
	Logger      *slog.Logger
}

// Server wires the HTTP API and the embedded web UI together.
type Server struct {
	store    *store.Store
	jobs     *jobs.Manager
	version  string
	commit   string
	date     string
	webUser  string
	webPass  string
	logger   *slog.Logger
	handler  http.Handler
}

// New creates the HTTP handler and scheduler owner.
func New(opts Options) *Server {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{
		store:   opts.Store,
		jobs:    opts.Jobs,
		version: opts.Version,
		commit:  opts.Commit,
		date:    opts.Date,
		webUser: opts.WebUsername,
		webPass: opts.WebPassword,
		logger:  logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/version", s.handleVersion)
	mux.HandleFunc("GET /api/stats", s.handleStats)

	mux.HandleFunc("GET /api/settings", s.handleGetSettings)
	mux.HandleFunc("PUT /api/settings", s.handlePutSettings)
	mux.HandleFunc("POST /api/settings/test", s.handleNetboxTest)
	mux.HandleFunc("POST /api/netbox/test", s.handleNetboxTest)

	mux.HandleFunc("GET /api/targets", s.handleListTargets)
	mux.HandleFunc("POST /api/targets", s.handleCreateTarget)
	mux.HandleFunc("GET /api/targets/{id}", s.handleGetTarget)
	mux.HandleFunc("PUT /api/targets/{id}", s.handleUpdateTarget)
	mux.HandleFunc("DELETE /api/targets/{id}", s.handleDeleteTarget)
	mux.HandleFunc("POST /api/targets/{id}/collect", s.handleCollectTarget)

	mux.HandleFunc("POST /api/collect", s.handleCollectBatch)

	mux.HandleFunc("GET /api/ip-targets", s.handleListIPTargets)
	mux.HandleFunc("POST /api/ip-targets", s.handleCreateIPTarget)
	mux.HandleFunc("GET /api/ip-targets/{id}", s.handleGetIPTarget)
	mux.HandleFunc("PUT /api/ip-targets/{id}", s.handleUpdateIPTarget)
	mux.HandleFunc("DELETE /api/ip-targets/{id}", s.handleDeleteIPTarget)
	mux.HandleFunc("POST /api/ip-targets/{id}/collect", s.handleCollectIPTarget)
	mux.HandleFunc("POST /api/ip-collect", s.handleCollectIPBatch)

	mux.HandleFunc("POST /api/scan", s.handleScan)

	mux.HandleFunc("GET /api/jobs", s.handleListJobs)
	mux.HandleFunc("GET /api/jobs/{id}", s.handleGetJob)
	mux.HandleFunc("DELETE /api/jobs", s.handleClearJobs)

	sub, err := fs.Sub(webFiles, "web")
	if err != nil {
		panic(err)
	}
	mux.Handle("/", http.FileServer(http.FS(sub)))

	s.handler = s.recoverer(s.accessLog(s.securityHeaders(s.basicAuth(mux))))
	return s
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"version": s.version,
	})
}

func (s *Server) handleVersion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"name":      "netbox-glean",
		"version":   s.version,
		"commit":    s.commit,
		"date":      s.date,
		"go":        runtime.Version(),
		"platform":  runtime.GOOS + "/" + runtime.GOARCH,
	})
}

func (s *Server) basicAuth(next http.Handler) http.Handler {
	if s.webPass == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/health" {
			next.ServeHTTP(w, r)
			return
		}
		user, pass, ok := r.BasicAuth()
		userOK := s.webUser == "" || subtle.ConstantTimeCompare([]byte(user), []byte(s.webUser)) == 1
		passOK := subtle.ConstantTimeCompare([]byte(pass), []byte(s.webPass)) == 1
		if !ok || !userOK || !passOK {
			w.Header().Set("WWW-Authenticate", `Basic realm="netbox-glean"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if strings.HasPrefix(r.URL.Path, "/api/") {
			s.logger.Info("http", "method", r.Method, "path", r.URL.Path, "status", rec.status, "duration", time.Since(start).String())
		}
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.logger.Error("panic", "error", rec, "path", r.URL.Path)
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}