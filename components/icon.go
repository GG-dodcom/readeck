// SPDX-FileCopyrightText: © 2026 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package components

import (
	"maps"

	"github.com/a-h/templ"
)

type icon struct {
	src      string
	w        int
	h        int
	class    templ.CSSClasses
	svgClass templ.CSSClasses
	attrs    templ.Attributes
}

// Icon renders an icon from a source sprite.
// The default source is "img/icons.svg".
func Icon(name string, options ...func(*icon)) templ.Component {
	i := &icon{
		src:      "img/icons.svg",
		w:        24,
		h:        24,
		class:    templ.CSSClasses{"svgicon"},
		svgClass: templ.CSSClasses{"w-4"},
		attrs:    templ.Attributes{},
	}

	for _, f := range options {
		f(i)
	}

	return i.component(name)
}

// WithIconSrc sets the icon's sprite source.
func WithIconSrc(src string) func(*icon) {
	return func(i *icon) {
		i.src = src
	}
}

// WithIconClass sets the icon wrapper's class.
func WithIconClass(c ...any) func(*icon) {
	return func(i *icon) {
		i.class = templ.Classes(c...)
	}
}

// WithIconSvgClass sets the icon svg's class.
func WithIconSvgClass(c ...any) func(*icon) {
	return func(i *icon) {
		i.svgClass = templ.Classes(c...)
	}
}

// WithIconSize sets the icon's dimension.
func WithIconSize(w, h int) func(*icon) {
	return func(i *icon) {
		if w > 0 {
			i.w = w
		}
		if h > 0 {
			i.h = h
		}
	}
}

// WithIconAttrs sets the icon wrapper's attributes.
func WithIconAttrs(attrs templ.Attributes) func(*icon) {
	return func(i *icon) {
		maps.Copy(i.attrs, attrs)
	}
}
