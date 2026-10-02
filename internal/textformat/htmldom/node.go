// Package htmldom は libxml2 2.13（Nokogiri::HTML4）互換の HTML 断片パーサ・
// シリアライザと、Nokogiri 相当の最小限の DOM 操作を提供する。
//
// Redmine の html-pipeline は Nokogiri::HTML::DocumentFragment で HTML を解析し、
// フィルタ適用後に to_s で文字列化する。その結果（自動で閉じられるタグ、
// 改行の挿入、属性値の URI エスケープ等）をバイト単位で再現するため、
// libxml2 の HTMLparser.c / HTMLtree.c の挙動を移植している。
package htmldom

import "strings"

// NodeType はノード種別。
type NodeType uint8

const (
	// FragmentNode は文書断片（Nokogiri::HTML::DocumentFragment 相当）。
	FragmentNode NodeType = iota
	// ElementNode は要素。
	ElementNode
	// TextNode はテキスト。
	TextNode
	// CommentNode はコメント。
	CommentNode
	// PINode は処理命令（<?target content>）。
	PINode
	// CDATANode は CDATA セクション（libxml2 では script/style の内容）。
	CDATANode
)

// Attr は属性。NoValue は値を持たない属性（libxml2 で children が NULL）。
type Attr struct {
	Name    string
	Value   string
	NoValue bool
}

// Node は DOM ノード。
type Node struct {
	Type NodeType
	// Data は要素名・テキスト内容・コメント内容・PI のターゲット名。
	Data string
	// PIContent は PI の内容（無い場合は HasPIContent=false）。
	PIContent    string
	HasPIContent bool
	Attr         []Attr

	Parent, FirstChild, LastChild, PrevSibling, NextSibling *Node
}

// NewElement は要素ノードを作る。
func NewElement(name string, attrs ...Attr) *Node {
	return &Node{Type: ElementNode, Data: name, Attr: attrs}
}

// NewText はテキストノードを作る。
func NewText(s string) *Node {
	return &Node{Type: TextNode, Data: s}
}

// IsElement は要素かどうか（name を指定した場合は要素名も比較する）。
func (n *Node) IsElement(name ...string) bool {
	if n == nil || n.Type != ElementNode {
		return false
	}
	if len(name) == 0 {
		return true
	}
	for _, nm := range name {
		if n.Data == nm {
			return true
		}
	}
	return false
}

// AppendChild は c を末尾の子として追加する（c は事前に切り離される）。
func (n *Node) AppendChild(c *Node) {
	c.Unlink()
	c.Parent = n
	c.PrevSibling = n.LastChild
	if n.LastChild != nil {
		n.LastChild.NextSibling = c
	} else {
		n.FirstChild = c
	}
	n.LastChild = c
}

// InsertBefore は c を n の直前に挿入する。
func (n *Node) InsertBefore(c *Node) {
	if c == n {
		return
	}
	c.Unlink()
	p := n.Parent
	c.Parent = p
	c.NextSibling = n
	c.PrevSibling = n.PrevSibling
	if n.PrevSibling != nil {
		n.PrevSibling.NextSibling = c
	} else if p != nil {
		p.FirstChild = c
	}
	n.PrevSibling = c
}

// InsertAfter は c を n の直後に挿入する。
func (n *Node) InsertAfter(c *Node) {
	if c == n {
		return
	}
	c.Unlink()
	p := n.Parent
	c.Parent = p
	c.PrevSibling = n
	c.NextSibling = n.NextSibling
	if n.NextSibling != nil {
		n.NextSibling.PrevSibling = c
	} else if p != nil {
		p.LastChild = c
	}
	n.NextSibling = c
}

// Unlink は n を親から切り離す。
func (n *Node) Unlink() {
	if n.Parent != nil {
		if n.Parent.FirstChild == n {
			n.Parent.FirstChild = n.NextSibling
		}
		if n.Parent.LastChild == n {
			n.Parent.LastChild = n.PrevSibling
		}
	}
	if n.PrevSibling != nil {
		n.PrevSibling.NextSibling = n.NextSibling
	}
	if n.NextSibling != nil {
		n.NextSibling.PrevSibling = n.PrevSibling
	}
	n.Parent, n.PrevSibling, n.NextSibling = nil, nil, nil
}

// Children は子ノードのスライスを返す（操作中の変更に影響されない）。
func (n *Node) Children() []*Node {
	var out []*Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		out = append(out, c)
	}
	return out
}

// ReplaceWithChildren は Nokogiri の node.replace(node.children) 相当。
// 子を n の位置へ移し、n を切り離す。
func (n *Node) ReplaceWithChildren() {
	for _, c := range n.Children() {
		n.InsertBefore(c)
	}
	n.Unlink()
}

// ReplaceWith は n を nodes で置き換える。
func (n *Node) ReplaceWith(nodes ...*Node) {
	for _, c := range nodes {
		n.InsertBefore(c)
	}
	n.Unlink()
}

// RemoveChildren はすべての子を切り離す。
func (n *Node) RemoveChildren() {
	for _, c := range n.Children() {
		c.Unlink()
	}
}

// AttrIndex は属性の位置を返す（無ければ -1）。
func (n *Node) AttrIndex(name string) int {
	for i, a := range n.Attr {
		if a.Name == name {
			return i
		}
	}
	return -1
}

// HasAttr は属性を持つかどうか。
func (n *Node) HasAttr(name string) bool { return n.AttrIndex(name) >= 0 }

// GetAttr は属性値を返す（Nokogiri の node[name]）。
func (n *Node) GetAttr(name string) (string, bool) {
	if i := n.AttrIndex(name); i >= 0 {
		return n.Attr[i].Value, true
	}
	return "", false
}

// AttrVal は属性値を返す（無ければ空文字列）。
func (n *Node) AttrVal(name string) string {
	v, _ := n.GetAttr(name)
	return v
}

// SetAttr は属性値を設定する（xmlSetProp 相当: 既存なら位置を保ち、無ければ末尾に追加）。
func (n *Node) SetAttr(name, value string) {
	if i := n.AttrIndex(name); i >= 0 {
		n.Attr[i].Value = value
		n.Attr[i].NoValue = false
		return
	}
	n.Attr = append(n.Attr, Attr{Name: name, Value: value})
}

// RemoveAttr は属性を削除する。
func (n *Node) RemoveAttr(name string) {
	if i := n.AttrIndex(name); i >= 0 {
		n.Attr = append(n.Attr[:i:i], n.Attr[i+1:]...)
	}
}

// Text は子孫のテキスト内容を連結して返す（xmlNodeGetContent 相当）。
func (n *Node) Text() string {
	switch n.Type {
	case TextNode, CDATANode, CommentNode:
		return n.Data
	case PINode:
		return n.PIContent
	}
	var sb strings.Builder
	var walk func(*Node)
	walk = func(m *Node) {
		for c := m.FirstChild; c != nil; c = c.NextSibling {
			switch c.Type {
			case TextNode, CDATANode:
				sb.WriteString(c.Data)
			case ElementNode:
				walk(c)
			}
		}
	}
	walk(n)
	return sb.String()
}

// SetText は子をすべて取り除き、1 つのテキストノードにする（Nokogiri の node.content=）。
func (n *Node) SetText(s string) {
	n.RemoveChildren()
	if s != "" {
		n.AppendChild(NewText(s))
	}
}

// Walk は n 以下を前順で辿る。f が false を返すとその子孫は辿らない。
// 走査中のノード変更に備え、次の兄弟は子を辿る前に取得する。
func (n *Node) Walk(f func(*Node) bool) {
	if !f(n) {
		return
	}
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		c.Walk(f)
		c = next
	}
}

// FindAll は条件に合う子孫要素を文書順で集める（Nokogiri の search 相当）。
func (n *Node) FindAll(match func(*Node) bool) []*Node {
	var out []*Node
	var walk func(*Node)
	walk = func(m *Node) {
		for c := m.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == ElementNode {
				if match(c) {
					out = append(out, c)
				}
				walk(c)
			}
		}
	}
	walk(n)
	return out
}

// Ancestors は親を順に辿り、条件に合うものがあれば true を返す。
func (n *Node) HasAncestor(match func(*Node) bool) bool {
	for p := n.Parent; p != nil; p = p.Parent {
		if match(p) {
			return true
		}
	}
	return false
}
