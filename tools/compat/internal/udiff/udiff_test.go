package udiff

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
)

// apply は編集列から a と b を復元して整合性を確認する。
func apply(ops []Op) (a, b []string) {
	for _, o := range ops {
		if o.Kind != Insert {
			a = append(a, o.Line)
		}
		if o.Kind != Delete {
			b = append(b, o.Line)
		}
	}
	return
}

func eq(x, y []string) bool {
	return strings.Join(x, "\n") == strings.Join(y, "\n") && len(x) == len(y)
}

func TestDiffRoundTrip(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < 300; i++ {
		gen := func() []string {
			n := r.IntN(30)
			s := make([]string, n)
			for j := range s {
				s[j] = fmt.Sprint(r.IntN(5))
			}
			return s
		}
		a, b := gen(), gen()
		ga, gb := apply(Diff(a, b))
		if !eq(ga, a) || !eq(gb, b) {
			t.Fatalf("round trip failed for %v -> %v", a, b)
		}
	}
}

func TestDiffMinimal(t *testing.T) {
	ops := Diff([]string{"a", "b", "c", "d"}, []string{"a", "x", "c", "d"})
	changes := 0
	for _, o := range ops {
		if o.Kind != Equal {
			changes++
		}
	}
	if changes != 2 {
		t.Errorf("expected 2 changes, got %d: %v", changes, ops)
	}
}

func TestUnified(t *testing.T) {
	a := "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n"
	b := "1\n2\n3\n4\nfive\n6\n7\n8\n9\n10\n"
	got := Unified(a, b, "a", "b", 2)
	want := "--- a\n+++ b\n@@ -3,5 +3,5 @@\n 3\n 4\n-5\n+five\n 6\n 7\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if Unified(a, a, "a", "b", 3) != "" {
		t.Error("identical input should give empty diff")
	}
}

func TestUnifiedEmptySide(t *testing.T) {
	got := Unified("", "x\n", "a", "b", 3)
	want := "--- a\n+++ b\n@@ -0,0 +1,1 @@\n+x\n"
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}
