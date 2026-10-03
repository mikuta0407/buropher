// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package notify

// チケット以外の通知の受信者計算（Redmine のモデルの notified_users / notified_watchers / notified_mentions）。
// チケットの受信者は internal/issues（Issue#notified_users 等）が計算する。

import (
	"context"
	"regexp"
	"slices"
	"strings"

	"github.com/dlclark/regexp2"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/repository"
)

// recipientCtx は受信者計算中のキャッシュ。
type recipientCtx struct {
	s     *Service
	ctx   context.Context
	users map[int64]*domain.User
	ns    map[int64]*repository.NotificationSetting
}

func (s *Service) newRecipientCtx(ctx context.Context) *recipientCtx {
	return &recipientCtx{s: s, ctx: ctx, users: map[int64]*domain.User{}, ns: map[int64]*repository.NotificationSetting{}}
}

func (r *recipientCtx) user(id int64) *domain.User {
	if u, ok := r.users[id]; ok {
		return u
	}
	u, err := repository.GetUser(r.ctx, r.s.DB, id)
	if err != nil {
		u = nil
	}
	r.users[id] = u
	return u
}

func (r *recipientCtx) mailNotification(id int64) string {
	ns, ok := r.ns[id]
	if !ok {
		var err error
		ns, err = repository.GetNotificationSetting(r.ctx, r.s.DB, id, r.s.Settings.String("default_notification_option"), r.s.Settings.Bool("default_users_no_self_notified"))
		if err != nil {
			return "none"
		}
		r.ns[id] = ns
	}
	return ns.MailNotification
}

// allowed は user.allowed_to?(perm, project)。
func (r *recipientCtx) allowed(u *domain.User, perm string, p *domain.Project) bool {
	if u == nil || p == nil {
		return false
	}
	ok, err := authz.New(r.s.DB, u).AllowedTo(r.ctx, domain.Perm(perm), p)
	return err == nil && ok
}

// filter は ids のうち keep を満たす有効なユーザー。
func (r *recipientCtx) filter(ids []int64, keep func(u *domain.User) bool) []int64 {
	var out []int64
	for _, id := range ids {
		u := r.user(id)
		if u == nil {
			continue
		}
		if keep == nil || keep(u) {
			out = append(out, id)
		}
	}
	return out
}

// projectNotifiedUsers は Project#notified_users。
func (r *recipientCtx) projectNotifiedUsers(p *domain.Project) []int64 {
	ids, err := repository.ProjectNotifiedUserIDs(r.ctx, r.s.DB, p.ID, r.s.Settings.String("default_notification_option"))
	if err != nil {
		r.s.logErr("project notified users", err)
	}
	return ids
}

// notifiedWatchers は acts_as_watchable#notified_watchers（visible が nil なら可視性で絞らない）。
func (r *recipientCtx) notifiedWatchers(kind string, id int64, visible func(u *domain.User) bool) []int64 {
	ids, err := repository.ActiveWatcherUserIDs(r.ctx, r.s.DB, kind, id)
	if err != nil {
		r.s.logErr("watchers", err)
		return nil
	}
	return r.filter(ids, func(u *domain.User) bool {
		if strings.TrimSpace(u.Mail) == "" || r.mailNotification(u.ID) == "none" {
			return false
		}
		return visible == nil || visible(u)
	})
}

// union は配列の和（Ruby の |。出現順）。
func union(lists ...[]int64) []int64 {
	var out []int64
	for _, l := range lists {
		for _, id := range l {
			if !slices.Contains(out, id) {
				out = append(out, id)
			}
		}
	}
	return out
}

func (s *Service) project(ctx context.Context, id int64) *domain.Project {
	p, err := repository.GetProject(ctx, s.DB, id)
	if err != nil {
		return nil
	}
	return p
}

// NewsRecipients は Mailer.deliver_news_added の受信者（news.notified_users | news.notified_watchers_for_added_news）。
func (s *Service) NewsRecipients(ctx context.Context, newsID int64) ([]int64, error) {
	n, err := repository.GetMailNews(ctx, s.DB, newsID)
	if err != nil {
		return nil, err
	}
	p := s.project(ctx, n.ProjectID)
	if p == nil {
		return nil, nil
	}
	r := s.newRecipientCtx(ctx)
	members, err := repository.ProjectActiveUserIDs(ctx, s.DB, p.ID)
	if err != nil {
		return nil, err
	}
	// News#notified_users（none 以外は notify_about? が真）
	nu := r.filter(members, func(u *domain.User) bool {
		mn := r.mailNotification(u.ID)
		return mn != "" && mn != "none" && r.allowed(u, "view_news", p)
	})
	var watchers []int64
	if mid, err := repository.EnabledModuleID(ctx, s.DB, p.ID, "news"); err == nil && mid != 0 {
		watchers = r.notifiedWatchers("project_module", mid, nil)
		if !p.IsPublic {
			watchers = slices.DeleteFunc(watchers, func(id int64) bool { return !slices.Contains(members, id) })
		}
	}
	return union(nu, watchers), nil
}

// newsNotifiedUsersAndWatchers は news.notified_users | news.notified_watchers（コメントの受信者）。
func (s *Service) CommentRecipients(ctx context.Context, commentID int64) ([]int64, error) {
	c, err := repository.GetMailComment(ctx, s.DB, commentID)
	if err != nil {
		return nil, err
	}
	n, err := repository.GetMailNews(ctx, s.DB, c.NewsID)
	if err != nil {
		return nil, err
	}
	p := s.project(ctx, n.ProjectID)
	if p == nil {
		return nil, nil
	}
	r := s.newRecipientCtx(ctx)
	members, err := repository.ProjectActiveUserIDs(ctx, s.DB, p.ID)
	if err != nil {
		return nil, err
	}
	nu := r.filter(members, func(u *domain.User) bool {
		mn := r.mailNotification(u.ID)
		return mn != "" && mn != "none" && r.allowed(u, "view_news", p)
	})
	nw := r.notifiedWatchers("news", n.ID, func(u *domain.User) bool { return r.allowed(u, "view_news", p) })
	return union(nu, nw), nil
}

// DocumentRecipients は Document#notified_users（project.notified_users のうち文書を閲覧できるユーザー）。
func (s *Service) DocumentRecipients(ctx context.Context, documentID int64) ([]int64, error) {
	d, err := repository.GetMailDocument(ctx, s.DB, documentID)
	if err != nil {
		return nil, err
	}
	p := s.project(ctx, d.ProjectID)
	if p == nil {
		return nil, nil
	}
	r := s.newRecipientCtx(ctx)
	return r.filter(r.projectNotifiedUsers(p), func(u *domain.User) bool { return r.allowed(u, "view_documents", p) }), nil
}

// AttachmentsRecipients は Mailer.deliver_attachments_added の受信者。
func (s *Service) AttachmentsRecipients(ctx context.Context, attachmentIDs []int64) ([]int64, error) {
	atts, err := repository.MailAttachmentsByIDs(ctx, s.DB, attachmentIDs)
	if err != nil || len(atts) == 0 || atts[0].ContainerKind == nil || atts[0].ContainerID == nil {
		return nil, err
	}
	r := s.newRecipientCtx(ctx)
	switch *atts[0].ContainerKind {
	case "project", "version":
		pid := *atts[0].ContainerID
		if *atts[0].ContainerKind == "version" {
			pid, _, err = repository.VersionProject(ctx, s.DB, pid)
			if err != nil {
				return nil, err
			}
		}
		p := s.project(ctx, pid)
		if p == nil {
			return nil, nil
		}
		return r.filter(r.projectNotifiedUsers(p), func(u *domain.User) bool { return r.allowed(u, "view_files", p) }), nil
	case "document":
		return s.DocumentRecipients(ctx, *atts[0].ContainerID)
	}
	return nil, nil
}

// MessageRecipients は Mailer.deliver_message_posted の受信者
// （message.notified_users | root.notified_watchers | board.notified_watchers）。
func (s *Service) MessageRecipients(ctx context.Context, messageID int64) ([]int64, error) {
	m, err := repository.GetMailMessage(ctx, s.DB, messageID)
	if err != nil {
		return nil, err
	}
	p := s.project(ctx, m.ProjectID)
	if p == nil {
		return nil, nil
	}
	r := s.newRecipientCtx(ctx)
	vis := func(u *domain.User) bool { return r.allowed(u, "view_messages", p) }
	nu := r.filter(r.projectNotifiedUsers(p), vis)
	rw := r.notifiedWatchers("message", m.RootID(), vis)
	bw := r.notifiedWatchers("board", m.BoardID, vis)
	return union(nu, rw, bw), nil
}

// WikiRecipients は Mailer.deliver_wiki_content_added / updated の受信者。
// author は User.current（メンションの可視性判定に使う）、oldText は更新前の本文（追加なら nil）。
func (s *Service) WikiRecipients(ctx context.Context, pageID int64, version int, updated bool, author *domain.User, oldText *string) ([]int64, error) {
	w, err := repository.GetMailWikiContent(ctx, s.DB, pageID, version)
	if err != nil {
		return nil, err
	}
	p := s.project(ctx, w.ProjectID)
	if p == nil {
		return nil, nil
	}
	r := s.newRecipientCtx(ctx)
	vis := func(u *domain.User) bool { return r.allowed(u, "view_wiki_pages", p) }
	nu := r.filter(r.projectNotifiedUsers(p), vis)
	var pw []int64
	if updated {
		pw = r.notifiedWatchers("wiki_page", w.PageID, vis)
	}
	ww := r.notifiedWatchers("wiki", w.WikiID, vis)
	cur := w.Text
	mentioned := s.mentionedUsers(ctx, author, oldText, &cur)
	nm := r.filter(mentioned, func(u *domain.User) bool {
		return strings.TrimSpace(u.Mail) != "" && r.mailNotification(u.ID) != "none" && vis(u)
	})
	if updated {
		return union(nu, pw, ww, nm), nil
	}
	return union(nu, ww, nm), nil
}

// mentionPattern は Redmine::Acts::Mentionable::MENTION_PATTERN。
var mentionPattern = regexp2.MustCompile(`(?:^|\W)@([A-Za-z0-9_\-@\.]*?)(?=(?=[\p{P}\p{S}][^A-Za-z0-9_\/])|\s|[\p{P}\p{S}]?$)`, regexp2.IgnoreCase|regexp2.Multiline)

var (
	quotedRe     = regexp.MustCompile(`(?s)\r\n(?:>\s)+(.*?)\r\n`)
	textilePreRe = regexp.MustCompile(`(?s)<pre>(.*?)</pre>`)
	cmCodeRe     = regexp.MustCompile("(?s)(~~~|```)(.*?)(~~~|```)")
)

func (s *Service) scanMentions(content *string) []string {
	if content == nil {
		return nil
	}
	c := quotedRe.ReplaceAllString(*content, "")
	switch s.Settings.String("text_formatting") {
	case "textile":
		c = textilePreRe.ReplaceAllString(c, "")
	case "common_mark":
		c = cmCodeRe.ReplaceAllString(c, "")
	}
	var out []string
	m, _ := mentionPattern.FindStringMatch(c)
	for m != nil {
		out = append(out, m.GroupByNumber(1).String())
		m, _ = mentionPattern.FindNextMatch(m)
	}
	return out
}

// mentionedUsers は get_mentioned_users(old, new)（新たに現れたログイン名の、User.current に見える有効なユーザー）。
func (s *Service) mentionedUsers(ctx context.Context, current *domain.User, old, cur *string) []int64 {
	prev := s.scanMentions(old)
	var logins []string
	for _, l := range s.scanMentions(cur) {
		if !slices.Contains(prev, l) && !slices.Contains(logins, l) {
			logins = append(logins, l)
		}
	}
	if len(logins) == 0 {
		return nil
	}
	if current == nil {
		current = &domain.User{Principal: domain.Principal{Kind: domain.KindAnonymousUser}}
	}
	vis, err := authz.New(s.DB, current).PrincipalVisibleCondition(ctx)
	if err != nil {
		s.logErr("mentions", err)
		return nil
	}
	ids, err := repository.ActiveUserIDsByLogins(ctx, s.DB, logins, vis)
	if err != nil {
		s.logErr("mentions", err)
	}
	return ids
}
