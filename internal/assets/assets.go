// Package assets は Propshaft 互換のアセットパイプラインを提供する。
//
// web/assets（embed もしくは開発時のディスク）からファイルを集め、Redmine 6.1.2
// （Propshaft 1.1 + Redmine::AssetPath）と同じ論理パスを割り当て、
// `/assets/<論理パス(拡張子除く)>-<digest>.<ext>` で配信する。
// CSS/JS 内の url() / sourceMappingURL は Propshaft のコンパイラと同様に書き換える。
package assets

import (
	"bytes"
	"compress/gzip"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultPrefix は Propshaft の config.assets.prefix の既定値。
const DefaultPrefix = "/assets"

// Options はパイプライン構築時の設定。
type Options struct {
	// RelativeURLRoot は Rails の relative_url_root 相当（例: "/redmine"）。空ならルート配置。
	RelativeURLRoot string
	// ExtraThemes は外部テーマディレクトリ（直下に <theme>/stylesheets/application.css を持つ）。
	// Redmine の Rails.root/themes 相当。nil なら同梱テーマのみ。
	ExtraThemes fs.FS
	// Dev が true なら、アクセス時（最短 1 秒間隔）にファイル変更を検知して再構築する。
	// os.DirFS と組み合わせて使う。
	Dev bool
}

// Asset はコンパイル済みの 1 アセット。
type Asset struct {
	// LogicalPath は Propshaft の論理パス（例: "jquery/jquery-ui-1.13.2.css"）。
	LogicalPath string
	// DigestedPath はダイジェスト付きパス（例: "jquery/jquery-ui-1.13.2-0123abcd.css"）。
	DigestedPath string
	// Digest はコンパイル後内容の SHA1 先頭 8 桁。
	Digest string
	// ContentType は配信時の Content-Type。
	ContentType string

	content []byte
	gz      []byte
}

// Content はコンパイル済み内容を返す（呼び出し側で変更しないこと）。
func (a *Asset) Content() []byte { return a.content }

// Pipeline はアセット一式を保持する。並行利用可能。
type Pipeline struct {
	fsys    fs.FS
	opts    Options
	prefix  string // "/assets"（relative_url_root を含まない。Propshaft resolver の prefix）
	urlRoot string // relative_url_root（末尾スラッシュなし）

	idx atomic.Pointer[index]

	devMu     sync.Mutex
	lastCheck time.Time
	signature string
}

type index struct {
	assets    map[string]*Asset
	themes    []*Theme
	importmap importmapData
}

// New は fsys（web.Assets() もしくは os.DirFS(<dev_web_dir>/assets)）からパイプラインを構築する。
func New(fsys fs.FS, opts Options) (*Pipeline, error) {
	p := &Pipeline{
		fsys:    fsys,
		opts:    opts,
		prefix:  DefaultPrefix,
		urlRoot: strings.TrimRight(opts.RelativeURLRoot, "/"),
	}
	if err := p.Reload(); err != nil {
		return nil, err
	}
	return p, nil
}

// Reload はファイルを読み直して全アセットを再コンパイルする。
func (p *Pipeline) Reload() error {
	idx, err := p.build()
	if err != nil {
		return err
	}
	p.idx.Store(idx)
	if p.opts.Dev {
		p.devMu.Lock()
		p.signature, _ = p.computeSignature()
		p.lastCheck = time.Now()
		p.devMu.Unlock()
	}
	return nil
}

// current は現在のインデックスを返す。開発モードでは変更検知を行う。
func (p *Pipeline) current() *index {
	if p.opts.Dev {
		p.maybeReload()
	}
	return p.idx.Load()
}

func (p *Pipeline) maybeReload() {
	p.devMu.Lock()
	defer p.devMu.Unlock()
	if time.Since(p.lastCheck) < time.Second {
		return
	}
	p.lastCheck = time.Now()
	sig, err := p.computeSignature()
	if err != nil || sig == p.signature {
		return
	}
	idx, err := p.build()
	if err != nil {
		return
	}
	p.signature = sig
	p.idx.Store(idx)
}

// computeSignature は全ファイルのパス・サイズ・更新時刻から変更検知用の値を作る。
func (p *Pipeline) computeSignature() (string, error) {
	h := sha1.New()
	walk := func(fsys fs.FS, tag string) error {
		if fsys == nil {
			return nil
		}
		return fs.WalkDir(fsys, ".", func(name string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			fmt.Fprintf(h, "%s:%s:%d:%d\n", tag, name, info.Size(), info.ModTime().UnixNano())
			return nil
		})
	}
	if err := walk(p.fsys, "a"); err != nil {
		return "", err
	}
	if err := walk(p.opts.ExtraThemes, "t"); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Lookup は論理パスからアセットを引く。
func (p *Pipeline) Lookup(logical string) (*Asset, bool) {
	a, ok := p.current().assets[logical]
	return a, ok
}

// LogicalPaths は全論理パスをソートして返す（manifest のキー相当）。
func (p *Pipeline) LogicalPaths() []string {
	idx := p.current()
	out := make([]string, 0, len(idx.assets))
	for k := range idx.assets {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Manifest は Propshaft の .manifest.json 相当（論理パス → ダイジェスト付きパス）を返す。
func (p *Pipeline) Manifest() map[string]string {
	idx := p.current()
	m := make(map[string]string, len(idx.assets))
	for k, a := range idx.assets {
		m[k] = a.DigestedPath
	}
	return m
}

// urlPrefix は CSS/JS 内で使う接頭辞（File.join(relative_url_root, prefix).chomp("/")）。
func (p *Pipeline) urlPrefix() string {
	return strings.TrimRight(p.urlRoot+p.prefix, "/")
}

// ---- 収集 ----

// source はコンパイル前の 1 ファイル。
type source struct {
	logical string
	fsys    fs.FS
	name    string // fsys 内のパス
	// transition は Redmine::Asset の url 置換表（テーマ CSS のみ）。
	transition []kv
	raw        []byte
}

type kv struct{ key, val string }

// 平坦化してルートに置く Propshaft のロードパス（Rails の app/assets/* はソート順）。
// themes はここではなく、"<theme>/..." と "themes/<theme>/..." の 2 通りで別途登録する。
var flatRoots = []string{"fonts", "images", "javascripts", "stylesheets"}

// importmap 由来のロードパス（app/javascript, vendor/javascript とgem の app/assets/javascripts 相当）。
var importmapRoots = []string{"javascript", "vendor"}

func (p *Pipeline) build() (*index, error) {
	var order []string
	srcs := map[string]*source{}
	add := func(s *source) {
		// Propshaft と同じく先勝ち（||=）。
		if _, ok := srcs[s.logical]; ok {
			return
		}
		srcs[s.logical] = s
		order = append(order, s.logical)
	}

	walkInto := func(fsys fs.FS, root string, fn func(rel, name string)) error {
		if _, err := fs.Stat(fsys, root); err != nil {
			return nil //nolint:nilerr // ルートが無いソースは空として扱う
		}
		return fs.WalkDir(fsys, root, func(name string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			base := path.Base(name)
			if name != root && strings.HasPrefix(base, ".") {
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				return nil
			}
			rel := strings.TrimPrefix(name, root+"/")
			fn(rel, name)
			return nil
		})
	}

	// 1. app/assets/{fonts,images,javascripts,stylesheets}
	for _, root := range flatRoots {
		if err := walkInto(p.fsys, root, func(rel, name string) {
			add(&source{logical: rel, fsys: p.fsys, name: name})
		}); err != nil {
			return nil, err
		}
	}
	// 2. app/assets/themes（Propshaft の通常ロードパスとして "<theme>/<sub>/<file>" が出来る）
	if err := walkInto(p.fsys, "themes", func(rel, name string) {
		add(&source{logical: rel, fsys: p.fsys, name: name})
	}); err != nil {
		return nil, err
	}
	// 3. app/javascript, vendor/javascript（+ gem 由来の stimulus / requestjs）
	for _, root := range importmapRoots {
		if err := walkInto(p.fsys, root, func(rel, name string) {
			add(&source{logical: rel, fsys: p.fsys, name: name})
		}); err != nil {
			return nil, err
		}
	}
	// 4. テーマ（Redmine::AssetPath, prefix "themes/<dir>/"）
	themes, err := p.scanThemes()
	if err != nil {
		return nil, err
	}
	defaultDests, err := p.defaultDests()
	if err != nil {
		return nil, err
	}
	for _, th := range themes {
		files, err := th.assetFiles()
		if err != nil {
			return nil, err
		}
		tr := &transition{}
		for _, d := range defaultDests {
			tr.addDest(d[0], d[1])
		}
		for _, f := range files {
			tr.addSrc(f.intermediate, f.logical)
			tr.addDest(f.intermediate, f.logical)
		}
		tmap := tr.update()
		for _, f := range files {
			s := &source{logical: f.logical, fsys: th.fsys, name: f.name}
			if path.Ext(f.logical) == ".css" {
				s.transition = tmap[logicalDir(f.logical)]
			}
			add(s)
		}
	}

	for _, s := range srcs {
		b, err := fs.ReadFile(s.fsys, s.name)
		if err != nil {
			return nil, err
		}
		s.raw = b
	}

	c := &compiler{
		srcs:      srcs,
		urlPrefix: p.urlPrefix(),
		done:      map[string]*Asset{},
		visiting:  map[string]bool{},
	}
	idx := &index{assets: make(map[string]*Asset, len(srcs)), themes: themes}
	for _, l := range order {
		idx.assets[l] = c.compile(l)
	}
	if !p.opts.Dev {
		for _, a := range idx.assets {
			a.gz = gzipIfUseful(a)
		}
	}
	idx.importmap = p.buildImportmap(idx)
	return idx, nil
}

// defaultDests は Redmine の default_asset_path（javascripts, images, stylesheets）の
// 各ファイルを (intermediate_path, logical_path) で返す。テーマの url 置換表の計算に使う。
func (p *Pipeline) defaultDests() ([][2]string, error) {
	var out [][2]string
	for _, root := range []string{"javascripts", "images", "stylesheets"} {
		if _, err := fs.Stat(p.fsys, root); err != nil {
			continue
		}
		err := fs.WalkDir(p.fsys, root, func(name string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if name != root && strings.HasPrefix(path.Base(name), ".") {
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				return nil
			}
			out = append(out, [2]string{"/" + name, strings.TrimPrefix(name, root+"/")})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// logicalDir は Pathname#dirname 相当（ルートは "."）。
func logicalDir(logical string) string {
	return path.Dir(logical)
}

// ---- コンパイル ----

type compiler struct {
	srcs      map[string]*source
	urlPrefix string
	done      map[string]*Asset
	visiting  map[string]bool
}

// digestedPathRe は Propshaft::Asset#digested_path の /\.(\w+(\.map)?)$/。
var digestedPathRe = regexp.MustCompile(`\.([A-Za-z0-9_]+(?:\.map)?)$`)

// alreadyDigestedRe は Propshaft::Asset#already_digested? の判定。
var alreadyDigestedRe = regexp.MustCompile(`-[0-9a-zA-Z_-]{7,128}\.digested`)

func digestedPath(logical, digest string) string {
	if alreadyDigestedRe.MatchString(logical) {
		return logical
	}
	loc := digestedPathRe.FindStringIndex(logical)
	if loc == nil {
		return logical
	}
	return logical[:loc[0]] + "-" + digest + logical[loc[0]:]
}

func sha8(b []byte) string {
	sum := sha1.Sum(b)
	return hex.EncodeToString(sum[:])[:8]
}

// compile は論理パスのアセットをコンパイルする（参照先を再帰的に先にコンパイル）。
func (c *compiler) compile(logical string) *Asset {
	if a, ok := c.done[logical]; ok {
		return a
	}
	s := c.srcs[logical]
	ct := contentType(logical)
	content := s.raw
	c.visiting[logical] = true
	switch ct {
	case "text/css":
		in := string(content)
		if len(s.transition) > 0 {
			in = convertTransition(in, s.transition)
		}
		in = c.cssAssetURLs(logical, in)
		in = c.sourceMappingURLs(logical, in)
		content = []byte(in)
	case "text/javascript":
		in := string(content)
		in = c.jsAssetURLs(logical, in)
		in = c.sourceMappingURLs(logical, in)
		content = []byte(in)
	}
	delete(c.visiting, logical)
	d := sha8(content)
	a := &Asset{
		LogicalPath:  logical,
		DigestedPath: digestedPath(logical, d),
		Digest:       d,
		ContentType:  ct,
		content:      content,
	}
	c.done[logical] = a
	return a
}

// find は参照先アセットのダイジェスト付きパスを返す。循環参照時は生内容のダイジェストを使う。
func (c *compiler) find(logical string) (string, bool) {
	if _, ok := c.srcs[logical]; !ok {
		return "", false
	}
	if c.visiting[logical] {
		return digestedPath(logical, sha8(c.srcs[logical].raw)), true
	}
	return c.compile(logical).DigestedPath, true
}

// contentType は Rails の Mime 登録 + Rack の静的配信に合わせた Content-Type。
func contentType(logical string) string {
	switch strings.ToLower(path.Ext(logical)) {
	case ".css":
		return "text/css"
	case ".js":
		return "text/javascript"
	case ".map":
		return "application/json"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".svg":
		return "image/svg+xml"
	case ".webp":
		return "image/webp"
	case ".ico":
		return "image/vnd.microsoft.icon"
	case ".woff2":
		return "font/woff2"
	case ".woff":
		return "font/woff"
	case ".ttf":
		return "font/ttf"
	case ".otf":
		return "font/otf"
	case ".txt":
		return "text/plain"
	case ".html":
		return "text/html"
	case ".json":
		return "application/json"
	}
	return "application/octet-stream"
}

func compressible(ct string) bool {
	switch ct {
	case "text/css", "text/javascript", "application/json", "image/svg+xml", "text/plain", "text/html", "image/vnd.microsoft.icon":
		return true
	}
	return false
}

func gzipIfUseful(a *Asset) []byte {
	if !compressible(a.ContentType) || len(a.content) < 1024 {
		return nil
	}
	var buf bytes.Buffer
	w, _ := gzip.NewWriterLevel(&buf, gzip.DefaultCompression)
	_, _ = w.Write(a.content) // bytes.Buffer への書き込みは失敗しない
	_ = w.Close()
	if buf.Len() >= len(a.content)*9/10 {
		return nil
	}
	return buf.Bytes()
}
