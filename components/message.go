// SPDX-FileCopyrightText: © 2026 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package components

import (
	"maps"

	"github.com/a-h/templ"
)

type message struct {
	typeClass string
	icon      string
	removable bool
	delay     int
	class     templ.CSSClasses
	attrs     templ.Attributes
}

func newMessage(options ...func(*message)) *message {
	m := &message{
		typeClass: "info",
		icon:      "o-info",
		attrs:     templ.Attributes{},
	}

	for _, f := range options {
		f(m)
	}

	return m
}

// Message renders a message component.
func Message(options ...func(*message)) templ.Component {
	return newMessage(options...).component()
}

// WithMessageType sets the message's type.
func WithMessageType(s string) func(*message) {
	return func(mc *message) {
		mc.typeClass = s
		switch mc.typeClass {
		case "info":
			mc.icon = "o-info"
		case "success":
			mc.icon = "o-check-on"
		case "error":
			mc.icon = "o-error"
		}
	}
}

// WithMessageIcon sets the message's icon.
func WithMessageIcon(s string) func(*message) {
	return func(mc *message) {
		mc.icon = s
	}
}

// WithMessageRemovable marks the message as removable.
func WithMessageRemovable(mc *message) {
	mc.removable = true
	mc.attrs["data-controller"] = "remover"
}

// WithMessageDelay sets a delay to the message for its removal.
func WithMessageDelay(d int) func(*message) {
	return func(mc *message) {
		mc.delay = d
		mc.attrs["data-controller"] = "remover"
		mc.attrs["data-remover-delay-value"] = mc.delay
	}
}

// WithMessageClass adds a class to the message.
func WithMessageClass(c string) func(*message) {
	return func(mc *message) {
		mc.class = append(mc.class, c)
	}
}

// WithMessageAttrs adds the given attributes to the message.
func WithMessageAttrs(attrs templ.Attributes) func(*message) {
	return func(mc *message) {
		maps.Copy(mc.attrs, attrs)
	}
}
