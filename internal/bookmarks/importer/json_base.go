// SPDX-FileCopyrightText: © 2025 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package importer

import (
	"context"
	"encoding/json"
	"io"

	"codeberg.org/readeck/readeck/pkg/forms/v2"
)

type jsonBaseAdapter[S any, T BookmarkImporter] struct {
	idx       int
	Items     []T
	loadItems func(*S) ([]T, error)
}

func (adapter *jsonBaseAdapter[S, T]) Form(ctx context.Context) importBinder {
	return forms.New[FileImportForm](ctx)
}

func (adapter *jsonBaseAdapter[S, T]) Params(form forms.FormBinder) ([]byte, error) {
	if !form.IsValid() {
		return nil, nil
	}
	f := form.(*FileImportForm)

	reader, err := f.Data.Value().Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close() //nolint:errcheck

	dec := json.NewDecoder(reader)
	data := new(S)
	if err := dec.Decode(data); err != nil {
		f.Data.AddErrors(errInvalidFile)
		return nil, nil
	}

	if adapter.Items, err = adapter.loadItems(data); err != nil {
		f.Data.AddErrors(err)
		return nil, nil
	}

	return json.Marshal(adapter)
}

func (adapter *jsonBaseAdapter[S, T]) LoadData(data []byte) error {
	return json.Unmarshal(data, adapter)
}

func (adapter *jsonBaseAdapter[S, T]) Next() (BookmarkImporter, error) {
	if adapter.idx+1 > len(adapter.Items) {
		return nil, io.EOF
	}

	adapter.idx++
	return adapter.Items[adapter.idx-1], nil
}
