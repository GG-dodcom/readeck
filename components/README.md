---
SPDX-FileCopyrightText: © 2026 Olivier Meunier <olivier@neokraft.net>
SPDX-License-Identifier: AGPL-3.0-only

TODO: these notes should move to a main CONTRIB.md file, when we have one.
---

# Readeck templates

Readeck uses [Templ](https://templ.guide/) as a template/component engine.

Templ provides type safe templates and very fast execution. Here are some conventions used in Readeck when it comes to templates.

## Views and Components

A view is a page rendered by an `http.Handler`.

- Views don't have global variables but always have a context.
- Views are functions that take (typed) arguments.
- Views live in the same package as their handler and, therefore, have access to all the package's members (exported or not).
- `.templ` files are always prefixed with `x-` (only for file organization)

To avoid crowding a package with a lot of functions, a package providing views **must** declare
empty structs and add methods for each view and/or components.

The scructs cannot contain anything so they occupy no storage space and all have the same address.

```templ
package something

import . "codeberg.org/readeck/readeck/components"

type Views struct{}

templ (_ Views) menu() {
	@SideMenuTitle() {
		Cookbook
	}
	<menu class="mt-4">
		// menu items...
	</menu>
}

templ (v Views) base(title string) {
	@SideMenuLayout(title, v.menu()) {
		{ children... }
	}
}

templ (v Views) somePage() {
	@v.base("a title") {
		// content...
	}
}
```

Then a handler can call the view like this:

```go
server.RenderComponent(w, r, 200, Views{}.somePage())
```

On complex views and components, it's best to split `Views{}` and `Components{}` in their respective structs.

## Component helpers

The package `codeberg.org/readeck/readeck/components` provides helper functions to work with components.

It can be imported globally as:

```go
import (
	. "codeberg.org/readeck/readeck/components"
)
```

Form helpers are in `codeberg.org/readeck/readeck/components/forms`, which is usually imported with
a shortcut:

```go
import (
	F "codeberg.org/readeck/readeck/components/forms"
)
```