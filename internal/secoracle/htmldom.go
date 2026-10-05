// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package secoracle

import (
	"errors"
	"fmt"
	"strings"

	"golang.org/x/net/html"

	"github.com/mikuta0407/buropher/internal/textformat/htmldom"
)

// impliedElements は HTML5 パーサが文脈から補う要素（直列化の結果に現れても構わない）。
var impliedElements = toSet("tbody", "tr", "colgroup", "p", "html", "head", "body")

// booleanAttrs は libxml2 が値を出力しない属性（htmlBooleanAttrs）。
var booleanAttrs = toSet("checked", "compact", "declare", "defer", "disabled", "ismap",
	"multiple", "nohref", "noresize", "noshade", "nowrap", "readonly", "selected")

// CheckDOMRender は htmldom の木 t と、その直列化 htmldom.RenderHTML5(t)（Redmine 7.0 の Nokogiri HTML5 直列化）をブラウザ（HTML5 パーサ）が解析した
// 木とを比べる差分オラクル。サニタイザが判定した木と、ブラウザが実際に構築する木が食い違わないこと
// （直列化で要素・属性が増えない、属性値が変わらない）を確かめる。
//
//   - ブラウザ側の要素名は t に現れる要素名か、HTML5 パーサが補う要素のみ
//   - ブラウザ側の各属性（名前）は、t の同名の要素のいずれかが持つ属性
//   - URL 以外の属性値は t の同名要素・同名属性の値のいずれかと（制御文字・改行の正規化後に）一致する
//
// 要素名・属性名が HTML として不正な文字を含む木（パーサが作らないもの）は比較できないため対象外にする。
func CheckDOMRender(t *htmldom.Node) error {
	type elAttr struct{ el, attr string }
	elems := map[string]bool{}
	attrs := map[elAttr]bool{}
	values := map[elAttr]map[string]bool{}
	var collect func(n *htmldom.Node)
	collect = func(n *htmldom.Node) {
		if n.Type == htmldom.ElementNode {
			el := strings.ToLower(n.Data)
			elems[el] = true
			for _, a := range n.Attr {
				k := elAttr{el, strings.ToLower(a.Name)}
				attrs[k] = true
				if values[k] == nil {
					values[k] = map[string]bool{}
				}
				if a.NoValue {
					values[k][""] = true
					values[k][normAttr(a.Name)] = true
				} else {
					values[k][normAttr(a.Value)] = true
				}
				// 真偽属性（checked など）は値を出力しない
				if strings.EqualFold(a.Value, a.Name) || booleanAttrs[strings.ToLower(a.Name)] {
					values[k][""] = true
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			collect(c)
		}
	}
	collect(t)

	out := htmldom.RenderHTML5(t)
	nodes, err := ParseBody(out)
	if err != nil {
		return err
	}
	var errs []error
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			el := n.Data
			if !elems[el] && !impliedElements[el] {
				errs = append(errs, fmt.Errorf("browser sees element <%s> not in sanitized tree", el))
			}
			for _, a := range n.Attr {
				k := elAttr{el, strings.ToLower(a.Key)}
				if a.Namespace != "" {
					k.attr = a.Namespace + ":" + k.attr
				}
				if !attrs[k] {
					errs = append(errs, fmt.Errorf("browser sees attribute %s on <%s> not in sanitized tree (value %q)", k.attr, el, a.Val))
					continue
				}
				if urlAttrs[k.attr] {
					continue
				}
				if !values[k][normAttr(a.Val)] {
					errs = append(errs, fmt.Errorf("browser sees %s=%q on <%s>; sanitized values %q", k.attr, a.Val, el, keys(values[k])))
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	for _, n := range nodes {
		walk(n)
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("%w\nrendered: %q", err, out)
	}
	return nil
}

// normAttr は直列化と HTML5 パーサで変わり得る部分（制御文字・NUL・改行の表現）を正規化する。
func normAttr(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.Map(func(r rune) rune {
		if (r < 0x20 && r != '\n' && r != '\t') || r == 0x7f || r == 0xfffd {
			return -1
		}
		return r
	}, s)
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
