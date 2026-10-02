package httpx

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// testdata/rack_parse_nested_query.jsonl は rack 3.2.7 の Rack::Utils.parse_nested_query に
// ランダムな文字列を与えて得た結果（{"q":..., "r":... | "e": 例外クラス名}）。差分テストに使う。
func TestParseNestedQueryDifferential(t *testing.T) {
	data, err := os.ReadFile("testdata/rack_parse_nested_query.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var rec struct {
			Q string          `json:"q"`
			R json.RawMessage `json:"r"`
			E string          `json:"e"`
		}
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatal(err)
		}
		got, err := ParseNestedQuery(rec.Q)
		if rec.E != "" {
			if err == nil {
				t.Errorf("%q: expected %s, got %s", rec.Q, rec.E, mustJSON(t, got))
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: unexpected error %v", rec.Q, err)
			continue
		}
		want, err := DecodeJSON(rec.R)
		if err != nil {
			t.Fatal(err)
		}
		if g, w := mustJSON(t, got), mustJSON(t, want); g != w {
			t.Errorf("%q:\n got  %s\n want %s", rec.Q, g, w)
		}
	}
}
