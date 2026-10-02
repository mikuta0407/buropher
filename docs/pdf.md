# PDF 出力

buropher は Redmine と同じ画面・URL から PDF を出力する。

| 画面 | URL | ファイル名 |
| --- | --- | --- |
| チケット | `/issues/:id.pdf` | `<プロジェクト識別子>-<id>.pdf` |
| チケット一覧 | `/issues.pdf`, `/projects/:id/issues.pdf`（クエリの条件・列・グループ・合計を反映） | `<クエリ名>.pdf`（未保存なら `issues.pdf`） |
| Wiki ページ | `/projects/:id/wiki/:page.pdf`（`export_wiki_pages` 権限） | `<ページ名>.pdf` |
| Wiki 全体 | `/projects/:id/wiki/export.pdf` | `<プロジェクト識別子>.pdf` |
| ガントチャート | `/issues/gantt.pdf`, `/projects/:id/issues/gantt.pdf` | `[<プロジェクト識別子>-]gantt.pdf` |

Redmine の PDF は rbpdf（TCPDF）で作られる。buropher は純 Go の
[go-pdf/fpdf](https://github.com/go-pdf/fpdf) の上に TCPDF と同じ座標系・レイアウト
（A4、余白 10mm、行の高さ = 文字サイズ × 1.25、`issue_to_pdf` / `issues_to_pdf` / `wiki_page_to_pdf` /
`Gantt#to_pdf` と同じ配置）を再現しており、**見た目は近似であってバイト単位では一致しない**。

チケットの説明・注記・Wiki 本文などの書式付きテキストは、textilizable の HTML を簡易に描画する
（段落、見出し、太字・斜体・下線・取り消し線、コード・pre、箇条書き・番号付きリスト、引用、表、リンク、
水平線）。添付ファイルの画像（PNG / JPEG / GIF / WebP）は、Redmine と同じく
`!image.png!`（ファイル名）・`/attachments/download/:id/`・`/attachments/thumbnail/:id/:size` の形で
参照されたものを埋め込む。外部 URL の画像は取得しない。

## フォント

Redmine はロケールごとにフォントを選ぶ（`general_pdf_fontname`。ja は `kozminproregular`、
zh は `stsongstdlight`、zh-TW は `msungstdlight`、ko は `hysmyeongjostdmedium`、多くのロケールは
`freesans`）。buropher は次のようにする。

- **埋め込みフォント**: DejaVu Sans（通常・太字）と DejaVu Sans Mono をバイナリに埋め込んでいる
  （ラテン文字・ギリシャ文字・キリル文字・アラビア文字・ヘブライ文字などを含む。ライセンスは
  `internal/pdf/fonts/LICENSE`）。斜体は文字を傾けて描く。
- **CJK フォント**: バイナリを大きくしないよう埋め込まない。フォントディレクトリに TrueType フォントを
  置くと使う。
- 文字ごとに「ロケールのフォント → DejaVu → その他の追加フォント」の順でグリフを持つフォントを選ぶ。
  どのフォントにも無い文字は `?` に置き換える（エラーにはならない）。日本語のロケール以外でも、
  追加フォントがあれば日本語は表示される。
- `<pre>` やコードは DejaVu Sans Mono（無い文字は本文のフォント）で描く。

### 設定

`config.toml`:

```toml
[pdf]
# 追加フォントを置くディレクトリ（環境変数 BUROPHER_PDF_FONT_DIR でも指定できる）
font_dir = "/usr/local/share/buropher/fonts"

# ロケールごとのフォント（省略時は font_dir の既知のファイル名から自動で選ぶ）
[pdf.fonts]
ja = "ipaexg.ttf"
# 太字を別ファイルで持つフォントは "通常,太字"
ko = "NanumGothic.ttf,NanumGothicBold.ttf"
# 全ロケールの代替フォント（";" 区切りで複数）
fallback = "DroidSansFallbackFull.ttf"
```

`[pdf.fonts]` を省略すると、`font_dir` にある次のファイルを自動で使う（先にあるものを優先）。

| ロケール | ファイル名 |
| --- | --- |
| ja | `ipaexg.ttf`, `ipag.ttf`, `ipagp.ttf`, `ipaexm.ttf`, `ipam.ttf`, `NotoSansJP-Regular.ttf`, `DroidSansFallbackFull.ttf` |
| zh | `NotoSansSC-Regular.ttf`, `DroidSansFallbackFull.ttf` |
| zh-TW | `NotoSansTC-Regular.ttf`, `DroidSansFallbackFull.ttf` |
| ko | `NanumGothic.ttf`, `NotoSansKR-Regular.ttf`, `DroidSansFallbackFull.ttf` |

`NotoSans*-Regular.ttf` / `NanumGothic.ttf` は同じディレクトリの `*-Bold.ttf` / `NanumGothicBold.ttf`
を太字に使う。太字のファイルが無いフォントは線を太らせて太字を表現する。

### 使えるフォントの形式

fpdf の制約により **TrueType アウトライン（glyf）の単体の `.ttf`** だけを使える。

- CFF（PostScript アウトライン）の OpenType（`.otf`、Noto Sans CJK の `.otf` / `.ttc` など）は使えない。
- TrueType Collection（`.ttc`）は使えない（必要なら 1 書体ずつ `.ttf` に取り出す）。
- 使えないファイルを指定した場合は起動後の最初の PDF 出力時に警告をログに出し、そのフォントを無視する。

### インストール例

日本語（IPAex フォント。IPA フォントライセンス）:

```sh
mkdir -p /usr/local/share/buropher/fonts
cd /tmp && curl -LO https://moji.or.jp/wp-content/ipafont/IPAexfont/IPAexfont00401.zip
unzip IPAexfont00401.zip && cp IPAexfont00401/ipaexg.ttf /usr/local/share/buropher/fonts/
```

Debian / Ubuntu のパッケージを使う場合:

```sh
apt install fonts-ipaexfont-gothic      # /usr/share/fonts/opentype/ipaexfont-gothic/ipaexg.ttf
apt install fonts-nanum                 # /usr/share/fonts/truetype/nanum/NanumGothic.ttf
apt install fonts-droid-fallback        # /usr/share/fonts/truetype/droid/DroidSansFallbackFull.ttf（CJK 全般）
```

ディレクトリが分かれる場合は `font_dir` に 1 つ指定し、他は `[pdf.fonts]` に絶対パスで書く。

中国語（簡体字・繁体字）は Google Fonts の Noto Sans SC / TC（可変フォントの `.ttf`。
`NotoSansSC-Regular.ttf` などの静的版を推奨）、韓国語は Nanum Gothic を使える。
Droid Sans Fallback（Apache License 2.0）は 1 ファイルで日中韓の文字をひととおり含む。

## Redmine との違い

- 文字の幅・改行位置・ページ分割の位置は近似（フォントが異なるため）。
- HTML の描画は TCPDF の writeHTML の近似で、CSS は `style` 属性の `color` と `text-align` だけを読む。
  表の罫線は細い黒線（Redmine の CSS 指定 `border: 2px #ff0000 solid` は再現しない）。
- 外部 URL の画像は埋め込まない（TCPDF はダウンロードを試みる）。
- 右から左に書く言語（ar / fa / he）の RTL レイアウトは行わない。
- 文書のメタデータの Creator は `Redmine`、Producer は `buropher`。
