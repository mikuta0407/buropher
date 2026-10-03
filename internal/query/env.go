// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package query

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/i18n"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/settings"
)

// Env はクエリの評価環境 (Redmine の User.current / Setting / I18n.locale / Time.now に相当)。
// 1 リクエスト用で並行利用は想定しない (Authorizer がキャッシュを持つため)。
type Env struct {
	// Q は DB ハンドル。
	Q db.Queryer
	// Auth は User.current の権限判定器。
	Auth *authz.Authorizer
	// Settings は Setting。
	Settings *settings.Settings
	// L はラベル (フィルタ名・演算子名・エラーメッセージ) の翻訳に使う。nil なら英語。
	L *i18n.Localizer
	// Now は現在時刻 (テストで固定する)。nil なら time.Now。
	Now func() time.Time
	// TimeZone は User.current.time_zone (pref.time_zone)。nil ならタイムゾーン未設定ユーザ。
	TimeZone *time.Location
	// ServerLocation は Time.local のタイムゾーン (サーバのローカル時刻)。nil なら time.Local。
	ServerLocation *time.Location

	// ProjectNestedSet はプロジェクトのツリー順 (lft) の計算方法。nil なら repository.ProjectNestedSet。
	ProjectNestedSet func(ctx context.Context) (map[int64]repository.NestedSetValue, error)

	bookmarks       []int64
	bookmarksLoaded bool
}

// NewEnv は user を主体とする Env を作り、pref.time_zone を読み込む。
func NewEnv(ctx context.Context, q db.Queryer, user *domain.User, st *settings.Settings) (*Env, error) {
	e := &Env{Q: q, Auth: authz.New(q, user), Settings: st}
	if user.Logged() && user.ID != 0 {
		var tz sql.NullString
		err := q.Get(ctx, &tz, `SELECT time_zone FROM user_preferences WHERE user_id = ?`, user.ID)
		if err != nil && !errors.Is(err, db.ErrNoRows) {
			return nil, err
		}
		if tz.Valid {
			e.TimeZone = i18n.UserLocation(tz.String)
		}
	}
	return e, nil
}

// User は User.current。
func (e *Env) User() *domain.User { return e.Auth.User() }

func (e *Env) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e *Env) serverLoc() *time.Location {
	if e.ServerLocation != nil {
		return e.ServerLocation
	}
	return time.Local
}

// userLoc は日付境界の計算に使うタイムゾーン (User.current.time_zone、無ければ Time.local)。
func (e *Env) userLoc() *time.Location {
	if e.TimeZone != nil {
		return e.TimeZone
	}
	return e.serverLoc()
}

// Today は User.current.today (ユーザのタイムゾーンでの今日。UTC 0 時の time.Time で表す)。
func (e *Env) Today() time.Time {
	y, m, d := e.now().In(e.userLoc()).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// l は Redmine の l(key, args...)。
func (e *Env) l(key string, args ...any) string {
	if e.L != nil {
		return e.L.L(key, args...)
	}
	return defaultLocalizer().L(key, args...)
}

// defaultLocalizer は Env.L が無いときの英語の Localizer。
var defaultLocalizer = sync.OnceValue(func() *i18n.Localizer {
	return i18n.Default().NewLocalizer("en", i18n.Settings{}, nil)
})

func (e *Env) setting(name string) string {
	if e.Settings == nil {
		if d := settings.Lookup(name); d != nil {
			if s, ok := d.Default.(string); ok {
				return s
			}
		}
		return ""
	}
	return e.Settings.String(name)
}

func (e *Env) settingBool(name string) bool { return settings.RubyToI(e.setting(name)) > 0 }

func (e *Env) settingValue(name string) any {
	if e.Settings == nil {
		if d := settings.Lookup(name); d != nil {
			return d.Default
		}
		return nil
	}
	return e.Settings.Value(name)
}

func (e *Env) settingStrings(name string) []string {
	if e.Settings != nil {
		return e.Settings.Strings(name)
	}
	return anyStrings(e.settingValue(name))
}

// settingHashStrings は serialized Hash 設定 (time_entry_list_defaults 等) の配列値。
func (e *Env) settingHashStrings(name, key string) []string {
	m, _ := e.settingValue(name).(map[string]any)
	return anyStrings(m[key])
}

func anyStrings(v any) []string {
	a, _ := v.([]any)
	out := make([]string, 0, len(a))
	for _, x := range a {
		out = append(out, i18n.RubyToS(x))
	}
	return out
}

// BookmarkedProjectIDs は User#bookmarked_project_ids。
func (e *Env) BookmarkedProjectIDs(ctx context.Context) ([]int64, error) {
	if !e.bookmarksLoaded {
		u := e.User()
		if u.Logged() && u.ID != 0 {
			if err := e.Q.Select(ctx, &e.bookmarks, `SELECT project_id FROM user_project_bookmarks WHERE user_id = ? ORDER BY position, project_id`, u.ID); err != nil {
				return nil, err
			}
		}
		e.bookmarksLoaded = true
	}
	return e.bookmarks, nil
}

func (e *Env) dialect() db.Dialect { return e.Q.Dialect() }

func (e *Env) boolLit(b bool) string { return e.dialect().BoolLiteral(b) }
