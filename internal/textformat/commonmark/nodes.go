package commonmark

import (
	"github.com/yuin/goldmark/ast"
)

// comrak の AST に合わせた独自ノード。goldmark の構文解析結果をこれらへ
// 正規化してから、comrak の html.rs と同じ規則で HTML を出力する。

var (
	// KindAlert は GitHub 形式のアラート（> [!NOTE]）。
	KindAlert = ast.NewNodeKind("CMAlert")
	// KindFootnoteDef は脚注定義。
	KindFootnoteDef = ast.NewNodeKind("CMFootnoteDef")
	// KindFootnoteRef は脚注参照。
	KindFootnoteRef = ast.NewNodeKind("CMFootnoteRef")
	// KindStr は確定済みの文字列（実体参照・エスケープ解決済み）。
	KindStr = ast.NewNodeKind("CMStr")
	// KindEscaped はバックスラッシュエスケープ（escaped_char_spans）。
	KindEscaped = ast.NewNodeKind("CMEscaped")
	// KindSoftBreak はソフト改行。
	KindSoftBreak = ast.NewNodeKind("CMSoftBreak")
	// KindHardBreak はハード改行。
	KindHardBreak = ast.NewNodeKind("CMHardBreak")
	// KindLink はリンク（自動リンクを含む）。
	KindLink = ast.NewNodeKind("CMLink")
	// KindImage は画像。
	KindImage = ast.NewNodeKind("CMImage")
	// KindCode はコードスパン。
	KindCode = ast.NewNodeKind("CMCode")
	// KindHTMLInline はインライン HTML。
	KindHTMLInline = ast.NewNodeKind("CMHTMLInline")
)

// Alert はアラートブロック。
type Alert struct {
	ast.BaseBlock
	AlertType string // note, tip, important, warning, caution
	Title     string
	HasTitle  bool
}

// Kind implements ast.Node.
func (n *Alert) Kind() ast.NodeKind { return KindAlert }

// Dump implements ast.Node.
func (n *Alert) Dump(source []byte, level int) { ast.DumpHelper(n, source, level, nil, nil) }

// FootnoteDef は脚注定義。
type FootnoteDef struct {
	ast.BaseBlock
	Name      string
	TotalRefs int
	Ix        int
}

// Kind implements ast.Node.
func (n *FootnoteDef) Kind() ast.NodeKind { return KindFootnoteDef }

// Dump implements ast.Node.
func (n *FootnoteDef) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, map[string]string{"Name": n.Name}, nil)
}

// FootnoteRef は脚注参照。
type FootnoteRef struct {
	ast.BaseInline
	Name   string
	Ix     int
	RefNum int
}

// Kind implements ast.Node.
func (n *FootnoteRef) Kind() ast.NodeKind { return KindFootnoteRef }

// Dump implements ast.Node.
func (n *FootnoteRef) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, map[string]string{"Name": n.Name}, nil)
}

// Str は確定済みの文字列。
type Str struct {
	ast.BaseInline
	Value string
}

// Kind implements ast.Node.
func (n *Str) Kind() ast.NodeKind { return KindStr }

// Dump implements ast.Node.
func (n *Str) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, map[string]string{"Value": n.Value}, nil)
}

// Escaped はバックスラッシュでエスケープされた 1 文字（子に Str を持つ）。
type Escaped struct{ ast.BaseInline }

// Kind implements ast.Node.
func (n *Escaped) Kind() ast.NodeKind { return KindEscaped }

// Dump implements ast.Node.
func (n *Escaped) Dump(source []byte, level int) { ast.DumpHelper(n, source, level, nil, nil) }

// SoftBreak はソフト改行。
type SoftBreak struct{ ast.BaseInline }

// Kind implements ast.Node.
func (n *SoftBreak) Kind() ast.NodeKind { return KindSoftBreak }

// Dump implements ast.Node.
func (n *SoftBreak) Dump(source []byte, level int) { ast.DumpHelper(n, source, level, nil, nil) }

// HardBreak はハード改行。
type HardBreak struct{ ast.BaseInline }

// Kind implements ast.Node.
func (n *HardBreak) Kind() ast.NodeKind { return KindHardBreak }

// Dump implements ast.Node.
func (n *HardBreak) Dump(source []byte, level int) { ast.DumpHelper(n, source, level, nil, nil) }

// Link はリンク。
type Link struct {
	ast.BaseInline
	URL, Title string
}

// Kind implements ast.Node.
func (n *Link) Kind() ast.NodeKind { return KindLink }

// Dump implements ast.Node.
func (n *Link) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, map[string]string{"URL": n.URL}, nil)
}

// Image は画像。
type Image struct {
	ast.BaseInline
	URL, Title string
}

// Kind implements ast.Node.
func (n *Image) Kind() ast.NodeKind { return KindImage }

// Dump implements ast.Node.
func (n *Image) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, map[string]string{"URL": n.URL}, nil)
}

// Code はコードスパン。
type Code struct {
	ast.BaseInline
	Literal string
}

// Kind implements ast.Node.
func (n *Code) Kind() ast.NodeKind { return KindCode }

// Dump implements ast.Node.
func (n *Code) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, map[string]string{"Literal": n.Literal}, nil)
}

// HTMLInline はインライン HTML。
type HTMLInline struct {
	ast.BaseInline
	Literal string
}

// Kind implements ast.Node.
func (n *HTMLInline) Kind() ast.NodeKind { return KindHTMLInline }

// Dump implements ast.Node.
func (n *HTMLInline) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, map[string]string{"Literal": n.Literal}, nil)
}
