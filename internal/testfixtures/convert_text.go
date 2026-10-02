package testfixtures

import (
	"fmt"
	"strings"
)

// テキスト系コンテンツ (文書・フォーラム・Wiki・添付・リポジトリ履歴・チケット履歴・ニュースコメント) の変換。
// 変換規則は internal/redmineimport/importer (conv_content.go / conv_poly.go / conv_issues.go) に合わせる。

func loadDocuments(c *loadCtx, rows []row) error {
	for _, r := range rows {
		if err := c.exec(`INSERT INTO documents (id, project_id, category_id, title, description, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
			r.int("id", 0), r.int("project_id", 0), r.int("category_id", 0), r.str("title"), r.text("description", nil),
			ts(c, r, "created_on")); err != nil {
			return err
		}
	}
	return nil
}

// loadMessages は messages を投入し、boards.last_message_id (boards フィクスチャの値) を設定する。
// messages.parent_id / last_reply_id は遅延制約なのでそのまま入れる。
func loadMessages(c *loadCtx, rows []row) error {
	for _, r := range rows {
		if err := c.exec(`INSERT INTO messages (id, board_id, parent_id, subject, content, author_id, replies_count, last_reply_id,
  locked, sticky, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.int("id", 0), r.int("board_id", 0), r.nint("parent_id"), r.str("subject"), r.text("content", nil), r.nint("author_id"),
			r.int("replies_count", 0), r.nint("last_reply_id"), r.bool("locked", false), r.int("sticky", 0) > 0,
			ts(c, r, "created_on"), ts(c, r, "updated_on")); err != nil {
			return err
		}
	}
	boards, err := readFixture("boards", c.now)
	if err != nil {
		return err
	}
	for _, b := range boards {
		if v := b.nint("last_message_id"); v != nil {
			if err := c.exec(`UPDATE boards SET last_message_id = ? WHERE id = ?`, v, b.int("id", 0)); err != nil {
				return err
			}
		}
	}
	return nil
}

func loadComments(c *loadCtx, rows []row) error {
	for _, r := range rows {
		if t := r.str("commented_type"); t != "News" {
			// News 以外 (プラグイン) は行き先が無いので捨てる
			continue
		}
		if err := c.exec(`INSERT INTO news_comments (id, news_id, author_id, content, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
			r.int("id", 0), r.int("commented_id", 0), r.int("author_id", 0), r.text("content", nil),
			ts(c, r, "created_on"), ts(c, r, "updated_on")); err != nil {
			return err
		}
	}
	return nil
}

// loadWikiPages は wiki_pages を投入する。current_version は wiki_contents / wiki_content_versions の投入時に設定する
// (コンテンツの無いページは 0)。
func loadWikiPages(c *loadCtx, rows []row) error {
	for _, r := range rows {
		if err := c.exec(`INSERT INTO wiki_pages (id, wiki_id, title, parent_id, protected, current_version, created_at) VALUES (?, ?, ?, ?, ?, 0, ?)`,
			r.int("id", 0), r.int("wiki_id", 0), r.str("title"), r.nint("parent_id"), r.bool("protected", false),
			ts(c, r, "created_on")); err != nil {
			return err
		}
	}
	return nil
}

// loadWikiContentVersions は wiki_content_versions (履歴) を wiki_page_versions に入れる。
// フィクスチャの data は非圧縮 (compression "" / 未指定) のみ対応。
// wiki_contents を投入しない場合に備え、current_version を最大版まで進める。
func loadWikiContentVersions(c *loadCtx, rows []row) error {
	for _, r := range rows {
		if comp := r.str("compression"); comp != "" && comp != "none" {
			return fmt.Errorf("%s: compression %q is not supported", r.label, comp)
		}
		page, ver := r.int("page_id", 0), r.int("version", 0)
		if err := c.exec(`INSERT INTO wiki_page_versions (id, page_id, version, author_id, text, comments, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			r.int("id", 0), page, ver, r.nint("author_id"), r.str("data"), r.text("comments", ""), ts(c, r, "updated_on")); err != nil {
			return err
		}
		if err := c.exec(`UPDATE wiki_pages SET current_version = ? WHERE id = ? AND current_version < ?`, ver, page, ver); err != nil {
			return err
		}
	}
	return nil
}

// loadWikiContents は wiki_contents (最新版) を wiki_page_versions に統合する。
// 同じ (page_id, version) の履歴があれば wiki_contents の内容で上書きし (Redmine の表示は wiki_contents を使うため)、
// 無ければ新しい id で追加する。wiki_pages.current_version は wiki_contents.version にする。
func loadWikiContents(c *loadCtx, rows []row) error {
	for _, r := range rows {
		page, ver := r.int("page_id", 0), r.int("version", 1)
		var n int
		if err := c.tx.Get(c.ctx, &n, `SELECT COUNT(*) FROM wiki_page_versions WHERE page_id = ? AND version = ?`, page, ver); err != nil {
			return err
		}
		if n > 0 {
			if err := c.exec(`UPDATE wiki_page_versions SET author_id = ?, text = ?, comments = ?, updated_at = ? WHERE page_id = ? AND version = ?`,
				r.nint("author_id"), r.str("text"), r.text("comments", ""), ts(c, r, "updated_on"), page, ver); err != nil {
				return err
			}
		} else {
			// 明示 id で入れた履歴と衝突しないよう、最大 id の次を使う (PostgreSQL のシーケンスは最後に合わせる)
			var next int64
			if err := c.tx.Get(c.ctx, &next, `SELECT COALESCE(MAX(id), 0) + 1 FROM wiki_page_versions`); err != nil {
				return err
			}
			if err := c.exec(`INSERT INTO wiki_page_versions (id, page_id, version, author_id, text, comments, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
				next, page, ver, r.nint("author_id"), r.str("text"), r.text("comments", ""), ts(c, r, "updated_on")); err != nil {
				return err
			}
		}
		if err := c.exec(`UPDATE wiki_pages SET current_version = ? WHERE id = ?`, ver, page); err != nil {
			return err
		}
	}
	return nil
}

// attachmentContainerKinds は attachments.container_type → container_kind。
var attachmentContainerKinds = map[string]string{
	"Issue": "issue", "Project": "project", "Version": "version", "WikiPage": "wiki_page", "Message": "message",
	"News": "news", "Document": "document", "CustomValue": "custom_value",
}

func loadAttachments(c *loadCtx, rows []row) error {
	for _, r := range rows {
		var kind, cid any
		typ, id := r.str("container_type"), r.int("container_id", 0)
		if typ != "" && id != 0 {
			k, ok := attachmentContainerKinds[typ]
			if !ok {
				return fmt.Errorf("%s: unknown container_type %q", r.label, typ)
			}
			kind, cid = k, id
		}
		// 長さで digest のアルゴリズムを判定する (64 桁 = SHA256, 32 桁 = MD5、それ以外は NULL)
		var digest, algo any
		switch d := strings.ToLower(strings.TrimSpace(r.str("digest"))); len(d) {
		case 64:
			digest, algo = d, "sha256"
		case 32:
			digest, algo = d, "md5"
		}
		if err := c.exec(`INSERT INTO attachments (id, container_kind, container_id, filename, disk_directory, disk_filename, filesize,
  content_type, digest, digest_algo, downloads, author_id, description, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.int("id", 0), kind, cid, r.str("filename"), r.text("disk_directory", nil), r.str("disk_filename"), r.int("filesize", 0),
			r.text("content_type", nil), digest, algo, r.int("downloads", 0), r.int("author_id", 0), r.text("description", nil),
			ts(c, r, "created_on")); err != nil {
			return err
		}
	}
	return nil
}

func loadChangesets(c *loadCtx, rows []row) error {
	for _, r := range rows {
		if err := c.exec(`INSERT INTO changesets (id, repository_id, revision, committer, committed_at, comments, commit_date, scmid, user_id)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.int("id", 0), r.int("repository_id", 0), r.str("revision"), r.nstr("committer"), ts(c, r, "committed_on"),
			r.nstr("comments"), r.date("commit_date"), r.nstr("scmid"), r.nint("user_id")); err != nil {
			return err
		}
	}
	return nil
}

func loadChanges(c *loadCtx, rows []row) error {
	for _, r := range rows {
		if err := c.exec(`INSERT INTO changeset_files (id, changeset_id, action, path, from_path, from_revision, revision, branch)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			r.int("id", 0), r.int("changeset_id", 0), r.str("action"), r.str("path"), r.nstr("from_path"),
			r.nstr("from_revision"), r.nstr("revision"), r.nstr("branch")); err != nil {
			return err
		}
	}
	return nil
}
