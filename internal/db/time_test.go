package db

import (
	"strings"
	"testing"
	"time"
)

func TestTimeFormatAndParse(t *testing.T) {
	tm := time.Date(2026, 10, 2, 23, 28, 40, 788013000, time.FixedZone("JST", 9*3600))
	s := FormatTime(tm)
	if s != "2026-10-02T14:28:40.788013Z" || len(s) != 27 {
		t.Fatalf("FormatTime = %q", s)
	}
	for _, in := range []string{s, "2026-10-02 14:28:40.788013", "2026-10-02T23:28:40.788013+09:00", "2026-10-02 23:28:40.788013+09:00"} {
		got, err := ParseTime(in)
		if err != nil || !got.Equal(tm) || got.Location() != time.UTC {
			t.Errorf("ParseTime(%q) = %v, %v", in, got, err)
		}
	}
	if _, err := (Time{}).Value(); err == nil {
		t.Error("zero Time accepted")
	}
	if v, _ := (NullTime{}).Value(); v != nil {
		t.Error("invalid NullTime not NULL")
	}
	var d Date
	if err := d.Scan(time.Date(2026, 2, 3, 0, 0, 0, 0, time.UTC)); err != nil || d.String() != "2026-02-03" {
		t.Errorf("Date scan = %v, %v", d, err)
	}
	if err := d.Scan("2026-02-04"); err != nil || d.String() != "2026-02-04" {
		t.Errorf("Date scan string = %v, %v", d, err)
	}
	if _, err := ParseDate("2026/02/04"); err == nil {
		t.Error("bad date accepted")
	}
}

func TestSQLiteDSN(t *testing.T) {
	full, mem, err := sqliteDSN("data/buropher.db")
	if err != nil || mem {
		t.Fatal(err)
	}
	for _, want := range []string{"file:data/buropher.db?", "foreign_keys%281%29", "journal_mode%28WAL%29", "busy_timeout", "synchronous%28NORMAL%29", "_txlock=immediate"} {
		if !strings.Contains(full, want) {
			t.Errorf("dsn %q lacks %q", full, want)
		}
	}
	full, mem, err = sqliteDSN(":memory:")
	if err != nil || !mem || strings.Contains(full, "WAL") {
		t.Errorf("memory dsn = %q %v %v", full, mem, err)
	}
	full, _, _ = sqliteDSN("file:x.db?_txlock=deferred")
	if !strings.Contains(full, "_txlock=deferred") || strings.Contains(full, "immediate") {
		t.Errorf("txlock override = %q", full)
	}
}
