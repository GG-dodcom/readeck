// SPDX-FileCopyrightText: © 2023 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package onboarding provides the routes and forms for
// the initial onboarding process.
package onboarding

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"codeberg.org/readeck/readeck/configs"
	"codeberg.org/readeck/readeck/internal/auth/users"
	"codeberg.org/readeck/readeck/internal/server"
	"codeberg.org/readeck/readeck/pkg/forms"
)

// SetupRoutes mounts the routes for the onboarding domain.
func SetupRoutes(s *server.Server) {
	if configs.Config.Commissioned {
		// Do not even add the route if there are users
		return
	}

	r := chi.NewRouter()
	r.Use(
		server.Csrf,
		server.WithSession(),
	)

	h := &viewHandler{r, s}
	s.AddRoute("/onboarding", r)
	r.Get("/", h.onboarding)
	r.Post("/", h.onboarding)
}

type viewHandler struct {
	chi.Router
	srv *server.Server
}

func (h *viewHandler) onboarding(w http.ResponseWriter, r *http.Request) {
	count, err := users.Users.Count()
	if err != nil {
		server.Err(w, r, err)
		return
	}
	if count > 0 {
		// Double check that there's no user yet.
		server.Redirect(w, r, "/login")
		return
	}

	f := newOnboardingForm(server.Locale(r))

	if r.Method == http.MethodPost {
		forms.Bind(f, r)
		if f.IsValid() {
			user, err := f.createUser(server.Locale(r).Tag.String())
			if err != nil {
				server.Log(r).Error("", slog.Any("err", err))
			} else {
				// All good, create a new session for the user
				configs.Config.Commissioned = true

				sess := server.GetSession(r)
				sess.Payload.User = user.ID
				sess.Payload.Seed = user.Seed
				sess.Save(w, r)

				server.Redirect(w, r, "/")
				return
			}
		}
		w.WriteHeader(http.StatusUnprocessableEntity)
	}

	server.RenderComponent(w, r, http.StatusOK, Views{}.onboarding(f))
}
