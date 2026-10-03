// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package apibuilder

import (
	"testing"
	"time"
)

// groupShow は groups/show.api.rsb と同じ構造を組み立てる。
func groupShow(b Builder) {
	b.Object("group", func() {
		b.Value("id", 10)
		b.Value("name", "A Team")
		b.Array("users", nil, func() {
			b.Attrs("user", A("id", 8, "name", "User Misc"))
		})
		b.Array("memberships", nil, func() {
			b.Object("membership", func() {
				b.Value("id", 6)
				b.Attrs("project", A("id", 5, "name", "Private child of eCookbook"))
				b.Array("roles", nil, func() {
					b.Attrs("role", A("id", 1, "name", "Manager"))
					b.Attrs("role", A("id", 2, "name", "Developer", "inherited", true))
				})
			})
		})
	})
}

func TestGroupShow(t *testing.T) {
	// 参照 Redmine の /groups/10.xml?include=users,memberships（inherited を追加）
	x := New("xml")
	groupShow(x)
	wantXML := `<?xml version="1.0" encoding="UTF-8"?><group><id>10</id><name>A Team</name><users type="array"><user id="8" name="User Misc"/></users><memberships type="array"><membership><id>6</id><project id="5" name="Private child of eCookbook"/><roles type="array"><role id="1" name="Manager"/><role id="2" name="Developer" inherited="true"/></roles></membership></memberships></group>`
	if got := string(x.Output()); got != wantXML {
		t.Errorf("xml:\n got %s\nwant %s", got, wantXML)
	}
	j := New("json")
	groupShow(j)
	wantJSON := `{"group":{"id":10,"name":"A Team","users":[{"id":8,"name":"User Misc"}],"memberships":[{"id":6,"project":{"id":5,"name":"Private child of eCookbook"},"roles":[{"id":1,"name":"Manager"},{"id":2,"name":"Developer","inherited":true}]}]}}`
	if got := string(j.Output()); got != wantJSON {
		t.Errorf("json:\n got %s\nwant %s", got, wantJSON)
	}
}

func TestArrayMetaAndValues(t *testing.T) {
	ts := time.Date(2006, 7, 19, 17, 12, 21, 0, time.UTC)
	build := func(b Builder) {
		b.Array("users", A("total_count", 1, "offset", 0, "limit", 25), func() {
			b.Object("user", func() {
				b.Value("id", 1)
				b.Value("admin", true)
				b.Value("created_on", ts)
				b.Value("passwd_changed_on", (*time.Time)(nil))
				b.Value("mail", "<a&b>")
				b.Array("custom_fields", nil, func() {
					b.ObjectAttrs("custom_field", A("id", 4, "name", "Phone"), func() { b.Value("value", nil) })
					b.ObjectAttrs("custom_field", A("id", 5, "name", "M", "multiple", true), func() {
						b.Array("value", nil, func() { b.Value("value", "a"); b.Value("value", "b") })
					})
					b.ObjectAttrs("custom_field", A("id", 6, "name", "E"), func() { b.Value("value", "") })
				})
			})
		})
	}
	j := New("json")
	build(j)
	wantJSON := `{"users":[{"id":1,"admin":true,"created_on":"2006-07-19T17:12:21Z","passwd_changed_on":null,"mail":"\u003ca\u0026b\u003e","custom_fields":[{"id":4,"name":"Phone","value":null},{"id":5,"name":"M","multiple":true,"value":["a","b"]},{"id":6,"name":"E","value":""}]}],"total_count":1,"offset":0,"limit":25}`
	if got := string(j.Output()); got != wantJSON {
		t.Errorf("json:\n got %s\nwant %s", got, wantJSON)
	}
	x := New("xml")
	build(x)
	wantXML := `<?xml version="1.0" encoding="UTF-8"?><users total_count="1" offset="0" limit="25" type="array"><user><id>1</id><admin>true</admin><created_on>2006-07-19T17:12:21Z</created_on><passwd_changed_on/><mail>&lt;a&amp;b&gt;</mail><custom_fields type="array"><custom_field id="4" name="Phone"><value/></custom_field><custom_field id="5" name="M" multiple="true"><value type="array"><value>a</value><value>b</value></value></custom_field><custom_field id="6" name="E"><value></value></custom_field></custom_fields></user></users>`
	if got := string(x.Output()); got != wantXML {
		t.Errorf("xml:\n got %s\nwant %s", got, wantXML)
	}
}

func TestJSONP(t *testing.T) {
	j := New("json", Options{Callback: "cb"})
	j.Value("a", 1.0)
	if got := string(j.Output()); got != `cb({"a":1.0})` {
		t.Errorf("got %s", got)
	}
	if j.ContentType() != "application/javascript; charset=utf-8" {
		t.Errorf("content type %s", j.ContentType())
	}
	if New("csv") != nil {
		t.Error("csv builder")
	}
}

func TestRubyFloat(t *testing.T) {
	for in, want := range map[float64]string{1: "1.0", 0.5: "0.5", 1e20: "1.0e+20", 0.00001: "1.0e-05", 123.25: "123.25"} {
		if got := rubyFloat(in); got != want {
			t.Errorf("%v: got %s want %s", in, got, want)
		}
	}
}
