// SPDX-FileCopyrightText: © 2021 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package admin

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"codeberg.org/readeck/readeck/components"
	"codeberg.org/readeck/readeck/internal/auth"
	"codeberg.org/readeck/readeck/internal/server"
	"codeberg.org/readeck/readeck/internal/server/urls"
	"codeberg.org/readeck/readeck/pkg/forms/v2"
)

// adminViews is an HTTP handler for the user profile web views.
type adminViews struct {
	chi.Router
	*adminAPI
}

func newAdminViews(api *adminAPI) *adminViews {
	r := server.AuthenticatedRouter(server.WithRedirectLogin)
	h := &adminViews{r, api}

	r.With(server.WithPermission("admin:users", "read")).Group(func(r chi.Router) {
		r.With(api.withUserList).Get("/", h.main)
		r.With(api.withUserList).Get("/users", h.userList)
		r.Get("/users/add", h.userCreate)
		r.With(api.withUser).Get("/users/{uid:[a-zA-Z0-9]{18,22}}", h.userInfo)
	})

	r.With(server.WithPermission("admin:users", "write")).Group(func(r chi.Router) {
		r.Post("/users/add", h.userCreate)
		r.With(api.withUser).Post("/users/{uid:[a-zA-Z0-9]{18,22}}", h.userInfo)
		r.With(api.withUser).Post("/users/{uid:[a-zA-Z0-9]{18,22}}/delete", h.userDelete)
	})

	return h
}

func (h *adminViews) main(w http.ResponseWriter, r *http.Request) {
	server.Redirect(w, r, "./users")
}

func (h *adminViews) userList(w http.ResponseWriter, r *http.Request) {
	tr := server.Locale(r)
	ul := getUserList(r.Context())

	ctx := components.WithBreadcrumb(r.Context(), [][2]string{
		{tr.Gettext("Users")},
	})
	server.RenderComponent(w, r.WithContext(ctx), http.StatusOK, Views{}.userList(ul))
}

func (h *adminViews) userCreate(w http.ResponseWriter, r *http.Request) {
	tr := server.Locale(r)
	f := forms.New[userForm](r.Context())

	if r.Method == http.MethodPost {
		forms.Bind(r, f)
		if f.IsValid() {
			u, err := f.createUser()
			if err != nil {
				server.Log(r).Error("", slog.Any("err", err))
			} else {
				server.AddFlash(w, r, "success", tr.Gettext("User created."))
				server.Redirect(w, r, "./..", u.UID)
				return
			}
		}
		w.WriteHeader(http.StatusUnprocessableEntity)
	}

	ctx := components.WithBreadcrumb(r.Context(), [][2]string{
		{tr.Gettext("Users"), urls.AbsoluteURL(r, "/admin/users").String()},
		{tr.Gettext("New User")},
	})
	server.RenderComponent(w, r.WithContext(ctx), http.StatusOK, Views{}.userCreate(f))
}

func (h *adminViews) userInfo(w http.ResponseWriter, r *http.Request) {
	tr := server.Locale(r)
	u := getUser(r.Context())

	f := forms.New[userForm](r.Context())
	f.SetUser(u)

	if r.Method == http.MethodPost {
		forms.Bind(r, f)

		if f.IsValid() {
			if _, err := f.updateUser(u); err != nil {
				server.Log(r).Error("", slog.Any("err", err))
			} else {
				// Refresh session if same user
				if auth.GetRequestUser(r).ID == u.ID {
					sess := server.GetSession(r)
					sess.Payload.User = u.ID
					sess.Payload.Seed = u.Seed
				}
				server.AddFlash(w, r, "success", tr.Gettext("User updated."))
				server.Redirect(w, r, u.UID)
				return
			}
		}
		w.WriteHeader(http.StatusUnprocessableEntity)
	}

	item, err := newExtendedUserItem(r.Context(), u)
	if err != nil {
		server.Err(w, r, err)
		return
	}

	ctx := components.WithBreadcrumb(r.Context(), [][2]string{
		{tr.Gettext("Users"), urls.AbsoluteURL(r, "/admin/users").String()},
		{item.Username},
	})
	server.RenderComponent(w, r.WithContext(ctx), http.StatusOK, Views{}.userInfo(f, item))
}

func (h *adminViews) userDelete(w http.ResponseWriter, r *http.Request) {
	f := forms.New[deleteForm](r.Context())
	f.To.Set("/admin/users")
	forms.Bind(r, f)

	u := getUser(r.Context())
	if u.ID == auth.GetRequestUser(r).ID {
		server.Err(w, r, errSameUser)
		return
	}

	if err := f.trigger(u); err != nil {
		server.Err(w, r, err)
		return
	}
	server.Redirect(w, r, f.To.String())
}
