// Package activity は Redmine の活動（Redmine::Activity, Redmine::Activity::Fetcher,
// acts_as_activity_provider, acts_as_event）の移植。
//
// イベント種別の登録は lib/redmine/preparation.rb の Activity.map と同じ順序・既定値:
//
//	issues (Issue, Journal) / changesets / news / documents (Document, Attachment) /
//	files (Attachment) / wiki_edits (WikiContentVersion, 既定 OFF) / messages (既定 OFF) /
//	time_entries (既定 OFF)
//
// 各プロバイダの find_events（期間・作成者・件数・可視性）は providers.go、
// acts_as_event の属性（タイトル・説明・日時・作成者・URL・種別・グループ）の組み立ては events.go。
// 検索（internal/search）も検索結果の表示に同じ Event を使う。
package activity

import (
	"time"

	"github.com/mikuta0407/buropher/internal/domain"
)

// Provider はイベントを提供するモデル（Redmine のクラス名）。
type Provider string

const (
	ProviderIssue              Provider = "Issue"
	ProviderJournal            Provider = "Journal"
	ProviderChangeset          Provider = "Changeset"
	ProviderNews               Provider = "News"
	ProviderDocument           Provider = "Document"
	ProviderAttachment         Provider = "Attachment"
	ProviderWikiContentVersion Provider = "WikiContentVersion"
	ProviderMessage            Provider = "Message"
	ProviderTimeEntry          Provider = "TimeEntry"
	// 検索結果だけに現れるもの
	ProviderWikiPage Provider = "WikiPage"
	ProviderProject  Provider = "Project"
)

// eventType は Redmine::Activity.register の 1 件。
type eventType struct {
	name      string
	isDefault bool
	providers []Provider
}

// registry は Redmine::Activity.available_event_types と providers（登録順）。
var registry = []eventType{
	{"issues", true, []Provider{ProviderIssue, ProviderJournal}},
	{"changesets", true, []Provider{ProviderChangeset}},
	{"news", true, []Provider{ProviderNews}},
	{"documents", true, []Provider{ProviderDocument, ProviderAttachment}},
	{"files", true, []Provider{ProviderAttachment}},
	{"wiki_edits", false, []Provider{ProviderWikiContentVersion}},
	{"messages", false, []Provider{ProviderMessage}},
	{"time_entries", false, []Provider{ProviderTimeEntry}},
}

// AvailableEventTypes は Redmine::Activity.available_event_types。
func AvailableEventTypes() []string {
	out := make([]string, len(registry))
	for i, t := range registry {
		out[i] = t.name
	}
	return out
}

// DefaultEventTypes は Redmine::Activity.default_event_types。
func DefaultEventTypes() []string {
	var out []string
	for _, t := range registry {
		if t.isDefault {
			out = append(out, t.name)
		}
	}
	return out
}

func providersOf(name string) []Provider {
	for _, t := range registry {
		if t.name == name {
			return t.providers
		}
	}
	return nil
}

// permissionFor は Fetcher#event_types が使う権限（activity_provider_options[:permission]、
// 無ければ :"view_#{event_type}"）。コアのプロバイダはいずれも view_<種別> になる。
func permissionFor(eventType string, _ Provider) string { return "view_" + eventType }

// Event は acts_as_event を持つレコード 1 件（event_* の値を評価済みのもの）。
type Event struct {
	// Provider はモデル（クラス名）。
	Provider Provider
	// ID はレコードの id。
	ID int64
	// Project は event の project（Changeset は repository.project、WikiContentVersion は page.wiki.project）。
	Project *domain.Project
	// Datetime は event_datetime（UTC）。
	Datetime time.Time
	// Title は event_title。
	Title string
	// Description は event_description。
	Description string
	// DescriptionNull は event_description が nil（API で null を出す）。
	DescriptionNull bool
	// Author は event_author（*domain.User / string（リポジトリのコミッター）/ nil）。
	Author any
	// URL は event_url（パスのみ。アンカー・クエリを含む）。
	URL string
	// Type は event_type（CSS クラス・アイコン名）。
	Type string
	// Group は event_group の同一性キー（"Issue:12" など。グループが無ければ自身）。
	Group string
}

// AuthorUser は event_author が User ならそれを返す。
func (e *Event) AuthorUser() *domain.User {
	if u, ok := e.Author.(*domain.User); ok {
		return u
	}
	return nil
}
