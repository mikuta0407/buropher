// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package httpx

import (
	"fmt"
	"regexp"
	"strings"
)

// Rack::QueryParser の既定上限（rack 3.2）。
const (
	DefaultParamDepthLimit = 32      // Rack::Utils.param_depth_limit
	DefaultBytesizeLimit   = 4194304 // RACK_QUERY_PARSER_BYTESIZE_LIMIT
	DefaultParamsLimit     = 4096    // RACK_QUERY_PARSER_PARAMS_LIMIT
)

// ParamErrorKind はパラメータ解析エラーの種類。
type ParamErrorKind int

const (
	// ParamTypeError は構造の衝突（Rack::QueryParser::ParameterTypeError）。
	ParamTypeError ParamErrorKind = iota
	// ParamInvalid は不正な %-エンコーディングや UTF-8（InvalidParameterError）。
	ParamInvalid
	// ParamLimit は上限超過（QueryLimitError / ParamsTooDeepError）。
	ParamLimit
	// ParamParse は JSON/XML ボディの解析失敗（ActionDispatch::Http::Parameters::ParseError）。
	ParamParse
	// ParamTooLarge は multipart のパート数・ボディサイズ超過（Rails では 413）。
	ParamTooLarge
)

// ParamError はパラメータ解析エラー。Rails はこれらを 400 Bad Request（ParamTooLarge は 413）にする。
type ParamError struct {
	Kind ParamErrorKind
	Msg  string
}

func (e *ParamError) Error() string { return e.Msg }

// Status は対応する HTTP ステータスを返す。
func (e *ParamError) Status() int {
	if e.Kind == ParamTooLarge {
		return 413
	}
	return 400
}

// QueryParser は Rack::QueryParser の移植。
type QueryParser struct {
	DepthLimit    int
	BytesizeLimit int
	ParamsLimit   int
}

// DefaultQueryParser は Rack の既定値を持つパーサ。
var DefaultQueryParser = &QueryParser{
	DepthLimit:    DefaultParamDepthLimit,
	BytesizeLimit: DefaultBytesizeLimit,
	ParamsLimit:   DefaultParamsLimit,
}

// ParseNestedQuery は Rack::Utils.parse_nested_query 相当（Rails の deep_munge 前の生の結果）。
// 配列内の nil は残る。Rails の params と同じ結果が欲しい場合は ParseRailsQuery を使う。
func ParseNestedQuery(qs string) (*Params, error) {
	return DefaultQueryParser.ParseNestedQuery(qs)
}

// ParseRailsQuery は Rails の query_parameters 相当: Rack の解析結果に対し
// UTF-8 検証（check_param_encoding）と deep_munge（配列から nil を除去）を行う。
func ParseRailsQuery(qs string) (*Params, error) {
	p, err := DefaultQueryParser.ParseNestedQuery(qs)
	if err != nil {
		return nil, err
	}
	if err := CheckParamEncoding(p); err != nil {
		return nil, err
	}
	DeepMunge(p)
	return p, nil
}

// ParseNestedQuery は Rack::QueryParser#parse_nested_query の移植。
func (qp *QueryParser) ParseNestedQuery(qs string) (*Params, error) {
	params := NewParams()
	err := qp.eachQueryPair(qs, func(k string, v any) error {
		_, err := qp.normalize(params, &k, v, 0)
		return err
	})
	if err != nil {
		return nil, err
	}
	return params, nil
}

// Normalize は Rack::QueryParser#normalize_params 相当。multipart の各パートの取り込みにも使う。
func (qp *QueryParser) Normalize(params *Params, name string, v any) error {
	_, err := qp.normalize(params, &name, v, 0)
	return err
}

var querySepRe = regexp.MustCompile(`& *`)

// eachQueryPair は Rack の each_query_pair の移植（区切りは /& */）。
func (qp *QueryParser) eachQueryPair(qs string, fn func(k string, v any) error) error {
	if qs == "" {
		return nil
	}
	if qp.BytesizeLimit > 0 && len(qs) > qp.BytesizeLimit {
		return &ParamError{Kind: ParamLimit, Msg: fmt.Sprintf("total query size exceeds limit (%d)", qp.BytesizeLimit)}
	}
	n := -1
	if qp.ParamsLimit > 0 {
		n = qp.ParamsLimit + 1
	}
	pairs := querySepRe.Split(qs, n)
	if qp.ParamsLimit > 0 && len(pairs) > qp.ParamsLimit {
		count := len(pairs) + strings.Count(pairs[len(pairs)-1], "&")
		return &ParamError{Kind: ParamLimit, Msg: fmt.Sprintf("total number of query parameters (%d) exceeds limit (%d)", count, qp.ParamsLimit)}
	}
	for _, p := range pairs {
		if p == "" {
			continue
		}
		var k string
		var v any
		if i := strings.IndexByte(p, '='); i >= 0 {
			ks, err := DecodeWWWFormComponent(p[:i])
			if err != nil {
				return err
			}
			vs, err := DecodeWWWFormComponent(p[i+1:])
			if err != nil {
				return err
			}
			k, v = ks, vs
		} else {
			ks, err := DecodeWWWFormComponent(p)
			if err != nil {
				return err
			}
			k, v = ks, nil
		}
		if err := fn(k, v); err != nil {
			return err
		}
	}
	return nil
}

// DecodeWWWFormComponent は Ruby の URI.decode_www_form_component 相当。
// "+" は空白、%XX はバイトにデコード。%XX 形式でない "%" はエラー。
func DecodeWWWFormComponent(s string) (string, error) {
	if !strings.ContainsAny(s, "%+") {
		return s, nil
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '+':
			b.WriteByte(' ')
		case '%':
			if i+2 >= len(s) {
				return "", &ParamError{Kind: ParamInvalid, Msg: fmt.Sprintf("invalid %%-encoding (%s)", s)}
			}
			h, ok1 := unhex(s[i+1])
			l, ok2 := unhex(s[i+2])
			if !ok1 || !ok2 {
				return "", &ParamError{Kind: ParamInvalid, Msg: fmt.Sprintf("invalid %%-encoding (%s)", s)}
			}
			b.WriteByte(h<<4 | l)
			i += 2
		default:
			b.WriteByte(c)
		}
	}
	return b.String(), nil
}

func unhex(c byte) (byte, bool) {
	switch {
	case '0' <= c && c <= '9':
		return c - '0', true
	case 'a' <= c && c <= 'f':
		return c - 'a' + 10, true
	case 'A' <= c && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// rubyClassName はエラーメッセージ用に Ruby のクラス名を返す。
func rubyClassName(v any) string {
	switch v.(type) {
	case nil:
		return "NilClass"
	case string:
		return "String"
	case []any:
		return "Array"
	case *Params:
		return "Rack::QueryParser::Params"
	case *UploadedFile:
		return "Hash"
	case int64:
		return "Integer"
	case float64:
		return "Float"
	case bool:
		return "TrueClass"
	}
	return "Object"
}

// normalize は Rack::QueryParser#_normalize_params の忠実な移植。
// 戻り値は Ruby の戻り値（*Params / []any / nil）。name==nil は Ruby の nil 名を表す。
func (qp *QueryParser) normalize(params *Params, name *string, v any, depth int) (any, error) {
	if depth >= qp.DepthLimit {
		return nil, &ParamError{Kind: ParamLimit, Msg: "exceeded available parameter key space"}
	}

	var k, after string
	switch {
	case name == nil:
		k, after = "", ""
	case depth == 0:
		n := *name
		// 先頭の [ は特別扱いしない
		if start := indexFrom(n, '[', 1); start >= 0 {
			k = n[:start]
			after = n[start:]
		} else {
			k = n
			after = ""
		}
	case strings.HasPrefix(*name, "[]"):
		k = "[]"
		after = (*name)[2:]
	case strings.HasPrefix(*name, "[") && indexFrom(*name, ']', 1) >= 0:
		n := *name
		start := indexFrom(n, ']', 1)
		k = n[1:start]
		after = n[start+1:]
	default:
		k = *name
		after = ""
	}

	if k == "" {
		return nil, nil
	}

	switch {
	case after == "":
		if k == "[]" && depth != 0 {
			return []any{v}, nil
		}
		params.Set(k, v)
	case after == "[":
		params.Set(*name, v)
	case after == "[]":
		cur := rubyOrEmpty(params, k, func() any { return []any{} })
		arr, ok := cur.([]any)
		if !ok {
			return nil, &ParamError{Kind: ParamTypeError, Msg: fmt.Sprintf("expected Array (got %s) for param `%s'", rubyClassName(cur), k)}
		}
		params.Set(k, append(arr, v))
	case strings.HasPrefix(after, "[]"):
		// x[][y]（配列中のハッシュ）の認識
		var childKey string
		ok := false
		if len(after) >= 3 && after[2] == '[' && strings.HasSuffix(after, "]") && len(after)-4 >= 0 {
			childKey = after[3 : 3+len(after)-4]
			ok = childKey != "" && !strings.Contains(childKey, "[") && !strings.Contains(childKey, "]")
		}
		if !ok {
			// その他のネスト配列
			childKey = after[2:]
		}
		cur := rubyOrEmpty(params, k, func() any { return []any{} })
		arr, isArr := cur.([]any)
		if !isArr {
			return nil, &ParamError{Kind: ParamTypeError, Msg: fmt.Sprintf("expected Array (got %s) for param `%s'", rubyClassName(cur), k)}
		}
		var last any
		if len(arr) > 0 {
			last = arr[len(arr)-1]
		}
		if lp, isHash := last.(*Params); isHash && !paramsHashHasKey(lp, childKey) {
			if _, err := qp.normalize(lp, &childKey, v, depth+1); err != nil {
				return nil, err
			}
		} else {
			r, err := qp.normalize(NewParams(), &childKey, v, depth+1)
			if err != nil {
				return nil, err
			}
			arr = append(arr, r)
		}
		params.Set(k, arr)
	default:
		cur := rubyOrEmpty(params, k, func() any { return NewParams() })
		h, ok := cur.(*Params)
		if !ok {
			return nil, &ParamError{Kind: ParamTypeError, Msg: fmt.Sprintf("expected Hash (got %s) for param `%s'", rubyClassName(cur), k)}
		}
		r, err := qp.normalize(h, &after, v, depth+1)
		if err != nil {
			return nil, err
		}
		params.Set(k, r)
	}
	return params, nil
}

// rubyOrEmpty は Ruby の `params[k] ||= default` を再現する（nil は未設定扱い）。
func rubyOrEmpty(params *Params, k string, def func() any) any {
	if cur, ok := params.Get(k); ok && cur != nil {
		if b, isBool := cur.(bool); !isBool || b {
			return cur
		}
	}
	d := def()
	params.Set(k, d)
	return d
}

func indexFrom(s string, c byte, from int) int {
	if from >= len(s) {
		return -1
	}
	i := strings.IndexByte(s[from:], c)
	if i < 0 {
		return -1
	}
	return i + from
}

var bracketSplitRe = regexp.MustCompile(`[\[\]]+`)

// paramsHashHasKey は Rack の params_hash_has_key? の移植。
func paramsHashHasKey(h *Params, key string) bool {
	if strings.Contains(key, "[]") {
		return false
	}
	parts := rubySplit(bracketSplitRe, key)
	var cur any = h
	for _, part := range parts {
		if part == "" {
			continue
		}
		cp, ok := cur.(*Params)
		if !ok {
			return false
		}
		v, ok := cp.Get(part)
		if !ok {
			return false
		}
		cur = v
	}
	return true
}

// rubySplit は Ruby の String#split(regexp) 相当（末尾の空要素を除去）。
func rubySplit(re *regexp.Regexp, s string) []string {
	parts := re.Split(s, -1)
	for len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}
