// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package assets

import (
	"path"
	"regexp"
	"strings"
)

// cssURLRe は Propshaft::Compiler::CssAssetUrls::ASSET_URL_PATTERN から否定先読みを除いたもの。
//
//	/url\(\s*["']?(?!(?:\#|%23|data|http|\/\/))([^"'\s?#)]+)([#?][^"')]+)?\s*["']?\)/
//
// 先読み部分は matchExcluded で後判定する（RE2 は先読み非対応）。
var cssURLRe = regexp.MustCompile(`url\(\s*["']?([^"'\s?#)]+)([#?][^"')]+)?\s*["']?\)`)

// jsURLRe は Propshaft::Compiler::JsAssetUrls::ASSET_URL_PATTERN（先読み除く）。
var jsURLRe = regexp.MustCompile(`RAILS_ASSET_URL\(\s*["']?([^"'\s?#)]+)([#?][^"')]+)?\s*["']?\)`)

// sourceMappingRe は Propshaft::Compiler::SourceMappingUrls::SOURCE_MAPPING_PATTERN。
//
//	%r{(//|/\*)# sourceMappingURL=(.+\.map)(\s*?\*\/)?\s*?\Z}
//
// Ruby の \Z（末尾または末尾改行の直前）は (\n?)\z で表し、末尾改行は置換後も残す。
var sourceMappingRe = regexp.MustCompile(`(//|/\*)# sourceMappingURL=(.+\.map)(\s*?\*/)?\s*?(\n?)\z`)

// redmineURLRe は Redmine::Asset::ASSET_URL_PATTERN。
var redmineURLRe = regexp.MustCompile(`url\(\s*["']?[^"'\s)]+\s*["']?\s*\)`)

// matchExcluded は (?!(?:\#|%23|data|http|\/\/)) に該当する（＝書き換え対象外）か。
// group1 は '#' を含み得ないので '#' の判定は不要。
func matchExcluded(target string) bool {
	return strings.HasPrefix(target, "%23") || strings.HasPrefix(target, "data") ||
		strings.HasPrefix(target, "http") || strings.HasPrefix(target, "//")
}

// replaceAssetURLs は先読み除外付きで re のマッチを順に置換する。
// 除外されたマッチは Ruby と同様に開始位置 +1 から再走査する。
func replaceAssetURLs(re *regexp.Regexp, input string, repl func(target, fingerprint string) string) string {
	var b strings.Builder
	pos := 0
	last := 0
	for pos <= len(input) {
		m := re.FindStringSubmatchIndex(input[pos:])
		if m == nil {
			break
		}
		start, end := pos+m[0], pos+m[1]
		target := input[pos+m[2] : pos+m[3]]
		if matchExcluded(target) {
			pos = start + 1
			continue
		}
		fingerprint := ""
		if m[4] >= 0 {
			fingerprint = input[pos+m[4] : pos+m[5]]
		}
		b.WriteString(input[last:start])
		b.WriteString(repl(target, fingerprint))
		last = end
		pos = end
	}
	if last == 0 {
		return input
	}
	b.WriteString(input[last:])
	return b.String()
}

// resolvePath は Propshaft コンパイラの resolve_path。
func resolvePath(dir, filename string) string {
	switch {
	case strings.HasPrefix(filename, "../"):
		return path.Clean(path.Join(dir, filename))
	case strings.HasPrefix(filename, "/"):
		return strings.TrimPrefix(filename, "/")
	default:
		return path.Join(dir, strings.TrimPrefix(filename, "./"))
	}
}

func (c *compiler) cssAssetURLs(logical, input string) string {
	dir := logicalDir(logical)
	return replaceAssetURLs(cssURLRe, input, func(target, fingerprint string) string {
		if dp, ok := c.find(resolvePath(dir, target)); ok {
			return `url("` + c.urlPrefix + "/" + dp + fingerprint + `")`
		}
		// 見つからない場合は Propshaft と同じく fingerprint を落として引用符で囲む。
		return `url("` + target + `")`
	})
}

func (c *compiler) jsAssetURLs(logical, input string) string {
	dir := logicalDir(logical)
	return replaceAssetURLs(jsURLRe, input, func(target, fingerprint string) string {
		if dp, ok := c.find(resolvePath(dir, target)); ok {
			return `"` + c.urlPrefix + "/" + dp + fingerprint + `"`
		}
		return `"` + target + `"`
	})
}

func (c *compiler) sourceMappingURLs(logical, input string) string {
	m := sourceMappingRe.FindStringSubmatchIndex(input)
	if m == nil {
		return input
	}
	commentStart := input[m[2]:m[3]]
	mapURL := input[m[4]:m[5]]
	commentEnd := ""
	if m[6] >= 0 {
		commentEnd = input[m[6]:m[7]]
	}
	trailingNL := input[m[8]:m[9]]

	// source_mapping_url.gsub!(/^(.+\/)?#{url_prefix}\//, "")
	stripRe := regexp.MustCompile(`^(.+/)?` + regexp.QuoteMeta(c.urlPrefix) + `/`)
	mapURL = stripRe.ReplaceAllString(mapURL, "")
	resolved := mapURL
	if d := logicalDir(logical); d != "." {
		resolved = path.Join(d, mapURL)
	}
	var repl string
	if dp, ok := c.find(resolved); ok {
		repl = commentStart + "# sourceMappingURL=" + c.urlPrefix + "/" + dp + commentEnd
	} else {
		repl = commentStart + commentEnd
	}
	return input[:m[0]] + repl + trailingNL
}

// convertTransition は Redmine::Asset#convert_path。url(...) ごとに置換表を順に 1 回ずつ適用する。
func convertTransition(input string, conv []kv) string {
	return redmineURLRe.ReplaceAllStringFunc(input, func(matched string) string {
		for _, e := range conv {
			matched = strings.Replace(matched, e.key, e.val, 1)
		}
		return matched
	})
}

// ---- Redmine::AssetPath::Transition の移植 ----

type pathPair struct{ dir, ldir string }

type transition struct {
	src, dest []pathPair
}

func pairOf(intermediate, logical string) pathPair {
	return pathPair{path.Dir(intermediate), path.Dir("/" + logical)}
}

func addUniquePair(list []pathPair, p pathPair) []pathPair {
	for _, q := range list {
		if q == p {
			return list
		}
	}
	return append(list, p)
}

func (t *transition) addSrc(intermediate, logical string) {
	if path.Ext(intermediate) == ".css" {
		t.src = addUniquePair(t.src, pairOf(intermediate, logical))
	}
}

func (t *transition) addDest(intermediate, logical string) {
	ext := path.Ext(intermediate)
	if ext == ".js" || ext == ".map" {
		return
	}
	dirname := path.Dir(intermediate)
	for i, d := range t.dest {
		if isChildPath(dirname, d.dir) {
			t.dest = append(t.dest[:i:i], t.dest[i+1:]...)
			t.dest = addUniquePair(t.dest, pairOf(intermediate, logical))
			return
		}
	}
	for _, d := range t.dest {
		if isParentPath(dirname, d.dir) {
			return
		}
	}
	t.dest = addUniquePair(t.dest, pairOf(intermediate, logical))
}

// ascends は Pathname#ascend（自身からルートまで）。
func ascends(p, target string) bool {
	for {
		if p == target {
			return true
		}
		if p == "/" || p == "." || p == "" {
			return false
		}
		p = path.Dir(p)
	}
}

func isParentPath(p, other string) bool { return p != other && ascends(p, other) }
func isChildPath(p, other string) bool  { return p != other && ascends(other, p) }

// relPath は Pathname#relative_path_from（target を base からの相対で表す）。
func relPath(base, target string) string {
	split := func(s string) []string {
		s = strings.Trim(s, "/")
		if s == "" {
			return nil
		}
		return strings.Split(s, "/")
	}
	b, t := split(base), split(target)
	i := 0
	for i < len(b) && i < len(t) && b[i] == t[i] {
		i++
	}
	var parts []string
	for range b[i:] {
		parts = append(parts, "..")
	}
	parts = append(parts, t[i:]...)
	if len(parts) == 0 {
		return "."
	}
	return strings.Join(parts, "/")
}

// update は置換表（CSS の論理ディレクトリ → 順序付き置換リスト）を作る。
func (t *transition) update() map[string][]kv {
	out := map[string][]kv{}
	for _, s := range t.src {
		for _, d := range t.dest {
			if s == d {
				continue
			}
			before := relPath(s.dir, d.dir)
			after := relPath(s.ldir, d.ldir)
			if before == after {
				continue
			}
			key := strings.Replace(s.ldir, "/", "", 1)
			if key == "" {
				key = "."
			}
			list := out[key]
			replaced := false
			for i := range list {
				if list[i].key == before {
					list[i].val = after
					replaced = true
					break
				}
			}
			if !replaced {
				list = append(list, kv{before, after})
			}
			out[key] = list
		}
	}
	return out
}
