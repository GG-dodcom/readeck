// SPDX-FileCopyrightText: © 2023 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"net/http"

	"codeberg.org/readeck/readeck/internal/auth"
	"codeberg.org/readeck/readeck/locales"
	"codeberg.org/readeck/readeck/pkg/ctxr"
	"codeberg.org/readeck/readeck/pkg/forms"
)

type (
	ctxLocaleKey struct{}
)

var withLocale, getLocale = ctxr.WithChecker[*locales.Locale](ctxLocaleKey{})

// LoadLocale is a middleware that loads the correct locale for the current user.
// It defaults to English if no user is connected or no language is set.
func LoadLocale(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := auth.GetRequestUser(r)
		lang := "en"
		var tr *locales.Locale
		if !user.IsAnonymous() {
			lang = user.Lang()
		} else {
			// No user connected, used the browser preference
			lang = r.Header.Get("accept-language")
		}

		tr = locales.LoadTranslation(lang)
		ctx := withLocale(r.Context(), tr)
		ctx = forms.WithTranslator(ctx, tr)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Locale returns the current request's locale.
func Locale(r *http.Request) *locales.Locale {
	return LocaleContext(r.Context())
}

// LocaleContext returns the given context's locale or defaults to en-US.
func LocaleContext(ctx context.Context) *locales.Locale {
	if t, ok := getLocale(ctx); ok {
		return t
	}
	return locales.LoadTranslation("en")
}
