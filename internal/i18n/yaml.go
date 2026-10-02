package i18n

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Symbol は YAML 中の Ruby シンボル（`:year` など）を表す。
// I18n では訳文の値がシンボルの場合、そのキーへのリンクとして解決される。
type Symbol string

// parseYAML は Ruby の Psych（YAML 1.1 寄り）と同じ型解決規則で YAML を読み込む。
// 戻り値の値は string / bool / int64 / float64 / nil / Symbol / []any / map[string]any のいずれか。
func parseYAML(data []byte) (map[string]any, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if doc.Kind == 0 || len(doc.Content) == 0 {
		return map[string]any{}, nil
	}
	v, err := convertNode(doc.Content[0])
	if err != nil {
		return nil, err
	}
	m, ok := v.(map[string]any)
	if !ok {
		if v == nil {
			return map[string]any{}, nil
		}
		return nil, fmt.Errorf("i18n: トップレベルがマッピングではありません")
	}
	return m, nil
}

func convertNode(n *yaml.Node) (any, error) {
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			return nil, nil
		}
		return convertNode(n.Content[0])
	case yaml.AliasNode:
		return convertNode(n.Alias)
	case yaml.SequenceNode:
		arr := make([]any, 0, len(n.Content))
		for _, c := range n.Content {
			v, err := convertNode(c)
			if err != nil {
				return nil, err
			}
			arr = append(arr, v)
		}
		return arr, nil
	case yaml.MappingNode:
		m := make(map[string]any, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			kn, vn := n.Content[i], n.Content[i+1]
			// マージキー（<<）
			if kn.Kind == yaml.ScalarNode && kn.Value == "<<" && kn.Style == 0 {
				src, err := convertNode(vn)
				if err != nil {
					return nil, err
				}
				var srcs []any
				if a, ok := src.([]any); ok {
					srcs = a
				} else {
					srcs = []any{src}
				}
				for _, s := range srcs {
					if sm, ok := s.(map[string]any); ok {
						for k, v := range sm {
							if _, exists := m[k]; !exists {
								m[k] = v
							}
						}
					}
				}
				continue
			}
			kv, err := convertNode(kn)
			if err != nil {
				return nil, err
			}
			v, err := convertNode(vn)
			if err != nil {
				return nil, err
			}
			m[keyString(kv)] = v
		}
		return m, nil
	case yaml.ScalarNode:
		return convertScalar(n), nil
	}
	return nil, nil
}

// keyString はマッピングキーを I18n のキー（シンボル）相当の文字列にする。
func keyString(v any) string {
	switch k := v.(type) {
	case string:
		return k
	case Symbol:
		return string(k)
	case nil:
		return ""
	default:
		return RubyToS(k)
	}
}

func convertScalar(n *yaml.Node) any {
	// クォート・ブロックスカラーは常に文字列
	if n.Style&(yaml.SingleQuotedStyle|yaml.DoubleQuotedStyle|yaml.LiteralStyle|yaml.FoldedStyle) != 0 {
		return n.Value
	}
	if n.Style&yaml.TaggedStyle != 0 {
		switch n.Tag {
		case "!!str", "tag:yaml.org,2002:str":
			return n.Value
		case "!!int":
			if i, err := strconv.ParseInt(strings.ReplaceAll(n.Value, "_", ""), 0, 64); err == nil {
				return i
			}
		case "!!float":
			if f, err := strconv.ParseFloat(strings.ReplaceAll(n.Value, "_", ""), 64); err == nil {
				return f
			}
		case "!!null":
			return nil
		case "!!bool":
			return psychTokenize(n.Value)
		}
	}
	return psychTokenize(n.Value)
}

var (
	reStringish   = regexp.MustCompile(`^[^\d.:-]?[\p{L}_\s!@#$%^&*(){}<>|/\\~;=]+`)
	reFloat       = regexp.MustCompile(`^(?:[-+]?([0-9][0-9_,]*)?\.[0-9]*([eE][-+][0-9]+)?|[-+]?\.(inf|Inf|INF)|\.(nan|NaN|NAN))$`)
	reIntStrict   = regexp.MustCompile(`^(?:[-+]?0b[0-1_]+|[-+]?0[0-7_]+|[-+]?(0|[1-9][0-9_]*)|[-+]?0x[0-9a-fA-F_]+)$`)
	reIntLegacy   = regexp.MustCompile(`^(?:[-+]?0b[0-1_,]+|[-+]?0[0-7_,]+|[-+]?(?:0|[1-9](?:[0-9]|,[0-9]|_[0-9])*)|[-+]?0x[0-9a-fA-F_,]+)$`)
	reSymQuoted   = regexp.MustCompile(`^:(["'])(.*)(["'])`)
	reNull        = regexp.MustCompile(`(?i)^null$`)
	reTrue        = regexp.MustCompile(`(?i)^(yes|true|on)$`)
	reFalse       = regexp.MustCompile(`(?i)^(no|false|off)$`)
	reNotYTONF    = regexp.MustCompile(`(?i)^[^ytonf~]`)
	reDotOnly     = regexp.MustCompile(`^[-+]?\.$`)
	reSexagesimal = regexp.MustCompile(`^[-+]?[0-9][0-9_]*(:[0-5]?[0-9]){1,2}$`)
)

// psychTokenize は Psych::ScalarScanner#tokenize の移植（日時型は文字列のまま扱う）。
func psychTokenize(s string) any {
	if s == "" {
		return nil
	}
	if reStringish.MatchString(s) || strings.Contains(s, "\n") {
		if len([]rune(s)) > 5 {
			return s
		}
		switch {
		case reNotYTONF.MatchString(s):
			return s
		case s == "~" || reNull.MatchString(s):
			return nil
		case reTrue.MatchString(s):
			return true
		case reFalse.MatchString(s):
			return false
		default:
			return s
		}
	}
	ls := strings.ToLower(s)
	switch {
	case ls == ".inf" || ls == "+.inf":
		return math.Inf(1)
	case ls == "-.inf":
		return math.Inf(-1)
	case ls == ".nan":
		return math.NaN()
	}
	if strings.HasPrefix(s, ":") && len(s) > 1 {
		if m := reSymQuoted.FindStringSubmatch(s); m != nil && m[1] == m[3] {
			return Symbol(strings.TrimPrefix(m[2], ":"))
		}
		return Symbol(s[1:])
	}
	if reSexagesimal.MatchString(s) {
		return s // 60進数表記はロケールでは使われないため文字列扱い
	}
	if reFloat.MatchString(s) {
		if reDotOnly.MatchString(s) {
			return s
		}
		t := strings.NewReplacer(",", "", "_", "").Replace(s)
		if f, err := strconv.ParseFloat(t, 64); err == nil {
			return f
		}
		return s
	}
	if reIntStrict.MatchString(s) || reIntLegacy.MatchString(s) {
		t := strings.NewReplacer(",", "", "_", "").Replace(s)
		neg := false
		if strings.HasPrefix(t, "-") {
			neg, t = true, t[1:]
		} else {
			t = strings.TrimPrefix(t, "+")
		}
		base := 10
		switch {
		case strings.HasPrefix(t, "0b"):
			base, t = 2, t[2:]
		case strings.HasPrefix(t, "0x"):
			base, t = 16, t[2:]
		case len(t) > 1 && t[0] == '0':
			base, t = 8, t[1:]
		}
		if i, err := strconv.ParseInt(t, base, 64); err == nil {
			if neg {
				i = -i
			}
			return i
		}
		return s
	}
	return s
}
