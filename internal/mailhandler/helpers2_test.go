// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package mailhandler

import (
	"fmt"
	"strings"

	"github.com/mikuta0407/buropher/internal/auth/password"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/repository"
)

func passwordVerify(hash, pw string) (bool, error) { return password.Verify(hash, pw) }

// createIssueCustomField はチケット用カスタムフィールドを作る（全トラッカー）。
func (c *tc) createIssueCustomField(name, format string, multiple, forAll bool, possible []string) int64 {
	c.t.Helper()
	var pv any
	if possible != nil {
		parts := make([]string, len(possible))
		for i, s := range possible {
			parts[i] = fmt.Sprintf("%q", s)
		}
		pv = "[" + strings.Join(parts, ",") + "]"
	}
	var pos int
	c.must(c.d.Get(c.ctx, &pos, `SELECT COALESCE(MAX(position), 0) + 1 FROM custom_fields WHERE owner_kind = 'issue'`))
	id, err := c.d.InsertReturningID(c.ctx, `INSERT INTO custom_fields (owner_kind, name, field_format, is_for_all, is_required, visible,
  multiple, possible_values, editable, position) VALUES ('issue', ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		name, format, forAll, false, true, multiple, pv, true, pos)
	c.must(err)
	var trackers []int64
	c.must(c.d.Select(c.ctx, &trackers, `SELECT id FROM trackers ORDER BY id`))
	for _, t := range trackers {
		c.exec(`INSERT INTO custom_fields_trackers (custom_field_id, tracker_id) VALUES (?, ?)`, id, t)
	}
	return id
}

// customValues はチケットのカスタムフィールドの値（id 順）。
func (c *tc) customValues(issueID, cfID int64) []string {
	c.t.Helper()
	var vs []string
	c.must(c.d.Select(c.ctx, &vs, `SELECT COALESCE(value, '') FROM custom_values WHERE customized_kind = 'issue' AND customized_id = ? AND custom_field_id = ? ORDER BY id`,
		issueID, cfID))
	return vs
}

// createUser はユーザーを作る。
func (c *tc) createUser(login, first, last, mail string) int64 {
	c.t.Helper()
	id, err := c.d.InsertReturningID(c.ctx, `INSERT INTO principals (kind, status, firstname, lastname, created_at, updated_at) VALUES ('user', 1, ?, ?, ?, ?)`,
		first, last, db.NewTime(frozenNow), db.NewTime(frozenNow))
	c.must(err)
	c.exec(`INSERT INTO user_accounts (principal_id, login, admin, language) VALUES (?, ?, ?, 'en')`, id, login, false)
	c.exec(`INSERT INTO email_addresses (user_id, address, is_default, notify, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		id, mail, true, true, db.NewTime(frozenNow), db.NewTime(frozenNow))
	return id
}

func (c *tc) addMember(principal, project int64, roles ...int64) {
	c.t.Helper()
	_, err := repository.CreateMember(c.ctx, c.d, project, principal, roles)
	c.must(err)
}

func (c *tc) createGroup(name string) int64 {
	c.t.Helper()
	id, err := c.d.InsertReturningID(c.ctx, `INSERT INTO principals (kind, status, name, created_at, updated_at) VALUES ('group', 1, ?, ?, ?)`,
		name, db.NewTime(frozenNow), db.NewTime(frozenNow))
	c.must(err)
	return id
}

// createTracker は Tracker.generate!(:name => name)（既定ステータス New）。
func (c *tc) createTracker(name string) int64 {
	c.t.Helper()
	var pos int
	c.must(c.d.Get(c.ctx, &pos, `SELECT COALESCE(MAX(position), 0) + 1 FROM trackers`))
	id, err := c.d.InsertReturningID(c.ctx, `INSERT INTO trackers (name, default_status_id, position) VALUES (?, 1, ?)`, name, pos)
	c.must(err)
	return id
}

func dbNow() db.Time { return db.NewTime(frozenNow) }
