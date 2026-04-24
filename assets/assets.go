// SPDX-FileCopyrightText: © 2021 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package assets contains Readeck's static files and templates.
package assets

import (
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
)

// Assets contains all the static files needed by the app
//
//go:embed www www/*
var Assets embed.FS

// StaticFilesFS returns the assets "www" subfolder as an HTTP
// filesystem.
func StaticFilesFS() http.FileSystem {
	sub, err := fs.Sub(Assets, "www")
	if err != nil {
		panic(err)
	}
	return http.FS(sub)
}

var assetMap map[string]string

func init() {
	f, err := Assets.Open("www/manifest.json")
	if err != nil {
		panic(err)
	}
	defer f.Close() //nolint:errcheck
	if err := json.NewDecoder(f).Decode(&assetMap); err != nil {
		panic(err)
	}
}

// AssetMap returns the loaded asset map.
func AssetMap() map[string]string {
	return assetMap
}
