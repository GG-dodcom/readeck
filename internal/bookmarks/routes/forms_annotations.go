// SPDX-FileCopyrightText: © 2023 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package routes

import (
	"strings"
	"time"

	"github.com/go-shiori/dom"
	"golang.org/x/net/html"

	"codeberg.org/readeck/readeck/internal/bookmarks"
	"codeberg.org/readeck/readeck/internal/bookmarks/dataset"
	"codeberg.org/readeck/readeck/pkg/base58"
	"codeberg.org/readeck/readeck/pkg/forms/v2"
)

type annotationUpdateForm struct {
	forms.Form
	Color forms.TextField `json:"color" validate:"trim required max_len:32"`
	Note  forms.TextField `json:"note"  validate:"trim max_len:1024"`
}

type annotationForm struct {
	forms.Form
	StartSelector forms.TextField    `json:"start_selector" validate:"trim required max_len:256"`
	StartOffset   forms.IntegerField `json:"start_offset"   validate:"required gte:0"`
	EndSelector   forms.TextField    `json:"end_selector"   validate:"trim required max_len:256"`
	EndOffset     forms.IntegerField `json:"end_offset"     validate:"required gte:0"`
	Color         forms.TextField    `json:"color"          validate:"trim required max_len:32"`
	Note          forms.TextField    `json:"note"           validate:"trim max_len:1024"`
}

func (f *annotationForm) addToBookmark(bi *dataset.Bookmark) (*bookmarks.BookmarkAnnotation, error) {
	annotation := &bookmarks.BookmarkAnnotation{
		ID:            base58.NewUUID(),
		StartSelector: f.StartSelector.Value(),
		StartOffset:   f.StartOffset.Value(),
		EndSelector:   f.EndSelector.Value(),
		EndOffset:     f.EndOffset.Value(),
		Color:         f.Color.Value(),
		Created:       time.Now().UTC(),
		Note:          f.Note.Value(),
	}

	// Try to insert the new annotation
	reader, err := bi.GetArticle()
	if err != nil {
		return nil, err
	}

	var doc *html.Node
	if doc, err = html.Parse(reader); err != nil {
		return nil, err
	}
	root := dom.QuerySelector(doc, "body")

	// Add annotation and store its text content
	contents := &strings.Builder{}
	err = annotation.AddToNode(root, dataset.AnnotationTag, func(n *html.Node, index, ln int) {
		contents.WriteString(n.FirstChild.Data)
		dataset.AnnotationCallback(false)(annotation, n, index, ln)
	})
	if err != nil {
		return nil, err
	}

	annotation.Text = strings.TrimSpace(contents.String())

	// All good? Create the annotation now
	b := bi.Bookmark
	if b.Annotations == nil {
		b.Annotations = bookmarks.BookmarkAnnotations{}
	}

	b.Annotations.Add(annotation)
	b.Annotations.Sort(root, dataset.AnnotationTag)

	err = b.Update(map[string]any{
		"annotations": b.Annotations,
	})
	if err != nil {
		return nil, err
	}

	return annotation, nil
}
