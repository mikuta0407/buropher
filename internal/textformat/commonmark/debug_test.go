package commonmark

import (
	"os"
	"testing"

	"github.com/yuin/goldmark/text"
)

// TestDebugDump は CM_DEBUG 環境変数の Markdown の AST と HTML を表示する（開発用）。
func TestDebugDump(t *testing.T) {
	path := os.Getenv("CM_DEBUG_FILE")
	if path == "" {
		t.Skip("CM_DEBUG_FILE not set")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	b := []byte(src)
	doc := newParser().Parse(text.NewReader(b))
	doc.Dump(b, 0)
	t.Logf("raw: %q", MarkdownToHTML(src, true))
	t.Logf("out: %q", Format(src, Options{}))
}
