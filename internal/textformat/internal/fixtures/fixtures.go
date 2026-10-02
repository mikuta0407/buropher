// Package fixtures は Redmine 本体で生成した差分テスト用データ
// （internal/textformat/commonmark/testdata/fixtures.json）を読み込む。
package fixtures

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
)

// Entry はコーパスの 1 件。
type Entry struct {
	Name        string          `json:"name"`
	Mode        string          `json:"mode"`
	Input       string          `json:"input"`
	Hardbreaks  *bool           `json:"hardbreaks"`
	Lang        string          `json:"lang"`
	Filename    string          `json:"filename"`
	Index       int             `json:"index"`
	Replacement string          `json:"replacement"`
	Hash        string          `json:"hash"`
	Expected    json.RawMessage `json:"expected"`
}

// ExpectedString は expected を文字列として返す。
func (e Entry) ExpectedString() string {
	var s string
	_ = json.Unmarshal(e.Expected, &s)
	return s
}

// File は fixtures.json 全体。
type File struct {
	IconsPath string  `json:"icons_path"`
	Entries   []Entry `json:"entries"`
}

// Load は fixtures.json を読み込む。
func Load() (*File, error) {
	_, self, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(self), "..", "..", "commonmark", "testdata", "fixtures.json")
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f File
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, err
	}
	for i := range f.Entries {
		if f.Entries[i].Mode == "" {
			f.Entries[i].Mode = "commonmark"
		}
	}
	return &f, nil
}

// ByMode は指定モードのエントリを返す。
func (f *File) ByMode(mode string) []Entry {
	var out []Entry
	for _, e := range f.Entries {
		if e.Mode == mode {
			out = append(out, e)
		}
	}
	return out
}
