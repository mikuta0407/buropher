package mailhandler

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mikuta0407/buropher/internal/customfield"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/i18n"
	"github.com/mikuta0407/buropher/internal/issues"
	"github.com/mikuta0407/buropher/internal/repository"
)

// このファイルは本文のキーワード（"Status: Resolved" のような行）によるチケット属性の指定
// （get_keyword / extract_keyword! / issue_attributes_from_keywords / custom_field_values_from_keywords）。

// keywordAttr はキーワードの属性。Redmine ではコアの属性はシンボル（訳語もキーになる）、
// カスタムフィールドは名前の文字列。
type keywordAttr struct {
	name   string
	symbol bool
}

func sym(name string) keywordAttr { return keywordAttr{name: name, symbol: true} }

func (a keywordAttr) cacheKey() string {
	if a.symbol {
		return ":" + a.name
	}
	return "s" + a.name
}

// getKeyword は get_keyword(attr, options)。override が nil なら allow_override で判定する。
func (r *receiver) getKeyword(attr keywordAttr, format string, override *bool) *string {
	if v, ok := r.keywords[attr.cacheKey()]; ok {
		return v
	}
	var ov bool
	if override != nil {
		ov = *override
	} else {
		ov = r.opts.overridable(attr.name)
	}
	var result *string
	if ov {
		result = r.extractKeyword(attr, format)
	}
	if result == nil && attr.symbol {
		// handler_options[:issue][attr].present?（カスタムフィールドの名前は既定値のキーにならない）
		if v, ok := r.opts.issue[attr.name]; ok && strings.TrimSpace(v) != "" {
			result = &v
		}
	}
	r.keywords[attr.cacheKey()] = result
	return result
}

var (
	humanizeIDRe   = regexp.MustCompile(`_id\z`)
	humanizeWordRe = regexp.MustCompile(`(?i)[a-z\d]+`)
)

// humanize は ActiveSupport の String#humanize（先頭の "_" と末尾の "_id" を除き、"_" を空白にし、
// 英数字の語を小文字にして先頭を大文字にする）。
func humanize(s string) string {
	s = strings.TrimLeft(s, "_")
	s = humanizeIDRe.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "_", " ")
	s = humanizeWordRe.ReplaceAllStringFunc(s, strings.ToLower)
	if s == "" {
		return s
	}
	// \A\w の先頭 1 文字を大文字に
	if r, size := utf8.DecodeRuneInString(s); isWordRune(r) {
		return strings.ToUpper(string(r)) + s[size:]
	}
	return s
}

func isWordRune(r rune) bool {
	return r == '_' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r > 0x7f
}

// translateDefault は l(key, :default => ”, :locale => locale)。
func (r *receiver) translateDefault(locale, key string) string {
	return i18n.RubyToS(r.h.bundle().TranslateDefault(locale, key, nil, ""))
}

// extractKeyword は extract_keyword!（本文から attr のキーワード行を取り除き、その値を返す）。
func (r *receiver) extractKeyword(attr keywordAttr, format string) *string {
	keys := []string{humanize(attr.name)}
	if attr.symbol {
		if r.user != nil && strings.TrimSpace(r.user.Language) != "" {
			keys = append(keys, r.translateDefault(r.user.Language, "field_"+attr.name))
		}
		if lang := r.h.Settings.String("default_language"); strings.TrimSpace(lang) != "" {
			keys = append(keys, r.translateDefault(lang, "field_"+attr.name))
		}
	}
	var quoted []string
	for _, k := range keys {
		if strings.TrimSpace(k) != "" {
			quoted = append(quoted, regexp.QuoteMeta(k))
		}
	}
	if format == "" {
		format = ".+"
	}
	re, err := regexp.Compile(`(?mi)^(` + strings.Join(quoted, "|") + `)[ \t]*:[ \t]*(` + format + `)\s*$`)
	if err != nil {
		r.h.logger().Error("MailHandler: invalid keyword pattern (" + err.Error() + ")")
		return nil
	}
	text := r.cleanedUpTextBody()
	loc := re.FindStringSubmatchIndex(text)
	if loc == nil {
		return nil
	}
	v := rubyStrip(text[loc[4]:loc[5]])
	text = text[:loc[0]] + text[loc[1]:]
	r.cleanBody = &text
	return &v
}

// getKeywordBool は get_keyword_bool（"1" / "0" と、既定言語・ユーザーの言語の Yes / No）。
func (r *receiver) getKeywordBool(attr string) *bool {
	trueValues := []string{"1"}
	falseValues := []string{"0"}
	locales := []string{r.h.Settings.String("default_language")}
	if r.user != nil {
		locales = append(locales, r.user.Language)
	}
	for _, locale := range locales {
		if strings.TrimSpace(locale) == "" {
			continue
		}
		trueValues = append(trueValues, r.translateDefault(locale, "general_text_yes"), r.translateDefault(locale, "general_text_Yes"))
		falseValues = append(falseValues, r.translateDefault(locale, "general_text_no"), r.translateDefault(locale, "general_text_No"))
	}
	var alts []string
	for _, v := range append(append([]string{}, trueValues...), falseValues...) {
		if strings.TrimSpace(v) != "" {
			alts = append(alts, regexp.QuoteMeta(v))
		}
	}
	value := r.getKeyword(sym(attr), strings.Join(alts, "|"), nil)
	if value == nil {
		return nil
	}
	if slices.Contains(trueValues, *value) {
		t := true
		return &t
	}
	if slices.Contains(falseValues, *value) {
		f := false
		return &f
	}
	return nil
}

// named は scope :named（LOWER(name) = LOWER(arg.strip)）。
func named(name, arg string) bool {
	return strings.ToLower(name) == strings.ToLower(strings.TrimSpace(arg))
}

// issueAttributesFromKeywords は issue_attributes_from_keywords（値が空の属性は除く）。
func (r *receiver) issueAttributesFromKeywords(ctx context.Context, env *issues.Env, iss *issues.Issue) (issues.Params, error) {
	attrs := issues.Params{}
	project, err := env.ProjectOf(ctx, iss)
	if err != nil {
		return nil, err
	}
	if k := r.getKeyword(sym("tracker"), "", nil); k != nil && project != nil {
		ts, err := env.ProjectTrackers(ctx, project)
		if err != nil {
			return nil, err
		}
		for _, t := range ts {
			if named(t.Name, *k) {
				attrs["tracker_id"] = t.ID
				break
			}
		}
	}
	if k := r.getKeyword(sym("status"), "", nil); k != nil {
		sts, err := env.Statuses(ctx)
		if err != nil {
			return nil, err
		}
		var found *domain.IssueStatus
		for _, s := range sts {
			if named(s.Name, *k) && (found == nil || s.ID < found.ID) {
				found = s
			}
		}
		if found != nil {
			attrs["status_id"] = found.ID
		}
	}
	if k := r.getKeyword(sym("priority"), "", nil); k != nil {
		ps, err := env.Priorities(ctx)
		if err != nil {
			return nil, err
		}
		for _, p := range ps {
			if named(p.Name, *k) {
				attrs["priority_id"] = p.ID
				break
			}
		}
	}
	if k := r.getKeyword(sym("category"), "", nil); k != nil && project != nil {
		cats, err := repository.ProjectIssueCategories(ctx, env.Q, project.ID)
		if err != nil {
			return nil, err
		}
		for _, c := range cats {
			if named(c.Name, *k) {
				attrs["category_id"] = c.ID
				break
			}
		}
	}
	if k := r.getKeyword(sym("assigned_to"), "", nil); k != nil {
		refs, err := env.AssignableUsers(ctx, iss)
		if err != nil {
			return nil, err
		}
		ps, err := r.keywordPrincipalsFromRefs(ctx, env.Q, refs)
		if err != nil {
			return nil, err
		}
		if p := detectByKeyword(ps, *k); p != nil {
			attrs["assigned_to_id"] = p.ID
		}
	}
	if k := r.getKeyword(sym("fixed_version"), "", nil); k != nil && project != nil {
		vs, err := repository.LoadVersionsWhere(ctx, env.Q, repository.SharedVersionsCondition(project))
		if err != nil {
			return nil, err
		}
		var found *domain.Version
		for _, v := range vs {
			if named(v.Name, *k) && (found == nil || v.ID < found.ID) {
				found = v
			}
		}
		if found != nil {
			attrs["fixed_version_id"] = found.ID
		}
	}
	set := func(key string, v *string) {
		if v != nil && strings.TrimSpace(*v) != "" {
			attrs[key] = *v
		}
	}
	set("start_date", r.getKeyword(sym("start_date"), `\d{4}-\d{2}-\d{2}`, nil))
	set("due_date", r.getKeyword(sym("due_date"), `\d{4}-\d{2}-\d{2}`, nil))
	set("estimated_hours", r.getKeyword(sym("estimated_hours"), "", nil))
	set("done_ratio", r.getKeyword(sym("done_ratio"), `(\d|10)?0`, nil))
	// false.blank? は true なので false は除かれる
	if b := r.getKeywordBool("is_private"); b != nil && *b {
		attrs["is_private"] = true
	}
	set("parent_issue_id", r.getKeyword(sym("parent_issue"), "", nil))
	return attrs, nil
}

// customFieldValuesFromKeywords は custom_field_values_from_keywords（カスタムフィールド名のキーワード）。
func (r *receiver) customFieldValuesFromKeywords(ctx context.Context, q db.Queryer, env *issues.Env, iss *issues.Issue) (map[string]any, error) {
	cfvs, err := env.CustomFieldValues(ctx, iss)
	if err != nil {
		return nil, err
	}
	project, err := env.ProjectOf(ctx, iss)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	for _, v := range cfvs {
		cf := v.Field
		k := r.getKeyword(keywordAttr{name: cf.Name}, "", nil)
		if k == nil {
			continue
		}
		cz := &customfield.Customized{Kind: "issue", ID: iss.ID, ProjectID: iss.ProjectID}
		if project != nil {
			cz.ProjectIdentifier = project.Identifier
		}
		out[strconv.FormatInt(cf.ID, 10)] = r.cfValueFromKeyword(ctx, q, cf, *k, cz)
	}
	return out, nil
}

// cfValueFromKeyword は custom_field.value_from_keyword(keyword, customized)。
func (r *receiver) cfValueFromKeyword(ctx context.Context, q db.Queryer, cf *customfield.CustomField, keyword string, cz *customfield.Customized) any {
	env := r.cfEnv(ctx, q)
	if cf.FieldFormat == "user" {
		// UserFormat#value_from_keyword: Principal.detect_by_keyword(possible_values_records, k)
		users, err := r.projectUsers(ctx, q, cz.ProjectID, cfRoleIDs(cf))
		if err != nil {
			r.h.logger().Error("MailHandler: user custom field", "err", err)
		}
		ps := r.keywordPrincipalsFromUsers(users)
		return parseKeyword(cf, keyword, func(k string) (string, bool) {
			if p := detectByKeyword(ps, k); p != nil {
				return strconv.FormatInt(p.ID, 10), true
			}
			return "", false
		})
	}
	return customfield.FindFormat(cf.FieldFormat).ValueFromKeyword(env, cf, keyword, cz)
}

func cfRoleIDs(cf *customfield.CustomField) []int64 {
	var ids []int64
	for _, s := range cf.SettingList("user_role") {
		if strings.TrimSpace(s) != "" {
			ids = append(ids, customfield.RubyToI(s))
		}
	}
	return ids
}

// parseKeyword は CustomFieldFormat::Base#parse_keyword（複数値はカンマ区切りを最長一致で分割する）。
func parseKeyword(cf *customfield.CustomField, keyword string, find func(string) (string, bool)) any {
	if !cf.Multiple {
		if v, ok := find(strings.TrimSpace(keyword)); ok {
			return v
		}
		return nil
	}
	values := []string{}
	for len(keyword) > 0 {
		k := keyword
		for {
			if v, ok := find(strings.TrimSpace(k)); ok {
				values = append(values, v)
				break
			}
			i := strings.LastIndex(k, ",")
			if i < 0 {
				break
			}
			k = k[:i]
		}
		// keyword.slice!(/\A#{Regexp.escape k},?/)
		keyword = strings.TrimPrefix(keyword, k)
		keyword = strings.TrimPrefix(keyword, ",")
	}
	return values
}

// cfEnv はカスタムフィールドの選択肢の取得（customfield.Env）。
func (r *receiver) cfEnv(ctx context.Context, q db.Queryer) *customfield.Env {
	loc := r.localizer()
	env := &customfield.Env{
		T:              loc.L,
		NormalizeFloat: loc.NormalizeFloat,
		Enumerations: func(cfID int64, activeOnly bool) []*customfield.Enumeration {
			es, err := customfield.Enumerations(ctx, q, cfID, activeOnly)
			if err != nil {
				r.h.logger().Error("MailHandler: custom field enumerations", "err", err)
			}
			return es
		},
		SharedVersions: func(projectID int64, statuses []string) []customfield.Option {
			p, err := repository.GetProject(ctx, q, projectID)
			if err != nil {
				return nil
			}
			vs, err := repository.LoadVersionsWhere(ctx, q, repository.SharedVersionsCondition(p))
			if err != nil {
				r.h.logger().Error("MailHandler: shared versions", "err", err)
				return nil
			}
			var out []customfield.Option
			for _, v := range vs {
				if len(statuses) > 0 && !slices.Contains(statuses, v.Status) {
					continue
				}
				out = append(out, customfield.Option{Label: v.Name, Value: strconv.FormatInt(v.ID, 10)})
			}
			return out
		},
		ProjectUsers: func(projectID int64, roleIDs []int64) []customfield.Option {
			users, err := r.projectUsers(ctx, q, projectID, roleIDs)
			if err != nil {
				return nil
			}
			format := r.h.Settings.String("user_format")
			var out []customfield.Option
			for _, u := range users {
				out = append(out, customfield.Option{Label: u.Name(format), Value: strconv.FormatInt(u.ID, 10)})
			}
			return out
		},
	}
	if r.user != nil {
		env.CurrentUserID = r.user.ID
	}
	return env
}

// projectUsers は project.users（roleIDs が空でなければそのロールのメンバー。ユーザーのみ）。
func (r *receiver) projectUsers(ctx context.Context, q db.Queryer, projectID int64, roleIDs []int64) ([]*domain.User, error) {
	if projectID == 0 {
		return nil, nil
	}
	ids, err := repository.ProjectMemberUserIDs(ctx, q, projectID, roleIDs)
	if err != nil {
		return nil, err
	}
	um, err := repository.UsersByIDs(ctx, q, ids)
	if err != nil {
		return nil, err
	}
	var users []*domain.User
	for _, id := range ids {
		if u := um[id]; u != nil && u.Kind == domain.KindUser && u.Active() {
			users = append(users, u)
		}
	}
	return users, nil
}

// ---------------------------------------------------------------- Principal.detect_by_keyword

// keywordPrincipal は detect_by_keyword の比較に使う属性。
type keywordPrincipal struct {
	ID        int64
	IsUser    bool
	Login     string
	Mail      string
	Firstname string
	Lastname  string
	Name      string
}

// detectByKeyword は Principal.detect_by_keyword(principals, keyword)（login → メールアドレス →
// "名 姓" → 表示名の順に、大文字小文字を区別せずに探す）。
func detectByKeyword(ps []keywordPrincipal, keyword string) *keywordPrincipal {
	if strings.TrimSpace(keyword) == "" {
		return nil
	}
	eq := func(a, b string) bool { return asciiLower(a) == asciiLower(b) }
	for i := range ps {
		if eq(keyword, ps[i].Login) {
			return &ps[i]
		}
	}
	for i := range ps {
		if eq(keyword, ps[i].Mail) {
			return &ps[i]
		}
	}
	if strings.Contains(keyword, " ") {
		f := strings.Fields(keyword)
		first, last := "", ""
		if len(f) > 0 {
			first = f[0]
		}
		if len(f) > 1 {
			last = f[1]
		}
		for i := range ps {
			if ps[i].IsUser && eq(first, ps[i].Firstname) && eq(last, ps[i].Lastname) {
				return &ps[i]
			}
		}
	}
	for i := range ps {
		if eq(keyword, ps[i].Name) {
			return &ps[i]
		}
	}
	return nil
}

// asciiLower は String#casecmp の比較（ASCII の大文字小文字のみ同一視する）。
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

func (r *receiver) keywordPrincipalsFromRefs(ctx context.Context, q db.Queryer, refs []*issues.PrincipalRef) ([]keywordPrincipal, error) {
	var ids []int64
	for _, ref := range refs {
		ids = append(ids, ref.ID)
	}
	users, err := repository.UsersByIDs(ctx, q, ids)
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return nil, err
	}
	format := r.h.Settings.String("user_format")
	out := make([]keywordPrincipal, 0, len(refs))
	for _, ref := range refs {
		kp := keywordPrincipal{ID: ref.ID, IsUser: ref.IsUser(), Login: ref.Login, Firstname: ref.Firstname,
			Lastname: ref.Lastname, Name: ref.DisplayName(format)}
		if u := users[ref.ID]; u != nil {
			kp.Mail = u.Mail
		}
		out = append(out, kp)
	}
	return out, nil
}

func (r *receiver) keywordPrincipalsFromUsers(us []*domain.User) []keywordPrincipal {
	format := r.h.Settings.String("user_format")
	out := make([]keywordPrincipal, 0, len(us))
	for _, u := range us {
		out = append(out, keywordPrincipal{ID: u.ID, IsUser: true, Login: u.Login, Mail: u.Mail, Firstname: u.Firstname,
			Lastname: u.Lastname, Name: u.Name(format)})
	}
	return out
}
