package normalize

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

// orderedObject はキー順を保持した JSON オブジェクト。
type orderedObject struct {
	keys []string
	vals map[string]any
}

// JSON は JSON をキー順を保ったまま（SortKeys ならソートして）2 スペースインデントで整形する。
func (n *Normalizer) JSON(body []byte) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	v, err := decodeValue(dec)
	if err != nil {
		return "", err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("json: trailing data")
	}
	var b strings.Builder
	n.writeJSON(&b, v, "", 0)
	b.WriteByte('\n')
	return b.String(), nil
}

func decodeValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			obj := &orderedObject{vals: map[string]any{}}
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				k, ok := kt.(string)
				if !ok {
					return nil, fmt.Errorf("json: invalid key %v", kt)
				}
				v, err := decodeValue(dec)
				if err != nil {
					return nil, err
				}
				if _, dup := obj.vals[k]; !dup {
					obj.keys = append(obj.keys, k)
				}
				obj.vals[k] = v
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return obj, nil
		case '[':
			arr := []any{}
			for dec.More() {
				v, err := decodeValue(dec)
				if err != nil {
					return nil, err
				}
				arr = append(arr, v)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return arr, nil
		}
		return nil, fmt.Errorf("json: unexpected delimiter %v", t)
	default:
		return tok, nil
	}
}

func jsonString(s string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimSuffix(buf.String(), "\n")
}

func (n *Normalizer) writeJSON(b *strings.Builder, v any, key string, depth int) {
	if key != "" && n.maskKeys[key] && v != nil {
		b.WriteString(jsonString(PlaceholderMasked))
		return
	}
	switch t := v.(type) {
	case *orderedObject:
		if len(t.keys) == 0 {
			b.WriteString("{}")
			return
		}
		keys := t.keys
		if val(n.cfg.SortKeys) {
			keys = append([]string(nil), keys...)
			sort.Strings(keys)
		}
		b.WriteString("{\n")
		for i, k := range keys {
			indent(b, depth+1)
			b.WriteString(jsonString(k))
			b.WriteString(": ")
			n.writeJSON(b, t.vals[k], k, depth+1)
			if i < len(keys)-1 {
				b.WriteByte(',')
			}
			b.WriteByte('\n')
		}
		indent(b, depth)
		b.WriteByte('}')
	case []any:
		if len(t) == 0 {
			b.WriteString("[]")
			return
		}
		b.WriteString("[\n")
		for i, e := range t {
			indent(b, depth+1)
			n.writeJSON(b, e, "", depth+1)
			if i < len(t)-1 {
				b.WriteByte(',')
			}
			b.WriteByte('\n')
		}
		indent(b, depth)
		b.WriteByte(']')
	case string:
		b.WriteString(jsonString(n.String(t)))
	case json.Number:
		b.WriteString(t.String())
	case bool:
		fmt.Fprintf(b, "%t", t)
	case nil:
		b.WriteString("null")
	default:
		fmt.Fprintf(b, "%v", t)
	}
}
