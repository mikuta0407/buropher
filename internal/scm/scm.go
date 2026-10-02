// Copyright (C) 2026 buropher contributors
// SPDX-License-Identifier: GPL-2.0-or-later

// Package scm は Redmine の SCM アダプタ（lib/redmine/scm/adapters/abstract_adapter.rb,
// git_adapter.rb）の移植。buropher は Git のみ対応する（D-15）。
//
// Redmine と同じくシステムの git コマンドを実行して結果を解析する（git の実行ファイルは
// 設定 scm.git_command で変更できる）。それ以外は純 Go。
package scm

import (
	"errors"
	"sort"
	"strings"
	"time"
)

// ErrCommandAborted は ScmCommandAborted（git が 0 以外で終了した）。
var ErrCommandAborted = errors.New("scm: command aborted")

// CommandFailed は Redmine::Scm::Adapters::CommandFailed（error_scm_command_failed で表示する）。
type CommandFailed struct{ Message string }

func (e *CommandFailed) Error() string { return e.Message }

// Entry は Redmine::Scm::Adapters::Entry（ファイルまたはディレクトリ）。
type Entry struct {
	Name string
	Path string
	// Kind は "dir" / "file"。
	Kind string
	// Size はファイルサイズ（ディレクトリは nil）。
	Size *int64
	// Lastrev は最後に変更したリビジョン（report_last_commit が無効なら空の Revision、ルートは nil）。
	Lastrev *Revision
}

// IsDir は is_dir?。
func (e *Entry) IsDir() bool { return e.Kind == "dir" }

// SizeValue は size（nil なら 0）。
func (e *Entry) SizeValue() int64 {
	if e.Size == nil {
		return 0
	}
	return *e.Size
}

// IsFile は is_file?。
func (e *Entry) IsFile() bool { return e.Kind == "file" }

// SortEntriesByName は Entries#sort_by_name（種類 dir < file、次に名前のバイト順）。
func SortEntriesByName(es []*Entry) []*Entry {
	out := append([]*Entry(nil), es...)
	sort.SliceStable(out, func(i, j int) bool {
		x, y := out[i], out[j]
		if x.Kind == y.Kind {
			return x.Name < y.Name
		}
		return x.Kind < y.Kind
	})
	return out
}

// Revision は Redmine::Scm::Adapters::Revision。
type Revision struct {
	Identifier string
	Scmid      string
	Revision   string
	Author     string
	// Time はコミット時刻（コミッタのタイムゾーンを保持する）。
	Time    time.Time
	Message string
	Paths   []Change
	Parents []string
}

// FormatIdentifier は GitAdapter::Revision#format_identifier（先頭 8 文字）。
func (r *Revision) FormatIdentifier() string {
	if len(r.Identifier) > 8 {
		return r.Identifier[:8]
	}
	return r.Identifier
}

// Equal は Revision#==（scmid → identifier → revision の順で比較）。
func (r *Revision) Equal(o *Revision) bool {
	switch {
	case o == nil:
		return false
	case r.Scmid != "":
		return r.Scmid == o.Scmid
	case r.Identifier != "":
		return r.Identifier == o.Identifier
	case r.Revision != "":
		return r.Revision == o.Revision
	}
	return false
}

// Change はリビジョンで変更されたファイル（{:action, :path, :from_path, :from_revision}）。
type Change struct {
	Action       string
	Path         string
	FromPath     string
	FromRevision string
}

// Branch は GitAdapter::GitBranch。
type Branch struct {
	Name      string
	Scmid     string
	IsDefault bool
}

// Annotate は Redmine::Scm::Adapters::Annotate。
type Annotate struct {
	Lines     []string
	Revisions []*Revision
	// Previous は git blame の previous（"<sha> <path>"。無ければ ""）。
	Previous []string
}

// Content は annotate.content（行を改行で連結）。
func (a *Annotate) Content() string { return strings.Join(a.Lines, "\n") }

// Empty は empty?。
func (a *Annotate) Empty() bool { return len(a.Lines) == 0 }

// HasPrevious は previous_annotations.any?。
func (a *Annotate) HasPrevious() bool {
	for _, p := range a.Previous {
		if p != "" {
			return true
		}
	}
	return false
}

// IsBinary は ScmData.binary?（NUL を含むか、制御文字が 10% を超える）。
func IsBinary(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	n := 0
	for _, c := range data {
		if c == 0 {
			return true
		}
		if (c < 0x20 || c == 0x7f) && c != '\t' && c != '\r' && c != '\n' {
			n++
		}
	}
	return float64(n)/float64(len(data)) > 0.1
}

// WithLeadingSlash は with_leading_slash。
func WithLeadingSlash(p string) string {
	if strings.HasPrefix(p, "/") {
		return p
	}
	return "/" + p
}
