// SPDX-FileCopyrightText: © 2021 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"strings"

	"github.com/a-h/templ"

	"codeberg.org/readeck/readeck/internal/auth"
	"codeberg.org/readeck/readeck/internal/auth/users"
	"codeberg.org/readeck/readeck/internal/profile/preferences"
	"codeberg.org/readeck/readeck/pkg/ctxr"
)

type (
	ctxRequestKey     struct{}
	ctxUserKey        struct{}
	ctxPreferencesKey struct{}
)

var (
	withRequest = ctxr.Setter[*http.Request](ctxRequestKey{})
	// GetRequest returns the request.
	GetRequest = ctxr.Getter[*http.Request](ctxRequestKey{})

	withUser = ctxr.Setter[*users.User](ctxUserKey{})
	// GetUser returns a [users.User].
	GetUser = ctxr.Getter[*users.User](ctxUserKey{})

	withPreferences = ctxr.Setter[*preferences.Preferences](ctxPreferencesKey{})
	// GetPreferences returns the user's [preferences.Preferences].
	GetPreferences = ctxr.Getter[*preferences.Preferences](ctxPreferencesKey{})
)

// componentContext returns a [context.Context] with some values
// needed for component rendering.
// The resulting context always contains the request and its user.
func componentContext(r *http.Request) context.Context {
	ctx := r.Context()
	ctx = withUser(ctx, auth.GetRequestUser(r))
	ctx = withRequest(ctx, r)
	ctx = withPreferences(ctx, &preferences.Preferences{}) // lazy loaded
	return ctx
}

// RenderComponent renders a [templ.Component] in the response's writer.
func RenderComponent(
	w http.ResponseWriter, r *http.Request,
	status int, component templ.Component,
) {
	if w.Header().Get("content-type") == "" {
		w.Header().Set("content-type", "text/html; charset=utf-8")
	}
	w.WriteHeader(status)

	// Set some context information
	if err := component.Render(componentContext(r), w); err != nil {
		Err(w, r, err)
		return
	}
}

// RenderTurboStreamComponent yields an HTML response with turbo-stream content-type using the
// given component. The template result is enclosed in a turbo-stream tag
// with action and target as specified.
// You can call this method as many times as needed to output several turbo-stream tags
// in the same HTTP response.
func RenderTurboStreamComponent(
	w http.ResponseWriter, r *http.Request,
	component templ.Component,
	action, target string,
	attrs map[string]string,
) {
	extraAttrs := new(strings.Builder)
	for k, v := range attrs {
		extraAttrs.WriteString(k + `="` + html.EscapeString(v) + `" `)
	}

	log := Log(r).With(
		slog.String("action", action),
		slog.String("target", target),
		slog.Any("attrs", attrs),
	)

	w.Header().Set("Content-Type", "text/vnd.turbo-stream.html; charset=utf-8")
	fmt.Fprintf(w, `<turbo-stream action="%s" %starget="%s"><template>%s`, action, extraAttrs, target, "\n")
	err := component.Render(componentContext(r), w)
	fmt.Fprint(w, "</template></turbo-stream>\n\n")
	if err != nil {
		log.Error("turbo stream", slog.Any("err", err))
		return
	}

	log.Debug("turbo stream")
}
