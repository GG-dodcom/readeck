// SPDX-FileCopyrightText: © 2021 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package cookbook

import (
	"context"
	"io"
	"iter"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"

	"codeberg.org/readeck/readeck/internal/bookmarks"
	"codeberg.org/readeck/readeck/internal/server"
	"codeberg.org/readeck/readeck/pkg/forms"
)

type cookbookViews struct {
	chi.Router
	*cookbookAPI
}

func newCookbookViews(api *cookbookAPI) *cookbookViews {
	r := server.AuthenticatedRouter(server.WithRedirectLogin)
	v := &cookbookViews{r, api}

	r.With(server.WithPermission("cookbook", "read")).Group(func(r chi.Router) {
		r.Get("/", func(w http.ResponseWriter, r *http.Request) {
			server.RenderComponent(w, r, 200, Views{}.prose())
		})
		r.Get("/colors", func(w http.ResponseWriter, r *http.Request) {
			server.RenderComponent(w, r, 200, Views{}.colors())
		})
		r.Get("/ui", v.uiView)
		r.Get("/extract", v.extractView)
	})

	return v
}

func (v *cookbookViews) uiView(w http.ResponseWriter, r *http.Request) {
	f := newCookbookForm()
	ef := newCookbookForm()
	forms.BindURL(ef, r)
	ef.IsValid()

	server.RenderComponent(w, r, 200, Views{}.ui(f, ef))
}

func (v *cookbookViews) extractView(w http.ResponseWriter, r *http.Request) {
	f := newExtractForm()
	forms.BindURL(f, r)

	var res *extractResult
	var html io.Reader

	if f.IsValid() && f.Get("url").String() != "" {
		ex := v.getExtractor(f.Get("url").String(), r)
		res = v.getExtractResult(ex)
		html = strings.NewReader(bookmarks.ExtractHTMLBody(res.HTML))
	}

	server.RenderComponent(w, r, 200, Views{}.extract(f, res, html))
}

func newCookbookForm() *forms.Form {
	return forms.Must(
		context.Background(),
		forms.NewTextField("text", forms.Required, forms.IsEmail),
		forms.NewTextField("select", forms.Default("choice 2"), forms.Choices(
			forms.Choice("Choice 1", "choice 1"),
			forms.Choice("Choice 2", "choice 2"),
			forms.Choice("Choice 3", "choice 3"),
		)),
		forms.NewTextListField("choices", forms.Default([]string{"b"}), forms.Required, forms.Choices(
			forms.Choice("Choice A", "a"),
			forms.Choice("Choice B", "b"),
			forms.Choice("Choice C", "c"),
		)),
	)
}

func orderedMap[T any](data map[string]T) iter.Seq2[string, T] {
	return func(yield func(string, T) bool) {
		keys := slices.SortedFunc(maps.Keys(data), strings.Compare)
		for _, k := range keys {
			if !yield(k, data[k]) {
				return
			}
		}
	}
}
