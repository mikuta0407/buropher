# Third-party notices

Buropher (Copyright (C) 2026 mikuta0407 and Buropher contributors) is a derivative
work of [Redmine](https://www.redmine.org/) (Copyright (C) 2006- Jean-Philippe Lang)
and is licensed under **GPL-2.0-or-later** (see [LICENSE](LICENSE) and [NOTICE](NOTICE)).

This file lists the third-party components that are bundled in the repository and in
the `buropher` binary (assets, templates and locales are embedded with `go:embed`),
together with their licenses. License texts of the Go modules are available in the
module sources (`go mod download`, then `$(go env GOMODCACHE)/<module>@<version>/LICENSE`).

## 1. GPL compatibility

Buropher is distributed under "GPL version 2 or (at your option) any later version".

- **MIT, BSD-2-Clause, BSD-3-Clause, ISC, public domain**: permissive licenses that are
  compatible with both GPLv2 and GPLv3. Their copyright notices are reproduced below or
  in the bundled files.
- **MPL-2.0** (`github.com/go-sql-driver/mysql`): MPL-2.0 §3.3 allows distributing the
  covered code in a "Larger Work" under GPL-2.0-or-later ("Secondary License"), as long
  as the MPL-covered source files stay available under MPL-2.0. The module's files are
  not modified.
- **Apache-2.0** (`github.com/coreos/go-oidc`, `github.com/go-jose/go-jose`,
  `github.com/klauspost/compress`, `github.com/golang-sql/civil`,
  `github.com/sethvargo/go-retry`, `gopkg.in/yaml.v3` / `go.yaml.in/yaml/v3` (partly)):
  Apache-2.0 is considered incompatible with GPL **version 2 only**, but compatible with
  GPL **version 3**. Because Buropher is licensed GPL-2.0-*or-later*, recipients may (and,
  for binaries that link these modules, effectively do) use the combined work under the
  terms of GPLv3. Source-only distribution of Buropher remains GPL-2.0-or-later.
  If you need a GPLv2-only build, these modules (OIDC sign-in, zstd archives of the
  Redmine importer, YAML parsing) would have to be replaced.
- **SIL Open Font License 1.1** (Noto Sans) and the **Bitstream Vera / Arev font
  licenses** (DejaVu): font licenses that allow bundling with software under any license;
  the fonts are distributed unmodified with their license texts.
- **CC BY 2.5 / CC BY 3.0** (Silk / Fugue icons inherited from Redmine): images are
  distributed with attribution (below), as in Redmine.

## 2. Bundled web assets (`web/assets/`, from Redmine 7.0.2)

License texts of these components are in [`docs/licenses/`](docs/licenses/) (copied from
Redmine's `doc/licenses` by `tools/sync-upstream.sh`).

| Component | Version | License | Files | License text |
|---|---|---|---|---|
| [jQuery](https://jquery.com/) | 3.7.1 | MIT (c) OpenJS Foundation and other contributors | `javascripts/jquery-3.7.1-ui-1.13.3.js` | `jquery.txt` |
| [jQuery UI](https://jqueryui.com/) (incl. datepicker translations and theme) | 1.13.3 (JS), 1.13.2 (CSS) | MIT (c) OpenJS Foundation and other contributors | `javascripts/jquery-3.7.1-ui-1.13.3.js`, `javascripts/i18n/datepicker-*.js`, `stylesheets/jquery/` | `jquery-ui.txt` |
| [rails-ujs](https://github.com/rails/rails/tree/main/actionview/app/javascript) (actionview gem) | 8.1.4 | MIT (c) David Heinemeier Hansson | `javascripts/rails-ujs.js` | — |
| [Stimulus](https://stimulus.hotwired.dev/) (stimulus-rails gem) | 3.2.2 (stimulus-rails 1.3.4) | MIT (c) Basecamp, LLC | `vendor/stimulus.min.js`, `vendor/stimulus.min.js.map`, `vendor/stimulus-loading.js` | — |
| [requestjs-rails](https://github.com/rails/requestjs-rails) | 0.0.14 | MIT (c) 2021 Marcelo Lauxen | `vendor/requestjs.js` | — |
| [Turndown](https://github.com/mixmark-io/turndown) | 7.2.0 | MIT (c) 2017 Dom Christie | `vendor/turndown.js` | `turndown.txt` |
| [Chart.js](https://www.chartjs.org/) | 4.5.1 | MIT (c) 2014-2022 Chart.js Contributors | `vendor/chart.min.js` | `chartjs.txt` |
| [tablesort](https://github.com/tristen/tablesort) | 5.7.0 (`vendor/`), 5.2.1 (`javascripts/`) | MIT (c) Tristen Brown | `vendor/tablesort.min.js`, `vendor/tablesort.number.min.js`, `javascripts/tablesort-5.2.1.min.js`, `javascripts/tablesort-5.2.1.number.min.js` | `tablesort.txt` |
| [Tribute](https://github.com/zurb/tribute) | 5.1.3 | MIT (c) 2017-2020 ZURB, Inc., (c) 2014 Jeff Collins | `javascripts/tribute-5.1.3.min.js`, `javascripts/tribute.min.js.map`, `stylesheets/tribute-5.1.3.css` | `tribute.txt` |
| [Open Color](https://yeun.github.io/open-color/) | 1.9.1 | MIT (c) 2016 heeyeun | `stylesheets/open-color.css` | `open-color.txt` |
| jsToolBar (from [DotClear](https://dotclear.org/), modified by Jean-Philippe Lang for Redmine) | — | GPL (c) 2005 Nicolas Martin & Olivier Meunier and contributors | `javascripts/jstoolbar/` | — |
| [Noto Sans](https://fonts.google.com/noto/specimen/Noto+Sans) | — | SIL Open Font License 1.1 (c) 2022 The Noto Project Authors | `fonts/NotoSans-*.woff2` | `notosans.txt` |
| [Tabler Icons](https://tabler.io/icons) | 3.43.0 | MIT (c) 2020-2025 Paweł Kuna | `images/icons.svg` (and other SVG icons) | `tabler-icons.txt` |
| [Silk Icons](http://www.famfamfam.com/lab/icons/silk/) | — | CC BY 2.5, Mark James | PNG icons in `images/` | `silk-icons.txt` |
| [Fugue Icons](https://p.yusukekamiyamane.com/) | — | CC BY 3.0, Yusuke Kamiyamane | PNG icons in `images/` | `fugue-icons.txt` |

Redmine 7.0 removed Raphaël (the old Gantt / revision graph drawing library), `gantt.js` and
`rtl.css`; they are no longer bundled.

The remaining files in `web/assets/` (Redmine's own stylesheets, scripts, Stimulus
controllers, themes and images) are part of Redmine (GPL-2.0-or-later); see NOTICE.

## 3. Fonts embedded for PDF export (`internal/pdf/fonts/`)

| Component | Version | License |
|---|---|---|
| [DejaVu fonts](https://dejavu-fonts.github.io/) (DejaVuSans, DejaVuSans-Bold, DejaVuSansMono; gzip-compressed, unmodified) | 2.37 | Bitstream Vera Fonts license and Arev Fonts license; DejaVu changes are public domain. Full text: `internal/pdf/fonts/LICENSE` |

## 4. Translation files (`web/locales/rails/`)

Loaded before Redmine's own translations, exactly as Redmine does at runtime.

| Component | Version | License | Files |
|---|---|---|---|
| Ruby on Rails (activesupport, activemodel, activerecord, actionview) | 8.1.x | MIT (c) David Heinemeier Hansson | `activesupport.en.yml`, `activemodel.en.yml`, `activerecord.en.yml`, `actionview.en.yml` |
| [doorkeeper-i18n](https://github.com/doorkeeper-gem/doorkeeper-i18n) | 5.2.9 | MIT (c) Tute Costa | `doorkeeper.<locale>.yml` |
| [doorkeeper](https://github.com/doorkeeper-gem/doorkeeper) | 5.8.2 | MIT (c) Applicake / Doorkeeper contributors | `doorkeeper-gem.en.yml` |

`web/locales/redmine/` contains Redmine's translations (GPL-2.0-or-later, see NOTICE).

## 5. Ported code and behavior

| Component | License | Where |
|---|---|---|
| [Rouge](https://github.com/rouge-ruby/rouge) 5.1.0 — syntax highlighting lexers and regex-lexer engine, ported from Ruby to Go | MIT (c) 2012 Jeanine Adkisson and contributors | `internal/textformat/highlight/rouge*.go` (header: `GPL-2.0-or-later AND MIT`), generator `tools/gen-rouge-lexers.rb`, language list `web/templates/help/wiki_syntax/code_highlighting_languages.tsv` |
| [libxml2](https://gitlab.gnome.org/GNOME/libxml2) 2.13.9 — HTML parser/serializer behavior of `HTMLparser.c` / `HTMLtree.c` (as used by Nokogiri) ported to Go, HTML entity table | MIT (c) Daniel Veillard | `internal/textformat/htmldom/` (header: `GPL-2.0-or-later AND MIT`) |
| [golang.org/x/net/html](https://pkg.go.dev/golang.org/x/net/html) v0.58.0 — HTML5 tokenizer/parser, forked and modified to reproduce `Nokogiri::HTML5` (gumbo) parsing used by Redmine 7.0's `Loofah.html5_fragment` | BSD-3-Clause (c) 2009 The Go Authors | `internal/textformat/htmldom/internal/h5/` (header: `BSD-3-Clause AND GPL-2.0-or-later`, license text in that directory) |
| [Loofah](https://github.com/flavorjones/loofah), [rails-html-sanitizer](https://github.com/rails/rails-html-sanitizer), [Nokogiri](https://nokogiri.org/) — sanitizer and HTML handling behavior reproduced (not copied) | MIT | `internal/textformat/sanitize/`, `internal/view/rails/sanitize.go`, `internal/mail/inline.go`, `internal/mailhandler/htmltext.go` |
| RedCloth 3.0.4 (as modified in Redmine's `redcloth3.rb`) | BSD (c) 2004 why the lucky stiff | `internal/textformat/textile/redcloth3.go` |
| [comrak](https://github.com/kivikakk/comrak) via [commonmarker](https://github.com/gjtorikian/commonmarker) 2.8.3 — CommonMark/GFM output reproduced on top of goldmark (behavior, not code) | BSD-2-Clause (comrak), MIT (commonmarker) | `internal/textformat/commonmark/` |
| [goldmark](https://github.com/yuin/goldmark) | MIT | Go module dependency (see below) |

## 6. Test data (not part of the binary)

| Data | Origin / license |
|---|---|
| Redmine test fixtures (`internal/testfixtures/testdata/redmine/`, `internal/mailhandler/testdata/`, `testdata/compat/files/`, ...) | Redmine 7.0.2 `test/fixtures`, GPL-2.0-or-later |
| Golden outputs (`testdata/compat/golden/`, `internal/*/testdata/`) | generated by running Redmine 7.0.2 (some with 7.0.1 or 6.1.2; and Rails I18n, Rouge, commonmarker) |
| CommonMark spec examples (`internal/textformat/commonmark/testdata/corpus_spec.json`) | [CommonMark Spec](https://spec.commonmark.org/), CC BY-SA 4.0 (c) John MacFarlane |
| comrak test inputs (`internal/textformat/commonmark/testdata/corpus_comrak.json`) | [comrak](https://github.com/kivikakk/comrak) test suite, BSD-2-Clause (c) Asherah Connor |
| Git fixture repository for SCM tests | Redmine's `test/fixtures/repositories/git_repository.tar.gz`; **not bundled** (tests extract it from a local Redmine checkout under `_reference/`) |
| Playwright e2e tests (`e2e/`, npm dev dependencies) | not distributed |

## 7. Go module dependencies

Modules linked into `cmd/buropher` (`go list -deps ./cmd/buropher`). License types were
detected from each module's LICENSE file in the module cache.

| Module | Version | License |
|---|---|---|
| filippo.io/edwards25519 | v1.2.0 | BSD-3-Clause |
| github.com/alecthomas/chroma/v2 | v2.27.0 | MIT |
| github.com/andybalholm/cascadia | v1.3.5 | BSD-2-Clause |
| github.com/Azure/go-ntlmssp | v0.1.1 | MIT |
| github.com/boombuler/barcode | v1.1.0 | MIT |
| github.com/BurntSushi/toml | v1.6.0 | MIT |
| github.com/coreos/go-oidc/v3 | v3.21.0 | Apache-2.0 |
| github.com/dlclark/regexp2 | v1.12.0 | MIT |
| github.com/dlclark/regexp2/v2 | v2.8.1 | MIT |
| github.com/dustin/go-humanize | v1.0.1 | MIT |
| github.com/emersion/go-imap/v2 | v2.0.0-beta.8 | MIT |
| github.com/emersion/go-message | v0.18.2 | MIT |
| github.com/emersion/go-sasl | v0.0.0-20241020182733-b788ff22d5a6 | MIT |
| github.com/go-asn1-ber/asn1-ber | v1.5.8 | MIT |
| github.com/go-chi/chi/v5 | v5.3.2 | MIT |
| github.com/go-jose/go-jose/v4 | v4.1.5 | Apache-2.0 |
| github.com/go-ldap/ldap/v3 | v3.4.14 | MIT |
| github.com/go-pdf/fpdf | v0.9.0 | MIT |
| github.com/go-sql-driver/mysql | v1.10.0 | MPL-2.0 |
| github.com/golang-sql/civil | v0.0.0-20220223132316-b832511892a9 | Apache-2.0 |
| github.com/golang-sql/sqlexp | v0.1.0 | BSD-3-Clause |
| github.com/google/uuid | v1.6.0 | BSD-3-Clause |
| github.com/jackc/pgpassfile | v1.0.0 | MIT |
| github.com/jackc/pgservicefile | v0.0.0-20240606120523-5a60cdf6a761 | MIT |
| github.com/jackc/pgx/v5 | v5.11.0 | MIT |
| github.com/jackc/puddle/v2 | v2.2.2 | MIT |
| github.com/jmoiron/sqlx | v1.4.0 | MIT |
| github.com/klauspost/compress | v1.19.2 | Apache-2.0 (with BSD-3-Clause / MIT parts) |
| github.com/lann/builder | v0.0.0-20180802200727-47ae307949d0 | MIT |
| github.com/lann/ps | v0.0.0-20150810152359-62de8c46ede0 | MIT |
| github.com/Masterminds/squirrel | v1.5.4 | MIT |
| github.com/mfridman/interpolate | v0.0.2 | MIT |
| github.com/microsoft/go-mssqldb | v1.11.0 | BSD-3-Clause |
| github.com/pressly/goose/v3 | v3.28.0 | MIT |
| github.com/remyoudompheng/bigfft | v0.0.0-20230129092748-24d4a6f8daec | BSD-3-Clause |
| github.com/sethvargo/go-retry | v0.4.0 | Apache-2.0 |
| github.com/shopspring/decimal | v1.4.0 | MIT |
| github.com/yuin/goldmark | v1.8.6 | MIT |
| golang.org/x/crypto | v0.57.0 | BSD-3-Clause |
| golang.org/x/image | v0.26.0 | BSD-3-Clause |
| golang.org/x/net | v0.58.0 | BSD-3-Clause |
| golang.org/x/oauth2 | v0.37.0 | BSD-3-Clause |
| golang.org/x/sync | v0.23.0 | BSD-3-Clause |
| golang.org/x/sys | v0.48.0 | BSD-3-Clause |
| golang.org/x/text | v0.42.0 | BSD-3-Clause |
| gopkg.in/yaml.v3 | v3.0.1 | MIT AND Apache-2.0 |
| go.uber.org/multierr | v1.11.0 | MIT |
| go.yaml.in/yaml/v3 | v3.0.5 | MIT AND Apache-2.0 |
| modernc.org/libc | v1.77.1 | BSD-3-Clause (third-party parts: see its LICENSE-3RD-PARTY.md, incl. musl libc, MIT) |
| modernc.org/mathutil | v1.7.1 | BSD-3-Clause |
| modernc.org/memory | v1.12.1 | BSD-3-Clause |
| modernc.org/sqlite | v1.60.1 | BSD-3-Clause; SQLite itself is public domain (LICENSE-SQLITE) |

The Go standard library and runtime (BSD-3-Clause, (c) The Go Authors) are linked into
every Go binary.

To regenerate the module list:

```sh
go list -deps -f '{{with .Module}}{{if not .Main}}{{.Path}} {{.Version}}{{end}}{{end}}' ./cmd/buropher | sort -u
```
