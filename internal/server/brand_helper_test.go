package server_test

import (
	"io"
	"net/http"
	"testing"

	"github.com/mikuta0407/buropher/internal/brand/brandtest"
)

// readUnbranded はレスポンス本文を読み、buropher の製品名表記（Buropher）を Redmine の表記に戻す
// （参照 Redmine の出力から作ったゴールデンと比べるため。brandtest.Unbrand）。
func readUnbranded(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(r)
	return brandtest.UnbrandBytes(b), err
}

// getRaw は get と同じだが、製品名表記を戻さずにそのままの本文を返す（buropher 独自の表示を確かめる用）。
func getRaw(t *testing.T, c *http.Client, u string) (*http.Response, string) {
	t.Helper()
	res, err := c.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res, string(b)
}
