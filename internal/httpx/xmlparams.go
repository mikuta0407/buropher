package httpx

import (
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"io"
	"strconv"
	"strings"
)

// XML ボディは actionpack-xml_parser（Hash.from_xml / ActiveSupport::XmlMini REXML バックエンド）
// 互換で Params に変換する。Redmine の REST API は XML でのリクエストを受け付けるため必要。

// xmlNode は XmlMini の中間表現（属性・子要素・__content__ を持つハッシュ）。
type xmlNode struct {
	keys []string
	vals map[string]any // string（属性値 / __content__）、*xmlNode、[]any（重複要素）
}

const xmlContentKey = "__content__"

func newXMLNode() *xmlNode { return &xmlNode{vals: map[string]any{}} }

// merge は XmlMini の merge!（既存キーなら配列化）。
func (n *xmlNode) merge(k string, v any) {
	if cur, ok := n.vals[k]; ok {
		if a, isArr := cur.([]any); isArr {
			n.vals[k] = append(a, v)
		} else {
			n.vals[k] = []any{cur, v}
		}
		return
	}
	n.keys = append(n.keys, k)
	n.vals[k] = v
}

func (n *xmlNode) get(k string) (any, bool) { v, ok := n.vals[k]; return v, ok }

func (n *xmlNode) str(k string) (string, bool) {
	v, ok := n.vals[k]
	if !ok {
		return "", false
	}
	s, isStr := v.(string)
	return s, isStr
}

// element は解析中の要素。
type xmlElem struct {
	node        *xmlNode
	texts       strings.Builder
	hasText     bool
	hasElements bool
}

var errXMLParse = &ParamError{Kind: ParamParse, Msg: "Error occurred while parsing request parameters"}

// parseXMLParams は Hash.from_xml(raw) || {} 相当。
func parseXMLParams(raw []byte) (*Params, error) {
	p, err := HashFromXML(raw)
	if err != nil {
		var pe *ParamError
		if errors.As(err, &pe) {
			return nil, pe
		}
		return nil, errXMLParse
	}
	return p, nil
}

// HashFromXML は ActiveSupport の Hash.from_xml の移植。
// 型属性（integer / boolean / float / decimal / array / string / base64Binary 等）を解釈する。
// yaml / symbol 型は DisallowedType としてエラーにする。
func HashFromXML(raw []byte) (*Params, error) {
	dec := xml.NewDecoder(bytes.NewReader(raw))
	dec.Strict = true
	var stack []*xmlElem
	var rootName string
	var root *xmlNode
	depth := 0
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if depth > 100 {
				return nil, errors.New("xml nesting too deep")
			}
			e := &xmlElem{node: newXMLNode()}
			for _, a := range t.Attr {
				e.node.merge(xmlQName(a.Name), a.Value)
			}
			if len(stack) > 0 {
				stack[len(stack)-1].hasElements = true
			} else if root != nil {
				return nil, errors.New("multiple root elements")
			}
			stack = append(stack, e)
		case xml.EndElement:
			depth--
			e := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			// XmlMini_REXML#collapse
			if e.hasElements {
				if strings.TrimSpace(e.texts.String()) != "" {
					e.node.merge(xmlContentKey, e.texts.String())
				}
			} else if e.hasText {
				e.node.merge(xmlContentKey, e.texts.String())
			}
			name := xmlQName(t.Name)
			if len(stack) == 0 {
				rootName, root = name, e.node
			} else {
				stack[len(stack)-1].node.merge(name, e.node)
			}
		case xml.CharData:
			if len(stack) > 0 {
				e := stack[len(stack)-1]
				e.texts.Write(t)
				e.hasText = true
			}
		}
	}
	if root == nil {
		return nil, errors.New("no root element")
	}
	v, err := xmlDeepToH(root)
	if err != nil {
		return nil, err
	}
	out := NewParams()
	out.Set(rootName, v)
	return out, nil
}

func xmlQName(n xml.Name) string {
	// encoding/xml は名前空間 URI を Space に入れる。Rails はプレフィクス付きの生の名前を使うが、
	// API で名前空間は使われないためローカル名のみとする。
	return n.Local
}

var errDisallowedType = &ParamError{Kind: ParamParse, Msg: "Disallowed type attribute"}

func xmlDeepToH(v any) (any, error) {
	switch x := v.(type) {
	case *xmlNode:
		return xmlProcessHash(x)
	case []any:
		out := make([]any, 0, len(x))
		for _, e := range x {
			r, err := xmlDeepToH(e)
			if err != nil {
				return nil, err
			}
			out = append(out, r)
		}
		return out, nil
	default:
		return v, nil
	}
}

func xmlProcessHash(n *xmlNode) (any, error) {
	typ, hasType := n.str("type")
	if hasType && (typ == "yaml" || typ == "symbol") {
		return nil, errDisallowedType
	}
	content, hasContent := n.str(xmlContentKey)

	// become_array?
	if typ == "array" {
		var entries any
		found := false
		for _, k := range n.keys {
			if _, isStr := n.vals[k].(string); !isStr {
				entries, found = n.vals[k], true
				break
			}
		}
		if !found || (hasContent && content == "") {
			return []any{}, nil
		}
		switch e := entries.(type) {
		case []any:
			return xmlDeepToH(e)
		case *xmlNode:
			r, err := xmlDeepToH(e)
			if err != nil {
				return nil, err
			}
			return []any{r}, nil
		}
		return nil, errXMLParse
	}

	// become_content?
	if typ == "file" || (hasContent && (len(n.keys) == 1 || strings.TrimSpace(content) != "")) {
		return xmlProcessContent(typ, content, n)
	}

	// become_empty_string?
	nilAttr, _ := n.str("nil")
	if typ == "string" && nilAttr != "true" {
		return "", nil
	}

	// become_hash?（nothing? でも garbage? でもない）
	nothing := len(n.keys) == 0 || nilAttr == "true"
	garbage := hasType && len(n.keys) == 1
	if !nothing && !garbage {
		out := NewParams()
		for _, k := range n.keys {
			r, err := xmlDeepToH(n.vals[k])
			if err != nil {
				return nil, err
			}
			out.Set(k, r)
		}
		return out, nil
	}
	return nil, nil
}

// xmlProcessContent は ActiveSupport::XmlMini::PARSING の型変換。
// date / dateTime は Redmine 側で文字列として扱うため文字列のまま返す。
func xmlProcessContent(typ, content string, n *xmlNode) (any, error) {
	switch typ {
	case "integer":
		return RubyToI(content), nil
	case "boolean":
		s := strings.TrimSpace(content)
		return s == "1" || s == "true", nil
	case "float", "double", "decimal":
		return rubyToF(content), nil
	case "base64Binary":
		b, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(content), ""))
		if err != nil {
			return nil, errXMLParse
		}
		return string(b), nil
	case "binary":
		if enc, _ := n.str("encoding"); enc == "base64" {
			b, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(content), ""))
			if err != nil {
				return nil, errXMLParse
			}
			return string(b), nil
		}
		return content, nil
	case "file":
		// Rails は StringIO を返す。ここでは UploadedFile に変換する。
		data := []byte(content)
		if b, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(content), "")); err == nil {
			data = b
		}
		name, _ := n.str("name")
		if name == "" {
			name = "untitled"
		}
		ct, _ := n.str("content_type")
		if ct == "" {
			ct = "application/octet-stream"
		}
		return &UploadedFile{Filename: name, ContentType: ct, Size: int64(len(data)), data: data}, nil
	default:
		return content, nil
	}
}

// rubyToF は Ruby の String#to_f 相当（先頭の数値部分のみ）。
func rubyToF(s string) float64 {
	s = strings.TrimLeft(s, " \t\n\r\f\v")
	end := 0
	seenDigit, seenDot, seenExp := false, false, false
	for end < len(s) {
		c := s[end]
		switch {
		case c >= '0' && c <= '9':
			seenDigit = true
		case (c == '+' || c == '-') && (end == 0 || s[end-1] == 'e' || s[end-1] == 'E'):
		case c == '.' && !seenDot && !seenExp:
			seenDot = true
		case (c == 'e' || c == 'E') && seenDigit && !seenExp:
			seenExp = true
		default:
			goto done
		}
		end++
	}
done:
	for end > 0 {
		if f, err := strconv.ParseFloat(s[:end], 64); err == nil {
			return f
		}
		end--
	}
	return 0
}
