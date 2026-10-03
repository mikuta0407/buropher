// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package query

import (
	"context"

	"github.com/mikuta0407/buropher/internal/domain"
)

// UserOption はユーザーの選択肢（名前と id）。
type UserOption struct {
	ID     int64
	Name   string
	Status int
}

// Users は Query#users（Query#principals のうちユーザー。並びは Principal#<=>）。
// activity_authors_options_for_select などクエリ以外の画面から使う。
func (q *Query) Users(ctx context.Context) ([]UserOption, error) {
	ps, err := q.principals(ctx)
	if err != nil {
		return nil, err
	}
	uf := q.env.setting("user_format")
	var out []UserOption
	for _, p := range ps {
		if !isUserKind(p.Kind) {
			continue
		}
		out = append(out, UserOption{ID: p.ID, Name: displayName(p, uf), Status: p.Status})
	}
	return out, nil
}

// ActiveUsers は Query.new(project: project).users.select(&:active?)。
func ActiveUsers(ctx context.Context, env *Env, project *domain.Project) ([]UserOption, error) {
	q, err := New(ctx, env, KindIssue, project)
	if err != nil {
		return nil, err
	}
	us, err := q.Users(ctx)
	if err != nil {
		return nil, err
	}
	var out []UserOption
	for _, u := range us {
		if u.Status == domain.StatusActive {
			out = append(out, u)
		}
	}
	return out, nil
}
