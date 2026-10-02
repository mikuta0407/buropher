package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
)

// UploadedFile は multipart でアップロードされたファイル（ActionDispatch::Http::UploadedFile 相当）。
// 小さいファイルはメモリ、大きいファイルは一時ファイルに保持する。
// ParamsMiddleware を使う場合、一時ファイルはリクエスト終了時に削除される。
// 永続化したい場合はハンドラ内で Open して別の場所へコピーすること。
type UploadedFile struct {
	// Filename はパス部分を除いた元のファイル名（original_filename）。
	Filename string
	// ContentType はパートの Content-Type（なければ空）。
	ContentType string
	// Header はパートのヘッダ。
	Header textproto.MIMEHeader
	// Size はバイト数。
	Size int64

	data []byte
	path string
}

// Open はファイル内容を読み出すリーダを返す。
func (f *UploadedFile) Open() (io.ReadSeekCloser, error) {
	if f.path != "" {
		return os.Open(f.path)
	}
	return nopSeekCloser{bytes.NewReader(f.data)}, nil
}

// TempPath は一時ファイルのパスを返す（メモリ保持なら空）。
func (f *UploadedFile) TempPath() string { return f.path }

// Remove は一時ファイルを削除する。
func (f *UploadedFile) Remove() error {
	if f.path == "" {
		return nil
	}
	err := os.Remove(f.path)
	f.path = ""
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// MarshalJSON はデバッグ用の表現を返す。
func (f *UploadedFile) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{"filename": f.Filename, "content_type": f.ContentType, "size": f.Size})
}

type nopSeekCloser struct{ *bytes.Reader }

func (nopSeekCloser) Close() error { return nil }

// ParseOptions はリクエストボディ解析の設定。ゼロ値は既定値を意味する。
type ParseOptions struct {
	// Query はクエリ文字列・urlencoded ボディ・multipart の正規化に使うパーサ。
	Query *QueryParser
	// MaxBodyBytes は JSON/XML ボディの上限（既定 16MiB）。超過時は 413。
	MaxBodyBytes int64
	// MaxMemoryPerFile はアップロードファイルをメモリに保持する上限（既定 1MiB）。超えると一時ファイル。
	MaxMemoryPerFile int64
	// TempDir は一時ファイルの作成先（既定 os.TempDir()）。/tmp が小さい環境では data/ 配下を指定する。
	TempDir string
	// MultipartPartLimit は multipart の総パート数上限（Rack 既定 4096）。
	MultipartPartLimit int
	// MultipartFileLimit はファイルパート数上限（Rack 既定 128）。
	MultipartFileLimit int
	// MultipartBufferedLimit は非ファイルパートの合計バイト上限（Rack 既定 16MiB）。
	MultipartBufferedLimit int64
	// MaxUploadBytes は multipart ボディ全体の上限（0 は無制限）。
	MaxUploadBytes int64
}

func (o *ParseOptions) withDefaults() ParseOptions {
	var out ParseOptions
	if o != nil {
		out = *o
	}
	if out.Query == nil {
		out.Query = DefaultQueryParser
	}
	if out.MaxBodyBytes == 0 {
		out.MaxBodyBytes = 16 << 20
	}
	if out.MaxMemoryPerFile == 0 {
		out.MaxMemoryPerFile = 1 << 20
	}
	if out.MultipartPartLimit == 0 {
		out.MultipartPartLimit = 4096
	}
	if out.MultipartFileLimit == 0 {
		out.MultipartFileLimit = 128
	}
	if out.MultipartBufferedLimit == 0 {
		out.MultipartBufferedLimit = 16 << 20
	}
	return out
}

// RequestParams は解析済みのクエリ・ボディパラメータ。
type RequestParams struct {
	// Query は query_parameters 相当。
	Query *Params
	// Body は request_parameters 相当。
	Body *Params

	files []*UploadedFile
}

// Cleanup はアップロードの一時ファイルを削除する。
func (rp *RequestParams) Cleanup() {
	if rp == nil {
		return
	}
	for _, f := range rp.files {
		_ = f.Remove()
	}
}

// ParseRequest は Rails の query_parameters / request_parameters と同じ規則で
// クエリ文字列とボディを解析する。
//
//   - application/json（および text/x-json 等の別名）: JSON。ハッシュ以外は {"_json": ...}
//   - application/xml（text/xml, application/x-xml）: Hash.from_xml 互換
//   - application/x-www-form-urlencoded、または Content-Type なしの POST: Rack の nested query
//   - multipart/form-data（multipart/mixed, multipart/related）: Rack の multipart
//   - それ以外: ボディは読まない（/uploads の octet-stream などはハンドラが r.Body を読む）
//
// 解析後に UTF-8 検証と deep_munge（配列の nil 除去）を行う。
func ParseRequest(r *http.Request, opts *ParseOptions) (*RequestParams, error) {
	o := opts.withDefaults()
	rp := &RequestParams{}

	q, err := o.Query.ParseNestedQuery(r.URL.RawQuery)
	if err != nil {
		return nil, err
	}
	if err := CheckParamEncoding(q); err != nil {
		return nil, err
	}
	DeepMunge(q)
	rp.Query = q

	body, err := parseBody(r, &o, rp)
	if err != nil {
		rp.Cleanup()
		return nil, err
	}
	if err := CheckParamEncoding(body); err != nil {
		rp.Cleanup()
		return nil, err
	}
	DeepMunge(body)
	rp.Body = body
	return rp, nil
}

// MediaType は Content-Type からメディアタイプ部分を小文字で返す（Rails の content_mime_type の元文字列）。
func MediaType(r *http.Request) string {
	ct := r.Header.Get("Content-Type")
	if ct == "" {
		return ""
	}
	if i := strings.IndexAny(ct, ",;"); i >= 0 {
		ct = ct[:i]
	}
	return strings.ToLower(strings.TrimSpace(ct))
}

func parseBody(r *http.Request, o *ParseOptions, rp *RequestParams) (*Params, error) {
	if r.Body == nil || r.Body == http.NoBody {
		return NewParams(), nil
	}
	mt := MediaType(r)
	method := OriginalMethod(r)

	// Rails の parse_formatted_parameters（JSON / XML）
	if sym := lookupMimeSymbol(mt); sym == "json" || sym == "xml" {
		if r.ContentLength == 0 {
			return NewParams(), nil
		}
		raw, err := readLimited(r.Body, o.MaxBodyBytes)
		if err != nil {
			return nil, err
		}
		if len(raw) == 0 {
			return NewParams(), nil
		}
		if sym == "json" {
			return parseJSONParams(raw)
		}
		return parseXMLParams(raw)
	}

	// Rack の form_data? / parseable_data?
	switch {
	case mt == "multipart/form-data" || mt == "multipart/mixed" || mt == "multipart/related":
		return parseMultipart(r, o, rp)
	case mt == "application/x-www-form-urlencoded" || (mt == "" && method == http.MethodPost):
		raw, err := readLimited(r.Body, int64(o.Query.BytesizeLimit))
		if err != nil {
			var pe *ParamError
			if errors.As(err, &pe) && pe.Kind == ParamTooLarge {
				return nil, &ParamError{Kind: ParamLimit, Msg: fmt.Sprintf("total query size exceeds limit (%d)", o.Query.BytesizeLimit)}
			}
			return nil, err
		}
		s := string(raw)
		// Safari の末尾 NUL 対策（Rack と同じ）
		s = strings.TrimSuffix(s, "\x00")
		return o.Query.ParseNestedQuery(s)
	}
	return NewParams(), nil
}

func readLimited(rd io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		b, err := io.ReadAll(rd)
		if err != nil {
			return nil, &ParamError{Kind: ParamParse, Msg: "bad content body"}
		}
		return b, nil
	}
	b, err := io.ReadAll(io.LimitReader(rd, limit+1))
	if err != nil {
		return nil, &ParamError{Kind: ParamParse, Msg: "bad content body"}
	}
	if int64(len(b)) > limit {
		return nil, &ParamError{Kind: ParamTooLarge, Msg: "request body too large"}
	}
	return b, nil
}

const jsonMaxNesting = 100 // Ruby JSON.parse の max_nesting 既定値

var errJSONParse = &ParamError{Kind: ParamParse, Msg: "Error occurred while parsing request parameters"}

// parseJSONParams は ActiveSupport::JSON.decode ＋ Rails の JSON パラメータパーサ相当。
func parseJSONParams(raw []byte) (*Params, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	v, err := decodeJSONValue(dec, 0)
	if err != nil {
		return nil, errJSONParse
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errJSONParse
	}
	if p, ok := v.(*Params); ok {
		return p, nil
	}
	out := NewParams()
	out.Set("_json", v)
	return out, nil
}

// DecodeJSON は JSON をキー順保持の Params/[]any/スカラーに変換する（数値は int64 か float64）。
func DecodeJSON(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	v, err := decodeJSONValue(dec, 0)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("unexpected trailing data")
	}
	return v, nil
}

func decodeJSONValue(dec *json.Decoder, depth int) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		if depth+1 > jsonMaxNesting {
			return nil, errors.New("nesting too deep")
		}
		switch t {
		case '{':
			p := NewParams()
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				k, ok := kt.(string)
				if !ok {
					return nil, errors.New("invalid object key")
				}
				v, err := decodeJSONValue(dec, depth+1)
				if err != nil {
					return nil, err
				}
				p.Set(k, v)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return p, nil
		case '[':
			a := []any{}
			for dec.More() {
				v, err := decodeJSONValue(dec, depth+1)
				if err != nil {
					return nil, err
				}
				a = append(a, v)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return a, nil
		}
		return nil, errors.New("unexpected delimiter")
	case json.Number:
		s := string(t)
		if !strings.ContainsAny(s, ".eE") {
			if n, err := strconv.ParseInt(s, 10, 64); err == nil {
				return n, nil
			}
			// int64 を超える整数は文字列として保持（Ruby は Bignum）
			return s, nil
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil, err
		}
		return f, nil
	default:
		// string, bool, nil
		return t, nil
	}
}

var percentSeqRe = regexp.MustCompile(`%.?.?`)
var validPercentRe = regexp.MustCompile(`^%[0-9a-fA-F]{2}$`)

// normalizeMultipartFilename は Rack の normalize_filename 相当。
func normalizeMultipartFilename(fn string) string {
	seqs := percentSeqRe.FindAllString(fn, -1)
	all := true
	for _, s := range seqs {
		if !validPercentRe.MatchString(s) {
			all = false
			break
		}
	}
	if all && len(seqs) > 0 {
		if u, err := url.PathUnescape(fn); err == nil {
			fn = u
		}
	}
	return strings.ToValidUTF8(fn, "�")
}

var (
	cdNameRe     = regexp.MustCompile(`(?i);\s*name=(?:"((?:\\.|[^"])*)"|([^;\s]*))`)
	cdFilenameRe = regexp.MustCompile(`(?i);\s*filename=(?:"((?:\\.|[^"])*)"|([^;\s]*))`)
)

// dispositionParams は Content-Disposition から name / filename を取り出す。
// filename が存在しない場合 hasFilename=false。
func dispositionParams(cd string) (name string, filename string, hasFilename bool) {
	if cd == "" {
		return "", "", false
	}
	if _, params, err := mime.ParseMediaType(cd); err == nil {
		name = params["name"]
		filename, hasFilename = params["filename"]
		return
	}
	// ParseMediaType が失敗する壊れたヘッダ（IE のバックスラッシュ等）の救済
	if m := cdNameRe.FindStringSubmatch(cd); m != nil {
		name = m[1] + m[2]
	}
	if m := cdFilenameRe.FindStringSubmatch(cd); m != nil {
		filename = m[1] + m[2]
		hasFilename = true
	}
	return
}

func parseMultipart(r *http.Request, o *ParseOptions, rp *RequestParams) (*Params, error) {
	_, ctParams, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	boundary := ctParams["boundary"]
	if err != nil || boundary == "" {
		return nil, &ParamError{Kind: ParamParse, Msg: "bad content body"}
	}
	var body io.Reader = r.Body
	if o.MaxUploadBytes > 0 {
		body = io.LimitReader(r.Body, o.MaxUploadBytes+1)
	}
	cr := &countingReader{r: body}
	mr := multipart.NewReader(cr, boundary)
	params := NewParams()
	parts, files := 0, 0
	var buffered int64
	for {
		part, err := mr.NextRawPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			if o.MaxUploadBytes > 0 && cr.n > o.MaxUploadBytes {
				return nil, &ParamError{Kind: ParamTooLarge, Msg: "multipart body too large"}
			}
			return nil, &ParamError{Kind: ParamParse, Msg: "bad content body"}
		}
		parts++
		if parts > o.MultipartPartLimit {
			return nil, &ParamError{Kind: ParamTooLarge, Msg: fmt.Sprintf("total number of parts exceeds limit (%d)", o.MultipartPartLimit)}
		}
		cd := part.Header.Get("Content-Disposition")
		name, filename, hasFilename := dispositionParams(cd)
		ctype := part.Header.Get("Content-Type")
		if hasFilename {
			filename = normalizeMultipartFilename(filename)
		}
		if name == "" {
			// Rack: name = filename || "#{content_type || 'text/plain'}[]"
			if hasFilename {
				name = filename
			} else {
				ct := ctype
				if ct == "" {
					ct = "text/plain"
				}
				name = ct + "[]"
			}
		}

		if !hasFilename {
			b, err := io.ReadAll(io.LimitReader(part, o.MultipartBufferedLimit-buffered+1))
			if err != nil {
				return nil, &ParamError{Kind: ParamParse, Msg: "bad content body"}
			}
			buffered += int64(len(b))
			if buffered > o.MultipartBufferedLimit {
				return nil, &ParamError{Kind: ParamTooLarge, Msg: "multipart buffered data exceeds limit"}
			}
			if err := o.Query.Normalize(params, name, string(b)); err != nil {
				return nil, err
			}
			continue
		}

		files++
		if files > o.MultipartFileLimit {
			return nil, &ParamError{Kind: ParamTooLarge, Msg: fmt.Sprintf("total number of files exceeds limit (%d)", o.MultipartFileLimit)}
		}
		uf, err := readUploadedFile(part, o)
		if err != nil {
			return nil, err
		}
		if filename == "" {
			// ファイル未選択（filename=""）はパラメータ自体を作らない（Rack と同じ）
			_ = uf.Remove()
			continue
		}
		rp.files = append(rp.files, uf)
		// Windows のフルパス対策で最後の要素のみ
		if i := strings.LastIndexAny(filename, `/\`); i >= 0 {
			filename = filename[i+1:]
		}
		uf.Filename = filename
		uf.ContentType = ctype
		uf.Header = part.Header
		if err := o.Query.Normalize(params, name, uf); err != nil {
			return nil, err
		}
	}
	return params, nil
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

func readUploadedFile(part io.Reader, o *ParseOptions) (*UploadedFile, error) {
	uf := &UploadedFile{}
	buf, err := io.ReadAll(io.LimitReader(part, o.MaxMemoryPerFile+1))
	if err != nil {
		return nil, &ParamError{Kind: ParamParse, Msg: "bad content body"}
	}
	if int64(len(buf)) <= o.MaxMemoryPerFile {
		uf.data = buf
		uf.Size = int64(len(buf))
		return uf, nil
	}
	f, err := os.CreateTemp(o.TempDir, "buropher-upload-*")
	if err != nil {
		return nil, fmt.Errorf("httpx: create temp file: %w", err)
	}
	uf.path = f.Name()
	n1, err := f.Write(buf)
	if err == nil {
		var n2 int64
		n2, err = io.Copy(f, part)
		uf.Size = int64(n1) + n2
	}
	cerr := f.Close()
	if err == nil {
		err = cerr
	}
	if err != nil {
		_ = uf.Remove()
		return nil, &ParamError{Kind: ParamParse, Msg: "bad content body"}
	}
	return uf, nil
}

// CheckParamEncoding は Rails の check_param_encoding 相当: 文字列値が UTF-8 として不正ならエラー。
func CheckParamEncoding(v any) error {
	switch x := v.(type) {
	case *Params:
		for _, k := range x.keys {
			if err := CheckParamEncoding(x.vals[k]); err != nil {
				return err
			}
		}
	case []any:
		for _, e := range x {
			if err := CheckParamEncoding(e); err != nil {
				return err
			}
		}
	case string:
		if !utf8.ValidString(x) {
			return &ParamError{Kind: ParamInvalid, Msg: "Invalid encoding for parameter: " + strings.ToValidUTF8(x, "�")}
		}
	}
	return nil
}

// DeepMunge は Rails の NoNilParamEncoder 相当: すべての配列から nil を取り除く（再帰）。
func DeepMunge(v any) any {
	switch x := v.(type) {
	case *Params:
		for _, k := range x.keys {
			x.vals[k] = DeepMunge(x.vals[k])
		}
		return x
	case []any:
		out := x[:0]
		for _, e := range x {
			e = DeepMunge(e)
			if e != nil {
				out = append(out, e)
			}
		}
		return out
	}
	return v
}

// ---- コンテキスト ----

type ctxKey int

const (
	ctxParams ctxKey = iota
	ctxOrigMethod
	ctxSession
	ctxRequestID
	ctxRemoteIP
	ctxSkipCSRF
	ctxFormats
)

type paramsState struct {
	rp *RequestParams
}

// ParamsMiddleware はクエリ・ボディを一度だけ解析してコンテキストに格納する。
// 解析エラー時は onError（nil なら Rails 本番と同じ空ボディの 400/413）を呼ぶ。
// アップロードの一時ファイルはハンドラ終了後に削除される。
func ParamsMiddleware(opts *ParseOptions, onError func(w http.ResponseWriter, r *http.Request, err *ParamError)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rp, err := ParseRequest(r, opts)
			if err != nil {
				var pe *ParamError
				if !errors.As(err, &pe) {
					pe = &ParamError{Kind: ParamParse, Msg: err.Error()}
				}
				if onError != nil {
					onError(w, r, pe)
				} else {
					w.Header().Set("Content-Type", "text/html; charset=utf-8")
					w.WriteHeader(pe.Status())
				}
				return
			}
			defer rp.Cleanup()
			ctx := context.WithValue(r.Context(), ctxParams, &paramsState{rp: rp})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequestParamsOf は ParamsMiddleware が解析した結果を返す（未実行なら nil）。
func RequestParamsOf(r *http.Request) *RequestParams {
	if st, ok := r.Context().Value(ctxParams).(*paramsState); ok {
		return st.rp
	}
	return nil
}

// QueryParams は query_parameters を返す。
func QueryParams(r *http.Request) *Params {
	if rp := RequestParamsOf(r); rp != nil {
		return rp.Query
	}
	p, err := ParseRailsQuery(r.URL.RawQuery)
	if err != nil {
		return NewParams()
	}
	return p
}

// BodyParams は request_parameters を返す（ParamsMiddleware 未実行なら空）。
func BodyParams(r *http.Request) *Params {
	if rp := RequestParamsOf(r); rp != nil {
		return rp.Body
	}
	return NewParams()
}

// PathParams は chi のルートパラメータを Params として返す（catch-all の "*" は "*" キー）。
func PathParams(r *http.Request) *Params {
	out := NewParams()
	rctx := chi.RouteContext(r.Context())
	if rctx == nil {
		return out
	}
	unescape := r.URL.RawPath != ""
	for i, k := range rctx.URLParams.Keys {
		if i >= len(rctx.URLParams.Values) {
			break
		}
		v := rctx.URLParams.Values[i]
		if unescape {
			if u, err := url.PathUnescape(v); err == nil {
				v = u
			}
		}
		out.Set(k, v)
	}
	return out
}

// ParamsOf は Rails の params 相当を返す: request_parameters に query_parameters、
// さらにパスパラメータを上書きマージしたもの。呼び出しごとに新しいトップレベルを作る
// （ネストした値は共有される）。
func ParamsOf(r *http.Request) *Params {
	return BodyParams(r).Merge(QueryParams(r)).Merge(PathParams(r))
}
