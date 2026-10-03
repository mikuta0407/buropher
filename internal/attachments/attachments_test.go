package attachments

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/db/dbtest"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/i18n"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/settings"
	"github.com/mikuta0407/buropher/internal/testfixtures"
)

func newSettings(t *testing.T, vals map[string]string) *settings.Settings {
	t.Helper()
	st := &settings.MemoryStore{Values: map[string]json.RawMessage{}}
	for k, v := range vals {
		b, _ := json.Marshal(v)
		st.Values[k] = b
	}
	s, err := settings.New(context.Background(), st)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func enLocalizer() *i18n.Localizer {
	return i18n.Default().NewLocalizer("en", i18n.Settings{}, nil)
}

// test/unit/attachment_test.rb の test_filename_should_be_basenamed / test_filename_should_be_sanitized。
func TestSanitizeFilename(t *testing.T) {
	cases := map[string]string{
		"path/to/the/file":                "file",
		`c:\windows\path\file.txt`:        "file.txt",
		"valid:[] invalid:?%*|\"'<>chars": "valid_[] invalid_chars",
		"line\nbreak/after\r\nname.txt":   "after_name.txt",
		"multi\nline\\dir\\x":             "x",
		"no_change-1.2.txt":               "no_change-1.2.txt",
		"日本語 ファイル.txt":                    "日本語 ファイル.txt",
		"trailing/":                       "",
		"a\rb":                            "a_b",
	}
	for in, want := range cases {
		if got := SanitizeFilename(in); got != want {
			t.Errorf("SanitizeFilename(%q) = %q, want %q", in, got, want)
		}
	}
}

// test_create_diskfile / test_create_diskfile_with_accented_*（期待値は Redmine のテストのもの）。
func TestDiskFilenameBase(t *testing.T) {
	cases := map[string]string{
		"test_file.txt":     "test_file.txt",
		"test_accentué.txt": "770c509475505f37c2b8fb6030434d6b.txt",
		"test_accentué":     "f8139524ebb8f32e51976982cd20a85d",
		"test_accentué.ça":  "cbb5b0f30978ba03731d61f9f6d10011",
		// 50 文字を超える ASCII 名は MD5 + 拡張子
		strings.Repeat("a", 51) + ".txt": md5hex(strings.Repeat("a", 51)+".txt") + ".txt",
		strings.Repeat("a", 50):          strings.Repeat("a", 50),
		"with space.txt":                 md5hex("with space.txt") + ".txt",
	}
	for in, want := range cases {
		if got := DiskFilenameBase(in); got != want {
			t.Errorf("DiskFilenameBase(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSuccDigitsAndTimestamp(t *testing.T) {
	for in, want := range map[string]string{"260115120000": "260115120001", "260115120059": "260115120060", "0999": "1000", "99": "100"} {
		if got := succDigits(in); got != want {
			t.Errorf("succDigits(%q) = %q, want %q", in, got, want)
		}
	}
	tm := time.Date(2026, 1, 5, 9, 8, 7, 0, time.UTC)
	if got := DiskTimestamp(tm); got != "260105090807" {
		t.Errorf("DiskTimestamp = %q", got)
	}
	if got := TargetDirectory(tm); got != "2026/01" {
		t.Errorf("TargetDirectory = %q", got)
	}
}

func TestParseToken(t *testing.T) {
	id, dg, ok := ParseToken("12.abcdef0123")
	if !ok || id != 12 || dg != "abcdef0123" {
		t.Errorf("ParseToken = %d %q %v", id, dg, ok)
	}
	for _, bad := range []string{"", "12", "x.abc", "12.ABC", "12.abc\n", " 12.abc"} {
		if _, _, ok := ParseToken(bad); ok {
			t.Errorf("ParseToken(%q) should fail", bad)
		}
	}
}

// test_valid_extension_should_be_case_insensitive。
func TestValidExtension(t *testing.T) {
	s := &Store{Settings: newSettings(t, map[string]string{"attachment_extensions_allowed": "txt, Png"})}
	if !s.ValidExtension(".pnG") || s.ValidExtension(".jpeg") {
		t.Error("allowed list")
	}
	s = &Store{Settings: newSettings(t, map[string]string{"attachment_extensions_denied": "txt, .Png"})}
	if s.ValidExtension(".pnG") || !s.ValidExtension(".jpeg") {
		t.Error("denied list")
	}
	s = &Store{Settings: newSettings(t, nil)}
	if !s.ValidExtension("") || !s.ValidExtension(".exe") {
		t.Error("no lists")
	}
}

func TestValidate(t *testing.T) {
	l := enLocalizer()
	s := &Store{Settings: newSettings(t, map[string]string{"attachment_max_size": "1", "attachment_extensions_denied": "exe"})}
	a := &domain.Attachment{Filename: "big.exe", AuthorID: 1}
	got := s.Validate(a, 1025, true, l).FullMessages(l)
	want := []string{
		"This file cannot be uploaded because it exceeds the maximum allowed file size (1024)",
		"Attachment extension .exe is not allowed",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %q", got)
	}
	// 最大サイズちょうどは可、拡張子は filename_changed? のときだけ
	if errs := s.Validate(a, 1024, false, l); errs.Any() {
		t.Errorf("unexpected %q", errs.FullMessages(l))
	}
	a = &domain.Attachment{Filename: strings.Repeat("x", 256), Description: strings.Repeat("d", 256)}
	got = s.Validate(a, -1, false, l).FullMessages(l)
	want = []string{"Author cannot be blank", "File is too long (maximum is 255 characters)", "Description is too long (maximum is 255 characters)"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %q", got)
	}
	a = &domain.Attachment{Filename: " ", AuthorID: 1}
	if got := s.Validate(a, -1, false, l).FullMessages(l); len(got) != 1 || got[0] != "File cannot be blank" {
		t.Errorf("got %q", got)
	}
}

func md5hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

func newDBStore(t *testing.T, vals map[string]string) (*Store, context.Context, *db.DB) {
	t.Helper()
	ctx := context.Background()
	d := dbtest.New(t)
	now := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	if err := testfixtures.LoadContext(ctx, d, now, testfixtures.All()...); err != nil {
		t.Fatal(err)
	}
	s := &Store{Root: t.TempDir(), Settings: newSettings(t, vals), Now: func() time.Time { return now }}
	return s, ctx, d
}

func TestCreateAttachFilesAndDelete(t *testing.T) {
	saved := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = saved })
	s, ctx, d := newDBStore(t, nil)
	l := enLocalizer()
	admin, err := repository.GetUser(ctx, d, 1)
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("hello attachment\n")
	a, errs, err := s.Create(ctx, d, Upload{Filename: "dir/test_accentué.txt", Body: strings.NewReader(string(content)), Size: -1}, admin, l)
	if err != nil || errs.Any() {
		t.Fatalf("create: %v %v", err, errs.FullMessages(l))
	}
	sum := sha256.Sum256(content)
	if a.Digest != hex.EncodeToString(sum[:]) || a.DigestAlgo != "sha256" || a.Filesize != int64(len(content)) {
		t.Errorf("digest/size: %+v", a)
	}
	if a.Filename != "test_accentué.txt" || a.ContentType != "text/plain" || a.DiskDirectory != "2026/01" ||
		a.DiskFilename != "260115120000_770c509475505f37c2b8fb6030434d6b.txt" {
		t.Errorf("attrs: %+v", a)
	}
	if b, err := os.ReadFile(filepath.Join(s.Root, "2026", "01", a.DiskFilename)); err != nil || string(b) != string(content) {
		t.Errorf("disk: %q %v", b, err)
	}

	// 同じ内容のファイルは既存のファイルを共有する（reuse_existing_file_if_possible）。名前の衝突で秒が進む。
	b, _, err := s.Create(ctx, d, Upload{Filename: "test_accentué.txt", Body: strings.NewReader(string(content)), Size: int64(len(content))}, admin, l)
	if err != nil {
		t.Fatal(err)
	}
	if b.DiskFilename != a.DiskFilename {
		t.Errorf("reuse: %q != %q", b.DiskFilename, a.DiskFilename)
	}
	if _, err := os.Stat(filepath.Join(s.Root, "2026", "01", "260115120001_770c509475505f37c2b8fb6030434d6b.txt")); !os.IsNotExist(err) {
		t.Errorf("duplicate file should be removed: %v", err)
	}

	// トークンとファイル（multipart）を混ぜた params[:attachments]
	p := httpx.NewParams()
	e1 := httpx.NewParams()
	e1.Set("token", a.Token())
	e1.Set("filename", "renamed.txt")
	e1.Set("description", "  desc  ")
	e2 := httpx.NewParams()
	e2.Set("token", "999.abcdef")
	p.Set("2", e2)
	p.Set("1", e1)
	res, err := s.AttachFiles(ctx, d, domain.AttachmentContainerIssue, 1, p, admin, l)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != 1 || res.FailedCount != 1 || len(res.Unsaved) != 0 {
		t.Fatalf("result: %+v", res)
	}
	if got := res.FailedMessage(l); got != "1 file(s) could not be saved." {
		t.Errorf("failed message %q", got)
	}
	got, err := repository.GetAttachment(ctx, d, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ContainerKind != "issue" || got.ContainerID == nil || *got.ContainerID != 1 || got.Filename != "renamed.txt" || got.Description != "desc" {
		t.Errorf("attached: %+v", got)
	}
	if found, _ := FindByToken(ctx, d, a.Token()); found != nil {
		t.Error("attached attachment must not be found by token")
	}

	// 拒否された拡張子への改名は未保存になる
	s.Settings = newSettings(t, map[string]string{"attachment_extensions_denied": "exe"})
	e3 := httpx.NewParams()
	e3.Set("token", b.Token())
	e3.Set("filename", "x.exe")
	res, err = s.SaveAttachments(ctx, d, []any{e3}, admin, l)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Unsaved) != 1 || res.WarningNotSaved(l) != "1 file(s) could not be saved." {
		t.Errorf("unsaved: %+v", res)
	}

	// 削除: 共有しているファイルは残り、最後の 1 件でディスクから消える
	if err := s.Destroy(ctx, d, got); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.Root, "2026", "01", a.DiskFilename)
	if _, err := os.Stat(path); err != nil {
		t.Errorf("shared file removed: %v", err)
	}
	if err := s.Destroy(ctx, d, b); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file should be removed: %v", err)
	}

	// 最大サイズ超過（サイズ不明のアップロードは書き込み後に検証してファイルを消す）
	s.Settings = newSettings(t, map[string]string{"attachment_max_size": "1"})
	c, errs, err := s.Create(ctx, d, Upload{Filename: "big.bin", Body: strings.NewReader(strings.Repeat("x", 2000)), Size: -1}, admin, l)
	if err != nil || !errs.Any() || !c.NewRecord() {
		t.Fatalf("too big: %v %+v", err, c)
	}
	entries, _ := os.ReadDir(filepath.Join(s.Root, "2026", "01"))
	for _, e := range entries {
		if regexp.MustCompile(`big\.bin$`).MatchString(e.Name()) {
			t.Errorf("file left: %s", e.Name())
		}
	}
	// サイズ不明の本文は上限 + 1 バイトまでしか読まない（ディスクを使い尽くさない）
	big := strings.NewReader(strings.Repeat("x", 1<<20))
	if _, errs, err := s.Create(ctx, d, Upload{Filename: "huge.bin", Body: big, Size: -1}, admin, l); err != nil || !errs.Any() {
		t.Fatalf("huge: %v", err)
	}
	if read := int64(1<<20) - int64(big.Len()); read > 1025 {
		t.Errorf("read %d bytes of an oversized chunked upload (want <= 1025)", read)
	}
}
