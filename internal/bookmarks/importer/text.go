// SPDX-FileCopyrightText: © 2024 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package importer

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"slices"
	"strings"

	"codeberg.org/readeck/readeck/pkg/forms/v2"
)

type textAdapter struct {
	idx  int
	URLs []string `json:"url_list"`
}

func (adapter *textAdapter) Name(ctx context.Context) string {
	return forms.GetTranslator(ctx).Gettext("Text File")
}

func (adapter *textAdapter) Form(ctx context.Context) importBinder {
	return forms.New[FileImportForm](ctx)
}

func (adapter *textAdapter) Params(form forms.FormBinder) ([]byte, error) {
	if !form.IsValid() {
		return nil, nil
	}
	f := form.(*FileImportForm)

	reader, err := f.Data.Value().Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close() //nolint:errcheck

	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		b, err := newURLBookmark(strings.TrimSpace(scanner.Text()))
		if err == nil && b.URL() != "" && !slices.Contains(adapter.URLs, b.URL()) {
			adapter.URLs = append(adapter.URLs, b.URL())
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	if len(adapter.URLs) == 0 {
		f.Data.AddErrors(errInvalidFile)
		return nil, nil
	}

	slices.Reverse(adapter.URLs)
	return json.Marshal(adapter)
}

func (adapter *textAdapter) LoadData(data []byte) error {
	return json.Unmarshal(data, adapter)
}

func (adapter *textAdapter) Next() (BookmarkImporter, error) {
	if adapter.idx+1 > len(adapter.URLs) {
		return nil, io.EOF
	}

	adapter.idx++
	return newURLBookmark(adapter.URLs[adapter.idx-1])
}
