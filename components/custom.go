// SPDX-FileCopyrightText: © 2026 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package components

import (
	"context"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"

	"github.com/a-h/templ"

	"codeberg.org/readeck/readeck/configs"
)

var customTemplates *template.Template

// InitCustomTemplates initializes the custom templates.
// This feature is experimental.
func InitCustomTemplates() {
	if configs.Config.Customize.ExtraTemplates == "" {
		return
	}

	r, err := os.OpenRoot(configs.Config.Customize.ExtraTemplates)
	if err != nil {
		slog.Warn("custom templates", slog.Any("err", err))
		return
	}

	customTemplates = template.New("")
	customTemplates.Funcs(template.FuncMap{
		"url":      URL,
		"asset":    Asset,
		"cspnonce": CSPNonce,
	})

	err = fs.WalkDir(r.FS(), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".tmpl" {
			return nil
		}

		b, e := r.ReadFile(path)
		if e != nil {
			return e
		}
		t := customTemplates.New(path)
		if _, err := t.Parse(string(b)); err != nil {
			return err
		}
		slog.Debug("loaded custom template", slog.String("name", path))

		return nil
	})
	if err != nil {
		slog.Warn("custom templates", slog.Any("err", err))
		return
	}
}

// HasCustomTemplate returns whether a custom template exists.
func HasCustomTemplate(name string) bool {
	return customTemplates != nil && customTemplates.Lookup(name) != nil
}

// CustomTemplate returns a component from a defined custom template.
func CustomTemplate(name string, data map[string]any) templ.Component {
	if customTemplates == nil {
		return templ.NopComponent
	}

	return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		t := customTemplates.Lookup(name)
		if t == nil {
			return nil
		}

		tdata := map[string]any{
			"ctx": ctx,
		}
		maps.Copy(tdata, data)

		return t.Execute(w, tdata)
	})
}
