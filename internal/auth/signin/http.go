// SPDX-FileCopyrightText: © 2021 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package signin contains the routes for Readeck sign-in process.
package signin

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/go-chi/chi/v5"

	"codeberg.org/readeck/readeck/configs"
	"codeberg.org/readeck/readeck/internal/auth/users"
	"codeberg.org/readeck/readeck/internal/server"
	"codeberg.org/readeck/readeck/pkg/forms"
	"codeberg.org/readeck/readeck/pkg/totp"
)

// SetupRoutes mounts the routes for the auth domain.
func SetupRoutes(s *server.Server) {
	newAuthHandler(s)
}

type authHandler struct {
	chi.Router
	srv *server.Server
}

func newAuthHandler(s *server.Server) *authHandler {
	// Non authenticated routes
	r := chi.NewRouter()
	r.Use(
		server.Csrf,
		server.WithSession(),
	)

	h := &authHandler{r, s}
	s.AddRoute("/login", r)
	r.Get("/", h.login)
	r.Post("/", h.login)
	r.Get("/mfa", h.mfa)
	r.Post("/mfa", h.mfa)

	// Recovery
	r.With(server.WithPermission("email", "send")).Route("/recover", func(r chi.Router) {
		r.Get("/", h.recover)
		r.Post("/", h.recover)
		r.Get("/{code}", h.recover)
		r.Post("/{code}", h.recover)
	})

	// Authenticated routes
	ar := server.AuthenticatedRouter(server.WithRedirectLogin)
	s.AddRoute("/logout", ar)
	ar.Post("/", h.logout)

	return h
}

func (h *authHandler) redirTo(w http.ResponseWriter, r *http.Request, redir string) {
	// Get redirection from a form "redirect" parameter
	// Since it goes to Redirect(), it will be sanitized there
	// and can only stay within the app.
	if redir == "" || strings.HasPrefix(redir, "/login") {
		redir = "/"
	}
	server.Redirect(w, r, redir)
}

func (h *authHandler) redirToMFA(w http.ResponseWriter, r *http.Request, redir string) {
	v := url.Values{"r": {redir}}
	server.Redirect(w, r, "/login/mfa?"+v.Encode())
}

func (h *authHandler) login(w http.ResponseWriter, r *http.Request) {
	// f := newLoginForm(server.Locale(r))
	f := forms.New[loginForm](r.Context())

	if r.Method == http.MethodGet {
		// // Set the redirect value from the query string
		_ = forms.UnmarshalValues(r.URL.Query()[f.Redirect.Name()], &f.Redirect)

		// Do we have a session already?
		if sess := server.GetSession(r); sess.Payload.User != 0 {
			if sess.Payload.RequiresMFA {
				h.redirToMFA(w, r, f.Redirect.Value())
				return
			}
			h.redirTo(w, r, f.Redirect.Value())
			return
		}
	}

	if r.Method == http.MethodPost {
		forms.Bind(r, f)

		if f.IsValid() {
			user := f.checkUser()

			if user != nil {
				// User is authenticated, let's carry on
				sess := server.GetSession(r)
				sess.Payload.User = user.ID
				sess.Payload.Seed = user.Seed
				sess.Payload.RequiresMFA = user.RequiresMFA()
				sess.Save(w, r)

				if sess.Payload.RequiresMFA {
					h.redirToMFA(w, r, f.Redirect.Value())
					return
				}

				_ = user.Update(goqu.Record{
					"last_login": time.Now().UTC(),
				})

				h.redirTo(w, r, f.Redirect.Value())
				return
			}
			// we must set the content type to avoid the
			// error middleware interception.
			w.Header().Set("content-type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
		}
		w.WriteHeader(http.StatusUnprocessableEntity)
	}

	server.RenderComponent(w, r, http.StatusOK, Views{}.signIn(f))
}

func (h *authHandler) mfa(w http.ResponseWriter, r *http.Request) {
	sess := server.GetSession(r)
	if !sess.Payload.RequiresMFA {
		server.Redirect(w, r, "/")
		return
	}

	user := new(users.User)
	found, err := users.Users.Query().
		SelectAppend(goqu.C("totp_secret").Table("u")).
		Where(goqu.C("id").Eq(sess.Payload.User)).
		ScanStruct(user)
	if err != nil {
		server.Err(w, r, err)
		return
	}
	if !found {
		server.Status(w, r, 404)
		return
	}

	f := forms.New[totpForm](r.Context())

	if r.Method == http.MethodGet {
		// Set the redirect value from the query string
		_ = forms.UnmarshalValues(r.URL.Query()[f.Redirect.Name()], &f.Redirect)
	}

	status := http.StatusOK

	if r.Method == http.MethodPost {
		forms.Bind(r, f)
		if f.IsValid() {
			code := new(totp.Code)
			if err := configs.Keys.TOTPKey().DecodeJSON(user.TOTPSecret, code); err != nil {
				server.Err(w, r, err)
				return
			}

			ok, err := code.Verify(f.Code.Value(), time.Now().UTC(), 1)
			if err != nil {
				server.Err(w, r, err)
				return
			}
			if ok {
				sess.Payload.RequiresMFA = false
				sess.Save(w, r)

				redir := f.Redirect.Value()
				if redir == "" || strings.HasPrefix(redir, "/login") {
					redir = "/"
				}

				_ = user.Update(goqu.Record{
					"last_login": time.Now().UTC(),
				})

				server.Redirect(w, r, redir)
				return
			}
			f.Code.AddErrors(forms.Gettext("Invalid code"))
		}
		status = http.StatusUnprocessableEntity
	}

	server.RenderComponent(w, r, status, Views{}.mfa(f))
}

func (h *authHandler) logout(w http.ResponseWriter, r *http.Request) {
	// Clear session
	sess := server.GetSession(r)
	sess.Clear(w, r)

	server.Redirect(w, r, "/login")
}
