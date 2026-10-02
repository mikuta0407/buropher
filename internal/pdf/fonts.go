package pdf

// PDF のフォント。
//
// Redmine（rbpdf）はロケールごとに general_pdf_fontname / general_pdf_monospaced_fontname の
// フォントを使う（多くのロケールは freesans / dejavusans、ja は kozminproregular（CID）、
// zh は stsongstdlight、zh-TW は msungstdlight、ko は hysmyeongjostdmedium）。
// buropher は DejaVu Sans / DejaVu Sans Mono を埋め込み、CJK はバイナリを大きくしないよう
// 設定のフォントディレクトリ（[pdf] font_dir / BUROPHER_PDF_FONT_DIR）の TrueType フォントを使う。
// どのフォントにも無い文字は "?" に置き換える（docs/pdf.md）。

import (
	"bytes"
	"compress/gzip"
	"embed"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

//go:embed fonts/*.ttf.gz
var embeddedFonts embed.FS

// Config はフォントの設定（config の [pdf]）。
type Config struct {
	// Dir は追加フォント（CJK 等）を置くディレクトリ（空なら埋め込みフォントのみ）。
	Dir string
	// Fonts はロケール（"ja", "zh", "zh-TW", "ko" ...）ごとのフォントファイル（Dir からの相対パスか絶対パス）。
	// "regular.ttf,bold.ttf" のようにカンマ区切りで太字を指定できる。キー "fallback" は全ロケールの代替フォント。
	// 指定の無いロケールは Dir の既知のファイル名（ipaexg.ttf 等）から自動で選ぶ。
	Fonts map[string]string
	// Logger は読み込めないフォントの警告の出力先（nil なら slog.Default()）。
	Logger *slog.Logger
}

// cjkCandidates はロケールごとに自動で探すファイル名（先にあるものを優先）。
var cjkCandidates = map[string][]string{
	"ja":    {"ipaexg.ttf", "ipag.ttf", "ipagp.ttf", "ipaexm.ttf", "ipam.ttf", "NotoSansJP-Regular.ttf", "DroidSansFallbackFull.ttf", "DroidSansFallback.ttf"},
	"zh":    {"NotoSansSC-Regular.ttf", "NotoSansCJKsc-Regular.ttf", "DroidSansFallbackFull.ttf", "DroidSansFallback.ttf"},
	"zh-TW": {"NotoSansTC-Regular.ttf", "NotoSansCJKtc-Regular.ttf", "DroidSansFallbackFull.ttf", "DroidSansFallback.ttf"},
	"ko":    {"NanumGothic.ttf", "NotoSansKR-Regular.ttf", "NotoSansCJKkr-Regular.ttf", "DroidSansFallbackFull.ttf", "DroidSansFallback.ttf"},
}

// cjkBoldCandidates は自動選択したフォントに対応する太字のファイル名。
var cjkBoldCandidates = map[string]string{
	"NotoSansJP-Regular.ttf": "NotoSansJP-Bold.ttf",
	"NotoSansSC-Regular.ttf": "NotoSansSC-Bold.ttf",
	"NotoSansTC-Regular.ttf": "NotoSansTC-Bold.ttf",
	"NotoSansKR-Regular.ttf": "NotoSansKR-Bold.ttf",
	"NanumGothic.ttf":        "NanumGothicBold.ttf",
}

// face は 1 つの書体（スタイルごとのファイル）。
type face struct {
	id string
	// files はスタイル（"", "B", "I"）ごとのフォントデータの取得関数。
	files map[string]func() ([]byte, error)
	// sf はグリフの有無を調べるための sfnt（通常体）。
	sf *sfnt.Font

	mu     sync.Mutex
	styled map[string]*sfnt.Font
}

// sfntFor はスタイル st（style の結果）の幅を調べるための sfnt（読めなければ通常体）。
func (f *face) sfntFor(st string) *sfnt.Font {
	if st == "" || f.files[st] == nil {
		return f.sf
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.styled[st]; ok {
		return s
	}
	s := f.sf
	if data, err := f.files[st](); err == nil {
		if p, err := sfnt.Parse(data); err == nil {
			s = p
		}
	}
	if f.styled == nil {
		f.styled = map[string]*sfnt.Font{}
	}
	f.styled[st] = s
	return s
}

// style は s に最も近い、この書体が持つスタイルと、太字を合成するかを返す。
func (f *face) style(s string) (actual string, synthBold bool) {
	bold := strings.Contains(s, "B")
	italic := strings.Contains(s, "I")
	switch {
	case bold && f.files["B"] != nil:
		return "B", false
	case bold:
		return "", true
	case italic && f.files["I"] != nil:
		return "I", false
	}
	return "", false
}

// FontSet は読み込んだフォントの集合（アプリ全体で共有し、並行に使える）。
type FontSet struct {
	dejavu, mono *face
	// cjk はロケールごとの書体、extra は代替に使う書体（cjk を含む、重複なし）。
	cjk   map[string]*face
	extra []*face
}

var (
	defaultOnce sync.Once
	defaultSet  *FontSet
)

// Default は埋め込みフォントだけの FontSet（BUROPHER_PDF_FONT_DIR があればそれも使う）。
func Default() *FontSet {
	defaultOnce.Do(func() {
		defaultSet = NewFontSet(Config{Dir: os.Getenv("BUROPHER_PDF_FONT_DIR")})
	})
	return defaultSet
}

// NewFontSet は cfg のフォントを読み込む。読めないフォントは警告して無視する。
func NewFontSet(cfg Config) *FontSet {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	fs := &FontSet{cjk: map[string]*face{}}
	fs.dejavu = mustEmbeddedFace("dejavu", map[string]string{"": "DejaVuSans.ttf.gz", "B": "DejaVuSans-Bold.ttf.gz", "I": "DejaVuSans-Oblique.ttf.gz"})
	fs.mono = mustEmbeddedFace("dejavumono", map[string]string{"": "DejaVuSansMono.ttf.gz"})

	byPath := map[string]*face{}
	load := func(spec string) *face {
		parts := strings.Split(spec, ",")
		regular := resolvePath(cfg.Dir, strings.TrimSpace(parts[0]))
		if regular == "" {
			return nil
		}
		if f, ok := byPath[regular]; ok {
			return f
		}
		bold := ""
		if len(parts) > 1 {
			bold = resolvePath(cfg.Dir, strings.TrimSpace(parts[1]))
		} else if b := cjkBoldCandidates[filepath.Base(regular)]; b != "" {
			if p := filepath.Join(filepath.Dir(regular), b); fileExists(p) {
				bold = p
			}
		}
		f, err := fileFace(fmt.Sprintf("ext%d", len(byPath)), regular, bold)
		if err != nil {
			logger.Warn("pdf: フォントを読み込めません", "file", regular, "err", err)
			byPath[regular] = nil
			return nil
		}
		byPath[regular] = f
		fs.extra = append(fs.extra, f)
		return f
	}
	locales := make([]string, 0, len(cfg.Fonts))
	for k := range cfg.Fonts {
		locales = append(locales, k)
	}
	sort.Strings(locales)
	for _, loc := range locales {
		if loc == "fallback" {
			continue
		}
		if f := load(cfg.Fonts[loc]); f != nil {
			fs.cjk[loc] = f
		}
	}
	if cfg.Dir != "" {
		for _, loc := range []string{"ja", "zh", "zh-TW", "ko"} {
			if fs.cjk[loc] != nil {
				continue
			}
			for _, name := range cjkCandidates[loc] {
				p := filepath.Join(cfg.Dir, name)
				if !fileExists(p) {
					continue
				}
				if f := load(p); f != nil {
					fs.cjk[loc] = f
					break
				}
			}
		}
	}
	if s := cfg.Fonts["fallback"]; s != "" {
		for _, spec := range strings.Split(s, ";") {
			load(spec)
		}
	}
	return fs
}

// HasCJK は locale（"ja" 等）用の CJK フォントがあるか。
func (fs *FontSet) HasCJK(locale string) bool { return fs.cjkFace(locale) != nil }

func (fs *FontSet) cjkFace(locale string) *face {
	if f := fs.cjk[locale]; f != nil {
		return f
	}
	switch strings.ToLower(locale) {
	case "zh-tw":
		return fs.cjk["zh-TW"]
	case "zh-cn", "zh":
		return fs.cjk["zh"]
	}
	return nil
}

// chains はロケールの本文用・等幅用のフォントの優先順。
func (fs *FontSet) chains(locale string) (main, mono []*face) {
	var primary *face
	if IsCJK(locale) {
		primary = fs.cjkFace(locale)
	}
	add := func(list []*face, fs ...*face) []*face {
		for _, f := range fs {
			if f != nil && !containsFace(list, f) {
				list = append(list, f)
			}
		}
		return list
	}
	main = add(nil, primary, fs.dejavu)
	main = add(main, fs.extra...)
	mono = add(nil, fs.mono)
	mono = add(mono, main...)
	return main, mono
}

func containsFace(list []*face, f *face) bool {
	for _, x := range list {
		if x == f {
			return true
		}
	}
	return false
}

// IsCJK は IssuesPdfHelper#is_cjk?（ja / zh-tw / zh / ko）。
func IsCJK(locale string) bool {
	switch strings.ToLower(locale) {
	case "ja", "zh-tw", "zh", "ko":
		return true
	}
	return false
}

func resolvePath(dir, p string) string {
	if p == "" {
		return ""
	}
	if !filepath.IsAbs(p) && dir != "" {
		p = filepath.Join(dir, p)
	}
	return p
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// ---------------------------------------------------------------- 読み込み

// cachedBytes は 1 度だけ読み込むフォントデータ。
func cachedBytes(load func() ([]byte, error)) func() ([]byte, error) {
	var once sync.Once
	var data []byte
	var err error
	return func() ([]byte, error) {
		once.Do(func() { data, err = load() })
		return data, err
	}
}

func embeddedLoader(name string) func() ([]byte, error) {
	return cachedBytes(func() ([]byte, error) {
		raw, err := embeddedFonts.ReadFile("fonts/" + name)
		if err != nil {
			return nil, err
		}
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		return io.ReadAll(zr)
	})
}

func mustEmbeddedFace(id string, files map[string]string) *face {
	f := &face{id: id, files: map[string]func() ([]byte, error){}}
	for st, name := range files {
		f.files[st] = embeddedLoader(name)
	}
	data, err := f.files[""]()
	if err != nil {
		panic("pdf: 埋め込みフォント " + id + ": " + err.Error())
	}
	if err := f.parse(data); err != nil {
		panic("pdf: 埋め込みフォント " + id + ": " + err.Error())
	}
	return f
}

func fileLoader(path string) func() ([]byte, error) {
	return cachedBytes(func() ([]byte, error) { return os.ReadFile(path) })
}

// fileFace は TrueType（glyf）フォントのファイルから書体を作る（fpdf は CFF の OpenType と TTC を扱えない）。
func fileFace(id, regular, bold string) (*face, error) {
	f := &face{id: id, files: map[string]func() ([]byte, error){"": fileLoader(regular)}}
	data, err := f.files[""]()
	if err != nil {
		return nil, err
	}
	if err := checkTrueType(data); err != nil {
		return nil, err
	}
	if err := f.parse(data); err != nil {
		return nil, err
	}
	if bold != "" {
		bdata, err := os.ReadFile(bold)
		if err == nil && checkTrueType(bdata) == nil {
			f.files["B"] = func() ([]byte, error) { return bdata, nil }
		}
	}
	return f, nil
}

func checkTrueType(data []byte) error {
	if len(data) < 4 {
		return fmt.Errorf("フォントではありません")
	}
	switch string(data[:4]) {
	case "\x00\x01\x00\x00", "true":
		return nil
	case "OTTO":
		return fmt.Errorf("CFF（PostScript アウトライン）の OpenType フォントは使えません。TrueType（.ttf）を使ってください")
	case "ttcf":
		return fmt.Errorf("TrueType Collection（.ttc）は使えません。単体の .ttf を使ってください")
	}
	return fmt.Errorf("TrueType フォントではありません")
}

func (f *face) parse(data []byte) error {
	sf, err := sfnt.Parse(data)
	if err != nil {
		return err
	}
	f.sf = sf
	return nil
}

// metrics は 1 文書の中でのグリフの有無と幅のキャッシュ（sfnt.Buffer は並行に使えないので文書ごとに持つ）。
type metrics struct {
	buf   sfnt.Buffer
	has   map[*face]map[rune]bool
	width map[widthKey]float64
}

type widthKey struct {
	sf *sfnt.Font
	r  rune
}

func newMetrics() *metrics {
	return &metrics{has: map[*face]map[rune]bool{}, width: map[widthKey]float64{}}
}

func (m *metrics) glyph(f *face, r rune) (sfnt.GlyphIndex, bool) {
	idx, err := f.sf.GlyphIndex(&m.buf, r)
	return idx, err == nil && idx != 0
}

// hasRune は f が r のグリフを持つか。
func (m *metrics) hasRune(f *face, r rune) bool {
	hm := m.has[f]
	if hm == nil {
		hm = map[rune]bool{}
		m.has[f] = hm
	}
	if v, ok := hm[r]; ok {
		return v
	}
	_, ok := m.glyph(f, r)
	hm[r] = ok
	return ok
}

// advance は f のスタイル st（face.style の結果）での r の送り幅（em 単位）。
func (m *metrics) advance(f *face, st string, r rune) float64 {
	sf := f.sfntFor(st)
	k := widthKey{sf, r}
	if v, ok := m.width[k]; ok {
		return v
	}
	var w float64
	if idx, err := sf.GlyphIndex(&m.buf, r); err == nil && idx != 0 {
		upem := sf.UnitsPerEm()
		adv, err := sf.GlyphAdvance(&m.buf, idx, fixed.Int26_6(upem)<<6, font.HintingNone)
		if err == nil {
			w = float64(adv) / 64 / float64(upem)
		}
	}
	m.width[k] = w
	return w
}
