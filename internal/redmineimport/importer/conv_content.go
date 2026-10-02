package importer

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode/utf8"
)

// ---------------------------------------------------------------- documents / news / comments

func (im *imp) importDocuments() error {
	t := im.table("documents")
	ins := im.ins(t, "documents", "id", "project_id", "category_id", "title", "description", "created_at")
	err := im.src.each("documents", func(r rec) error {
		id := r.id()
		p := r.ref("project_id")
		if !im.projectExists(p) {
			t.drop(id, "project_id refers to a missing project")
			return nil
		}
		cat := r.ref("category_id")
		if !im.st.docCategories.has(cat) {
			if im.st.defaultDocCategory == 0 {
				t.drop(id, "category_id missing and no document category exists")
				return nil
			}
			t.repair(id, "category_id 0/missing; set to the default document category")
			cat = im.st.defaultDocCategory
		}
		im.st.documents.add(id)
		return ins.add(id, p, cat, r.str("title"), r.strNull("description", false), im.tsOr(t, id, r, "created_on"))
	})
	if err != nil {
		return err
	}
	_, err = ins.close()
	return err
}

func (im *imp) importNews() error {
	t := im.table("news")
	ins := im.ins(t, "news", "id", "project_id", "title", "summary", "description", "author_id", "comments_count", "created_at")
	err := im.src.each("news", func(r rec) error {
		id := r.id()
		p := r.ref("project_id")
		if !im.projectExists(p) {
			t.drop(id, "project_id NULL or missing")
			return nil
		}
		author := im.authorOr(t, id, "author_id", r.ref("author_id"))
		im.st.news.add(id)
		return ins.add(id, p, r.str("title"), r.strNull("summary", false), r.strNull("description", false), author, r.intOr("comments_count", 0), im.tsOr(t, id, r, "created_on"))
	})
	if err != nil {
		return err
	}
	_, err = ins.close()
	return err
}

func (im *imp) importComments() error {
	t := im.table("comments")
	ins := im.ins(t, "news_comments", "id", "news_id", "author_id", "content", "created_at", "updated_at")
	err := im.src.each("comments", func(r rec) error {
		id := r.id()
		if typ := r.str("commented_type"); typ != "News" {
			t.drop(id, "commented_type %q is not News (plugin)", typ)
			return nil
		}
		n := r.ref("commented_id")
		if !im.st.news.has(n) {
			t.drop(id, "commented_id refers to missing news")
			return nil
		}
		author := im.authorOr(t, id, "author_id", r.ref("author_id"))
		im.st.comments.add(id)
		return ins.add(id, n, author, r.strNull("content", false), im.tsOr(t, id, r, "created_on", "updated_on"), im.tsOr(t, id, r, "updated_on", "created_on"))
	})
	if err != nil {
		return err
	}
	_, err = ins.close()
	return err
}

// ---------------------------------------------------------------- boards / messages

func (im *imp) importBoards() error {
	t := im.table("boards")
	rows, err := im.src.all("boards")
	if err != nil {
		return err
	}
	project := map[int64]int64{}
	for _, r := range rows {
		if p := r.ref("project_id"); im.projectExists(p) {
			project[r.id()] = p
		} else {
			t.drop(r.id(), "project_id refers to a missing project")
		}
	}
	parent := map[int64]int64{}
	for id := range project {
		parent[id] = 0
	}
	var kept []rec
	for _, r := range rows {
		id := r.id()
		if _, ok := project[id]; !ok {
			continue
		}
		kept = append(kept, r)
		if p := r.ref("parent_id"); p != 0 {
			if pp, ok := project[p]; !ok || p == id || pp != project[id] {
				t.repair(id, "parent_id refers to a missing board (or another project's); set NULL")
			} else {
				parent[id] = p
			}
		}
	}
	for _, id := range breakCycles(parent) {
		t.repair(id, "cyclic parent_id; set NULL")
	}
	pos := positions(t, kept, "position", func(r rec) any { return r.ref("project_id") })
	ins := im.ins(t, "boards", "id", "project_id", "parent_id", "name", "description", "position", "topics_count", "messages_count", "last_message_id")
	for _, r := range kept {
		id := r.id()
		desc := r.str("description")
		if r.isNull("description") {
			t.repair(id, "description NULL; set ''")
		}
		im.st.boards[id] = project[id]
		// last_message_id は messages 取り込み後に setLastMessageIDs で設定する
		if v := r.ref("last_message_id"); v != 0 {
			im.lastIDs = append(im.lastIDs, lastIDFix{"boards", "last_message_id", id, v})
		}
		if err := ins.add(id, project[id], nullIfZero(parent[id]), r.str("name"), desc, pos[id], r.intOr("topics_count", 0), r.intOr("messages_count", 0), nil); err != nil {
			return err
		}
	}
	_, err = ins.close()
	return err
}

func (im *imp) importMessages() error {
	t := im.table("messages")
	// 1 周目: 親子関係(2 階層に正規化: 親はトピック = 親を持たないメッセージ)
	parent := map[int64]int64{}
	board := map[int64]int64{}
	err := im.src.each("messages", func(r rec) error {
		if b := r.ref("board_id"); im.st.boards[b] != 0 {
			parent[r.id()] = r.ref("parent_id")
			board[r.id()] = b
		}
		return nil
	})
	if err != nil {
		return err
	}
	for id, p := range parent {
		if p != 0 {
			if _, ok := parent[p]; !ok || p == id {
				t.repair(id, "parent_id refers to a missing message; became a topic")
				parent[id] = 0
			}
		}
	}
	for _, id := range breakCycles(parent) {
		t.repair(id, "cyclic parent_id; became a topic")
	}
	topic := func(id int64) int64 {
		cur := parent[id]
		for cur != 0 && parent[cur] != 0 {
			cur = parent[cur]
		}
		return cur
	}
	ins := im.ins(t, "messages", "id", "board_id", "parent_id", "subject", "content", "author_id", "replies_count", "last_reply_id",
		"locked", "sticky", "created_at", "updated_at")
	err = im.src.each("messages", func(r rec) error {
		id := r.id()
		b, ok := board[id]
		if !ok {
			t.drop(id, "board_id refers to a missing board")
			return nil
		}
		p := topic(id)
		if p != parent[id] {
			t.repair(id, "reply to a reply; re-attached to the topic")
		}
		var author any
		if a := r.ref("author_id"); a != 0 || !r.isNull("author_id") {
			author = im.authorOr(t, id, "author_id", a)
		}
		sticky := r.intOr("sticky", 0) > 0
		if v, ok := r.Row["sticky"].(bool); ok {
			sticky = v
		}
		im.st.messages.add(id)
		if v := r.ref("last_reply_id"); v != 0 {
			im.lastIDs = append(im.lastIDs, lastIDFix{"messages", "last_reply_id", id, v})
		}
		return ins.add(id, b, nullIfZero(p), r.str("subject"), r.strNull("content", false), author, r.intOr("replies_count", 0), nil,
			r.bool("locked", false), sticky, im.tsOr(t, id, r, "created_on", "updated_on"), im.tsOr(t, id, r, "updated_on", "created_on"))
	})
	if err != nil {
		return err
	}
	_, err = ins.close()
	return err
}

// ---------------------------------------------------------------- wiki

func (im *imp) importWikis() error {
	t := im.table("wikis")
	ins := im.ins(t, "wikis", "id", "project_id", "start_page")
	byProject := map[int64]bool{}
	rows, err := im.src.all("wikis")
	if err != nil {
		return err
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].id() < rows[j].id() })
	for _, r := range rows {
		id := r.id()
		p := r.ref("project_id")
		if !im.projectExists(p) {
			t.drop(id, "project_id refers to a missing project")
			continue
		}
		if byProject[p] {
			t.drop(id, "second wiki of the same project")
			continue
		}
		byProject[p] = true
		im.st.wikis[id] = p
		if err := ins.add(id, p, r.str("start_page")); err != nil {
			return err
		}
	}
	_, err = ins.close()
	return err
}

func (im *imp) importWikiPages() error {
	t := im.table("wiki_pages")
	// 最新版番号(wiki_contents.version)を先に集める
	err := im.src.each("wiki_contents", func(r rec) error {
		if v, ok := r.int("version"); ok {
			im.st.wikiCurrent[r.ref("page_id")] = v
		}
		return nil
	})
	if err != nil {
		return err
	}
	rows, err := im.src.all("wiki_pages")
	if err != nil {
		return err
	}
	wiki := map[int64]int64{}
	for _, r := range rows {
		if w := r.ref("wiki_id"); im.st.wikis[w] != 0 {
			wiki[r.id()] = w
		} else {
			t.drop(r.id(), "wiki_id refers to a missing wiki")
		}
	}
	parent := map[int64]int64{}
	var kept []rec
	for _, r := range rows {
		id := r.id()
		if _, ok := wiki[id]; !ok {
			continue
		}
		kept = append(kept, r)
		parent[id] = 0
		if p := r.ref("parent_id"); p != 0 {
			if pw, ok := wiki[p]; !ok || p == id || pw != wiki[id] {
				t.repair(id, "parent_id refers to a missing page (or another wiki's); set NULL")
			} else {
				parent[id] = p
			}
		}
	}
	for _, id := range breakCycles(parent) {
		t.repair(id, "cyclic parent_id; set NULL")
	}
	sort.SliceStable(kept, func(i, j int) bool { return kept[i].id() < kept[j].id() })
	titles := map[string]bool{}
	ins := im.ins(t, "wiki_pages", "id", "wiki_id", "title", "parent_id", "protected", "current_version", "created_at")
	for _, r := range kept {
		id := r.id()
		title := r.str("title")
		if strings.TrimSpace(title) == "" {
			title = fmt.Sprintf("Page_%d", id)
			t.repair(id, "empty title; generated")
		}
		k := fmt.Sprintf("%d\x00%s", wiki[id], strings.ToLower(title))
		if titles[k] {
			title = fmt.Sprintf("%s_%d", title, id)
			k = fmt.Sprintf("%d\x00%s", wiki[id], strings.ToLower(title))
			t.repair(id, "duplicate title (case-insensitive) in the wiki; renamed to <title>_<id>")
		}
		titles[k] = true
		im.st.wikiPages[id] = wiki[id]
		if err := ins.add(id, wiki[id], title, nullIfZero(parent[id]), r.bool("protected", false), im.st.wikiCurrent[id], im.tsOr(t, id, r, "created_on")); err != nil {
			return err
		}
	}
	_, err = ins.close()
	return err
}

// inflate は wiki_content_versions.data を compression 列に従って伸長する。
// Redmine の 'gzip' は実際には zlib(Zlib::Deflate)だが、gzip ヘッダ付きも受け付ける。
func inflate(data []byte, compression string) (string, error) {
	var b []byte
	switch compression {
	case "", "none":
		b = data
	case "gzip":
		var r io.ReadCloser
		var err error
		if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
			r, err = gzip.NewReader(bytes.NewReader(data))
		} else {
			r, err = zlib.NewReader(bytes.NewReader(data))
		}
		if err != nil {
			return "", err
		}
		defer r.Close()
		if b, err = io.ReadAll(r); err != nil {
			return "", err
		}
	default:
		return "", fmt.Errorf("unknown compression %q", compression)
	}
	if utf8.Valid(b) {
		return string(b), nil
	}
	return latin1ToUTF8(b), nil
}

// importWikiVersions は wiki_content_versions(全版)と wiki_contents(最新版)を wiki_page_versions に統合する。
func (im *imp) importWikiVersions() error {
	t := im.table("wiki_content_versions")
	ins := im.ins(t, "wiki_page_versions", "id", "page_id", "version", "author_id", "text", "comments", "updated_at")
	have := map[[2]int64]bool{}
	maxVersion := map[int64]int64{}
	var maxID int64
	err := im.src.each("wiki_content_versions", func(r rec) error {
		id := r.id()
		maxID = max(maxID, id)
		page := r.ref("page_id")
		if im.st.wikiPages[page] == 0 {
			t.drop(id, "page_id refers to a missing page")
			return nil
		}
		ver := r.intOr("version", 0)
		if ver < 1 {
			t.drop(id, "invalid version %d", ver)
			return nil
		}
		if have[[2]int64{page, ver}] {
			t.drop(id, "duplicate version of the page")
			return nil
		}
		var data []byte
		switch v := r.Row["data"].(type) {
		case []byte:
			data = v
		case string:
			data = []byte(v)
		}
		text, ierr := inflate(data, r.str("compression"))
		if ierr != nil {
			t.drop(id, "cannot decompress data: %v", ierr)
			return nil
		}
		var author any
		if !r.isNull("author_id") {
			author = im.authorOr(t, id, "author_id", r.ref("author_id"))
		}
		have[[2]int64{page, ver}] = true
		if ver > maxVersion[page] {
			maxVersion[page] = ver
		}
		return ins.add(id, page, ver, author, text, r.strNull("comments", false), im.tsOr(t, id, r, "updated_on"))
	})
	if err != nil {
		return err
	}
	if _, err := ins.close(); err != nil {
		return err
	}
	// wiki_contents: 最新版。同じ版があれば wiki_contents を正として上書き、なければ追加する。
	ct := im.table("wiki_contents")
	cins := im.ins(ct, "wiki_page_versions", "id", "page_id", "version", "author_id", "text", "comments", "updated_at")
	err = im.src.each("wiki_contents", func(r rec) error {
		id := r.id()
		page := r.ref("page_id")
		if im.st.wikiPages[page] == 0 {
			ct.drop(id, "page_id refers to a missing page")
			return nil
		}
		ver := r.intOr("version", 0)
		if ver < 1 {
			ct.drop(id, "invalid version %d", ver)
			return nil
		}
		var author any
		if !r.isNull("author_id") {
			author = im.authorOr(ct, id, "author_id", r.ref("author_id"))
		}
		text := r.str("text")
		upd := im.tsOr(ct, id, r, "updated_on")
		if have[[2]int64{page, ver}] {
			var cur struct {
				Text     string  `db:"text"`
				Comments *string `db:"comments"`
			}
			if err := im.tx.Get(im.ctx, &cur, `SELECT text, comments FROM wiki_page_versions WHERE page_id = ? AND version = ?`, page, ver); err != nil {
				return err
			}
			comments := r.strNull("comments", false)
			curComments := any(nil)
			if cur.Comments != nil {
				curComments = *cur.Comments
			}
			if cur.Text != text || curComments != comments {
				if _, err := im.exec(`UPDATE wiki_page_versions SET text = ?, author_id = ?, comments = ?, updated_at = ? WHERE page_id = ? AND version = ?`,
					text, author, comments, upd, page, ver); err != nil {
					return err
				}
				ct.repair(id, "latest version differed from wiki_content_versions; wiki_contents used")
			}
			ct.Imported["wiki_page_versions (merged)"]++
			return nil
		}
		have[[2]int64{page, ver}] = true
		if ver > maxVersion[page] {
			maxVersion[page] = ver
		}
		maxID++
		return cins.add(maxID, page, ver, author, text, r.strNull("comments", false), upd)
	})
	if err != nil {
		return err
	}
	if _, err := cins.close(); err != nil {
		return err
	}
	// 最新版のないページ: 版があれば最大版を current_version にする
	pt := im.table("wiki_pages")
	for page, v := range maxVersion {
		if _, ok := im.st.wikiCurrent[page]; ok {
			continue
		}
		if _, err := im.exec(`UPDATE wiki_pages SET current_version = ? WHERE id = ?`, v, page); err != nil {
			return err
		}
		pt.repair(page, "no wiki_contents row; current_version set to the latest version")
	}
	for page := range im.st.wikiPages {
		if _, ok := im.st.wikiCurrent[page]; !ok && maxVersion[page] == 0 {
			pt.repair(page, "page has no content (current_version 0)")
		}
	}
	return nil
}

func (im *imp) importWikiRedirects() error {
	t := im.table("wiki_redirects")
	ins := im.ins(t, "wiki_redirects", "id", "wiki_id", "title", "redirects_to", "redirects_to_wiki_id", "created_at")
	err := im.src.each("wiki_redirects", func(r rec) error {
		id := r.id()
		w := r.ref("wiki_id")
		if im.st.wikis[w] == 0 {
			t.drop(id, "wiki_id refers to a missing wiki")
			return nil
		}
		to := r.ref("redirects_to_wiki_id")
		if im.st.wikis[to] == 0 {
			t.repair(id, "redirects_to_wiki_id missing; set to wiki_id")
			to = w
		}
		title, redir := r.str("title"), r.str("redirects_to")
		if title == "" || redir == "" {
			t.drop(id, "title or redirects_to empty")
			return nil
		}
		return ins.add(id, w, title, redir, to, im.tsOr(t, id, r, "created_on"))
	})
	if err != nil {
		return err
	}
	_, err = ins.close()
	return err
}

// ---------------------------------------------------------------- repositories

var scmTypes = map[string]string{
	"Repository::Subversion": "subversion",
	"Repository::Git":        "git",
	"Repository::Mercurial":  "mercurial",
	"Repository::Cvs":        "cvs",
	"Repository::Bazaar":     "bazaar",
	"Repository::Filesystem": "filesystem",
}

func (im *imp) importRepositories() error {
	t := im.table("repositories")
	rows, err := im.src.all("repositories")
	if err != nil {
		return err
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].id() < rows[j].id() })
	ins := im.ins(t, "repositories", "id", "project_id", "scm", "url", "root_url", "login", "password", "path_encoding", "log_encoding",
		"extra_info", "identifier", "is_default", "created_at")
	idents := map[string]bool{}
	defaults := map[int64]bool{}
	for _, r := range rows {
		id := r.id()
		p := r.ref("project_id")
		if !im.projectExists(p) {
			t.drop(id, "project_id refers to a missing project")
			continue
		}
		scm, ok := scmTypes[r.str("type")]
		if !ok {
			t.drop(id, "unknown repository type %q", r.str("type"))
			continue
		}
		ident := r.strNull("identifier", true)
		if ident != nil {
			k := fmt.Sprintf("%d\x00%s", p, ident)
			if idents[k] {
				ident = fmt.Sprintf("%s-%d", ident, id)
				t.repair(id, "duplicate identifier in the project; renamed")
				k = fmt.Sprintf("%d\x00%s", p, ident)
			}
			idents[k] = true
		}
		isDef := r.bool("is_default", false)
		if isDef && defaults[p] {
			isDef = false
			t.repair(id, "second default repository of the project; is_default cleared")
		}
		if isDef {
			defaults[p] = true
		}
		var extra any
		if v, derr := decodeYAML(r.Row["extra_info"]); derr != nil {
			t.repair(id, "unparseable extra_info YAML; set NULL")
		} else if v != nil {
			extra = toJSON(v)
		}
		pw, _, err := im.secret(t, id, "password", r.Row["password"])
		if err != nil {
			return err
		}
		im.st.repositories.add(id)
		if err := ins.add(id, p, scm, r.str("url"), r.strNull("root_url", true), r.strNull("login", true), pw,
			r.strNull("path_encoding", true), r.strNull("log_encoding", true), extra, ident, isDef, im.tsOr(t, id, r, "created_on")); err != nil {
			return err
		}
	}
	_, err = ins.close()
	return err
}

func (im *imp) importChangesets() error {
	t := im.table("changesets")
	ins := im.ins(t, "changesets", "id", "repository_id", "revision", "committer", "committed_at", "comments", "commit_date", "scmid", "user_id")
	seen := map[string]bool{}
	err := im.src.each("changesets", func(r rec) error {
		id := r.id()
		repo := r.ref("repository_id")
		if !im.st.repositories.has(repo) {
			t.drop(id, "repository_id refers to a missing repository")
			return nil
		}
		rev := r.str("revision")
		k := fmt.Sprintf("%d\x00%s", repo, rev)
		if seen[k] {
			t.drop(id, "duplicate revision in the repository")
			return nil
		}
		seen[k] = true
		var user any
		if u := r.ref("user_id"); u != 0 {
			if im.principal(u) {
				user = u
			} else {
				t.repair(id, "user_id refers to a missing user; set NULL")
			}
		}
		im.st.changesets.add(id)
		return ins.add(id, repo, rev, r.strNull("committer", false), im.tsOr(t, id, r, "committed_on"), r.strNull("comments", false),
			im.dateNull(t, id, r, "commit_date"), r.strNull("scmid", true), user)
	})
	if err != nil {
		return err
	}
	_, err = ins.close()
	return err
}

func (im *imp) importChanges() error {
	t := im.table("changes")
	ins := im.ins(t, "changeset_files", "id", "changeset_id", "action", "path", "from_path", "from_revision", "revision", "branch")
	err := im.src.each("changes", func(r rec) error {
		id := r.id()
		cs := r.ref("changeset_id")
		if !im.st.changesets.has(cs) {
			t.drop(id, "changeset_id refers to a missing changeset")
			return nil
		}
		return ins.add(id, cs, r.str("action"), r.str("path"), r.strNull("from_path", true), r.strNull("from_revision", true),
			r.strNull("revision", true), r.strNull("branch", true))
	})
	if err != nil {
		return err
	}
	_, err = ins.close()
	return err
}

func (im *imp) importChangesetParents() error {
	return im.importPairs("changeset_parents", "changeset_parents", "changeset_id", "parent_id", im.st.changesets.has, im.st.changesets.has)
}

func (im *imp) importChangesetsIssues() error {
	return im.importPairs("changesets_issues", "changesets_issues", "changeset_id", "issue_id", im.st.changesets.has,
		func(id int64) bool { _, ok := im.st.issues[id]; return ok })
}

// importPairs は 2 列の結合テーブルを取り込む(参照先の存在確認と重複除去)。
func (im *imp) importPairs(source, target, colA, colB string, okA, okB func(int64) bool) error {
	t := im.table(source)
	ins := im.ins(t, target, colA, colB)
	seen := map[[2]int64]bool{}
	err := im.src.each(source, func(r rec) error {
		a, b := r.ref(colA), r.ref(colB)
		key := fmt.Sprintf("%d-%d", a, b)
		if !okA(a) {
			t.drop(key, "%s refers to a missing row", colA)
			return nil
		}
		if !okB(b) {
			t.drop(key, "%s refers to a missing row", colB)
			return nil
		}
		if seen[[2]int64{a, b}] {
			t.drop(key, "duplicate")
			return nil
		}
		seen[[2]int64{a, b}] = true
		return ins.add(a, b)
	})
	if err != nil {
		return err
	}
	_, err = ins.close()
	return err
}
