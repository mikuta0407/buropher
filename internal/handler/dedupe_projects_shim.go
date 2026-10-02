package handler

import (
	"strings"

	"github.com/mikuta0407/buropher/internal/domain"
)

// このファイルは projects ブランチ（handler/projects.go）にある関数の一時的な複製。
// query_view.go（projects ブランチから同一内容で取り込んだもの）が依存している。
// TODO(dedupe): projects ブランチが master に入ったらこのファイルを削除する。

func (c *Req) allowedToGloballyOrProject(perm string, p *domain.Project) bool {
	if p != nil {
		return c.AllowedTo(domain.Perm(perm), p)
	}
	return c.AllowedToGlobally(domain.Perm(perm))
}

func escapeHTML(s string) string {
	return strings.NewReplacer("&", "&amp;", `"`, "&quot;", "'", "&#39;", "<", "&lt;", ">", "&gt;").Replace(s)
}
