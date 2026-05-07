// SPDX-FileCopyrightText: © 2021 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package cookbook

import (
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
	f := forms.New[cookbookForm](r.Context())
	ef := forms.BindAs[cookbookForm](r)

	server.RenderComponent(w, r, 200, Views{}.ui(f, ef))
}

func (v *cookbookViews) extractView(w http.ResponseWriter, r *http.Request) {
	f := forms.BindAs[extractForm](r)

	var res *extractResult
	var html io.Reader

	if f.URL.IsBound() && f.IsValid() {
		ex := v.getExtractor(f.URL.Value(), r)
		res = v.getExtractResult(ex)
		html = strings.NewReader(bookmarks.ExtractHTMLBody(res.HTML))
	}

	server.RenderComponent(w, r, 200, Views{}.extract(f, res, html))
}

type cookbookForm struct {
	forms.Form
	Text     forms.TextField     `json:"text"       validate:"required is_email"`
	Select   forms.TextField     `json:"select"     validate:"single_choice"`
	Choices  forms.TextListField `json:"checkboxes" validate:"required multiple_choices"`
	Checkbox forms.BooleanField  `json:"checkbox"`
	File     forms.FileField     `json:"file"`
}

func (f *cookbookForm) GetTaggedValidator(name, _ string, tc *forms.TagContext) (forms.Validator, bool) {
	switch name {
	case "single_choice":
		forms.Choices(tc.Field,
			forms.Choice("Choice 1", "1"),
			forms.Choice("Choice 2", "2"),
			forms.Choice("Choice 3", "3"),
		)
		return nil, true
	case "multiple_choices":
		forms.Choices(tc.Field,
			forms.Choice("Choice A", "a"),
			forms.Choice("Choice B", "b"),
			forms.Choice("Choice C", "c"),
		)
		return nil, true
	}
	return nil, false
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
