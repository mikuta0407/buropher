// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package mailhandler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/attachments"
	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/i18n"
	"github.com/mikuta0407/buropher/internal/issues"
	"github.com/mikuta0407/buropher/internal/repository"
)

// このファイルは受信処理の本体（receive_issue / receive_issue_reply / receive_journal_reply /
// receive_message_reply / receive_news_reply / receive_comment_reply）と添付・ウォッチャー。

// localizer は Setting.default_language の Localizer（検証メッセージ・保存時のメッセージ用）。
func (r *receiver) localizer() *i18n.Localizer {
	lang := r.h.Settings.String("default_language")
	return r.h.bundle().NewLocalizer(lang, i18n.Settings{DefaultLanguage: lang}, nil)
}

// issueEnv は User.current = 送信者の issues.Env。
func (r *receiver) issueEnv(q db.Queryer) *issues.Env {
	e := issues.NewEnv(q, r.h.Settings, r.user)
	e.Now = r.h.now
	e.Notifier = r.h.IssueNotifier
	loc := r.localizer()
	e.Translate = func(key string, args ...any) string { return loc.L(key, args...) }
	e.DateFormat = loc.FormatDate
	return e
}

// withTx は fn を 1 トランザクションで実行し、失敗したら保存した添付のファイルを消す。
func (r *receiver) withTx(ctx context.Context, fn func(tx *db.Tx) error) error {
	err := r.h.DB.WithTx(ctx, fn)
	if err != nil && len(r.created) > 0 && r.h.Attachments != nil {
		if derr := r.h.Attachments.DeleteFromDisk(ctx, r.h.DB, r.created...); derr != nil {
			r.h.logger().Warn("MailHandler: delete attachment files", "err", derr)
		}
		r.created = nil
	}
	return err
}

func (r *receiver) allowedTo(ctx context.Context, q db.Queryer, perm string, p *domain.Project) (bool, error) {
	return authz.New(q, r.user).AllowedTo(ctx, domain.Perm(perm), p)
}

func (r *receiver) dispatchNotifications(ctx context.Context, res *issues.SaveResult) {
	if res == nil || len(res.Notifications) == 0 {
		return
	}
	if err := r.issueEnv(r.h.DB).Dispatch(ctx, res.Notifications); err != nil {
		r.h.logger().Error("MailHandler: issue notification", "err", err)
	}
}

func (r *receiver) validationMessage(errs *domain.ValidationErrors) string {
	loc := r.localizer()
	tr := func(key string, args ...any) string {
		if strings.HasPrefix(key, "field_") && !loc.Bundle.Exists(loc.Lang, key) {
			return strings.TrimPrefix(key, "field_")
		}
		return loc.L(key, args...)
	}
	return "Validation failed: " + strings.Join(errs.FullMessages(tr), ", ")
}

// receiveIssue は receive_issue（新しいチケットを作る）。
func (r *receiver) receiveIssue(ctx context.Context) (any, error) {
	var iss *issues.Issue
	var res *issues.SaveResult
	err := r.withTx(ctx, func(tx *db.Tx) error {
		env := r.issueEnv(tx)
		project, err := r.targetProject(ctx, tx)
		if err != nil {
			return err
		}
		// チケットを追加できないプロジェクトへのメールは受け付けない
		if !project.AllowsTo(domain.Perm("add_issues")) {
			return unauthorized("not possible to add issues to project [%s]", project.Name)
		}
		if !r.opts.noPermissionCheck {
			ok, err := r.allowedTo(ctx, tx, "add_issues", project)
			if err != nil {
				return err
			}
			if !ok {
				return unauthorized("not allowed to add issues to project [%s]", project.Name)
			}
		}
		// Issue.new(:author => user, :project => project)
		if iss, err = env.NewBlank(ctx); err != nil {
			return err
		}
		iss.AuthorID = r.user.ID
		if err := env.SetProject(ctx, iss, project, false); err != nil {
			return err
		}
		attrs, err := r.issueAttributesFromKeywords(ctx, env, iss)
		if err != nil {
			return err
		}
		if r.opts.noPermissionCheck {
			var tid int64
			if v, ok := attrs["tracker_id"].(int64); ok {
				tid = v
			}
			if tid == 0 {
				ts, err := env.ProjectTrackers(ctx, project)
				if err != nil {
					return err
				}
				if len(ts) > 0 {
					tid = ts[0].ID
				}
			}
			if err := env.SetTrackerID(ctx, iss, tid); err != nil {
				return err
			}
		}
		if err := env.SafeAssign(ctx, iss, attrs, r.user); err != nil {
			return err
		}
		cfv, err := r.customFieldValuesFromKeywords(ctx, tx, env, iss)
		if err != nil {
			return err
		}
		if err := env.SafeAssign(ctx, iss, issues.Params{"custom_field_values": cfv}, r.user); err != nil {
			return err
		}
		iss.Subject = r.cleanedUpSubject()
		if strings.TrimSpace(iss.Subject) == "" {
			iss.Subject = "(" + r.h.bundle().T(r.h.Settings.String("default_language"), "text_no_subject", nil) + ")"
		}
		desc := r.cleanedUpTextBody()
		iss.SetDescription(&desc)
		if iss.StartDate == nil && r.h.Settings.Bool("default_issue_start_date_to_creation_date") {
			y, m, d := r.h.now().UTC().Date()
			t := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
			iss.StartDate = &t
		}
		if r.opts.issue["is_private"] == "1" {
			iss.IsPrivate = true
		}
		// 保存前に To / Cc をウォッチャーにする（ウォッチャーも返信できるように）
		if err := r.addIssueWatchers(ctx, tx, env, iss, project); err != nil {
			return err
		}
		// add_attachments（保存時にチケットへ紐付ける）
		atts, err := r.createAttachments(ctx, tx)
		if err != nil {
			return err
		}
		for _, a := range atts {
			iss.AttachSaved(a.ID)
		}
		ok, sr, err := env.Save(ctx, iss)
		if err != nil {
			return err
		}
		if !ok {
			return recordInvalid(r.validationMessage(&iss.Errors))
		}
		res = sr
		return nil
	})
	if err != nil {
		return nil, err
	}
	r.created = nil
	r.dispatchNotifications(ctx, res)
	r.h.logger().Info(fmt.Sprintf("MailHandler: issue #%d created by %s", iss.ID, r.userString()))
	return iss, nil
}

// receiveIssueReply は receive_issue_reply（既存のチケットにノートを追加する）。
func (r *receiver) receiveIssueReply(ctx context.Context, issueID int64, fromJournal *issues.Journal) (any, error) {
	var journal *issues.Journal
	var iss *issues.Issue
	var res *issues.SaveResult
	err := r.withTx(ctx, func(tx *db.Tx) error {
		env := r.issueEnv(tx)
		var err error
		if iss, err = env.Find(ctx, issueID); err != nil {
			return err
		}
		if iss == nil {
			return missingContainer("reply to nonexistant issue [#%d]", issueID)
		}
		// ノートを追加できないプロジェクトへのメールは受け付けない
		project, err := env.Project(ctx, iss.ProjectID)
		if err != nil {
			return err
		}
		if !project.AllowsTo(domain.Perm("add_issue_notes")) {
			return unauthorized("not possible to add notes to project [%s]", project.Name)
		}
		if !r.opts.noPermissionCheck {
			ok, err := env.NotesAddable(ctx, iss, r.user)
			if err != nil {
				return err
			}
			if !ok {
				return unauthorized("not allowed to add notes on issues to project [%s]", project.Name)
			}
		}
		// 新しいチケット用の既定値（CLI のオプション）は使わない
		r.opts.issue = map[string]string{}

		if journal, err = env.InitJournal(ctx, iss, r.user, ""); err != nil {
			return err
		}
		if fromJournal != nil && fromJournal.PrivateNotes {
			// 非公開ノートへの返信なら追加するノートも非公開にする
			journal.PrivateNotes = true
		}
		attrs, err := r.issueAttributesFromKeywords(ctx, env, iss)
		if err != nil {
			return err
		}
		if err := env.SafeAssign(ctx, iss, attrs, r.user); err != nil {
			return err
		}
		cfv, err := r.customFieldValuesFromKeywords(ctx, tx, env, iss)
		if err != nil {
			return err
		}
		if err := env.SafeAssign(ctx, iss, issues.Params{"custom_field_values": cfv}, r.user); err != nil {
			return err
		}
		journal.Notes = r.cleanedUpTextBody()

		if err := r.addIssueWatchers(ctx, tx, env, iss, project); err != nil {
			return err
		}
		atts, err := r.createAttachments(ctx, tx)
		if err != nil {
			return err
		}
		for _, a := range atts {
			iss.AttachSaved(a.ID)
		}
		ok, sr, err := env.Save(ctx, iss)
		if err != nil {
			return err
		}
		if !ok {
			return recordInvalid(r.validationMessage(&iss.Errors))
		}
		res = sr
		return nil
	})
	if err != nil {
		return nil, err
	}
	r.created = nil
	r.dispatchNotifications(ctx, res)
	r.h.logger().Info(fmt.Sprintf("MailHandler: issue #%d updated by %s", iss.ID, r.userString()))
	return journal, nil
}

// receiveJournalReply は receive_journal_reply（ジャーナルへの返信はそのチケットへの返信）。
func (r *receiver) receiveJournalReply(ctx context.Context, journalID int64) (any, error) {
	journal, err := r.issueEnv(r.h.DB).FindJournal(ctx, journalID)
	if err != nil {
		return nil, err
	}
	if journal != nil {
		return r.receiveIssueReply(ctx, journal.IssueID, journal)
	}
	if m := issueReplySubjectRe.FindStringSubmatch(r.email.Subject()); m != nil {
		r.h.logger().Info("MailHandler: reply to a nonexistant journal, calling receive_issue_reply with issue from subject")
		var id int64
		fmt.Sscan(m[1], &id)
		return r.receiveIssueReply(ctx, id, nil)
	}
	return nil, missingContainer("reply to nonexistant journal [%d]", journalID)
}

var msgSubjectPrefixRe = regexp.MustCompile(`(?m)^.*msg\d+\]`)

// receiveMessageReply は receive_message_reply（フォーラムのトピックに返信する）。
func (r *receiver) receiveMessageReply(ctx context.Context, messageID int64) (any, error) {
	q := r.h.DB
	message, err := repository.GetMessage(ctx, q, messageID)
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return nil, err
	}
	if message != nil && message.ParentID != nil {
		// Message#root
		if message, err = repository.GetMessage(ctx, q, *message.ParentID); err != nil && !errors.Is(err, repository.ErrNotFound) {
			return nil, err
		}
	}
	if message == nil {
		return nil, missingContainer("reply to nonexistant message [%d]", messageID)
	}
	board, err := repository.GetBoard(ctx, q, message.BoardID)
	if err != nil {
		return nil, err
	}
	project, err := repository.GetProject(ctx, q, board.ProjectID)
	if err != nil {
		return nil, err
	}
	// メッセージを追加できないプロジェクトへのメールは受け付けない
	if !project.AllowsTo(domain.Perm("add_messages")) {
		return nil, unauthorized("not possible to add messages to project [%s]", project.Name)
	}
	if !r.opts.noPermissionCheck {
		ok, err := r.allowedTo(ctx, q, "add_messages", project)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, unauthorized("not allowed to add messages to project [%s]", project.Name)
		}
	}
	if message.Locked {
		return nil, unauthorized("ignoring reply to a locked message [%d %s]", message.ID, message.Subject)
	}
	now := r.h.now()
	authorID := r.user.ID
	parentID := message.ID
	reply := &domain.Message{
		BoardID:   message.BoardID,
		ParentID:  &parentID,
		Subject:   strings.TrimSpace(msgSubjectPrefixRe.ReplaceAllString(r.cleanedUpSubject(), "")),
		Content:   r.cleanedUpTextBody(),
		AuthorID:  &authorID,
		Author:    r.user,
		Board:     board,
		CreatedAt: now,
		UpdatedAt: now,
	}
	board.Project = project
	// message.children << reply は検証に失敗しても例外にならない（保存されないまま reply を返す）
	if strings.TrimSpace(reply.Subject) == "" || strings.TrimSpace(reply.Content) == "" || len([]rune(reply.Subject)) > 255 {
		r.h.logger().Info("MailHandler: message reply is invalid and was not saved")
		return reply, nil
	}
	err = r.withTx(ctx, func(tx *db.Tx) error {
		if err := repository.InsertMessage(ctx, tx, reply); err != nil {
			return err
		}
		atts, err := r.createAttachments(ctx, tx)
		if err != nil {
			return err
		}
		if err := r.attachTo(ctx, tx, atts, domain.AttachmentContainerMessage, reply.ID); err != nil {
			return err
		}
		// after_create :add_author_as_watcher（Watcher はアクティブなユーザーのみ）
		if r.user.Active() {
			if err := repository.AddWatcher(ctx, tx, "message", message.ID, r.user.ID); err != nil {
				return err
			}
		}
		if err := repository.UpdateTopicCounters(ctx, tx, message.ID); err != nil {
			return err
		}
		return repository.ResetBoardCounters(ctx, tx, reply.BoardID)
	})
	if err != nil {
		return nil, err
	}
	r.created = nil
	r.notifyContent(ctx, "message_posted", "message_posted", reply)
	return reply, nil
}

// receiveNewsReply は receive_news_reply（ニュースにコメントする）。
func (r *receiver) receiveNewsReply(ctx context.Context, newsID int64) (any, error) {
	q := r.h.DB
	news, err := repository.GetNews(ctx, q, newsID)
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return nil, err
	}
	if news == nil {
		return nil, missingContainer("reply to nonexistant news [%d]", newsID)
	}
	project, err := repository.GetProject(ctx, q, news.ProjectID)
	if err != nil {
		return nil, err
	}
	// ニュースにコメントできないプロジェクトへのメールは受け付けない
	if !project.AllowsTo(domain.Perm("comment_news")) {
		return nil, unauthorized("not possible to add news comments to project [%s]", project.Name)
	}
	if !r.opts.noPermissionCheck {
		// News#commentable?(user)
		ok, err := r.allowedTo(ctx, q, "comment_news", project)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, unauthorized("not allowed to comment on news item [%d %s]", news.ID, news.Title)
		}
	}
	now := r.h.now()
	comment := &domain.Comment{NewsID: news.ID, AuthorID: r.user.ID, Author: r.user, Content: r.cleanedUpTextBody(),
		CreatedAt: now, UpdatedAt: now}
	// validates_presence_of :content（comment.save! は例外）
	if strings.TrimSpace(comment.Content) == "" {
		return nil, recordInvalid("Validation failed: Comment cannot be blank")
	}
	// コメントの行と news.comments_count の更新は 1 トランザクションで行う
	if err := r.h.DB.WithTx(ctx, func(tx *db.Tx) error { return repository.InsertComment(ctx, tx, comment) }); err != nil {
		return nil, err
	}
	r.notifyContent(ctx, "news_comment_added", "news_comment_added", comment)
	return comment, nil
}

// receiveCommentReply は receive_comment_reply（ニュースのコメントへの返信はそのニュースへのコメント）。
func (r *receiver) receiveCommentReply(ctx context.Context, commentID int64) (any, error) {
	newsID, err := repository.NewsCommentNewsID(ctx, r.h.DB, commentID)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, missingContainer("reply to nonexistant comment [%d]", commentID)
	}
	if err != nil {
		return nil, err
	}
	return r.receiveNewsReply(ctx, newsID)
}

// notifyContent は Setting.notified_events に event が含まれていれば通知する（after_create_commit :send_notification）。
func (r *receiver) notifyContent(ctx context.Context, event, action string, obj any) {
	if r.h.ContentNotifier == nil {
		return
	}
	if slices.Contains(r.h.Settings.Strings("notified_events"), event) {
		r.h.ContentNotifier.Notify(ctx, action, obj)
	}
}

// ---------------------------------------------------------------- 添付ファイル

// createAttachments は add_attachments のうち Attachment.create の部分（コンテナには未紐付け）。
func (r *receiver) createAttachments(ctx context.Context, q db.Queryer) ([]*domain.Attachment, error) {
	if r.h.Attachments == nil {
		return nil, nil
	}
	var out []*domain.Attachment
	for _, p := range r.email.Attachments() {
		filename := p.Filename()
		if !r.acceptAttachment(filename) {
			continue
		}
		data := p.DecodedBody()
		if len(data) == 0 {
			continue
		}
		up := attachments.Upload{Filename: filename, ContentType: p.MimeType(), Body: bytes.NewReader(data), Size: int64(len(data))}
		a, errs, err := r.h.Attachments.Create(ctx, q, up, r.user, r.localizer())
		if err != nil {
			return nil, err
		}
		if errs != nil && errs.Any() {
			// 検証に失敗した添付は保存しない（Redmine でも obj.attachments << は例外にならない）
			r.h.logger().Info("MailHandler: attachment " + filename + " was not saved")
			continue
		}
		r.created = append(r.created, a)
		out = append(out, a)
	}
	return out, nil
}

// attachTo は保存済みのコンテナに添付を紐付ける（obj.attachments << attachment）。
func (r *receiver) attachTo(ctx context.Context, q db.Queryer, atts []*domain.Attachment, kind string, id int64) error {
	if len(atts) == 0 {
		return nil
	}
	return r.h.Attachments.AttachSaved(ctx, q, &attachments.SaveResult{Files: atts}, kind, id)
}

// acceptAttachment は accept_attachment?（Setting.mail_handler_excluded_filenames に一致する添付は無視する）。
func (r *receiver) acceptAttachment(filename string) bool {
	if r.excluded == nil {
		r.excluded = []string{}
		for _, s := range strings.Split(r.h.Settings.String("mail_handler_excluded_filenames"), ",") {
			if s = strings.TrimSpace(s); s != "" {
				r.excluded = append(r.excluded, s)
			}
		}
	}
	useRegex := r.h.Settings.Bool("mail_handler_enable_regex_excluded_filenames")
	for _, pattern := range r.excluded {
		var src string
		if useRegex {
			src = `(?i)\A(?:` + pattern + `)\z`
		} else {
			src = `(?i)\A` + strings.ReplaceAll(regexp.QuoteMeta(pattern), `\*`, ".*") + `\z`
		}
		re, err := regexp.Compile(src)
		if err != nil {
			r.h.logger().Error("MailHandler: invalid pattern in mail_handler_excluded_filenames (" + err.Error() + ")")
			continue
		}
		if re.MatchString(filename) {
			r.h.logger().Info("MailHandler: ignoring attachment " + filename + " matching " + pattern)
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------- ウォッチャー

// addIssueWatchers は add_watchers（add_issue_watchers 権限があれば To / Cc のアクティブなユーザーをウォッチャーにする）。
func (r *receiver) addIssueWatchers(ctx context.Context, q db.Queryer, env *issues.Env, iss *issues.Issue, project *domain.Project) error {
	if !r.opts.noPermissionCheck {
		ok, err := r.allowedTo(ctx, q, "add_issue_watchers", project)
		if err != nil || !ok {
			return err
		}
	}
	var addresses []string
	for _, a := range append(r.email.To(), r.email.Cc()...) {
		s := strings.ToLower(strings.TrimSpace(a.Address))
		if s != "" && !slices.Contains(addresses, s) {
			addresses = append(addresses, s)
		}
	}
	if len(addresses) == 0 {
		return nil
	}
	ids, err := repository.ActiveUserIDsHavingMail(ctx, q, addresses)
	if err != nil {
		return err
	}
	current, err := env.WatcherIDs(ctx, iss)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if slices.Contains(current, id) {
			continue
		}
		if err := env.AddWatcher(ctx, iss, id); err != nil {
			return err
		}
	}
	return nil
}
