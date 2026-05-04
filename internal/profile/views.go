// SPDX-FileCopyrightText: © 2021 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package profile

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"codeberg.org/readeck/readeck/components"
	"codeberg.org/readeck/readeck/configs"
	"codeberg.org/readeck/readeck/internal/auth"
	"codeberg.org/readeck/readeck/internal/auth/tokens"
	"codeberg.org/readeck/readeck/internal/portability"
	"codeberg.org/readeck/readeck/internal/server"
	"codeberg.org/readeck/readeck/internal/server/urls"
	"codeberg.org/readeck/readeck/pkg/forms/v2"
)

// profileViews is an HTTP handler for the user profile web views.
type profileViews struct {
	chi.Router
	*profileAPI
}

// newProfileViews returns an new instance of ProfileViews.
func newProfileViews(api *profileAPI) *profileViews {
	r := server.AuthenticatedRouter(server.WithRedirectLogin)
	v := &profileViews{r, api}

	r.With(server.WithPermission("profile", "read")).Group(func(r chi.Router) {
		r.Get("/", v.userProfile)
		r.Get("/password", v.userPassword)
	})
	r.With(server.WithPermission("profile", "export")).Group(func(r chi.Router) {
		r.Get("/export", v.exportData)
		r.Post("/export", v.exportData)
	})

	r.With(server.WithPermission("profile", "write")).Group(func(r chi.Router) {
		r.Post("/", v.userProfile)
		r.Post("/password", v.userPassword)
		r.Get("/otp", v.userTOTP)
		r.Post("/otp", v.userTOTP)
		r.Post("/session", v.userSession)
	})
	r.With(server.WithPermission("profile", "import")).Group(func(r chi.Router) {
		r.Get("/import", v.importData)
		r.Post("/import", v.importData)
	})

	r.With(server.WithPermission("profile:tokens", "read")).Group(func(r chi.Router) {
		r.With(api.withTokenList(clientToken)).Get("/applications", v.applicationList)
		r.With(api.withTokenList(userToken)).Get("/tokens", v.tokenList)
		r.With(api.withToken(userToken)).Get("/tokens/{uid}", v.tokenInfo)
	})

	r.With(server.WithPermission("profile:tokens", "write")).Group(func(r chi.Router) {
		r.Post("/tokens", v.tokenCreate)
		r.With(api.withToken(userToken)).Post("/tokens/{uid}", v.tokenInfo)
		r.With(api.withToken(anyToken)).Post("/tokens/{uid}/delete", v.tokenDelete)
	})

	return v
}

// userProfile handles GET and POST requests on /profile.
func (v *profileViews) userProfile(w http.ResponseWriter, r *http.Request) {
	tr := server.Locale(r)
	user := auth.GetRequestUser(r)
	f := forms.New[profileForm](r.Context(), withProfileUser(user))

	if r.Method == http.MethodPost {
		forms.Bind(r, f)
		if f.IsValid() {
			if _, err := f.update(); err != nil {
				server.Log(r).Error("", slog.Any("err", err))
			} else {
				// Set the new seed in the session.
				// We needn't save the session since AddFlash does that already.
				sess := server.GetSession(r)
				sess.Payload.Seed = user.Seed
				server.AddFlash(w, r, "success", tr.Gettext("Profile updated."))
				server.Redirect(w, r, "profile")
				return
			}
		}
		w.WriteHeader(http.StatusUnprocessableEntity)
	}

	ctx := components.WithBreadcrumb(r.Context(), [][2]string{
		{tr.Gettext("Profile")},
	})

	server.RenderComponent(w, r.WithContext(ctx), http.StatusOK, Views{}.userProfile(f))
}

// userPassword handles GET and POST requests on /profile/password.
func (v *profileViews) userPassword(w http.ResponseWriter, r *http.Request) {
	tr := server.Locale(r)
	user := auth.GetRequestUser(r)
	f := forms.New[changePasswordForm](r.Context())

	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			server.Err(w, r, err)
			return
		}

		switch r.Form.Get("action") {
		case "change":
			if user.Locked() {
				server.Status(w, r, http.StatusForbidden)
				return
			}
			forms.Bind(r, f)
			if f.IsValid() {
				if err := f.update(); err != nil {
					server.Log(r).Error("", slog.Any("err", err))
				} else {
					// Set the new seed in the session.
					// We needn't save the session since AddFlash does it already.
					sess := server.GetSession(r)
					sess.Payload.Seed = user.Seed
					server.AddFlash(w, r, "success", tr.Gettext("Your password was changed."))
					server.Redirect(w, r, "password")
					return
				}
			}
		case "remove-totp":
			user.TOTPSecret = nil
			if err := user.Save(); err != nil {
				server.Err(w, r, err)
				return
			}
			server.AddFlash(w, r, "success", tr.Gettext("Your verification code was removed."))
			server.Redirect(w, r, "password")
			return
		}

		w.WriteHeader(http.StatusUnprocessableEntity)
	}

	ctx := components.WithBreadcrumb(r.Context(), [][2]string{
		{tr.Gettext("Profile"), urls.AbsoluteURL(r, "/profile").String()},
		{tr.Gettext("Security")},
	})

	server.RenderComponent(w, r.WithContext(ctx), http.StatusOK, Views{}.userPassword(f))
}

func (v *profileViews) userTOTP(w http.ResponseWriter, r *http.Request) {
	tr := server.Locale(r)
	user := auth.GetRequestUser(r)
	f := forms.New[totpForm](r.Context())

	switch r.Method {
	case http.MethodGet:
		f.generate()

	case http.MethodPost:
		forms.Bind(r, f)
		if !f.IsValid() {
			w.WriteHeader(http.StatusUnprocessableEntity)
			break
		}

		if err := f.save(); err != nil {
			server.Err(w, r, err)
			return
		}

		sess := server.GetSession(r)
		sess.Payload.Seed = user.Seed
		server.AddFlash(w, r, "success", tr.Gettext("Verification Code is now enabled."))
		server.Redirect(w, r, "/profile/password")
		return
	}

	ctx := components.WithBreadcrumb(r.Context(), [][2]string{
		{tr.Gettext("Profile"), urls.AbsoluteURL(r, "/profile").String()},
		{tr.Gettext("Security"), urls.AbsoluteURL(r, "/profile/password").String()},
		{tr.Gettext("Verification Code")},
	})

	server.RenderComponent(w, r.WithContext(ctx), http.StatusOK, Views{}.setupTOTP(f))
}

// userSession handles changes of user session preferences.
// This returns an API response but since it only works with a SessionAuthProvider
// it makes more sense to have it in the views.
func (v *profileViews) userSession(w http.ResponseWriter, r *http.Request) {
	_, ok := auth.GetRequestProvider(r).(*server.SessionAuthProvider)
	if !ok {
		server.TextMsg(w, r, http.StatusBadRequest, "invalid authentication provider")
		return
	}

	f := forms.BindAs[sessionPrefForm](r)

	if !f.IsValid() {
		server.Render(w, r, http.StatusUnprocessableEntity, f)
		return
	}

	sess := server.GetSession(r)
	updated, err := f.update(sess.Payload)
	if err != nil {
		server.Err(w, r, err)
		return
	}

	sess.Save(w, r)
	server.Render(w, r, http.StatusOK, updated)
}

func (v *profileViews) applicationList(w http.ResponseWriter, r *http.Request) {
	tl := getTokenList(r.Context())
	tr := server.Locale(r)
	ctx := components.WithBreadcrumb(r.Context(), [][2]string{
		{tr.Gettext("Profile"), urls.AbsoluteURL(r, "/profile").String()},
		{tr.Gettext("Applications")},
	})

	server.RenderComponent(w, r.WithContext(ctx), http.StatusOK, Views{}.applicationList(tl))
}

func (v *profileViews) tokenList(w http.ResponseWriter, r *http.Request) {
	tl := getTokenList(r.Context())
	tr := server.Locale(r)
	ctx := components.WithBreadcrumb(r.Context(), [][2]string{
		{tr.Gettext("Profile"), urls.AbsoluteURL(r, "/profile").String()},
		{tr.Gettext("API Tokens")},
	})

	server.RenderComponent(w, r.WithContext(ctx), http.StatusOK, Views{}.tokenList(tl))
}

func (v *profileViews) tokenCreate(w http.ResponseWriter, r *http.Request) {
	t := &tokens.Token{
		UserID:      &auth.GetRequestUser(r).ID,
		IsEnabled:   true,
		Application: "internal",
	}
	tr := server.Locale(r)
	if err := tokens.Tokens.Create(t); err != nil {
		server.Log(r).Error("server error", slog.Any("err", err))
		server.AddFlash(w, r, "error", tr.Gettext("An error occurred while creating your token."))
		server.Redirect(w, r, "tokens")
		return
	}

	server.AddFlash(w, r, "success", tr.Gettext("New token created."))
	server.Redirect(w, r, ".", t.UID)
}

func (v *profileViews) tokenInfo(w http.ResponseWriter, r *http.Request) {
	tr := server.Locale(r)
	ti := getToken(r.Context())
	f := forms.New[tokenForm](r.Context(), withTokenInfo(ti.Token))

	if r.Method == http.MethodPost {
		forms.Bind(r, f)
		if f.IsValid() {
			if err := f.update(ti.Token); err != nil {
				server.Log(r).Error("", slog.Any("err", err))
			} else {
				server.AddFlash(w, r, "success", tr.Gettext("Token was updated."))
				server.Redirect(w, r, ti.UID)
				return
			}
		}
		w.WriteHeader(http.StatusUnprocessableEntity)
	}

	encoded, err := configs.Keys.TokenKey().Encode(ti.UID)
	if err != nil {
		server.Status(w, r, http.StatusInternalServerError)
		return
	}

	ctx := components.WithBreadcrumb(r.Context(), [][2]string{
		{tr.Gettext("Profile"), urls.AbsoluteURL(r, "/profile").String()},
		{tr.Gettext("API Tokens"), urls.AbsoluteURL(r, "/profile/tokens").String()},
		{ti.UID},
	})

	server.RenderComponent(w, r.WithContext(ctx), http.StatusOK, Views{}.tokenInfo(f, ti, encoded))
}

func (v *profileViews) tokenDelete(w http.ResponseWriter, r *http.Request) {
	f := forms.New[deleteTokenForm](r.Context())
	f.To.Set("/profile/tokens")
	forms.Bind(r, f)

	ti := getToken(r.Context())
	if err := f.trigger(ti.Token); err != nil {
		server.Err(w, r, err)
		return
	}
	server.Redirect(w, r, f.To.Value())
}

func (v *profileViews) exportData(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		u := auth.GetRequestUser(r)
		ex, err := portability.NewSingleUserExporter(w, u)
		if err != nil {
			server.Err(w, r, err)
			return
		}
		defer ex.Close() //nolint:errcheck

		ex.SetLogger(func(s string, a ...any) {
			server.Log(r).Info(fmt.Sprintf(s, a...))
		})

		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", fmt.Sprintf(
			`attachment; filename="readeck-%s-%s.zip"`,
			u.Username,
			time.Now().UTC().Format("20060102-1504"),
		),
		)
		if err = portability.Export(ex); err != nil {
			server.Err(w, r, err)
		}
		return
	}

	tr := server.Locale(r)
	ctx := components.WithBreadcrumb(r.Context(), [][2]string{
		{tr.Gettext("Profile"), urls.AbsoluteURL(r, "/profile").String()},
		{tr.Gettext("Export")},
	})

	server.RenderComponent(w, r.WithContext(ctx), http.StatusOK, Views{}.exportProfile())
}

func (v *profileViews) importData(w http.ResponseWriter, r *http.Request) {
	tr := server.Locale(r)
	f := forms.New[importForm](r.Context())

	if r.Method == http.MethodPost {
		forms.Bind(r, f)

		if f.IsValid() {
			if err := f.load(r); err != nil {
				server.Log(r).Error("", slog.Any("err", err))
				f.Data.AddErrors(err)
			} else {
				server.AddFlash(w, r, "success", tr.Gettext("Profile imported."))
				server.Redirect(w, r, "/profile")
				return
			}
			return
		}
		w.WriteHeader(http.StatusUnprocessableEntity)
	}

	ctx := components.WithBreadcrumb(r.Context(), [][2]string{
		{tr.Gettext("Profile"), urls.AbsoluteURL(r, "/profile").String()},
		{tr.Gettext("Import")},
	})

	server.RenderComponent(w, r.WithContext(ctx), http.StatusOK, Views{}.importProfile(f))
}
