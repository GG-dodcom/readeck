// SPDX-FileCopyrightText: © 2026 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package components

import "codeberg.org/readeck/readeck/pkg/ctxr"

type ctxBreadcrumbKey struct{}

// WithBreadcrumb adds [BreadcrumbList] to the context.
var WithBreadcrumb, checkBreadcrumb = ctxr.WithChecker[[][2]string](ctxBreadcrumbKey{})
