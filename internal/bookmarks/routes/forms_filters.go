// SPDX-FileCopyrightText: © 2026 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package routes

import (
	"context"
	"net/url"
	"strconv"

	"codeberg.org/readeck/readeck/internal/bookmarks"
	"codeberg.org/readeck/readeck/internal/searchstring"
	"codeberg.org/readeck/readeck/pkg/forms"
	"codeberg.org/readeck/readeck/pkg/timetoken"
)

// FilterForm is the form used for bookmark search and global filtering.
type FilterForm struct {
	forms.Form
	title        int
	noPagination bool
	fixedLimit   uint
	sq           searchstring.SearchQuery

	IsActive   forms.BooleanField  `json:"bf"          validate:"trim"`
	Search     forms.TextField     `json:"search"      validate:"trim"`
	Title      forms.TextField     `json:"title"       validate:"trim"`
	Author     forms.TextField     `json:"author"      validate:"trim"`
	Site       forms.TextField     `json:"site"        validate:"trim"`
	Type       forms.TextListField `json:"type"        validate:"bookmark_type_choices"`
	IsLoaded   forms.BooleanField  `json:"is_loaded"`
	HasErrors  forms.BooleanField  `json:"has_errors"`
	HasLabels  forms.BooleanField  `json:"has_labels"`
	Labels     forms.TextField     `json:"labels"      validate:"trim"`
	Note       forms.TextField     `json:"note"        validate:"trim"`
	ReadStatus forms.TextListField `json:"read_status" validate:"read_status_choices"`
	IsMarked   forms.BooleanField  `json:"is_marked"`
	IsArchived forms.BooleanField  `json:"is_archived"`
	RangeStart forms.TextField     `json:"range_start" validate:"time_token"`
	RangeEnd   forms.TextField     `json:"range_end"   validate:"time_token"`
	ID         forms.TextListField `json:"id"          validate:"trim discard_empty"`
}

// GetTaggedValidator adds specific validators to the [FilterForm].
func (f *FilterForm) GetTaggedValidator(name, _ string, tc *forms.TagContext) (forms.Validator, bool) {
	switch name {
	case "bookmark_type_choices":
		forms.Choices(tc.Field,
			forms.Choice(forms.GetTranslator(tc.Context).Gettext("Article"), "article"),
			forms.Choice(forms.GetTranslator(tc.Context).Gettext("Picture"), "photo"),
			forms.Choice(forms.GetTranslator(tc.Context).Gettext("Video"), "video"),
		)
		return nil, true
	case "read_status_choices":
		forms.Choices(tc.Field,
			forms.Choice(forms.GetTranslator(tc.Context).Pgettext("status", "Unviewed"), filtersReadStatusUnread),
			forms.Choice(forms.GetTranslator(tc.Context).Pgettext("status", "In-Progress"), filtersReadStatusReading),
			forms.Choice(forms.GetTranslator(tc.Context).Pgettext("status", "Completed"), filtersReadStatusRead),
		)
		return nil, true
	case "time_token":
		return forms.ValueValidatorFunc[string](func(_ forms.Binder, v string) error {
			if v == "" {
				return nil
			}
			if _, err := timetoken.New(v); err != nil {
				return forms.Gettext(`"%s" is not a valid date value`, v)
			}
			return nil
		}), true
	default:
		return nil, false
	}
}

func newFilterForm(ctx context.Context) *FilterForm {
	f := forms.New[FilterForm](ctx)
	f.title = filtersTitleUnset
	return f
}

// newContextFilterForm returns an instance of filterForm. If one already
// exists in the given context, it's reused, otherwise it returns a new one.
func newContextFilterForm(ctx context.Context) *FilterForm {
	ff, ok := checkFilterForm(ctx)
	if !ok {
		ff = newFilterForm(ctx)
	}

	return ff
}

// Validate provides custom validation.
func (f *FilterForm) Validate() error {
	// First, we must build a search string based on
	// the provided free form search and
	// what we might have in the following fields:
	// title, author, site, label
	f.sq = searchstring.ParseQuery(f.Search.Value())

	for name, field := range f.Fields() {
		var fname string
		switch name {
		case "title", "author", "site", "note":
			fname = name
		case "labels":
			fname = "label"
		}

		if fname == "" || field.String() == "" {
			continue
		}

		q := searchstring.ParseField(field.String(), fname)
		f.sq.Terms = append(f.sq.Terms, q.Terms...)
	}

	// Remove duplicates from the query
	f.sq = f.sq.Dedup()

	// Remove field definition for unallowed fields
	f.sq = f.sq.Unfield("title", "author", "site", "label", "note")

	// Update the specific search fields
	for name, field := range f.Fields() {
		fname := "-"
		switch name {
		case "search":
			fname = ""
		case "title", "author", "site", "note":
			fname = name
		case "labels":
			fname = "label"
		}

		if fname == "-" {
			continue
		}
		v := f.sq.ExtractField(fname).RemoveFieldInfo().String()
		if v != field.String() {
			_ = forms.UnmarshalValues([]string{v}, field)
		}
	}

	return nil
}

func (f *FilterForm) getQueryParams() url.Values {
	q := url.Values{}
	for name, field := range f.Fields() {
		if !field.IsBound() || field.IsEmpty() || field.IsNil() {
			continue
		}

		switch v := field.V().(type) {
		case string:
			q.Add(name, v)
		case []string:
			q[name] = v
		case bool:
			q.Add(name, strconv.FormatBool(v))
		}
	}

	return q
}

// setMarked sets the IsMarked property.
func (f *FilterForm) setMarked() {
	_ = forms.UnmarshalValues([]string{"true"}, &f.IsMarked)
	f.title = filtersTitleFavorites
}

// setArchived sets the IsArchived property.
func (f *FilterForm) setArchived(v bool) {
	_ = forms.UnmarshalValues([]string{strconv.FormatBool(v)}, &f.IsArchived)
	if v {
		f.title = filtersTitleArchived
	} else {
		f.title = filtersTitleUnread
	}
}

func (f *FilterForm) setType(v string) {
	_ = forms.UnmarshalValues([]string{v}, &f.Type)
	switch v {
	case "article":
		f.title = filtersTitleArticles
	case "photo":
		f.title = filtersTitlePictures
	case "video":
		f.title = filtersTitleVideos
	}
}

func (f *FilterForm) toFilters() bookmarks.Filters {
	boolOrNil := func(field *forms.BooleanField) *bool {
		if !field.IsBound() || field.IsNil() || field.IsEmpty() {
			return nil
		}
		return new(field.Value())
	}

	res := bookmarks.Filters{
		Search:     f.Search.Value(),
		Title:      f.Title.Value(),
		Author:     f.Author.Value(),
		Site:       f.Site.Value(),
		Type:       f.Type.Value(),
		Labels:     f.Labels.Value(),
		Note:       f.Note.Value(),
		ReadStatus: f.ReadStatus.Value(),
		IsMarked:   boolOrNil(&f.IsMarked),
		IsArchived: boolOrNil(&f.IsArchived),
		IsLoaded:   boolOrNil(&f.IsLoaded),
		HasErrors:  boolOrNil(&f.HasErrors),
		HasLabels:  boolOrNil(&f.HasLabels),
		RangeStart: f.RangeStart.Value(),
		RangeEnd:   f.RangeEnd.Value(),
	}

	res.Normalize()

	return res
}

// fromFilters updates the form's values using the filters'
// It panics if the form is bound.
func (f *FilterForm) fromFilters(filters *bookmarks.Filters) {
	if f.IsBound() {
		panic("FilterForm is already bound")
	}

	boolOrNil := func(field *forms.BooleanField, val *bool) {
		if val != nil {
			_ = forms.UnmarshalValues([]string{strconv.FormatBool(*val)}, field)
		}
	}

	f.Search.Set(filters.Search)
	f.Title.Set(filters.Title)
	f.Author.Set(filters.Author)
	f.Site.Set(filters.Site)
	f.Type.Set(filters.Type)
	f.Labels.Set(filters.Labels)
	f.Note.Set(filters.Note)
	f.ReadStatus.Set(filters.ReadStatus)
	boolOrNil(&f.IsMarked, filters.IsMarked)
	boolOrNil(&f.IsArchived, filters.IsArchived)
	boolOrNil(&f.IsLoaded, filters.IsLoaded)
	boolOrNil(&f.HasErrors, filters.HasErrors)
	boolOrNil(&f.HasLabels, filters.HasLabels)
	f.RangeStart.Set(filters.RangeStart)
	f.RangeEnd.Set(filters.RangeEnd)
}
