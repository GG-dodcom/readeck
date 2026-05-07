// SPDX-FileCopyrightText: © 2024 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package videoplayer provides a route for an HLS embed video player.
package videoplayer

import (
	"net/http"

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
	type videoPlayerForm struct {
		forms.Form
		SRC    forms.URLField     `json:"src"  validate:"trim required is_url"`
		Type   forms.TextField    `json:"type" validate:"trim required_or_nil"`
		Width  forms.IntegerField `json:"w"    validate:"gte:0"`
		Height forms.IntegerField `json:"h"    validate:"gte:0"`
	}

	f := forms.New[videoPlayerForm](r.Context())
	forms.Choices(&f.Type,
		forms.Choice("hls", "hls"),
		forms.Choice("embed", "embed"),
		forms.Choice("video", "video"),
	)
	f.Type.Set("video")

	forms.Bind(r, f)

	if !f.IsValid() {
		server.Render(w, r, http.StatusUnprocessableEntity, f)
		return
	}

	src := (&f.SRC).Value()

	// Set appropriate CSP values for this ressource to work
	// as a video play in an iframe.
	policy := server.GetCSPHeader(r)
	policy.Set("connect-src", src.Hostname())
	policy.Set("worker-src", "blob:")
	policy.Add("media-src", "blob:", src.Hostname())
	policy.Add("frame-src", "blob:", src.Hostname())
	policy.Set("frame-ancestors", csp.Self)

	policy.Write(w.Header())
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")

	server.RenderComponent(w, r, http.StatusOK, Views{}.player(
		src.String(),
		f.Type.Value(),
		f.Width.Value(),
		f.Height.Value(),
	))
}
