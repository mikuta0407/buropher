package pagination

import (
	"reflect"
	"testing"
)

// Redmine の test/unit/lib/redmine/pagination_test.rb 相当。
func TestPaginator(t *testing.T) {
	p := New(0, 10, nil)
	if p.Offset() != 0 || p.FirstPage() != 0 || p.LastPage() != 0 || p.MultiplePages() || p.FirstItem() != 0 || p.LastItem() != 0 {
		t.Errorf("empty paginator: %+v", p)
	}
	p = New(150, 10, "7")
	if p.Offset() != 60 || p.FirstPage() != 1 || p.PreviousPage() != 6 || p.NextPage() != 8 || p.LastPage() != 15 ||
		p.FirstItem() != 61 || p.LastItem() != 70 || !p.MultiplePages() {
		t.Errorf("page 7: %+v", p)
	}
	if got := p.LinkedPages(); !reflect.DeepEqual(got, []int{1, 5, 6, 7, 8, 9, 15}) {
		t.Errorf("linked pages %v", got)
	}
	p = New(150, 10, "15")
	if p.NextPage() != 0 || p.LastItem() != 150 {
		t.Errorf("last page: %+v", p)
	}
	if got := New(5, 10, "-3").Page; got != 1 {
		t.Errorf("page < 1: %d", got)
	}
	if got := New(5, 10, "1").LinkedPages(); got != nil {
		t.Errorf("single page linked %v", got)
	}
}

func TestPerPageOptions(t *testing.T) {
	opts := []int{25, 50, 100}
	if got := PerPageOptions(opts, 25, 7); got != nil {
		t.Errorf("7 items: %v", got)
	}
	if got := PerPageOptions(opts, 25, 30); !reflect.DeepEqual(got, []int{25, 50}) {
		t.Errorf("30 items: %v", got)
	}
	if got := PerPageOptions(opts, 100, 30); !reflect.DeepEqual(got, []int{25, 50, 100}) {
		t.Errorf("selected beyond max: %v", got)
	}
}

func TestAPIOffsetAndLimit(t *testing.T) {
	for _, c := range []struct {
		off, lim, page string
		wo, wl         int
	}{
		{"", "", "", 0, 25}, {"10", "5", "", 10, 5}, {"-5", "200", "", 0, 100}, {"", "10", "3", 20, 10}, {"", "0", "0", 0, 25},
	} {
		if o, l := APIOffsetAndLimit(c.off, c.lim, c.page); o != c.wo || l != c.wl {
			t.Errorf("%+v: got %d %d", c, o, l)
		}
	}
}
