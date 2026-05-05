// SPDX-FileCopyrightText: © 2020 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package server is the main Readeck HTTP server.
// It defines common middlewares, guards, permission handlers, etc.
package server

import (
	"log/slog"
	"net/http"
	"os"
	"path"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"codeberg.org/readeck/readeck/configs"
	"codeberg.org/readeck/readeck/internal/auth"
	"codeberg.org/readeck/readeck/internal/bookmarks"
	"codeberg.org/readeck/readeck/internal/db"
	"codeberg.org/readeck/readeck/internal/metrics"
	"codeberg.org/readeck/readeck/internal/server/urls"
	"codeberg.org/readeck/readeck/pkg/http/request"
)

// Server is a wrapper around chi router.
type Server struct {
	*chi.Mux
}

// New create a new server. Routes must be added manually before
// calling ListenAndServe.
func New() *Server {
	s := &Server{
		chi.NewRouter(),
	}

	// Prepare authentication providers
	s.Use(
		middleware.Recoverer,
		InitRequest(),
		Logger(),
		metrics.Middleware,
		SetSecurityHeaders,
		CompressResponse,
		WithCacheControl,
		CannonicalPaths,
		auth.Init(
			&TokenAuthProvider{},
			&ForwardedAuthProvider{},
			&SessionAuthProvider{},
		),
		LoadLocale,
		ErrorPages,
		crossOriginGuard,
	)

	return s
}

// Init initializes the server and the template engine.
func (s *Server) Init() {
	// System routes
	s.AddRoute("/api/info", infoRoutes())
	s.AddRoute("/api/sys", sysRoutes())
	s.AddRoute("/logger", loggerRoutes())

	// web manifest
	s.AddRoute("/manifest.webmanifest", manifestRoutes())
}

// InitRequest returns a midleware that sets the absolute request URL
// and adds it to its context.
func InitRequest() func(next http.Handler) http.Handler {
	h := chi.Middlewares{}
	if configs.Config.Server.BaseURL != nil {
		h = append(h, request.InitBaseURL(configs.Config.Server.BaseURL.URL))
	}
	h = append(h, request.InitRequest(configs.TrustedProxies()...))

	return func(next http.Handler) http.Handler {
		return h.Handler(next)
	}
}

// AuthenticatedRouter returns a chi.Router instance
// with middlewares to force authentication.
func AuthenticatedRouter(middlewares ...func(http.Handler) http.Handler) chi.Router {
	r := chi.NewRouter()

	r.Use(middlewares...)
	r.Use(
		Csrf,
		WithSession(),
		auth.Required,
		LoadLocale,
		// It's already in the main router but this one will be called first and have
		// the current user information
		ErrorPages,
	)

	return r
}

// AddRoute adds a new route to the server, prefixed with
// the BasePath.
func (s *Server) AddRoute(pattern string, handler http.Handler) {
	s.Mount(path.Join(urls.Prefix(), pattern), handler)
}

// infoRoutes returns the route returning the service information.
func infoRoutes() http.Handler {
	r := chi.NewRouter()

	type versionInfo struct {
		Canonical string `json:"canonical"`
		Release   string `json:"release"`
		Build     string `json:"build"`
	}

	type serviceInfo struct {
		Version  versionInfo `json:"version"`
		Features []string    `json:"features"`
	}

	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		canonical := configs.Version()
		release, build, _ := strings.Cut(canonical, "-")

		res := serviceInfo{
			Version: versionInfo{
				Canonical: canonical,
				Release:   release,
				Build:     build,
			},
			Features: []string{
				"oauth",
			},
		}

		if auth.HasPermission(r, "email", "send") {
			res.Features = append(res.Features, "email")
		}
		slices.Sort(res.Features)

		Render(w, r, 200, res)
	})

	return r
}

// sysRoutes returns the route returning some system
// information.
func sysRoutes() http.Handler {
	r := AuthenticatedRouter()
	r.Use(WithPermission("system", "read"))

	type memInfo struct {
		Alloc      uint64 `json:"alloc"`
		TotalAlloc uint64 `json:"totalalloc"`
		Sys        uint64 `json:"sys"`
		NumGC      uint32 `json:"numgc"`
	}
	type storageInfo struct {
		Database  uint64 `json:"database"`
		Bookmarks uint64 `json:"bookmarks"`
	}

	type sysInfo struct {
		Version   string      `json:"version"`
		BuildDate time.Time   `json:"build_date"`
		OS        string      `json:"os"`
		Platform  string      `json:"platform"`
		Hostname  string      `json:"hostname"`
		CPUs      int         `json:"cpus"`
		GoVersion string      `json:"go_version"`
		Mem       memInfo     `json:"mem"`
		DiskUsage storageInfo `json:"disk_usage"`
	}

	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		host, _ := os.Hostname()

		var err error
		usage := storageInfo{}
		usage.Database, err = db.Driver().DiskUsage()
		if err != nil {
			Err(w, r, err)
			return
		}

		usage.Bookmarks, err = bookmarks.Bookmarks.DiskUsage(nil)
		if err != nil {
			Err(w, r, err)
			return
		}

		res := sysInfo{
			Version:   configs.Version(),
			BuildDate: configs.BuildTime(),
			OS:        runtime.GOOS,
			Platform:  runtime.GOARCH,
			Hostname:  host,
			CPUs:      runtime.NumCPU(),
			GoVersion: runtime.Version(),
			Mem: memInfo{
				Alloc:      m.Alloc,
				TotalAlloc: m.TotalAlloc,
				Sys:        m.Sys,
				NumGC:      m.NumGC,
			},
			DiskUsage: usage,
		}

		Render(w, r, 200, res)
	})

	return r
}

func loggerRoutes() http.Handler {
	r := chi.NewRouter()
	r.Post("/csp-report", cspReportHandler)

	return r
}

// IsTurboRequest returns true when the request was made with
// an x-turbo header.
func IsTurboRequest(r *http.Request) bool {
	return r.Header.Get("x-turbo") == "1"
}

// GetReqID returns the request ID.
func GetReqID(r *http.Request) string {
	return request.GetReqID(r.Context())
}

// Log returns a log entry including the request ID.
func Log(r *http.Request) *slog.Logger {
	return slog.With(slog.String("@id", GetReqID(r)))
}
