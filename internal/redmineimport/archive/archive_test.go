package archive

import (
	"archive/tar"
	"bytes"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func buildArchive(t *testing.T) ([]byte, string) {
	t.Helper()
	dir := t.TempDir()
	att := filepath.Join(dir, "a.bin")
	if err := os.WriteFile(att, []byte("attachment-data\x00\xff"), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	w, err := NewWriter(&buf, dir)
	if err != nil {
		t.Fatal(err)
	}
	cols := []Column{{"id", "integer"}, {"name", "string"}, {"ok", "boolean"}, {"hours", "float"}, {"data", "binary"}, {"at", "datetime"}}
	tw, err := w.BeginTable("issues", cols)
	if err != nil {
		t.Fatal(err)
	}
	rows := [][]any{
		{int64(1), "日本語 <b>&\"\n", true, 1.5, []byte{0, 1, 2, 255}, "2026-10-03 00:00:00.123456"},
		{int64(2), nil, false, 2.0, nil, nil},
		{int64(math.MaxInt64), "", nil, math.Inf(1), []byte{}, "2026-01-01 00:00:00"},
	}
	for _, r := range rows {
		if err := tw.WriteRow(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.WriteRow([]any{"x"}); err == nil {
		t.Error("expected column count error")
	}
	if err := tw.WriteRow([]any{int64(1), "\xff", nil, nil, nil, nil}); err == nil {
		t.Error("expected invalid utf-8 error")
	}
	empty, _ := w.BeginTable("empty_table", []Column{{"id", "integer"}})
	_ = empty
	if _, err := w.BeginTable("issues", cols); err == nil {
		t.Error("expected duplicate table error")
	}
	if _, err := w.AddFile("2026/10/a.bin", att); err != nil {
		t.Fatal(err)
	}
	if _, err := w.AddFile("../evil", att); err == nil {
		t.Error("expected unsafe path error")
	}
	m := &Manifest{Source: Source{DBKind: "sqlite", Timezone: "Asia/Tokyo"}, SchemaMigrations: []string{"1"}}
	tmp := w.TempDir()
	if err := w.Close(m); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Error("temp dir not removed")
	}
	return buf.Bytes(), att
}

func TestRoundTrip(t *testing.T) {
	data, _ := buildArchive(t)
	r, err := NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	m := r.Manifest()
	if m.Format != FormatName || m.FormatVersion != FormatVersion || m.Source.Timezone != "Asia/Tokyo" {
		t.Fatalf("manifest: %+v", m)
	}
	if len(m.Tables) != 2 || m.Tables[0].Name != "empty_table" || m.Tables[1].Rows != 3 {
		t.Fatalf("tables: %+v", m.Tables)
	}
	var got []Row
	var files []string
	for {
		e, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		switch e.Kind {
		case KindTable:
			rs := e.Rows()
			for rs.Next() {
				if e.Name == "issues" {
					got = append(got, rs.Row())
				}
			}
			if rs.Err() != nil {
				t.Fatal(rs.Err())
			}
		case KindFile:
			b, _ := io.ReadAll(e.Reader())
			if string(b) != "attachment-data\x00\xff" {
				t.Errorf("file content %q", b)
			}
			files = append(files, e.Name)
		}
	}
	if !reflect.DeepEqual(files, []string{"2026/10/a.bin"}) {
		t.Errorf("files %v", files)
	}
	want := []Row{
		{"id": int64(1), "name": "日本語 <b>&\"\n", "ok": true, "hours": 1.5, "data": []byte{0, 1, 2, 255}, "at": "2026-10-03 00:00:00.123456"},
		{"id": int64(2), "name": nil, "ok": false, "hours": 2.0, "data": nil, "at": nil},
		{"id": int64(math.MaxInt64), "name": "", "ok": nil, "hours": math.Inf(1), "data": []byte{}, "at": "2026-01-01 00:00:00"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rows:\n got %#v\nwant %#v", got, want)
	}
}

func TestReadTable(t *testing.T) {
	data, _ := buildArchive(t)
	p := filepath.Join(t.TempDir(), "x.tar.zst")
	os.WriteFile(p, data, 0o644)
	n := 0
	if err := ReadTable(p, "issues", func(r Row) error { n++; return nil }); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("rows %d", n)
	}
	if err := ReadTable(p, "nope", func(Row) error { return nil }); err == nil {
		t.Error("expected error")
	}
}

// 改ざん検出: tar を作り直して内容を 1 バイト変える
func TestTamperDetected(t *testing.T) {
	data, _ := buildArchive(t)
	zr, _ := zstd.NewReader(bytes.NewReader(data))
	tr := tar.NewReader(zr)
	var out bytes.Buffer
	zw, _ := zstd.NewWriter(&out)
	tw := tar.NewWriter(zw)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		b, _ := io.ReadAll(tr)
		if strings.HasPrefix(h.Name, "files/") {
			b[0] ^= 1
		}
		tw.WriteHeader(h)
		tw.Write(b)
	}
	tw.Close()
	zw.Close()
	r, err := NewReader(&out)
	if err != nil {
		t.Fatal(err)
	}
	var lastErr error
	for {
		_, err := r.Next()
		if err != nil {
			lastErr = err
			break
		}
	}
	if lastErr == io.EOF || !strings.Contains(lastErr.Error(), "sha256 mismatch") {
		t.Errorf("want sha256 mismatch, got %v", lastErr)
	}
}

func TestFormatFloat(t *testing.T) {
	for in, want := range map[float64]string{2: "2.0", 1.5: "1.5", 1e21: "1e+21", 0.30000000000000004: "0.30000000000000004"} {
		if got := FormatFloat(in); got != want {
			t.Errorf("FormatFloat(%v) = %s, want %s", in, got, want)
		}
	}
}

func TestCleanFilePath(t *testing.T) {
	ok := map[string]string{"2026/10/a.txt": "2026/10/a.txt", "a.txt": "a.txt", "./x/y": "x/y", "a//b": "a/b"}
	for in, want := range ok {
		if got, err := CleanFilePath(in); err != nil || got != want {
			t.Errorf("CleanFilePath(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "/etc/passwd", "../x", "a/../../x", "a/..", "."} {
		if _, err := CleanFilePath(bad); err == nil {
			t.Errorf("CleanFilePath(%q) should fail", bad)
		}
	}
}
