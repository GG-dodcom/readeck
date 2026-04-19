// SPDX-FileCopyrightText: © 2023 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package docs handles Readeck's documentation files and HTTP routes.
package docs

import (
	"embed"
	"encoding/json"
	"hash"
	"io/fs"
	"net/http"
)

// Assets is the [embed.FS] with documentation assets.
//
//go:embed assets assets/* licenses/*
var Assets embed.FS

// Files contains all the generated help files as an http.FS instance.
var Files http.FileSystem

// File is a documentation file.
type File struct {
	Route      string         `json:"route"`
	Aliases    []string       `json:"aliases"`
	File       string         `json:"file"`
	Etag       string         `json:"etag"`
	IsDocument bool           `json:"is_document"`
	Title      string         `json:"title"`
	Meta       map[string]any `json:"meta"`
}

// Section is a documentation language section.
type Section struct {
	Files map[string]*File `json:"files"`
	TOC   [][2]string      `json:"toc"`
}

// ManifestFile is the documentation files manifest.
type ManifestFile struct {
	Files    map[string]*File    `json:"files"`
	Sections map[string]*Section `json:"sections"`
}

// Manifest is the documentation manifest contents.
var Manifest *ManifestFile

// UpdateEtag implements the [server.Etagger] interface.
func (f *File) UpdateEtag(h hash.Hash) {
	h.Write([]byte(f.Etag))
}

func init() {
	sub, err := fs.Sub(Assets, "assets")
	if err != nil {
		panic(err)
	}
	Files = http.FS(sub)

	// Load manifest
	fd, err := Assets.Open("assets/manifest.json")
	if err != nil {
		panic(err)
	}

	dec := json.NewDecoder(fd)
	err = dec.Decode(&Manifest)
	if err != nil {
		panic(err)
	}
}
