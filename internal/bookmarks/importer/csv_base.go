// SPDX-FileCopyrightText: © 2025 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package importer

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"iter"
	"slices"

	"codeberg.org/readeck/readeck/pkg/csvstruct"
	"codeberg.org/readeck/readeck/pkg/forms"
)

type csvBaseAdapter[S any, T BookmarkImporter] struct {
	idx         int
	Items       []T
	openFileFn  func(forms.FileOpener) (iter.Seq2[*csv.Reader, error], error)
	buildItemFn func(*S) (T, error)
}

func (adapter *csvBaseAdapter[S, T]) Form(ctx context.Context) importBinder {
	return forms.New[FileImportForm](ctx)
}

func (adapter csvBaseAdapter[S, T]) Params(form forms.FormBinder) ([]byte, error) {
	if !form.IsValid() {
		return nil, nil
	}
	f := form.(*FileImportForm)

	// Open the iterator. Any error at this stage is fatal.
	seq, err := adapter.openFileFn(f.Data.Value())
	if err != nil {
		return nil, err
	}

	for cr, err := range seq {
		// Errors during iteration stop the process but with a message
		// in the form field.
		if err != nil {
			f.Data.AddErrors(errInvalidFile)
			return nil, nil
		}
		if err = adapter.loadItems(cr); err != nil {
			f.Data.AddErrors(err)
			return nil, nil
		}
	}

	if len(adapter.Items) == 0 {
		f.Data.AddErrors(errInvalidFile)
		return nil, nil
	}

	slices.Reverse(adapter.Items)
	return json.Marshal(adapter)
}

func (adapter *csvBaseAdapter[S, T]) loadItems(cr *csv.Reader) error {
	header, err := cr.Read()
	if err != nil {
		return errInvalidFile
	}
	scanner, err := csvstruct.NewScanner(header, new(S))
	if err != nil {
		return errInvalidFile
	}

	for {
		row, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}

		src := new(S)
		if err = scanner(row, src); err != nil {
			continue
		}

		item, err := adapter.buildItemFn(src)
		if err != nil {
			continue
		}

		// Ignore duplicates
		if slices.IndexFunc(adapter.Items, func(a T) bool {
			return a.URL() == item.URL()
		}) != -1 {
			continue
		}

		adapter.Items = append(adapter.Items, item)
	}

	return nil
}

func (adapter *csvBaseAdapter[S, T]) LoadData(data []byte) error {
	return json.Unmarshal(data, adapter)
}

func (adapter *csvBaseAdapter[S, T]) Next() (BookmarkImporter, error) {
	if adapter.idx+1 > len(adapter.Items) {
		return nil, io.EOF
	}

	adapter.idx++
	return adapter.Items[adapter.idx-1], nil
}

func csvOpenFile(fo forms.FileOpener) (iter.Seq2[*csv.Reader, error], error) {
	r, err := fo.Open()
	if err != nil {
		return nil, err
	}
	defer r.Close() //nolint:errcheck

	return func(yield func(*csv.Reader, error) bool) {
		yield(csv.NewReader(r), nil)
	}, nil
}
