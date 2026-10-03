package customfield

import (
	"strings"
	"testing"

	"github.com/mikuta0407/buropher/internal/i18n"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// このファイルは test/unit/lib/redmine/field_format/*_test.rb のうち DB を要しないものの移植。

func testEnv(t *testing.T) *Env {
	t.Helper()
	loc := i18n.Default().NewLocalizer("en", i18n.Settings{}, nil)
	return &Env{T: loc.L, NormalizeFloat: loc.NormalizeFloat}
}

func field(format string, opts ...func(cf *CustomField)) *CustomField {
	cf := &CustomField{OwnerKind: KindIssue, Name: "F", FieldFormat: format, Visible: true, Editable: true,
		PossibleValues: []string{}, Settings: map[string]any{}}
	for _, o := range opts {
		o(cf)
	}
	return cf
}

func withSetting(k string, v any) func(cf *CustomField) {
	return func(cf *CustomField) { cf.SetSetting(k, v) }
}

func withValues(vs ...string) func(cf *CustomField) {
	return func(cf *CustomField) { cf.PossibleValues = vs }
}

func str(s string) *string { return &s }

func formatted(env *Env, cf *CustomField, value any, c *Customized, html bool) string {
	return rails.ToS(FindFormat(cf.FieldFormat).FormattedValue(env, cf, value, c, html))
}

func TestAllFormats(t *testing.T) {
	want := []string{"string", "text", "link", "int", "float", "date", "list", "bool", "enumeration", "user", "version", "attachment", "progressbar"}
	if got := FormatNames; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("formats = %v", got)
	}
}

func TestAsSelect(t *testing.T) {
	env := testEnv(t)
	// test_as_select_should_return_enumeration_for_all_classes
	for _, ty := range Types {
		k := ty.Kind
		if !containsValue(AsSelect(env.T, k), "enumeration") {
			t.Errorf("%s: enumeration missing", k)
		}
	}
	var labels []string
	for _, o := range AsSelect(env.T, KindIssue) {
		labels = append(labels, o.Label)
	}
	if got := strings.Join(labels, ","); got != "Boolean,Date,File,Float,Integer,Key/value list,Link,List,Long text,Progress bar,Text,User,Version" {
		t.Errorf("as_select(Issue) = %s", got)
	}
	if containsValue(AsSelect(env.T, KindUser), "user") || containsValue(AsSelect(env.T, KindUser), "version") {
		t.Error("user/version formats should not be available for User")
	}
}

func TestStringAndTextFormatting(t *testing.T) {
	env := testEnv(t)
	cf := field("string")
	if got := formatted(env, cf, "*foo*", nil, true); got != "*foo*" {
		t.Errorf("string html = %q", got)
	}
	cf = field("text")
	if got := formatted(env, cf, "*foo*\nbar", nil, false); got != "*foo*\nbar" {
		t.Errorf("text = %q", got)
	}
	if got := formatted(env, cf, "*foo*\nbar", nil, true); !strings.Contains(got, "*foo*\n<br />bar") {
		t.Errorf("text html = %q", got)
	}
}

func TestURLPattern(t *testing.T) {
	env := testEnv(t)
	cases := []struct {
		format, pattern, value, want string
	}{
		{"string", "http://foo/%value%", "bar", `<a href="http://foo/bar" class="external">bar</a>`},
		{"string", "http://foo/%value%", "foo bar", `<a href="http://foo/foo%20bar" class="external">foo bar</a>`},
		{"string", "http://foo/%value%", "foo :bar", `<a href="http://foo/foo%20:bar" class="external">foo :bar</a>`},
		{"string", "http://foo/bar#anchor", "1", `<a href="http://foo/bar#anchor" class="external">1</a>`},
		{"string", "http://foo/%value%#anchor", "foo bar", `<a href="http://foo/foo%20bar#anchor" class="external">foo bar</a>`},
		{"list", "http://localhost/%value%", "foo", `<a href="http://localhost/foo" class="external">foo</a>`},
		{"int", "http://foo/%value%", "3", `<a href="http://foo/3" class="external">3</a>`},
		{"link", "http://foo/%value%", "bar", `<a href="http://foo/bar" class="external">bar</a>`},
	}
	for _, c := range cases {
		cf := field(c.format, withSetting("url_pattern", c.pattern))
		if got := formatted(env, cf, c.value, &Customized{}, true); got != c.want {
			t.Errorf("%s %q %q: got %s", c.format, c.pattern, c.value, got)
		}
		if got := formatted(env, cf, c.value, &Customized{}, false); got != c.value {
			t.Errorf("%s %q: non-html = %q", c.format, c.value, got)
		}
	}
	// test_field_with_url_pattern_and_multiple_values_should_link_values
	cf := field("list", withSetting("url_pattern", "http://localhost/%value%"))
	if got := formatted(env, cf, []string{"foo", "bar"}, nil, true); got != `<a href="http://localhost/bar" class="external">bar</a>, <a href="http://localhost/foo" class="external">foo</a>` {
		t.Errorf("multiple: %s", got)
	}
	if got := formatted(env, cf, "", nil, true); got != "" {
		t.Errorf("blank: %q", got)
	}
	// int の非 HTML はキャストされた整数
	if v := FindFormat("int").FormattedValue(env, field("int", withSetting("url_pattern", "x")), "3", nil, false); v != int64(3) {
		t.Errorf("int cast = %#v", v)
	}
}

func TestLinkFormat(t *testing.T) {
	env := testEnv(t)
	c := &Customized{ID: 10, ProjectID: 52, ProjectIdentifier: "foo_project-00"}
	for pattern, want := range map[string]string{
		"http://foo/%id%":                 `<a href="http://foo/10" class="external">bar</a>`,
		"http://foo/%project_id%":         `<a href="http://foo/52" class="external">bar</a>`,
		"http://foo/%project_identifier%": `<a href="http://foo/foo_project-00" class="external">bar</a>`,
	} {
		if got := formatted(env, field("link", withSetting("url_pattern", pattern)), "bar", c, true); got != want {
			t.Errorf("%s: %s", pattern, got)
		}
	}
	cf := field("link", withSetting("url_pattern", "http://foo/%m2%/%m1%"))
	cf.Regexp = str("^(.+)-(.+)$")
	if got := formatted(env, cf, "56-142", nil, true); got != `<a href="http://foo/142/56" class="external">56-142</a>` {
		t.Errorf("regexp groups: %s", got)
	}
	if got := formatted(env, field("link"), "http://foo/bar", nil, true); got != `<a href="http://foo/bar" class="external">http://foo/bar</a>` {
		t.Errorf("no pattern: %s", got)
	}
	if got := formatted(env, field("link"), "foo.bar", nil, true); got != `<a href="http://foo.bar" class="external">foo.bar</a>` {
		t.Errorf("default http: %s", got)
	}
}

func TestValidateURLPattern(t *testing.T) {
	env := testEnv(t)
	cf := field("string", withSetting("url_pattern", "http://foo/%value%"))
	if errs := ValidateField(env, cf, false); errs.Any() {
		t.Errorf("safe scheme: %v", errs.FullMessages(func(a string) string { return HumanAttributeName(env, a) }))
	}
	cf = field("string", withSetting("url_pattern", "foo://foo/%value%"))
	got := ValidateField(env, cf, false).FullMessages(func(a string) string { return HumanAttributeName(env, a) })
	if len(got) != 1 || got[0] != "URL is invalid" {
		t.Errorf("unsafe scheme: %v", got)
	}
}

func TestValidateField(t *testing.T) {
	env := testEnv(t)
	human := func(a string) string { return HumanAttributeName(env, a) }
	cf := field("list")
	cf.Name = ""
	got := ValidateField(env, cf, false).FullMessages(human)
	if strings.Join(got, "|") != "Name cannot be blank|Possible values cannot be blank" {
		t.Errorf("list: %v", got)
	}
	cf = field("list", withValues("a", "b"))
	cf.DefaultValue = str("c")
	if got := ValidateField(env, cf, true).FullMessages(human); strings.Join(got, "|") != "Name has already been taken|Default value is not included in the list" {
		t.Errorf("default: %v", got)
	}
	cf = field("int")
	cf.DefaultValue = str("x")
	cf.Regexp = str("(")
	if got := ValidateField(env, cf, false).FullMessages(human); strings.Join(got, "|") != "Regular expression is invalid" {
		t.Errorf("regexp: %v", got)
	}
	cf = field("int")
	cf.DefaultValue = str("x")
	if got := ValidateField(env, cf, false).FullMessages(human); strings.Join(got, "|") != "Default value is not a number" {
		t.Errorf("int default: %v", got)
	}
	cf = field("string")
	cf.Visible = false
	if got := ValidateField(env, cf, false).FullMessages(human); strings.Join(got, "|") != "Roles cannot be blank" {
		t.Errorf("roles: %v", got)
	}
	cf.OwnerKind = KindUser
	if ValidateField(env, cf, false).Any() {
		t.Error("user custom field does not require roles")
	}
}

func TestValidateSingleValues(t *testing.T) {
	env := testEnv(t)
	pb := Find("progressbar")
	cf := field("progressbar")
	for v, want := range map[string]string{"abc": "is not a number", "-10": "is invalid", "150": "is invalid", "50": "", "0": "", "100": "", "": ""} {
		errs := pb.ValidateSingleValue(env, cf, v, nil)
		if want == "" && len(errs) > 0 || want != "" && !contains(errs, want) {
			t.Errorf("progressbar %q: %v", v, errs)
		}
	}
	if errs := pb.ValidateValue(env, &CustomValue{CustomField: cf, Value: "120"}); !contains(errs, "is invalid") {
		t.Errorf("progressbar 120: %v", errs)
	}
	intF := Find("int")
	for v, ok := range map[string]bool{"12": true, " +3 ": true, "-1": true, "1.5": false, "x": false} {
		if got := len(intF.ValidateSingleValue(env, field("int"), v, nil)) == 0; got != ok {
			t.Errorf("int %q: %v", v, got)
		}
	}
	floatF := Find("float")
	for v, ok := range map[string]bool{"1.5": true, "1e3": true, " 2 ": true, "1_000.5": true, "0x1A": true, "3,33": false, "abc": false, "1.": false} {
		if got := len(floatF.ValidateSingleValue(env, field("float"), v, nil)) == 0; got != ok {
			t.Errorf("float %q: %v", v, got)
		}
	}
	dateF := Find("date")
	for v, ok := range map[string]bool{"2026-01-15": true, "2026-02-30": false, "2026-1-5": false, "x": false} {
		if got := len(dateF.ValidateSingleValue(env, field("date"), v, nil)) == 0; got != ok {
			t.Errorf("date %q: %v", v, got)
		}
	}
	str := field("string")
	minL, maxL := 2, 3
	str.MinLength, str.MaxLength = &minL, &maxL
	if errs := Find("string").ValidateSingleValue(env, str, "a", nil); !contains(errs, "is too short (minimum is 2 characters)") {
		t.Errorf("min: %v", errs)
	}
	if errs := Find("string").ValidateSingleValue(env, str, "abcd", nil); !contains(errs, "is too long (maximum is 3 characters)") {
		t.Errorf("max: %v", errs)
	}
}

func TestListValidation(t *testing.T) {
	env := testEnv(t)
	cf := field("list", withValues("Foo", "Bar"))
	l := Find("list")
	// test_possible_existing_value_should_be_valid
	cv := &CustomValue{CustomField: cf, Value: "Baz", ValueWas: "Baz"}
	if errs := l.ValidateValue(env, cv); len(errs) > 0 {
		t.Errorf("existing value: %v", errs)
	}
	var vals []string
	for _, o := range l.PossibleCustomValueOptions(env, cv) {
		vals = append(vals, o.Value)
	}
	if strings.Join(vals, ",") != "Foo,Bar,Baz" {
		t.Errorf("options = %v", vals)
	}
	// test_non_existing_value_should_be_invalid
	cv = &CustomValue{CustomField: cf, Value: "Baz"}
	if errs := ValidateCustomValue(env, cv); len(errs) != 1 || errs[0] != "is not included in the list" {
		t.Errorf("non existing: %v", errs)
	}
	// 複数値でない配列
	if errs := ValidateCustomValue(env, &CustomValue{CustomField: cf, Value: []string{"Foo"}}); !contains(errs, "is invalid") {
		t.Errorf("array: %v", errs)
	}
}

func TestCast(t *testing.T) {
	env := testEnv(t)
	pb := field("progressbar")
	for v, want := range map[string]any{"-10": int64(0), "0": int64(0), "50": int64(50), "120": int64(100)} {
		if got := Find("progressbar").Cast(env, pb, v, nil); got != want {
			t.Errorf("progressbar cast %q = %#v", v, got)
		}
	}
	if got := Find("progressbar").Cast(env, pb, "", nil); got != nil {
		t.Errorf("empty = %#v", got)
	}
	if got := Find("bool").Cast(env, field("bool"), "1", nil); got != true {
		t.Errorf("bool = %#v", got)
	}
	if got := Find("float").Cast(env, field("float"), "1.5", nil); got != 1.5 {
		t.Errorf("float = %#v", got)
	}
	got := Find("int").Cast(env, field("int"), []string{"3", "1", "2"}, nil).([]any)
	if len(got) != 3 || got[0] != int64(1) || got[2] != int64(3) {
		t.Errorf("int array = %#v", got)
	}
}

func TestSetValue(t *testing.T) {
	env := testEnv(t)
	cf := field("list")
	if got := Find("list").SetValue(env, cf, nil, []any{"a", "", "a", "b"}); strings.Join(got.([]string), ",") != "a,b" {
		t.Errorf("array = %#v", got)
	}
	if got := Find("list").SetValue(env, cf, nil, []any{""}); len(got.([]string)) != 1 || got.([]string)[0] != "" {
		t.Errorf("empty array = %#v", got)
	}
	de := i18n.Default().NewLocalizer("de", i18n.Settings{}, nil)
	envDE := &Env{T: de.L, NormalizeFloat: de.NormalizeFloat}
	if got := Find("float").SetValue(envDE, field("float"), nil, "3,33"); got != "3.33" {
		t.Errorf("de float = %#v", got)
	}
	if got := Find("float").SetValue(env, field("float"), nil, "3,33"); got != "3,33" {
		t.Errorf("en float = %#v", got)
	}
}

func TestBoolEditTag(t *testing.T) {
	env := testEnv(t)
	b := Find("bool")
	cf := field("bool", withSetting("edit_tag_style", "check_box"))
	got := string(b.EditTag(env, "abc", "xyz", &CustomValue{CustomField: cf}, nil))
	if got != `<span><input type="hidden" name="xyz" value="0" autocomplete="off" /><input type="checkbox" name="xyz" id="abc" value="1" /></span>` {
		t.Errorf("check_box: %s", got)
	}
	got = string(b.EditTag(env, "abc", "xyz", &CustomValue{CustomField: cf, Value: "1"}, nil))
	if !strings.Contains(got, `value="1" checked="checked"`) {
		t.Errorf("checked: %s", got)
	}
	cf = field("bool", withSetting("edit_tag_style", "radio"))
	got = string(b.EditTag(env, "abc", "xyz", &CustomValue{CustomField: cf}, nil))
	if strings.Count(got, `type="radio" name="xyz"`) != 3 {
		t.Errorf("radio: %s", got)
	}
	cf = field("bool")
	got = string(b.EditTag(env, "abc", "xyz", &CustomValue{CustomField: cf}, nil))
	if got != `<select name="xyz" id="abc"><option value="">&nbsp;</option><option value="1">Yes</option>`+"\n"+`<option value="0">No</option></select>` {
		t.Errorf("select: %s", got)
	}
}

func TestListEditTag(t *testing.T) {
	env := testEnv(t)
	l := Find("list")
	cf := field("list", withValues("Foo", "Bar"))
	got := string(l.EditTag(env, "abc", "xyz", &CustomValue{CustomField: cf, Value: "Bar"}, nil))
	want := `<select name="xyz" id="abc"><option value="">&nbsp;</option><option value="Foo">Foo</option>` + "\n" + `<option selected="selected" value="Bar">Bar</option></select>`
	if got != want {
		t.Errorf("select:\n%s\n%s", got, want)
	}
	cf = field("list", withValues("Foo", "Bar", "Baz"))
	cf.Multiple = true
	got = string(l.EditTag(env, "id", "name", &CustomValue{CustomField: cf, Value: []string{"Bar", "Baz"}}, nil))
	if strings.Count(got, `selected="selected"`) != 2 || !strings.Contains(got, `multiple="multiple"`) ||
		!strings.HasSuffix(got, `<input type="hidden" name="name" id="name" value="" autocomplete="off" />`) {
		t.Errorf("multiple: %s", got)
	}
	cf = field("list", withValues("Foo", "Bar"), withSetting("edit_tag_style", "check_box"))
	got = string(l.EditTag(env, "id", "name", &CustomValue{CustomField: cf, Value: "Bar"}, nil))
	want = `<span class=" check_box_group"><label><input type="radio" name="name" value="" /> (none)</label>` +
		`<label><input type="radio" name="name" value="Foo" /> Foo</label>` +
		`<label><input type="radio" name="name" value="Bar" checked="checked" /> Bar</label></span>`
	if got != want {
		t.Errorf("check_box:\n%s\n%s", got, want)
	}
	cf.Multiple = true
	got = string(l.EditTag(env, "id", "name", &CustomValue{CustomField: cf}, rails.NewHash("class", "list_cf cf_1")))
	if strings.Count(got, `type="checkbox"`) != 2 || strings.Count(got, `type="hidden"`) != 1 || !strings.HasPrefix(got, `<span class="list_cf cf_1 check_box_group">`) {
		t.Errorf("check_box multiple: %s", got)
	}
	// 必須で既定値なし
	cf = field("list", withValues("Foo"))
	cf.IsRequired = true
	got = string(l.EditTag(env, "id", "name", &CustomValue{CustomField: cf}, nil))
	if !strings.Contains(got, `<option value="">--- Please select ---</option>`) {
		t.Errorf("required: %s", got)
	}
}

func TestProgressbarTags(t *testing.T) {
	env := testEnv(t)
	pb := Find("progressbar")
	for _, step := range []int{5, 10} {
		cf := field("progressbar", withSetting("ratio_interval", step))
		got := string(pb.EditTag(env, "id", "name", &CustomValue{CustomField: cf, Value: "90"}, nil))
		if n := strings.Count(got, "<option"); n != 100/step+1 {
			t.Errorf("edit %d: %d options", step, n)
		}
		if !strings.Contains(got, `<option selected="selected" value="90">90 %</option>`) {
			t.Errorf("edit selected: %s", got)
		}
		got = string(pb.BulkEditTag(env, "id", "name", cf, []*Customized{{}, {}}, "90", nil))
		if n := strings.Count(got, "<option"); n != 100/step+2 {
			t.Errorf("bulk %d: %d options", step, n)
		}
	}
	if got := ProgressBar(50, ""); got != `<table class="progress progress-50"><tr><td style="width: 50%;" class="closed" title="50%"></td><td style="width: 50%;" class="todo"></td></tr></table><p class="percent"></p>` {
		t.Errorf("progress_bar: %s", got)
	}
	cf := field("progressbar")
	e := *env
	e.ActionName = "show"
	if got := formatted(&e, cf, nil, nil, true); got != string(ProgressBar(0, "0%")) {
		t.Errorf("nil as zero: %s", got)
	}
	if got := formatted(env, cf, 50, nil, false); got != "50" {
		t.Errorf("non html: %s", got)
	}
	cf2 := field("progressbar")
	pb.BeforeSave(env, cf2)
	if cf2.Setting("ratio_interval") != "10" {
		t.Errorf("default ratio interval = %v", cf2.SettingValue("ratio_interval"))
	}
	if pb.TotalableSupported || pb.FilterType != "integer" {
		t.Error("progressbar attributes")
	}
}

func TestBulkEditTags(t *testing.T) {
	env := testEnv(t)
	cf := field("string")
	cf.ID = 3
	got := string(Find("string").BulkEditTag(env, "issue_custom_field_values_3", "issue[custom_field_values][3]", cf, nil, "", nil))
	want := `<input type="text" name="issue[custom_field_values][3]" id="issue_custom_field_values_3" value="" />` +
		`<label class="inline"><input type="checkbox" name="issue[custom_field_values][3]" value="__none__" data-disables="#issue_custom_field_values_3" />Clear</label>`
	if got != want {
		t.Errorf("string bulk:\n%s\n%s", got, want)
	}
	cf = field("list", withValues("A", "B"))
	got = string(Find("list").BulkEditTag(env, "x", "n", cf, []*Customized{{}}, "", nil))
	want = `<select name="n" id="n"><option selected="selected" value="">(No change)</option>` + "\n" + `<option value="__none__">none</option>` + "\n" +
		`<option value="A">A</option>` + "\n" + `<option value="B">B</option></select>`
	if got != want {
		t.Errorf("list bulk:\n%s\n%s", got, want)
	}
}

func TestValueFromKeyword(t *testing.T) {
	env := testEnv(t)
	cf := field("list", withValues("Foo", "Bar", "Baz,qux"))
	l := Find("list")
	if v := l.ValueFromKeyword(env, cf, "foo", nil); v != "Foo" {
		t.Errorf("foo = %#v", v)
	}
	if v := l.ValueFromKeyword(env, cf, "baz,qux", nil); v != "Baz,qux" {
		t.Errorf("baz,qux = %#v", v)
	}
	if v := l.ValueFromKeyword(env, cf, "invalid", nil); v != nil {
		t.Errorf("invalid = %#v", v)
	}
	cf.Multiple = true
	for kw, want := range map[string]string{"foo,bar": "Foo|Bar", "baz,qux": "Baz,qux", "baz,qux,foo": "Baz,qux|Foo",
		"foo,invalid": "Foo", ",foo,": "Foo", ",foo, ,,": "Foo", "invalid": ""} {
		if got := strings.Join(l.ValueFromKeyword(env, cf, kw, nil).([]string), "|"); got != want {
			t.Errorf("%q = %q", kw, got)
		}
	}
}

func TestEnumerationAndRecordList(t *testing.T) {
	env := testEnv(t)
	env.Enumerations = func(cfID int64, active bool) []*Enumeration {
		all := []*Enumeration{{ID: 1, Name: "Foo", Active: true}, {ID: 2, Name: "Bar", Active: false}, {ID: 3, Name: "Baz", Active: true}}
		if !active {
			return all
		}
		return []*Enumeration{all[0], all[2]}
	}
	env.RecordOptions = func(format string, ids []string) []Option {
		var out []Option
		for _, id := range ids {
			if id == "2" {
				out = append(out, Option{"Bar", "2"})
			}
		}
		return out
	}
	e := Find("enumeration")
	cf := field("enumeration")
	opts := e.PossibleValuesOptions(env, cf, nil)
	if len(opts) != 2 || opts[1] != (Option{"Baz", "3"}) {
		t.Errorf("options = %v", opts)
	}
	if errs := e.ValidateValue(env, &CustomValue{CustomField: cf, Value: "2"}); len(errs) != 1 {
		t.Errorf("inactive value should be invalid: %v", errs)
	}
	if errs := e.ValidateValue(env, &CustomValue{CustomField: cf, Value: "2", ValueWas: "2"}); len(errs) != 0 {
		t.Errorf("previous value should be valid: %v", errs)
	}
	if v := e.ValueFromKeyword(env, cf, "bar", nil); v != "2" {
		t.Errorf("keyword = %#v", v)
	}
	if got := formatted(env, cf, "2", nil, false); got != "Bar" {
		t.Errorf("formatted = %q", got)
	}
}

func TestUserFormatOptions(t *testing.T) {
	env := testEnv(t)
	env.CurrentUserID = 2
	env.ProjectUsers = func(projectID int64, roleIDs []int64) []Option {
		if len(roleIDs) == 1 && roleIDs[0] == 1 {
			return []Option{{"John Smith", "2"}}
		}
		return []Option{{"Dave Lopper", "3"}, {"John Smith", "2"}}
	}
	u := Find("user")
	cf := field("user")
	if opts := u.PossibleValuesOptions(env, cf, nil); len(opts) != 0 {
		t.Errorf("no project: %v", opts)
	}
	opts := u.PossibleValuesOptions(env, cf, &Customized{ProjectID: 1})
	if len(opts) != 3 || opts[0] != (Option{"<< me >>", "2"}) {
		t.Errorf("options = %v", opts)
	}
	cf.SetSetting("user_role", []any{"1", ""})
	if opts := u.PossibleValuesOptions(env, cf, &Customized{ProjectID: 1}); len(opts) != 2 {
		t.Errorf("role filtered = %v", opts)
	}
	u.BeforeSave(env, cf)
	if l := cf.SettingList("user_role"); strings.Join(l, ",") != "1" {
		t.Errorf("before_save user_role = %v", l)
	}
}

func TestEncodeAndSafeScheme(t *testing.T) {
	if got := EncodeComponent("a b%/é"); got != "a%20b%25/%C3%A9" {
		t.Errorf("encode = %s", got)
	}
	for u, ok := range map[string]bool{"http://foo/": true, "/relative": true, "mailto:a@b": true, "foo://x": false, "javascript:alert(1)": false, "http://a b/": false} {
		if URIWithSafeScheme(u) != ok {
			t.Errorf("%s: want %v", u, ok)
		}
	}
}

// URL として解析できない値でも、javascript: などの危険なスキームのリンクは href を付けない
// （Redmine の SanitizationFilter は uri_with_link_safe_scheme? の正規表現で判定する）。
func TestLinkUnsafeSchemeWithUnparsableURL(t *testing.T) {
	env := testEnv(t)
	for _, v := range []string{"javascript://%0Aalert(1)", "JaVaScRiPt://x%0Aalert(1)//{", "vbscript://x|", "data://x%0A"} {
		for name, cf := range map[string]*CustomField{
			"link":    field("link"),
			"pattern": field("string", withSetting("url_pattern", "%value%")),
		} {
			if got := formatted(env, cf, v, nil, true); strings.Contains(got, "href") {
				t.Errorf("%s %q => %s", name, v, got)
			}
		}
	}
}

func TestLinkValueHTML(t *testing.T) {
	for v, want := range map[string]string{
		"javascript:alert(document.cookie)": `<a href="http://javascript:alert(document.cookie)">javascript:alert(document.cookie)</a>`,
		"javascript://%0Aalert(1)":          `<a>javascript://%0Aalert(1)</a>`,
		"www.example.com":                   `<a href="http://www.example.com" class="external">www.example.com</a>`,
		"https://example.com/?a=1&b=<x>":    `<a href="https://example.com/?a=1&amp;b=&lt;x&gt;">https://example.com/?a=1&amp;b=&lt;x&gt;</a>`,
	} {
		if got := string(LinkValueHTML(v)); got != want {
			t.Errorf("%q:\n got %s\nwant %s", v, got, want)
		}
	}
}

func TestSetPossibleValues(t *testing.T) {
	cf := field("list")
	SetPossibleValues(cf, "a\r\n b \n\n c")
	if strings.Join(cf.PossibleValues, "|") != "a|b|c" {
		t.Errorf("%v", cf.PossibleValues)
	}
}

func TestApplyFieldRules(t *testing.T) {
	cf := field("int")
	cf.Searchable, cf.Multiple = true, true
	ApplyFieldRules(cf)
	if cf.Searchable || cf.Multiple {
		t.Error("int supports neither searchable nor multiple")
	}
	cf = field("list")
	cf.Searchable, cf.Multiple = true, true
	ApplyFieldRules(cf)
	if !cf.Searchable || !cf.Multiple {
		t.Error("list supports both")
	}
}
