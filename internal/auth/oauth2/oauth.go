// SPDX-FileCopyrightText: © 2025 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package oauth2 provides the routes to authorize a client and retrieve an API token.
package oauth2

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"codeberg.org/readeck/readeck/internal/server"
	"codeberg.org/readeck/readeck/pkg/ctxr"
	"codeberg.org/readeck/readeck/pkg/forms"
)

type (
	ctxTokenFormKey struct{}
)

var withTokenForm, getTokenForm = ctxr.WithGetter[*tokenForm](ctxTokenFormKey{})

type tokenResponse struct {
	UID         string `json:"id"`
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Scope       string `json:"scope,omitempty"`
}

// oauthAPI contains the OAuth API routes.
type oauthAPI struct {
	chi.Router
}

func newOAuthAPI() *oauthAPI {
	router := &oauthAPI{chi.NewRouter()}
	router.Post("/client", router.clientCreate)
	router.Post("/device", router.deviceHandler)
	router.Post("/token", router.tokenHandler)

	// Revocation route is authenticated
	revoke := server.AuthenticatedRouter()
	revoke.Post("/", router.revokeToken)
	router.Mount("/revoke", revoke)

	return router
}

func (api *oauthAPI) tokenHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")

	f := forms.BindAs[tokenForm](r)

	if !f.IsValid() {
		server.Err(w, r, newFormError(f))
		return
	}

	ctx := withTokenForm(r.Context(), f)
	r = r.WithContext(ctx)

	switch f.GrantType.Value() {
	case grantTypeAuthCode:
		api.authorizationCodeHandler(w, r)
	case grantTypeDeviceCode:
		api.deviceCodeHandler(w, r)
	}
}

func (api *oauthAPI) revokeToken(w http.ResponseWriter, r *http.Request) {
	f := forms.BindAs[revokeTokenForm](r)

	if !f.IsValid() {
		server.Err(w, r, newFormError(f))
		return
	}

	if err := f.revoke(r); err != nil {
		switch err.(type) {
		case oauthError:
			server.Err(w, r, err)
		default:
			server.Err(w, r, errServerError.withError(err))
		}
		return
	}

	server.Render(w, r, http.StatusOK, map[string]string{})
}
