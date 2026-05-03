// SPDX-FileCopyrightText: © 2025 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package users

import "codeberg.org/readeck/readeck/internal/acls"

type translator interface {
	Pgettext(ctx, str string, vars ...any) string
}

var roleMap = map[string]func(tr translator) string{
	"user":  func(tr translator) string { return tr.Pgettext("role", "user") },
	"staff": func(tr translator) string { return tr.Pgettext("role", "staff") },
	"admin": func(tr translator) string { return tr.Pgettext("role", "admin") },

	"profile:read":    func(tr translator) string { return tr.Pgettext("role", "Profile : Read Only") },
	"bookmarks:read":  func(tr translator) string { return tr.Pgettext("role", "Bookmarks : Read Only") },
	"bookmarks:write": func(tr translator) string { return tr.Pgettext("role", "Bookmarks : Write Only") },
	"admin:read":      func(tr translator) string { return tr.Pgettext("role", "Admin : Read Only") },
	"admin:write":     func(tr translator) string { return tr.Pgettext("role", "Admin : Write Only") },
}

// GroupList returns a list of available groups identified by a permission name
// and a [User]. When the user is nil, returns all the available groups.
func GroupList(tr translator, name string, user *User) [][2]string {
	res := [][2]string{}
	groups := acls.ListGroups(name)
	for _, g := range groups {
		if user != nil && !acls.InGroup(g, user.Group) {
			continue
		}

		label := g
		if n, ok := roleMap[g]; ok {
			label = n(tr)
		}

		res = append(res, [2]string{g, label})
	}

	return res
}

// GroupNames converts a role list to a list of translated names.
func GroupNames(tr translator, groups []string) []string {
	res := make([]string, len(groups))

	for i, g := range groups {
		res[i] = g
		if n, ok := roleMap[g]; ok {
			res[i] = n(tr)
		}
	}

	return res
}
