package textdiff

import "testing"

// test/unit/lib/redmine/helpers/diff_test.rb の移植と、journals#diff の参照出力。
func TestDontDoubleEscape(t *testing.T) {
	before := "<stuff> with html & special chars</danger>"
	after := "other stuff <script>alert('foo');</alert>"
	got := ToHTML(before, after)
	want := `<span class="diff_in">&lt;stuff&gt; with html &amp; special chars&lt;/danger&gt;</span>` +
		` <span class="diff_out">other stuff &lt;script&gt;alert(&#39;foo&#39;);&lt;/alert&gt;</span>`
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestJournalDiff(t *testing.T) {
	// フィクスチャの journal_details 4（参照 Redmine の /journals/3/diff の出力）
	got := ToHTML("This word was and an other was added", "This word was removed and an other was")
	want := `This word was <span class="diff_out">removed</span> and an other was <span class="diff_in">added</span>`
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestSplitWords(t *testing.T) {
	got := splitWords("  a b\nc ")
	want := []string{"", "  ", "a", "b", "\n", "c"}
	if len(got) != len(want) {
		t.Fatalf("got %q want %q", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got %q want %q", got, want)
		}
	}
}
