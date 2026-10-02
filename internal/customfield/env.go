package customfield

import (
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// Env は書式が参照する外部（翻訳・DB・ビューヘルパー）。nil の関数は既定の振る舞いになる。
// *Env が nil でもよい（すべて既定）。
type Env struct {
	// T は翻訳（l / ::I18n.t）。nil ならキーをそのまま返す。
	T func(key string, args ...any) string

	// CurrentUserID は User.current.id（user 書式の "<< me >>"）。
	CurrentUserID int64

	// Enumerations は custom_field.enumerations（activeOnly なら .active。position 順）。
	Enumerations func(cfID int64, activeOnly bool) []*Enumeration
	// ProjectUsers は project.users（roleIDs が空でなければそのロールを持つメンバーのみ）を sorted 順で返す
	// （Label は User#name、Value は id）。
	ProjectUsers func(projectID int64, roleIDs []int64) []Option
	// SharedVersions は project.shared_versions（statuses が空でなければ status で絞る）を返す
	// （Label は Version#to_s、Value は id。Version#<=> の順）。
	SharedVersions func(projectID int64, statuses []string) []Option
	// SystemVersions は Version.visible.where(:sharing => 'system')（statuses で絞る。Version#<=> の順）。
	SystemVersions func(statuses []string) []Option
	// RecordOptions は RecordList の欠けている値（value_was）を target_class.where(:id => ids) から読む
	// （[to_s, id]）。format は "user" / "version" / "enumeration"。
	RecordOptions func(format string, ids []string) []Option

	// NormalizeFloat は float 書式の値の正規化（ロケールの小数点区切りを "." に。normalize_float）。
	NormalizeFloat func(s string) string
	// DoneRatioInterval は Setting.issue_done_ratio_interval.to_i（progressbar の既定刻み）。0 なら 10。
	DoneRatioInterval func() int

	// FormatObject は ApplicationHelper#format_object(object, html: false)（キャスト後の値の表示文字列）。
	// nil なら to_s。
	FormatObject func(v any, html bool) any
	// Textilizable はテキスト整形（textilizable(value, :object => customized)）。nil なら simple_format 相当。
	Textilizable func(text string, customized *Customized) rails.HTML
	// ProgressBar は progress_bar(pct, legend:)。
	ProgressBar func(pct int, legend string) rails.HTML
	// CalendarFor は calendar_for(field_id)（ヘッダへの datepicker の追加を含む）。
	CalendarFor func(fieldID string) rails.HTML
	// AttachmentForm は attachment 書式の edit_tag（attachments/form 部分テンプレート）。
	AttachmentForm func(tagName string, cv *CustomValue) rails.HTML
	// ActionName は action_name（progressbar の legend 表示判定）。
	ActionName string
}

func (e *Env) l(key string, args ...any) string {
	if e == nil || e.T == nil {
		return key
	}
	return e.T(key, args...)
}

func (e *Env) doneRatioInterval() int {
	if e != nil && e.DoneRatioInterval != nil {
		if n := e.DoneRatioInterval(); n > 0 {
			return n
		}
	}
	return 10
}

func (e *Env) formatObject(v any, html bool) any {
	if e != nil && e.FormatObject != nil {
		return e.FormatObject(v, html)
	}
	return rails.ToS(v)
}
