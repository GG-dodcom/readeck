// SPDX-FileCopyrightText: © 2021 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package routes

import (
	"errors"
	"maps"
	"net/http"
	"slices"
	"time"

	"codeberg.org/readeck/readeck/internal/bookmarks"
	"codeberg.org/readeck/readeck/internal/bookmarks/tasks"
	"codeberg.org/readeck/readeck/pkg/forms/v2"
)

type collectionDeleteForm struct {
	forms.Form
	Cancel forms.BooleanField `json:"cancel"`
	To     forms.TextField    `json:"_to"    validate:"max_len:512"`
}

func (f *collectionDeleteForm) trigger(c *bookmarks.Collection) error {
	if f.Cancel.Value() {
		return tasks.DeleteCollectionTask.Cancel(c.ID)
	}

	return tasks.DeleteCollectionTask.Run(c.ID, c.ID)
}

type collectionForm struct {
	// *forms.JoinedForms
	FilterForm
	Name     forms.TextField    `json:"name"      validate:"trim max_len:128"`
	IsPinned forms.BooleanField `json:"is_pinned"`
}

func newCollectionForm(r *http.Request) *collectionForm {
	f := forms.New[collectionForm](r.Context())
	switch r.Method {
	case http.MethodPatch:
		f.Name.SetValidators(append(f.Name.Validators(), forms.RequiredOrNil))
	case http.MethodPost:
		f.Name.SetValidators(append(f.Name.Validators(), forms.Required))
	}
	return f
}

func (f *collectionForm) setCollection(c *bookmarks.Collection) {
	// Regular values
	f.Name.Set(c.Name)
	f.IsPinned.Set(c.IsPinned)

	// Filters
	f.fromFilters(&c.Filters)
}

func (f *collectionForm) createCollection(userID int) (*bookmarks.Collection, error) {
	var err error
	defer func() {
		if err != nil {
			f.AddErrors(forms.ErrUnexpected)
		}
	}()

	if !f.IsBound() {
		return nil, errors.New("form is not bound")
	}

	c := &bookmarks.Collection{
		UserID:  &userID,
		Name:    f.Name.Value(),
		Filters: f.toFilters(),
	}

	err = bookmarks.Collections.Create(c)
	return c, err
}

func (f *collectionForm) updateCollection(c *bookmarks.Collection) (res map[string]any, err error) {
	if !f.IsBound() {
		err = errors.New("form is not bound")
		return
	}

	res = map[string]any{}
	updateMap := map[string]any{}
	filtersA := f.toFilters().ToMap()
	filtersB := c.Filters.ToMap()

	needsFilters := false
	if f.Name.IsBound() && f.Name.Value() != c.Name {
		res["name"] = f.Name.Value()
	}
	if f.IsPinned.IsBound() && f.IsPinned.Value() != c.IsPinned {
		res["is_pinned"] = f.IsPinned.Value()
	}
	maps.Copy(updateMap, res)

	for k, v := range filtersA {
		switch x := v.(type) {
		case string:
			if x == filtersB[k] {
				continue
			}
		case []string:
			if slices.Equal(x, filtersB[k].([]string)) {
				continue
			}
		case *bool:
			y := filtersB[k].(*bool)
			if x == nil && y == nil {
				continue
			}
			if x != nil && y != nil && *x == *y {
				continue
			}
		}
		needsFilters = true
		res[k] = v
	}

	if needsFilters {
		updateMap["filters"] = f.toFilters() // bookmarks.NewFiltersFromForm(f)
	}

	if len(res) > 0 {
		res["updated"] = time.Now().UTC()
		updateMap["updated"] = res["updated"]
		if err = c.Update(updateMap); err != nil {
			f.AddErrors(forms.ErrUnexpected)
			return
		}
	}
	res["id"] = c.UID
	return
}
