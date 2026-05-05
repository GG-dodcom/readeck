// SPDX-FileCopyrightText: © 2021 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

import fs from "fs"
import path from "path"
import {createRequire} from "module"
import zlib from "zlib"

const require = createRequire(import.meta.url)

import gulp from "gulp"
import gulpHash from "gulp-hash-filename"
import gulpEsbuild from "gulp-esbuild"
import gulpPostcss from "gulp-postcss"
import gulpRename from "gulp-rename"
import gulpSass from "gulp-sass"
import gulpSourcemaps from "gulp-sourcemaps"

import ordered from "ordered-read-streams"
import through from "through2"
import File from "vinyl"
import * as sass from "sass"

import {IconSet, SVG, cleanupSVG, runSVGO, scaleSVG} from "@iconify/tools"
import {deleteAsync as del} from "del"
import {stimulusPlugin} from "esbuild-plugin-stimulus"

import fontCatalog from "./ui/fonts.js"

const sassCompiler = gulpSass(sass)

const DEST = path.resolve("../assets/www")

// hashName returns a gulp stream for hashing filenames with the
// same pattern.
function hashName() {
  return gulpHash({
    format: "{name}.{hash:8}{ext}",
  })
}

function toPosixPath(p) {
  return process.platform === "win32" ? p.replace(/\\/g, "/") : p
}

// destCompress returns a gulp stream that compresses the current
// stream's files in gzip or brotli.
// It pushes the resulting file to the stream with an added suffix
// (.gz or .br).
function destCompress(format) {
  return through.obj(function (file, _, done) {
    if (
      file.isNull() ||
      file._isCompressed ||
      path.extname(file.basename) == ".map"
    ) {
      return done(null, file)
    }

    if (file.isStream()) {
      done(null, file)
      return
    }

    let options, fn
    let cf = file.clone({deep: true, contents: true})
    cf.basename = `${cf.basename}.${format}`

    if (format == "gz") {
      options = {
        level: 9,
      }
      fn = zlib.gzip
    } else if (format == "br") {
      options = {
        params: {
          [zlib.constants.BROTLI_PARAM_QUALITY]: 11,
        },
      }
      fn = zlib.brotliCompress
    } else {
      done(`unknown format ${format}`)
      return
    }

    fn(cf.contents, options, (err, contents) => {
      if (err) {
        done(err)
        return
      }

      cf.contents = contents
      cf._isCompressed = true
      this.push(cf)
      done(null, file)
    })
  })
}

// cleanFiles calls del() with some default options.
function cleanFiles(...args) {
  return del(args, {cwd: DEST, force: true})
}

// clean_js remove the JS assets.
function clean_js(done) {
  return cleanFiles("*.js", "*.js.*")
}

// clean_css removes the CSS assets (and fonts).
function clean_css() {
  return cleanFiles("*.css", "*.css.*", "fonts")
}

// clean_media removes static assets like svg or images.
function clean_media() {
  return cleanFiles("img")
}

function clean_vendor() {
  return cleanFiles("vendor")
}

// clean delete files in the destination folder
function clean_all(done) {
  return gulp.series(
    clean_js, //
    clean_css,
    clean_media,
    clean_vendor,
  )(done)
}

// js_bundle creates the JS bundle file using esbuild.
function js_bundle() {
  return ordered([
    gulp
      .src("src/main.js")
      .pipe(
        gulpEsbuild({
          sourcemap: "inline",
          outfile: "bundle.js",
          bundle: true,
          format: "esm",
          platform: "browser",
          metafile: false,
          minifyIdentifiers: true,
          minifyWhitespace: true,
          logLevel: "warning",
          plugins: [stimulusPlugin(), disableTurboStart()],
        }),
      )
      .pipe(gulpSourcemaps.init({loadMaps: true})) // This extracts the inline sourcemap
      .pipe(hashName())
      .pipe(gulpSourcemaps.write("."))
      .pipe(destCompress("gz"))
      .pipe(destCompress("br"))
      .pipe(gulp.dest(DEST)),
    gulp
      .src("src/public.js")
      .pipe(
        gulpEsbuild({
          sourcemap: "inline",
          outfile: "public.js",
          bundle: true,
          format: "esm",
          platform: "browser",
          metafile: false,
          minifyIdentifiers: true,
          minifyWhitespace: true,
          logLevel: "warning",
          plugins: [stimulusPlugin()],
        }),
      )
      .pipe(gulpSourcemaps.init({loadMaps: true})) // This extracts the inline sourcemap
      .pipe(hashName())
      .pipe(gulpSourcemaps.write("."))
      .pipe(destCompress("gz"))
      .pipe(destCompress("br"))
      .pipe(gulp.dest(DEST)),
  ])
}

// css_bundle creates the CSS bundle.
function css_bundle() {
  const processors = [
    require("postcss-import"),
    require("./ui/plugins/prose"),
    require("./ui/plugins/palettes.js"),
    require("tailwindcss"),
    require("./ui/plugins/responsive-units.js"),
    require("./ui/plugins/trim-fonts.js"),
    require("./ui/plugins/hover-media-query.js"),
    require("postcss-copy")({
      basePath: fontCatalog.basePath(),
      dest: DEST,
      ignore: (m) => {
        return m.ext != "woff2"
      },
      template: (m) => {
        return `./fonts/${m.name}.${m.hash.substr(0, 8)}.${m.ext}`
      },
    }),
    require("autoprefixer"),
    require("cssnano"),
  ]

  return gulp
    .src([
      "ui/index.scss", //
    ])
    .pipe(
      through.obj(function (file, _, done) {
        // Push @import from the font catalog
        if (file.isBuffer()) {
          const concat = []

          for (let l of file.contents.toString().split("\n")) {
            if (l.startsWith("//--fonts--")) {
              concat.push(Buffer.from(fontCatalog.atRules().join("\n")))
              concat.push(Buffer.from("\n\n"))
              continue
            }
            concat.push(Buffer.from(l + "\n"))
          }
          file.contents = Buffer.concat(concat)
        }

        this.push(file)
        done()
      }),
    )
    .pipe(gulpSourcemaps.init())
    .pipe(sassCompiler().on("error", sassCompiler.logError))
    .pipe(gulpRename("bundle.css"))
    .pipe(gulpPostcss(processors))
    .pipe(hashName())
    .pipe(gulpSourcemaps.write("."))
    .pipe(destCompress("gz"))
    .pipe(destCompress("br"))
    .pipe(gulp.dest(DEST))
}

// css_extra create the css files used as internal assets (epub, email)
function css_extra() {
  const processors = [
    require("postcss-import"),
    require("./ui/plugins/prose"),
    require("autoprefixer"),
  ]

  return ordered(
    [
      {src: "ui/epub/stylesheet.scss", dest: "epub.css"},
      {src: "ui/email/stylesheet.scss", dest: "email.css"},
    ].map((x) => {
      return gulp
        .src(x.src)
        .pipe(gulpSourcemaps.init())
        .pipe(sassCompiler().on("error", sassCompiler.logError))
        .pipe(gulpRename(x.dest))
        .pipe(gulpPostcss(processors))
        .pipe(gulp.dest(DEST))
    }),
  )
}

// optiSVG is a pipeline that cleans up and optimize an SVG input.
function optiSVG() {
  return through.obj(function (file, _, done) {
    if (file.isNull() || file.isStream()) {
      return done()
    }

    const icon = new SVG(file.contents)
    cleanupSVG(icon)
    runSVGO(icon)

    file.contents = Buffer.from(icon.toMinifiedString())
    this.push(file)
    done()
  })
}

// icon_sprite is a pipeline that converts a JSON icon list to an SVG with a symbol
// for each icon.
// Icons can be URLs (with a scheme being the iconify namespace)
// or a relative (to the JSON file) SVG path.
function icon_sprite() {
  return through.obj(function (file, _, done) {
    if (file.isNull() || file.isStream()) {
      return done()
    }

    let spec = {}
    const store = {}
    const icons = []
    const singleSVG = file.extname == ".svg"

    if (singleSVG) {
      // When the input file is a single SVG, we output a symbol with id=main
      spec["main"] = file.basename
    } else {
      spec = JSON.parse(file.contents)
    }

    const base = path.dirname(file.path) + "/"

    // load icons from iconify colletions
    for (let [id, v] of Object.entries(spec)) {
      let icon

      const url = new URL(v, "file://" + base)
      if (url.protocol === "file:") {
        // load from file
        icon = new SVG(
          fs.readFileSync(url.pathname, {encoding: "utf-8"}).toString(),
        )
        cleanupSVG(icon)
        runSVGO(icon)
      } else {
        // load from iconify
        const ns = url.protocol.slice(0, -1)

        if (store[ns] === undefined) {
          store[ns] = new IconSet(require(`@iconify-json/${ns}`).icons)
        }

        icon = store[ns].toSVG(url.pathname)
      }

      if (!singleSVG) {
        const size = url.searchParams.has("size")
          ? url.searchParams.get("size")
          : 24
        scaleSVG(icon, size / Math.max(icon.viewBox.height, icon.viewBox.width))
      }

      icon.$svg.tag = "symbol"
      icon.$svg.attribs.id = id
      delete icon.$svg.attribs.xmlns
      delete icon.$svg.attribs["xmlns:xlink"]

      icons.push(icon.toMinifiedString())
    }

    // render sprite
    let res = '<?xml version="1.0" encoding="UTF-8"?>\n'
    res +=
      '<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink">\n'
    res += icons.join("\n")
    res += "\n</svg>\n"

    file.contents = Buffer.from(res)
    file.extname = ".svg"
    this.push(file)
    done()
  })
}

// icon_bundle creates the icon bundle files
function icon_bundle() {
  return gulp
    .src(
      ["./media/icons.json", "./media/logos.json", "./media/logo-text.svg"],
      {encoding: false},
    )
    .pipe(icon_sprite())
    .pipe(hashName())
    .pipe(destCompress("gz"))
    .pipe(destCompress("br"))
    .pipe(gulp.dest(path.join(DEST, "img")))
}

// copy_files copies some files to the destination.
function copy_files() {
  return ordered([
    gulp
      .src("media/favicons/*", {encoding: false})
      .pipe(hashName())
      .pipe(gulp.dest(path.join(DEST, "img/fi"))),

    gulp
      .src(["media/logo-maskable.svg"], {
        encoding: false,
      })
      .pipe(optiSVG())
      .pipe(hashName())
      .pipe(destCompress("gz"))
      .pipe(destCompress("br"))
      .pipe(gulp.dest(path.join(DEST, "img"))),

    gulp
      .src("media/logo-text.png", {encoding: false})
      .pipe(hashName())
      .pipe(gulp.dest(path.join(DEST, "img"))),

    gulp
      .src(path.join(require.resolve("hls.js/dist/hls.min.js")), {
        encoding: false,
      })
      .pipe(gulpRename("hls.js"))
      .pipe(hashName())
      .pipe(destCompress("gz"))
      .pipe(destCompress("br"))
      .pipe(gulp.dest(path.join(DEST, "vendor"))),

    gulp
      .src("vendor/*", {encoding: false})
      .pipe(hashName())
      .pipe(destCompress("gz"))
      .pipe(destCompress("br"))
      .pipe(gulp.dest(path.join(DEST, "vendor"))),
  ])
}

// write_manifest generates a manifest.json file in the destination folder.
// It's a very naive process that lists all the files in the destination
// folder and creates a mapping for all the files having a hash suffix.
function write_manifest() {
  return gulp
    .src(path.join(DEST, "**"), {read: false})
    .pipe(buildManifest())
    .pipe(gulp.dest(DEST))
}

function buildManifest() {
  const rxFilename = new RegExp(/^(.+)(\.[a-f0-9]{8}\.)(.+)$/)
  const excluded = [".br", ".gz", ".map"]
  let manifest = {}

  function collect(file, _, done) {
    if (!file.stat.isFile()) {
      done()
      return
    }
    const f = toPosixPath(path.relative(DEST, file.path))
    if (f == "manifest.json" || excluded.includes(path.extname(f))) {
      done()
      return
    }

    const m = f.match(rxFilename)
    if (!!m) {
      manifest[`${m[1]}.${m[3]}`] = f
    } else {
      manifest[f] = f
    }

    done()
  }

  function flush(done) {
    const f = new File({path: "manifest.json"})
    f.contents = Buffer.from(JSON.stringify(manifest, null, "  ") + "\n")
    this.push(f)
    done(null, f)
  }

  return through.obj(collect, flush)
}

// ------------------------------------------------------------------
// Gulp pipelines
// ------------------------------------------------------------------

const full_build = gulp.series(
  clean_all,
  gulp.parallel(
    js_bundle, //
    icon_bundle,
    copy_files,
    css_bundle,
    css_extra,
  ),
  write_manifest,
)

function watch_js() {
  gulp.watch(
    ["src/**/*"],
    gulp.series(
      clean_js, //
      js_bundle,
      write_manifest,
    ),
  )
}

function watch_css() {
  gulp.watch(
    [
      "tailwind.config.js", //
      "ui/**/*",
      "../**/*.templ",
    ],
    gulp.series(
      clean_css, //
      css_bundle,
      css_extra,
      write_manifest,
    ),
  )
}

function watch_media() {
  gulp.watch(
    ["media/**/*"],
    gulp.series(
      clean_media, //
      icon_bundle,
      copy_files,
      write_manifest,
    ),
  )
}

function disableTurboStart() {
  return {
    name: "disableTurboStart",
    setup(build) {
      build.onLoad({filter: /@hotwired\/turbo\/.+\.js$/}, async (args) => {
        let text = await fs.promises.readFile(args.path, "utf8")
        return {
          contents: text.replace(/^start\(\);/m, ""),
        }
      })
    },
  }
}

export const clean = gulp.series(clean_all, write_manifest)
export const js = js_bundle
export const css = gulp.series(clean_css, css_bundle, css_extra)
export const epub = css_extra
export const icons = icon_bundle
export const copy = copy_files

export {watch_css as "watch:css"}
export {watch_js as "watch:js"}

export const dev = gulp.series(
  full_build,
  gulp.parallel(
    watch_js, //
    watch_css,
    watch_media,
  ),
)

export default full_build
