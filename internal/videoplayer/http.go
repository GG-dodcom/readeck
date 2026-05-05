// SPDX-FileCopyrightText: © 2024 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package videoplayer provides a route for an HLS embed video player.
package videoplayer

import (
	"context"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"

	"codeberg.org/readeck/readeck/internal/server"
	"codeberg.org/readeck/readeck/pkg/forms"
	"codeberg.org/readeck/readeck/pkg/http/csp"
)

// SetupRoutes mounts the routes for the videoplayer domain.
func SetupRoutes(s *server.Server) {
	// The /videoplayer route is not authenticated
	r := chi.NewRouter()
	r.Get("/", videoPlayerHandler)

	s.AddRoute("/videoplayer", r)
}

func videoPlayerHandler(w http.ResponseWriter, r *http.Request) {
	f := forms.Must(
		forms.WithTranslator(context.Background(), server.Locale(r)),
		forms.NewTextField("src", forms.Trim, forms.Required, forms.IsURL("http", "https")),
		forms.NewTextField("type",
			forms.Trim,
			forms.RequiredOrNil,
			forms.Default("video"),
			forms.ChoicesPairs([][2]string{
				{"hls", "hls"},
				{"embed", "embed"},
				{"video", "video"},
			})),
		forms.NewIntegerField("w", forms.Gte(1)),
		forms.NewIntegerField("h", forms.Gte(1)),
	)

	forms.BindURL(f, r)
	if !f.IsValid() {
		server.Render(w, r, http.StatusUnprocessableEntity, f)
		return
	}

	srcURL, _ := url.Parse(f.Get("src").String())

	// Set appropriate CSP values for this ressource to work
	// as a video play in an iframe.
	policy := server.GetCSPHeader(r)
	policy.Set("connect-src", srcURL.Hostname())
	policy.Set("worker-src", "blob:")
	policy.Add("media-src", "blob:", srcURL.Hostname())
	policy.Add("frame-src", "blob:", srcURL.Hostname())
	policy.Set("frame-ancestors", csp.Self)

	policy.Write(w.Header())
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")

	server.RenderComponent(w, r, http.StatusOK, Views{}.player(
		f.Get("src").String(),
		f.Get("type").String(),
		f.Get("w").(*forms.IntegerField).V(),
		f.Get("h").(*forms.IntegerField).V(),
	))
}
