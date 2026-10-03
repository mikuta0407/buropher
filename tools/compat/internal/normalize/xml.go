// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package normalize

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

type xmlNode struct {
	name     string
	attrs    []xml.Attr
	children []*xmlNode // name == "" のときテキストノード
	text     string
}

func xmlName(n xml.Name) string {
	if n.Space != "" {
		return n.Space + ":" + n.Local
	}
	return n.Local
}

// XML は XML を要素順を保ったまま整形する。属性はソートし、空白のみのテキストは捨てる。
func (n *Normalizer) XML(body []byte) (string, error) {
	dec := xml.NewDecoder(bytes.NewReader(body))
	dec.Strict = true
	root := &xmlNode{}
	stack := []*xmlNode{root}
	var header []string
	for {
		tok, err := dec.RawToken()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("xml: %w", err)
		}
		cur := stack[len(stack)-1]
		switch t := tok.(type) {
		case xml.ProcInst:
			header = append(header, fmt.Sprintf("<?%s %s?>", t.Target, strings.TrimSpace(string(t.Inst))))
		case xml.StartElement:
			el := &xmlNode{name: xmlName(t.Name), attrs: append([]xml.Attr(nil), t.Attr...)}
			cur.children = append(cur.children, el)
			stack = append(stack, el)
		case xml.EndElement:
			if len(stack) <= 1 {
				return "", fmt.Errorf("xml: unbalanced end element %s", xmlName(t.Name))
			}
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) > 1 {
				cur.children = append(cur.children, &xmlNode{text: string(t)})
			}
		}
	}
	if len(stack) != 1 {
		return "", fmt.Errorf("xml: unclosed elements")
	}
	var b strings.Builder
	for _, h := range header {
		b.WriteString(h)
		b.WriteByte('\n')
	}
	for _, c := range root.children {
		n.writeXML(&b, c, 0, false)
	}
	return b.String(), nil
}

func xmlEscape(s string) string {
	var buf bytes.Buffer
	_ = xml.EscapeText(&buf, []byte(s))
	return buf.String()
}

func (n *Normalizer) writeXML(b *strings.Builder, x *xmlNode, depth int, masked bool) {
	if x.name == "" {
		return
	}
	masked = masked || n.maskKeys[x.name]
	attrs := append([]xml.Attr(nil), x.attrs...)
	sort.SliceStable(attrs, func(i, j int) bool { return xmlName(attrs[i].Name) < xmlName(attrs[j].Name) })
	indent(b, depth)
	b.WriteByte('<')
	b.WriteString(x.name)
	for _, a := range attrs {
		v := n.String(a.Value)
		if n.maskKeys[xmlName(a.Name)] {
			v = PlaceholderMasked
		}
		fmt.Fprintf(b, ` %s="%s"`, xmlName(a.Name), xmlEscape(v))
	}

	// 空白のみのテキストは除外
	var kids []*xmlNode
	for _, c := range x.children {
		if c.name == "" && strings.TrimSpace(c.text) == "" {
			continue
		}
		kids = append(kids, c)
	}
	if len(kids) == 0 {
		b.WriteString("/>\n")
		return
	}
	b.WriteByte('>')
	textOnly := true
	for _, c := range kids {
		if c.name != "" {
			textOnly = false
		}
	}
	if textOnly {
		var t strings.Builder
		for _, c := range kids {
			t.WriteString(c.text)
		}
		s := n.String(t.String())
		if masked {
			s = PlaceholderMasked
		}
		b.WriteString(xmlEscape(s))
		fmt.Fprintf(b, "</%s>\n", x.name)
		return
	}
	b.WriteByte('\n')
	for _, c := range kids {
		if c.name == "" {
			indent(b, depth+1)
			b.WriteString(xmlEscape(n.String(strings.TrimSpace(c.text))))
			b.WriteByte('\n')
			continue
		}
		n.writeXML(b, c, depth+1, masked)
	}
	indent(b, depth)
	fmt.Fprintf(b, "</%s>\n", x.name)
}
