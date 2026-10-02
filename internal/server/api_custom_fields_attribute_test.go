package server_test

// Redmine の test/integration/api_test/custom_fields_attribute_test.rb の移植。

import (
	"net/http"
	"slices"
	"testing"
)

func TestAPICustomFieldsAttribute(t *testing.T) {
	cases := []struct {
		name, format string
		multiple     bool
		possible     []string
		def          *string
		value        string // JSON の値
		groupName    string
		want         []string
	}{
		{"test_integer_custom_fields_should_accept_strings", "int", false, nil, nil, `"52"`, "Foo", []string{"52"}},
		{"test_integer_custom_fields_should_accept_integers", "int", false, nil, nil, `52`, "Foo", []string{"52"}},
		{"test_boolean_custom_fields_should_accept_strings", "bool", false, nil, nil, ` "1"`, "Foo", []string{"1"}},
		{"test_boolean_custom_fields_should_accept_integers", "bool", false, nil, nil, ` 1`, "Foo", []string{"1"}},
		{"test_multivalued_custom_fields_should_accept_an_array", "list", true, []string{"V1", "V2", "V3"}, new("V2"), `["V1","V3"]`, "Foooo", []string{"V1", "V3"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts, d := newFixtureServer(t)
			cf := issuesAPICustomField(t, d, "group", "Custom field 1", tc.format, tc.multiple, tc.possible, tc.def, nil)
			body := `{"group":{"name":"` + tc.groupName + `","custom_field_values":{"` + itoaTest(cf) + `":` + tc.value + `}}}`
			res := apiCall(t, ts, http.MethodPost, "/groups.json", "application/json", body, apiCreds("admin"))
			res.expectStatus(t, http.StatusCreated)
			gid := issuesAPIInt(t, d, `SELECT MAX(id) FROM principals WHERE kind = 'group'`)
			got := issuesAPICFValues(t, d, "principal", gid, cf)
			slices.Sort(got)
			if !slices.Equal(got, tc.want) {
				t.Errorf("values %v, want %v", got, tc.want)
			}
		})
	}
}
