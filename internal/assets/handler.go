package assets

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// extractDigestRe は Propshaft::Asset.extract_path_and_digest の
// /-([0-9a-zA-Z]{7,128})\.(?!digested)([^.]|.map)+\z/（先読みは省略）。
var extractDigestRe = regexp.MustCompile(`-([0-9a-zA-Z]{7,128})\.(?:[^.]|.map)+$`)

// ExtractPathAndDigest はダイジェスト付きパスを論理パスとダイジェストに分ける。
func ExtractPathAndDigest(digested string) (logical, digest string) {
	m := extractDigestRe.FindStringSubmatch(digested)
	if m == nil || strings.HasPrefix(m[0][len(m[1])+2:], "digested") {
		return digested, ""
	}
	digest = m[1]
	return strings.Replace(digested, "-"+digest, "", 1), digest
}

// Handler は /assets/* を配信する http.Handler を返す（Propshaft::Server 相当）。
// リクエストパスの "/assets/" 接頭辞は内部で取り除く。
// ダイジェストが一致しない場合は Propshaft と同じく 404 "Not found" を返す。
func (p *Pipeline) Handler() http.Handler {
	return http.HandlerFunc(p.serveHTTP)
}

func notFound(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("Content-Length", "9")
	w.WriteHeader(http.StatusNotFound)
	w.Write([]byte("Not found"))
}

func (p *Pipeline) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	reqPath := strings.TrimPrefix(r.URL.Path, p.prefix+"/")
	reqPath = strings.TrimPrefix(reqPath, "/")
	logical, digest := ExtractPathAndDigest(reqPath)
	a, ok := p.Lookup(logical)
	// Propshaft::Asset#fresh?: ダイジェスト一致か、既にダイジェスト済み（*.digested）。
	if !ok || (a.Digest != digest && !alreadyDigestedRe.MatchString(a.LogicalPath)) {
		notFound(w)
		return
	}

	h := w.Header()
	etag := `"` + a.Digest + `"`
	h.Set("Content-Type", a.ContentType)
	h.Set("Vary", "Accept-Encoding")
	h.Set("ETag", etag)
	h.Set("Cache-Control", "public, max-age=31536000, immutable")
	if inm := r.Header.Get("If-None-Match"); inm != "" && etagMatch(inm, etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	body := a.content
	if a.gz != nil && acceptsGzip(r.Header.Get("Accept-Encoding")) {
		body = a.gz
		h.Set("Content-Encoding", "gzip")
	}
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	w.Write(body)
}

func etagMatch(header, etag string) bool {
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		part = strings.TrimPrefix(part, "W/")
		if part == "*" || part == etag || part == strings.Trim(etag, `"`) {
			return true
		}
	}
	return false
}

func acceptsGzip(ae string) bool {
	for _, part := range strings.Split(ae, ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(name), "gzip") {
			continue
		}
		if q, ok := strings.CutPrefix(strings.ReplaceAll(params, " ", ""), "q="); ok {
			if f, err := strconv.ParseFloat(q, 64); err == nil && f == 0 {
				return false
			}
		}
		return true
	}
	return false
}
