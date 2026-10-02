package assets

import (
	"io/fs"
	"path"
	"sort"
	"strings"
)

// Theme は Redmine::Themes::Theme 相当（アセット関連部分のみ）。
type Theme struct {
	// ID はディレクトリ名（Setting.ui_theme の値）。
	ID string
	// Name は ID を humanize したもの。
	Name string
	// Stylesheets は stylesheets/*.css の拡張子を除いた名前。
	Stylesheets []string
	// Images は images/* のファイル名。
	Images []string
	// Javascripts は javascripts/*.js の拡張子を除いた名前。
	Javascripts []string
	// Favicons は favicon/* のファイル名。
	Favicons []string

	fsys fs.FS  // テーマディレクトリを含む FS
	dir  string // fsys 内のテーマディレクトリ
}

// AssetPrefix は "themes/<id>/"。
func (t *Theme) AssetPrefix() string { return "themes/" + t.ID + "/" }

// StylesheetPath は Theme#stylesheet_path。
func (t *Theme) StylesheetPath(src string) string { return t.AssetPrefix() + src }

// ImagePath は Theme#image_path。
func (t *Theme) ImagePath(src string) string { return t.AssetPrefix() + src }

// JavascriptPath は Theme#javascript_path。
func (t *Theme) JavascriptPath(src string) string { return t.AssetPrefix() + src }

// Favicon は最初の favicon（無ければ空文字）。
func (t *Theme) Favicon() string {
	if len(t.Favicons) == 0 {
		return ""
	}
	return t.Favicons[0]
}

// FaviconPath は Theme#favicon_path。
func (t *Theme) FaviconPath() string { return t.AssetPrefix() + t.Favicon() }

// HasStylesheet は stylesheets に src が含まれるか。
func (t *Theme) HasStylesheet(src string) bool { return contains(t.Stylesheets, src) }

// HasImage は images に src が含まれるか。
func (t *Theme) HasImage(src string) bool { return contains(t.Images, src) }

// HasJavascript は javascripts に src が含まれるか。
func (t *Theme) HasJavascript(src string) bool { return contains(t.Javascripts, src) }

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// Themes はインストール済みテーマを Name 順で返す。
func (p *Pipeline) Themes() []*Theme { return p.current().themes }

// Theme は ID からテーマを引く（無ければ nil）。
func (p *Pipeline) Theme(id string) *Theme {
	if id == "" {
		return nil
	}
	for _, t := range p.current().themes {
		if t.ID == id {
			return t
		}
	}
	return nil
}

// scanThemes は Redmine::Themes.scan_themes。同梱 → 外部の順に探し、ID 重複は先勝ち。
func (p *Pipeline) scanThemes() ([]*Theme, error) {
	var themes []*Theme
	seen := map[string]bool{}
	scan := func(fsys fs.FS, root string) error {
		if fsys == nil {
			return nil
		}
		entries, err := fs.ReadDir(fsys, root)
		if err != nil {
			return nil
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			dir := path.Join(root, e.Name())
			// テーマは少なくとも application.css を上書きしている必要がある。
			if _, err := fs.Stat(fsys, path.Join(dir, "stylesheets/application.css")); err != nil {
				continue
			}
			if seen[e.Name()] {
				continue
			}
			seen[e.Name()] = true
			t := &Theme{ID: e.Name(), Name: humanize(e.Name()), fsys: fsys, dir: dir}
			t.Stylesheets = globNames(fsys, path.Join(dir, "stylesheets"), ".css")
			t.Images = globNames(fsys, path.Join(dir, "images"), "")
			t.Javascripts = globNames(fsys, path.Join(dir, "javascripts"), ".js")
			t.Favicons = globNames(fsys, path.Join(dir, "favicon"), "")
			themes = append(themes, t)
		}
		return nil
	}
	if err := scan(p.fsys, "themes"); err != nil {
		return nil, err
	}
	if err := scan(p.opts.ExtraThemes, "."); err != nil {
		return nil, err
	}
	sort.SliceStable(themes, func(i, j int) bool { return themes[i].Name < themes[j].Name })
	return themes, nil
}

// globNames は Dir.glob("dir/*.ext") のベース名（ext 指定時は拡張子除去）。
func globNames(fsys fs.FS, dir, ext string) []string {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, ".") {
			continue
		}
		if ext != "" {
			if !strings.HasSuffix(n, ext) {
				continue
			}
			n = strings.TrimSuffix(n, ext)
		}
		out = append(out, n)
	}
	return out
}

// humanize は ActiveSupport の String#humanize の簡易版。
func humanize(s string) string {
	s = strings.TrimSuffix(s, "_id")
	s = strings.ReplaceAll(s, "_", " ")
	s = strings.ToLower(s)
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

type themeFile struct {
	logical      string
	intermediate string
	name         string // fsys 内のパス
}

// assetFiles は Redmine::AssetPath#each_file（テーマ直下のディレクトリ（src 以外）配下）。
func (t *Theme) assetFiles() ([]themeFile, error) {
	entries, err := fs.ReadDir(t.fsys, t.dir)
	if err != nil {
		return nil, err
	}
	prefix := t.AssetPrefix()
	var out []themeFile
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "src" || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		sub := path.Join(t.dir, e.Name())
		err := fs.WalkDir(t.fsys, sub, func(name string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if name != sub && strings.HasPrefix(path.Base(name), ".") {
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				return nil
			}
			rel := strings.TrimPrefix(name, sub+"/")
			fromBase := strings.TrimPrefix(name, t.dir+"/")
			out = append(out, themeFile{
				logical:      prefix + rel,
				intermediate: "/" + prefix + fromBase,
				name:         name,
			})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
