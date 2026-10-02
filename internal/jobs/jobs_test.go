package jobs

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/db/dbtest"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time      { return c.t }
func (c *fakeClock) add(d time.Duration) { c.t = c.t.Add(d) }

func newQueue(t *testing.T, d *db.DB) (*Queue, *fakeClock) {
	clk := &fakeClock{t: time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)}
	q := New(d, Options{NoJitter: true, BackoffBase: time.Minute, BackoffMax: time.Hour, MaxAttempts: 3})
	q.Now = clk.now
	return q, clk
}

func TestEnqueueAndRun(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		q, _ := newQueue(t, d)
		var got []string
		q.Register("echo", func(ctx context.Context, j *Job) error {
			var p struct{ Msg string }
			if err := j.Decode(&p); err != nil {
				return err
			}
			got = append(got, p.Msg)
			return nil
		})
		id, err := q.Enqueue(ctx, nil, "echo", map[string]string{"Msg": "a"})
		if err != nil || id == 0 {
			t.Fatal(id, err)
		}
		if _, err := q.Enqueue(ctx, nil, "echo", map[string]string{"Msg": "b"}, Priority(10)); err != nil {
			t.Fatal(err)
		}
		n, err := q.RunPending(ctx)
		if err != nil || n != 2 {
			t.Fatal(n, err)
		}
		if len(got) != 2 || got[0] != "b" || got[1] != "a" {
			t.Errorf("order = %v (priority first)", got)
		}
		j, _ := q.Get(ctx, id)
		if j.State != StateSucceeded || j.Attempts != 1 || !j.FinishedAt.Valid {
			t.Errorf("job = %+v", j)
		}
	})
}

func TestRetryBackoffAndDeadLetter(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		q, clk := newQueue(t, d)
		var calls int32
		q.Register("fail", func(ctx context.Context, j *Job) error {
			atomic.AddInt32(&calls, 1)
			return errors.New("boom")
		})
		id, _ := q.Enqueue(ctx, nil, "fail", nil)
		start := clk.t
		if n, _ := q.RunPending(ctx); n != 1 {
			t.Fatalf("first run n=%d", n)
		}
		j, _ := q.Get(ctx, id)
		if j.State != StatePending || j.Attempts != 1 || *j.LastError != "boom" || !j.RunAt.Equal(start.Add(time.Minute)) {
			t.Fatalf("after 1st failure: %+v run_at=%v", j, j.RunAt)
		}
		// 実行予定前は取られない
		if n, _ := q.RunPending(ctx); n != 0 {
			t.Fatal("ran before run_at")
		}
		clk.add(time.Minute)
		q.RunPending(ctx)
		j, _ = q.Get(ctx, id)
		if j.Attempts != 2 || !j.RunAt.Equal(clk.t.Add(2*time.Minute)) {
			t.Fatalf("2nd backoff: %+v", j)
		}
		clk.add(2 * time.Minute)
		q.RunPending(ctx)
		j, _ = q.Get(ctx, id)
		if j.State != StateFailed || j.Attempts != 3 || !j.FinishedAt.Valid {
			t.Fatalf("dead letter: %+v", j)
		}
		if calls != 3 {
			t.Errorf("calls = %d", calls)
		}
	})
}

func TestRetryAfterAndPermanent(t *testing.T) {
	ctx := context.Background()
	d := dbtest.New(t)
	q, clk := newQueue(t, d)
	n := 0
	q.Register("ratelimited", func(ctx context.Context, j *Job) error {
		n++
		if n < 3 {
			return Retry(5*time.Second, errors.New("429"))
		}
		return nil
	})
	q.Register("perm", func(ctx context.Context, j *Job) error { return Permanent(errors.New("50007")) })
	q.Register("panic", func(ctx context.Context, j *Job) error { panic("oops") })
	id1, _ := q.Enqueue(ctx, nil, "ratelimited", nil)
	id2, _ := q.Enqueue(ctx, nil, "perm", nil)
	id3, _ := q.Enqueue(ctx, nil, "panic", nil, MaxAttempts(1))
	q.RunPending(ctx)
	j, _ := q.Get(ctx, id1)
	if j.State != StatePending || j.Attempts != 0 || !j.RunAt.Equal(clk.t.Add(5*time.Second)) {
		t.Fatalf("retry-after: %+v", j)
	}
	clk.add(5 * time.Second)
	q.RunPending(ctx)
	clk.add(5 * time.Second)
	q.RunPending(ctx)
	j, _ = q.Get(ctx, id1)
	if j.State != StateSucceeded || j.Attempts != 1 {
		t.Fatalf("after retries: %+v", j)
	}
	j, _ = q.Get(ctx, id2)
	if j.State != StateFailed || j.Attempts != 1 {
		t.Fatalf("permanent: %+v", j)
	}
	j, _ = q.Get(ctx, id3)
	if j.State != StateFailed || j.LastError == nil {
		t.Fatalf("panic: %+v", j)
	}
	// 未登録の種別は即失敗
	id4, _ := q.Enqueue(ctx, nil, "unknown", nil)
	q.RunPending(ctx)
	if j, _ := q.Get(ctx, id4); j.State != StateFailed {
		t.Fatalf("unknown kind: %+v", j)
	}
}

func TestUniqueKeyAndTx(t *testing.T) {
	ctx := context.Background()
	d := dbtest.New(t)
	q, _ := newQueue(t, d)
	q.Register("x", func(ctx context.Context, j *Job) error { return nil })
	id1, err := q.Enqueue(ctx, nil, "x", nil, UniqueKey("k"))
	if err != nil || id1 == 0 {
		t.Fatal(id1, err)
	}
	id2, err := q.Enqueue(ctx, nil, "x", nil, UniqueKey("k"))
	if err != nil || id2 != 0 {
		t.Fatalf("duplicate: %d %v", id2, err)
	}
	q.RunPending(ctx)
	if id3, _ := q.Enqueue(ctx, nil, "x", nil, UniqueKey("k")); id3 == 0 {
		t.Fatal("unique key must be released after success")
	}
	// ロールバックしたトランザクションのジョブは残らない
	_ = d.WithTx(ctx, func(tx *db.Tx) error {
		q.Enqueue(ctx, tx, "x", nil)
		return errors.New("rollback")
	})
	var cnt int
	d.Get(ctx, &cnt, `SELECT COUNT(*) FROM jobs WHERE state = 'pending'`)
	if cnt != 1 {
		t.Errorf("pending = %d", cnt)
	}
}

func TestRecoverStale(t *testing.T) {
	ctx := context.Background()
	d := dbtest.New(t)
	q, clk := newQueue(t, d)
	q.Register("x", func(ctx context.Context, j *Job) error { return nil })
	id, _ := q.Enqueue(ctx, nil, "x", nil)
	j, _ := q.claim(ctx)
	if j == nil || j.ID != id {
		t.Fatal("claim")
	}
	clk.add(20 * time.Minute)
	if err := q.RecoverStale(ctx); err != nil {
		t.Fatal(err)
	}
	if j, _ := q.Get(ctx, id); j.State != StatePending {
		t.Fatalf("state = %s", j.State)
	}
}

func TestWorkerLoop(t *testing.T) {
	d := dbtest.New(t)
	q := New(d, Options{Workers: 2, PollInterval: 10 * time.Millisecond})
	done := make(chan struct{}, 10)
	q.Register("x", func(ctx context.Context, j *Job) error { done <- struct{}{}; return nil })
	ctx, cancel := context.WithCancel(context.Background())
	go q.Run(ctx)
	defer cancel()
	for i := 0; i < 3; i++ {
		q.Enqueue(context.Background(), nil, "x", nil)
	}
	for i := 0; i < 3; i++ {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("timeout")
		}
	}
}

func TestScheduler(t *testing.T) {
	ctx := context.Background()
	d := dbtest.New(t)
	q, clk := newQueue(t, d)
	s := &Scheduler{Queue: q, Jobs: []Periodic{
		{Name: "cleanup", Kind: "cleanup", Schedule: Every(time.Hour), RunAtStart: true},
		{Name: "reminders", Kind: "reminders", Schedule: Daily{Hour: 8, Location: time.UTC}},
	}}
	next := []time.Time{clk.t, s.Jobs[1].Schedule.Next(clk.t)}
	if want := time.Date(2026, 1, 16, 8, 0, 0, 0, time.UTC); !next[1].Equal(want) {
		t.Fatalf("daily next = %v", next[1])
	}
	s.fire(ctx, next)
	count := func(kind string) int {
		var n int
		d.Get(ctx, &n, `SELECT COUNT(*) FROM jobs WHERE kind = ?`, kind)
		return n
	}
	if count("cleanup") != 1 || count("reminders") != 0 {
		t.Fatal("first fire")
	}
	// 未処理のうちは同じ periodic を二重に積まない
	clk.add(time.Hour)
	s.fire(ctx, next)
	if count("cleanup") != 1 {
		t.Fatal("duplicate periodic job")
	}
	clk.t = time.Date(2026, 1, 16, 8, 0, 1, 0, time.UTC)
	s.fire(ctx, next)
	if count("reminders") != 1 {
		t.Fatal("daily job not fired")
	}
	if _, err := ParseDaily("25:00", nil); err == nil {
		t.Error("ParseDaily must reject 25:00")
	}
}
