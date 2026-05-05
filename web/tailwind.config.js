// SPDX-FileCopyrightText: © 2021 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

const fontCatalog = require("./ui/fonts.js")

const colorVar = (name) => {
  return ({opacityValue}) => {
    if (opacityValue) {
      return `rgb(var(--color-${name}) / ${opacityValue})`
    }
    return `rgb(var(--color-${name}))`
  }
}

const varPalette = (name) => {
  return [50, 100, 200, 300, 400, 500, 600, 700, 800, 900, 950].reduce(
    (acc, cur) => {
      acc[cur] = colorVar(`${name}-${cur}`)
      return acc
    },
    {},
  )
}

// prettier-ignore
var config = {
  content: [
    "src/**/*.js",
    "../**/*.templ",
  ],
  darkMode: "class",
  theme: {
    colors: {
      inherit: "inherit",
      transparent: "transparent",
      current: "currentColor",
      black: colorVar("black"),
      white: colorVar("white"),
      app: {
        bg: colorVar("app-bg"),
        fg: colorVar("app-fg"),
      },
      gray: {
        ...varPalette("gray"),
        "light": colorVar("gray-light"),
        "dark": colorVar("gray-dark"),
      },
      red: varPalette("red"),
      green: varPalette("green"),
      blue: varPalette("blue"),
      yellow: varPalette("yellow"),
      primary: {
        ...varPalette("blue"),
        DEFAULT: colorVar("primary"),
        "light": colorVar("primary-light"),
        "dark": colorVar("primary-dark"),
      },

      // button colors
      btn: {
        "default": colorVar("btn-default"),
        "default-hover": colorVar("btn-default-hover"),
        "default-text": colorVar("btn-default-text"),
        "primary": colorVar("btn-primary"),
        "primary-hover": colorVar("btn-primary-hover"),
        "primary-text": colorVar("btn-primary-text"),
        "danger": colorVar("btn-danger"),
        "danger-hover": colorVar("btn-danger-hover"),
        "danger-text": colorVar("btn-danger-text"),
      },

      // highlight colors
      hl: {
        yellow: colorVar("hl-yellow"),
        "yellow-dark": colorVar("hl-yellow-dark"),
        red: colorVar("hl-red"),
        "red-dark": colorVar("hl-red-dark"),
        blue: colorVar("hl-blue"),
        "blue-dark": colorVar("hl-blue-dark"),
        green: colorVar("hl-green"),
        "green-dark": colorVar("hl-green-dark"),
      },
    },
    fontFamily: fontCatalog.families(),
    screens: {
      "xs": {raw: "only screen and (min-width: 340px)"},
      "sm": {raw: "only screen and (min-width: 640px)"},
      "md": {raw: "only screen and (min-width: 814px)"},
      "lg": {raw: "only screen and (min-width: 1024px)"},
      "xl": {raw: "only screen and (min-width: 1280px)"},
      "max-xl": {raw: "only screen and (max-width: 1280px)"},
      "max-lg": {raw: "only screen and (max-width: 1024px)"},
      "max-md": {raw: "only screen and (max-width: 814px)"},
      "max-sm": {raw: "only screen and (max-width: 640px)"},
      "max-xs": {raw: "only screen and (max-width: 340px)"},
      "touch": {raw: "(hover: none) and (pointer: coarse)"},
    },
    extend: {
      boxShadow: {
        "sidebar-l": "5px 0 10px -5px var(--default-shadow)",
        "panel-r": "0 4px 6px 6px var(--default-shadow)",
      },
      boxShadowColor: {
        DEFAULT: "var(--default-shadow)",
        sm: "var(--default-shadow)",
        md: "var(--default-shadow)",
        lg: "var(--default-shadow)",
        xl: "var(--default-shadow)",
        "2xl": "var(--default-shadow)",
        inner: "var(--default-shadow)",
      },
      contrast: {
        "105": "1.05",
      },
      fontSize: {
        "h1": "2.5rem",
        "h2": "2rem",
        "h3": "1.5rem",
      },
      gridTemplateColumns: {
        "bk-tools": "2fr auto auto",
      },
      height: {
        "screen": "100vh",
        "max-content": "max-content",
        "topnav": "4rem",
      },
      padding: {
        "16/9": "56.25%",
      },
      spacing: {
        "0.5": "0.125rem",
        "topnav": "4rem",
        ...[...Array(26).keys()].reduce((acc, x) => {
          acc[`col-${x+1}`] = `${(x+1) * 3.5}rem`
          return acc
        }, {}),
      },
      width: {
      },
      maxWidth: {
        "layout": "1920px",
        "std": "49rem",
      },
    },
  },
  plugins: [
    require("./ui/plugins/tw-interactions"),
  ],
}

module.exports = config
