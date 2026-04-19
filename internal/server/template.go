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
	"os"
	"reflect"
	"strings"

	"github.com/CloudyKit/jet/v6"
	"github.com/a-h/templ"

	"codeberg.org/readeck/readeck/configs"
	"codeberg.org/readeck/readeck/internal/auth"
	"codeberg.org/readeck/readeck/internal/auth/users"
	"codeberg.org/readeck/readeck/internal/profile/preferences"
	"codeberg.org/readeck/readeck/internal/server/urls"
	"codeberg.org/readeck/readeck/internal/templates"
	"codeberg.org/readeck/readeck/pkg/ctxr"
	"codeberg.org/readeck/readeck/pkg/glob"
	"codeberg.org/readeck/readeck/pkg/libjet"
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

// TC is a simple type to carry template context.
type TC map[string]any

// SetBreadcrumbs sets the current page's breadcrumbs.
func (tc TC) SetBreadcrumbs(items [][2]string) {
	tc["Breadcrumbs"] = items
}

// views holds all the views (templates).
var views *jet.Set

// GetTemplate returns a template from the current views.
func GetTemplate(name string) (*jet.Template, error) {
	return views.GetTemplate(name)
}

// RenderTemplate yields an HTML response using the given template and context.
func RenderTemplate(w http.ResponseWriter, r *http.Request,
	status int, name string, ctx TC,
) {
	t, err := views.GetTemplate(name)
	if err != nil {
		Err(w, r, err)
		return
	}

	if w.Header().Get("content-type") == "" {
		w.Header().Set("content-type", "text/html; charset=utf-8")
	}
	w.WriteHeader(status)

	if err = t.Execute(w, TemplateVars(r), ctx); err != nil {
		panic(err)
	}
}

// RenderTurboStream yields an HTML response with turbo-stream content-type using the
// given template and context. The template result is enclosed in a turbo-stream
// tag with action and target as specified.
// You can call this method as many times as needed to output several turbo-stream tags
// in the same HTTP response.
func RenderTurboStream(
	w http.ResponseWriter, r *http.Request,
	name, action, target string, ctx any,
	attrs map[string]string,
) {
	t, err := views.GetTemplate(name)
	if err != nil {
		Err(w, r, err)
		return
	}

	extraAttrs := new(strings.Builder)
	for k, v := range attrs {
		extraAttrs.WriteString(k + `="` + html.EscapeString(v) + `" `)
	}

	w.Header().Set("Content-Type", "text/vnd.turbo-stream.html; charset=utf-8")

	fmt.Fprintf(w, `<turbo-stream action="%s" %starget="%s"><template>%s`, action, extraAttrs, target, "\n")
	if err = t.Execute(w, TemplateVars(r), ctx); err != nil {
		panic(err)
	}
	fmt.Fprint(w, "</template></turbo-stream>\n\n")

	Log(r).Debug("turbo stream",
		slog.String("name", name),
		slog.String("action", action),
		slog.String("target", target),
	)
}

// initTemplates add global functions to the views.
func initTemplates() {
	views = templates.Catalog(
		os.DirFS(configs.Config.Customize.ExtraTemplates),
	)

	views.AddGlobalFunc("assetURL", func(args jet.Arguments) reflect.Value {
		args.RequireNumOfArguments("assetURL", 1, 1)
		name := args.Get(0).String()
		r := args.Runtime().Resolve("request").Interface().(*http.Request)

		return reflect.ValueOf(urls.PathOnly(urls.AssetURL(r, name)))
	})
	views.AddGlobalFunc("urlFor", func(args jet.Arguments) reflect.Value {
		parts := make([]string, args.NumOfArguments())
		for i := 0; i < args.NumOfArguments(); i++ {
			parts[i] = libjet.ToString(args.Get(i))
		}

		r := args.Runtime().Resolve("request").Interface().(*http.Request)
		return reflect.ValueOf(urls.PathOnly(urls.AbsoluteURL(r, parts...)))
	})
	views.AddGlobalFunc("pathIs", func(args jet.Arguments) reflect.Value {
		r := args.Runtime().Resolve("request").Interface().(*http.Request)
		cp := "/" + strings.TrimPrefix(r.URL.Path, urls.Prefix())
		for i := 0; i < args.NumOfArguments(); i++ {
			if glob.Glob(fmt.Sprintf("%v", args.Get(i)), cp) {
				return reflect.ValueOf(true)
			}
		}
		return reflect.ValueOf(false)
	})
	views.AddGlobalFunc("hasPermission", func(args jet.Arguments) reflect.Value {
		args.RequireNumOfArguments("hasPermission", 2, 2)
		obj := libjet.ToString(args.Get(0))
		act := libjet.ToString(args.Get(1))
		r, ok := args.Runtime().Resolve("request").Interface().(*http.Request)
		if !ok {
			return reflect.ValueOf(false)
		}
		return reflect.ValueOf(auth.HasPermission(r, obj, act))
	})
}

// TemplateVars returns the default variables set for a template
// in the request's context.
func TemplateVars(r *http.Request) jet.VarMap {
	cspNonce, _ := GetCSPNonce(r.Context())
	tr := Locale(r)

	user := auth.GetRequestUser(r)
	session := GetSession(r)

	return make(jet.VarMap).
		Set("basePath", urls.Prefix()).
		Set("currentPath", urls.CurrentPath(r)).
		Set("isTurbo", IsTurboRequest(r)).
		Set("request", r).
		Set("cspNonce", cspNonce).
		Set("user", user).
		Set("preferences", preferences.New(user, session)).
		Set("flashes", Flashes(r)).
		Set("translator", tr).
		Set("gettext", tr.Gettext).
		Set("ngettext", tr.Ngettext).
		Set("pgettext", tr.Pgettext).
		Set("npgettext", tr.Npgettext)
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
