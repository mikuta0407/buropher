package report

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// AllowEntry は許容差分 1 件。
type AllowEntry struct {
	// Case は "<scenario>/<case id>" に対する glob（path.Match 形式、例: "smoke/admin__*"）。
	Case string `yaml:"case"`
	// Reason は許容理由（レビュー用、必須）。
	Reason string `yaml:"reason"`
	// Ignore が true ならケース全体の差分を許容する。
	Ignore bool `yaml:"ignore"`
	// Lines は差分行（+/- を除いた内容）に対する正規表現。すべての差分行がいずれかに一致すれば許容。
	Lines []string `yaml:"lines"`

	lineRes []*regexp.Regexp
}

// Allowlist は testdata/compat/allowlist.yml の内容。
type Allowlist struct {
	Entries []*AllowEntry `yaml:"entries"`
}

// LoadAllowlist は許容リストを読み込む。ファイルが無ければ空リストを返す。
func LoadAllowlist(p string) (*Allowlist, error) {
	al := &Allowlist{}
	if p == "" {
		return al, nil
	}
	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return al, nil
	}
	if err != nil {
		return nil, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(al); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	for i, e := range al.Entries {
		if e.Case == "" {
			return nil, fmt.Errorf("%s: entries[%d]: case is required", p, i)
		}
		if e.Reason == "" {
			return nil, fmt.Errorf("%s: entries[%d]: reason is required", p, i)
		}
		if _, err := path.Match(e.Case, ""); err != nil {
			return nil, fmt.Errorf("%s: entries[%d]: bad glob: %w", p, i, err)
		}
		for _, l := range e.Lines {
			re, err := regexp.Compile(l)
			if err != nil {
				return nil, fmt.Errorf("%s: entries[%d]: %w", p, i, err)
			}
			e.lineRes = append(e.lineRes, re)
		}
	}
	return al, nil
}

// Allowed は差分が許容されるかを判定し、許容した場合は理由を返す。
func (al *Allowlist) Allowed(scenarioName, caseID, diff string) (string, bool) {
	key := scenarioName + "/" + caseID
	var changed []string
	for _, l := range strings.Split(diff, "\n") {
		if strings.HasPrefix(l, "+++ ") || strings.HasPrefix(l, "--- ") {
			continue
		}
		if strings.HasPrefix(l, "+") || strings.HasPrefix(l, "-") {
			changed = append(changed, l[1:])
		}
	}
	for _, e := range al.Entries {
		if ok, _ := path.Match(e.Case, key); !ok {
			continue
		}
		if e.Ignore {
			return e.Reason, true
		}
		if len(e.lineRes) == 0 {
			continue
		}
		all := true
		for _, l := range changed {
			hit := false
			for _, re := range e.lineRes {
				if re.MatchString(l) {
					hit = true
					break
				}
			}
			if !hit {
				all = false
				break
			}
		}
		if all {
			return e.Reason, true
		}
	}
	return "", false
}
