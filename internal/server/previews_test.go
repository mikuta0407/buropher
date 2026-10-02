package server_test

import (
	"testing"
)

// TestPreviews は previews#text / issue / news（common/_preview）の出力が参照 Redmine と一致することを確認する。
// 期待値は参照 Redmine に匿名で GET したもの（curl で取得）。
func TestPreviews(t *testing.T) {
	ts, _ := newFixtureServer(t)
	cases := []struct {
		golden, path string
	}{
		{"preview_text.html", "/preview/text?text=%231+%5B%5BWiki%5D%5D+%7B%7Bthumbnail(logo.gif)%7D%7D+%40jsmith%0A%0A%23+Head"},
		{"preview_empty.html", "/preview/text"},
		{"preview_issue.html", "/issues/preview?project_id=ecookbook&issue_id=3&text=attachment%3Aerror281.txt+%7B%7Bhello_world%7D%7D+%23note-1"},
		{"preview_news.html", "/news/preview?project_id=ecookbook&id=1&text=%7B%7Bhello_world%7D%7D+%5B%5BWiki%5D%5D"},
	}
	for _, c := range cases {
		t.Run(c.golden, func(t *testing.T) {
			res, body := get(t, newClient(t), ts.URL+c.path)
			if res.StatusCode != 200 {
				t.Fatalf("status %d", res.StatusCode)
			}
			if ct := res.Header.Get("Content-Type"); ct != "text/html; charset=utf-8" {
				t.Errorf("content-type %q", ct)
			}
			compareGolden(t, c.golden, body)
		})
	}
	t.Run("unknown project", func(t *testing.T) {
		res, _ := get(t, newClient(t), ts.URL+"/issues/preview?project_id=nope&text=x")
		if res.StatusCode != 404 {
			t.Errorf("status %d", res.StatusCode)
		}
	})
}
