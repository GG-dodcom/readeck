// SPDX-FileCopyrightText: © 2026 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package components

import "github.com/a-h/templ"

// BasePage renders an empty page with default and required head elements.
func BasePage(title string) templ.Component {
	return defaultLayout.BasePage(title)
}

// SideMenuLayout renders a page with a side menu.
func SideMenuLayout(title string, sidemenu templ.Component) templ.Component {
	return defaultLayout.SideMenuLayout(title, sidemenu, defaultLayout.SideMenuWrapper())
}

// SideMenuStdLayout renders a page with a side menu.
// The content has a standard maximum width.
func SideMenuStdLayout(title string, sidemenu templ.Component) templ.Component {
	return defaultLayout.SideMenuLayout(title, sidemenu, defaultLayout.SideMenuStdWrapper())
}

// Layout is our base layout for every Readeck page.
type Layout struct {
	Head templ.Component
}

// defaultLayout is an instance of [Layout] that we reuse
// about everywhere.
var defaultLayout = NewLayout()

// NewLayout returns a new [Layout].
func NewLayout() (l *Layout) {
	return &Layout{
		Head: templ.NopComponent,
	}
}
