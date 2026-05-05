// SPDX-FileCopyrightText: © 2023 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package docs contains routes to the documentation.
package docs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/komkom/toml"

	"codeberg.org/readeck/readeck/components"
	"codeberg.org/readeck/readeck/configs"
	"codeberg.org/readeck/readeck/docs"
	"codeberg.org/readeck/readeck/internal/auth"
	"codeberg.org/readeck/readeck/internal/bookmarks"
	"codeberg.org/readeck/readeck/internal/db"
	"codeberg.org/readeck/readeck/internal/server"
	"codeberg.org/readeck/readeck/internal/server/urls"
	"codeberg.org/readeck/readeck/locales"
	"codeberg.org/readeck/readeck/pkg/http/csp"
)

type (
	ctxFileKey     struct{}
	ctxSectionKey  struct{}
	ctxLanguageKey struct{}
)

type helpHandlers struct {
	chi.Router
	srv *server.Server
}

type licenseInfo struct {
	Name      string
	License   string
	Author    string
	URL       string
	Copyright string
}

const routePrefix = "/docs"

// SetupRoutes mounts the routes for the auth domain.
func SetupRoutes(s *server.Server) {
	handler := &helpHandlers{
		chi.NewRouter(),
		s,
	}

	// File routes
	for _, f := range docs.Manifest.Files {
		if f.IsDocument {
			continue
		}
		handler.With(handler.withFile(f)).Get("/"+f.Route, handler.serveStatic)
	}

	// Document routes
	// docHandler serves the document and requires authentication
	docHandler := handler.With(server.AuthenticatedRouter(server.WithRedirectLogin).Middlewares()...)
	for tag, section := range docs.Manifest.Sections {
		for _, f := range section.Files {
			// Document
			docHandler.With(
				server.WithPermission("docs", "read"),
				handler.withFile(f),
				handler.withSection(tag, section),
			).Get("/"+f.Route, handler.serveDocument)

			// Aliases
			for _, alias := range f.Aliases {
				docHandler.With(
					server.WithPermission("docs", "read"),
				).Get("/"+alias, handler.serveRedirect(routePrefix+"/"+f.Route))
			}
		}
	}

	// Changelog route
	f := docs.Manifest.Files["changelog"]
	docHandler.With(
		server.WithPermission("system", "read"),
		handler.withFile(f),
	).Get("/changelog", handler.serveDocument)

	// About page
	docHandler.With(
		server.WithPermission("system", "read"),
	).Get("/about", handler.serveAbout)

	// Documentation
	docHandler.With(server.WithPermission("docs", "read")).Get("/", handler.localeRedirect)
	docHandler.With(server.WithPermission("docs", "read")).Get("/{path}", handler.localeRedirect)

	// API documentation
	docHandler.With(
		server.WithPermission("docs", "read"),
	).Group(func(r chi.Router) {
		r.Get("/api", handler.serveAPIDocs)
		r.Get("/api.json", handler.serveAPISchema)
	})

	s.AddRoute(routePrefix, handler)
}

func (h *helpHandlers) withFile(f *docs.File) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if f == nil {
				server.Status(w, r, http.StatusNotFound)
				return
			}

			ctx := context.WithValue(r.Context(), ctxFileKey{}, f)

			server.WriteEtag(w, r, f)
			server.WithCaching(next).ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func (h *helpHandlers) withSection(tag string, section *docs.Section) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), ctxSectionKey{}, section)
			ctx = context.WithValue(ctx, ctxLanguageKey{}, tag)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func (h *helpHandlers) getSection(r *http.Request) (*docs.Section, string) {
	if section, ok := r.Context().Value(ctxSectionKey{}).(*docs.Section); ok {
		return section, r.Context().Value(ctxLanguageKey{}).(string)
	}

	tag := server.Locale(r).Tag.String()
	if _, ok := docs.Manifest.Sections[tag]; !ok {
		tag = "en"
	}
	return docs.Manifest.Sections[tag], tag
}

func (h *helpHandlers) serveDocument(w http.ResponseWriter, r *http.Request) {
	f, _ := r.Context().Value(ctxFileKey{}).(*docs.File)

	fd, err := docs.Files.Open(f.File)
	if err != nil {
		server.Err(w, r, err)
		return
	}
	defer fd.Close()

	var contents strings.Builder
	io.Copy(&contents, fd)
	repl := strings.NewReplacer(
		"readeck-instance://", urls.AbsoluteURL(r, "/").String(),
	)
	buf := new(bytes.Buffer)
	repl.WriteString(buf, contents.String())

	section, tag := h.getSection(r)
	tr := locales.LoadTranslation(tag)
	ctx := components.WithBreadcrumb(r.Context(), [][2]string{
		{tr.Gettext("Documentation"), urls.PathOnly(urls.AbsoluteURL(r, "/docs", tag, "/"))},
		{f.Title},
	})

	server.RenderComponent(w, r.WithContext(ctx), http.StatusOK, Views{}.document(
		f.Title,
		section.TOC,
		tag,
		buf,
	))
}

func (h *helpHandlers) serveStatic(w http.ResponseWriter, r *http.Request) {
	f, _ := r.Context().Value(ctxFileKey{}).(*docs.File)
	fd, err := docs.Files.Open(f.File)
	if err != nil {
		server.Err(w, r, err)
		return
	}
	defer fd.Close()

	http.ServeContent(w, r, f.File, time.Time{}, fd)
}

func (h *helpHandlers) localeRedirect(w http.ResponseWriter, r *http.Request) {
	tag := server.Locale(r).Tag.String()
	if _, ok := docs.Manifest.Sections[tag]; !ok {
		tag = "en"
	}

	server.Redirect(w, r, routePrefix+"/"+tag+"/"+chi.URLParam(r, "path"))
}

func (h *helpHandlers) serveRedirect(to string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		server.Redirect(w, r, to)
	}
}

func (h *helpHandlers) serveAbout(w http.ResponseWriter, r *http.Request) {
	fp, err := docs.Assets.Open("licenses/licenses.toml")
	if err != nil {
		server.Err(w, r, err)
		return
	}

	licenses := map[string][]licenseInfo{}
	dec := json.NewDecoder(toml.New(fp))
	if err = dec.Decode(&licenses); err != nil {
		server.Err(w, r, err)
		return
	}
	slices.SortFunc(licenses["licenses"], func(a, b licenseInfo) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})

	dbUsageVal, err := db.Driver().DiskUsage()
	if err != nil {
		server.Err(w, r, err)
		return
	}
	diskUsageVal, err := bookmarks.Bookmarks.DiskUsage(nil)
	if err != nil {
		server.Err(w, r, err)
		return
	}

	buildInfo := new(strings.Builder)
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, x := range info.Settings {
			fmt.Fprintf(buildInfo, "%s: %s\n", x.Key, strings.ReplaceAll(x.Value, ",", ", "))
		}
	}

	section, tag := h.getSection(r)
	tr := locales.LoadTranslation(tag)
	ctx := components.WithBreadcrumb(r.Context(), [][2]string{
		{tr.Gettext("Documentation"), urls.PathOnly(urls.AbsoluteURL(r, "/docs", tag, "/"))},
		{tr.Gettext("About Readeck")},
	})

	server.RenderComponent(w, r.WithContext(ctx), http.StatusOK, Views{}.about(
		section.TOC, tag,
		aboutInfo{
			version:     configs.Version(),
			buildTime:   configs.BuildTime(),
			buildInfo:   buildInfo,
			licenses:    licenses["licenses"],
			os:          runtime.GOOS,
			arch:        runtime.GOARCH,
			goVersion:   runtime.Version(),
			dbConnector: db.Driver().Name(),
			dbVersion:   db.Driver().Version(),
			dbSize:      dbUsageVal,
			diskUsage:   diskUsageVal,
		},
	))
}

func (h *helpHandlers) serveAPISchema(w http.ResponseWriter, r *http.Request) {
	fd, err := docs.Files.Open("api.json")
	if err != nil {
		server.Err(w, r, err)
		return
	}
	defer fd.Close()

	var contents strings.Builder
	io.Copy(&contents, fd)
	repl := strings.NewReplacer(
		"__ROOT_URI__", strings.TrimSuffix(urls.AbsoluteURL(r, "/").String(), "/"),
		"__BASE_URI__", urls.AbsoluteURL(r, "/api").String(),
	)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	repl.WriteString(w, contents.String())
}

func (h *helpHandlers) serveAPIDocs(w http.ResponseWriter, r *http.Request) {
	// By including a web component full of inline styles, we need
	// to relax the style-src policy.
	policy := server.GetCSPHeader(r).Clone()
	policy.Set("style-src", csp.ReportSample, csp.Self, csp.UnsafeInline)
	policy.Write(w.Header())

	tr := server.Locale(r)
	ctx := components.WithBreadcrumb(r.Context(), [][2]string{
		{tr.Gettext("Documentation"), urls.PathOnly(urls.AbsoluteURL(r, "/docs", tr.Tag.String(), "/"))},
		{"API"},
	})

	hideBadges := []string{}
	if !auth.GetRequestUser(r).HasPermission("api:cookbook", "read") {
		hideBadges = append(hideBadges, "admin only")
	}

	server.RenderComponent(w, r.WithContext(ctx), http.StatusOK, Views{}.api(
		urls.PathOnly(urls.AbsoluteURL(r, "/docs/api.json")),
		hideBadges,
	))
}
