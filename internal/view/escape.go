// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package view

import (
	"html/template"
	"text/template/parse"

	"github.com/mikuta0407/buropher/internal/view/rails"
)

// ERB の <%= %> と同じ自動エスケープを text/template で実現する。
//
// html/template は文脈依存エスケープにより ERB と異なるバイト列を出力する
// （" → &#34;、+ → &#43;、HTML コメントの除去、<script> 内の値の JSON 化、危険 URL の #ZgotmplZ 化など）。
// Redmine と同一の HTML を出すため、テンプレートは text/template で解析し、
// 出力を伴うすべてのアクション {{...}} のパイプライン末尾に escapeFuncName を追加する。
// これは ERB の「html_safe でない値は ERB::Util.html_escape する」と同じ規則で、
// template.HTML（= html_safe）の値だけがそのまま出力される。

const escapeFuncName = "_erb_h"

// erbEscape は ERB の <%= %> の出力処理。
func erbEscape(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case template.HTML:
		return string(x)
	case string:
		return rails.EscapeString(x)
	}
	return rails.EscapeString(rails.ToS(v))
}

// erbRaw はエスケープしない出力処理（.text.tmpl 用。ERB の text/plain テンプレートは
// escape_ignore_list によりエスケープされない）。値の文字列化は ERB と同じ to_s 規則。
func erbRaw(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case template.HTML:
		return string(x)
	case string:
		return x
	}
	return rails.ToS(v)
}

// addEscaper は木のすべての出力アクションに escapeFuncName を追加する。
func addEscaper(tree *parse.Tree, name string) {
	if tree == nil || tree.Root == nil {
		return
	}
	walkEscape(tree, tree.Root, name)
}

func walkEscape(tree *parse.Tree, node parse.Node, name string) {
	switch n := node.(type) {
	case *parse.ListNode:
		if n == nil {
			return
		}
		for _, c := range n.Nodes {
			walkEscape(tree, c, name)
		}
	case *parse.ActionNode:
		if len(n.Pipe.Decl) > 0 || n.Pipe.IsAssign {
			return
		}
		id := parse.NewIdentifier(name).SetTree(tree).SetPos(n.Pos)
		n.Pipe.Cmds = append(n.Pipe.Cmds, &parse.CommandNode{
			NodeType: parse.NodeCommand,
			Pos:      n.Pos,
			Args:     []parse.Node{id},
		})
	case *parse.IfNode:
		walkEscape(tree, n.List, name)
		walkEscape(tree, n.ElseList, name)
	case *parse.RangeNode:
		walkEscape(tree, n.List, name)
		walkEscape(tree, n.ElseList, name)
	case *parse.WithNode:
		walkEscape(tree, n.List, name)
		walkEscape(tree, n.ElseList, name)
	}
}
