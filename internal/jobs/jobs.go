// Package jobs は DB（jobs テーブル）を使う永続ジョブキューとワーカー、定期実行スケジューラ。
//
// Redmine は ActiveJob（既定はプロセス内 async アダプタ）でメールを deliver_later するが、
// プロセス終了でジョブが消えるため、buropher では jobs テーブルに積んで再送・失敗記録を行う。
//
//	q := jobs.New(db)
//	q.Register("notify.email", func(ctx context.Context, j *jobs.Job) error { ... })
//	q.Enqueue(ctx, tx, "notify.email", payload)           // 書き込みと同じトランザクションで積める
//	go q.Run(ctx)                                          // buropher serve が起動するワーカー
//
// ハンドラのエラーは指数バックオフで再試行し、max_attempts 回失敗したら state='failed'（デッドレター）にする。
// Retry(after, err) は回数を数えずに指定時間後へ延期（Discord の 429 等）、Permanent(err) は即座に失敗にする。
package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	mrand "math/rand/v2"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mikuta0407/buropher/internal/clock"
	"github.com/mikuta0407/buropher/internal/db"
)

// ジョブの状態。
const (
	StatePending   = "pending"
	StateRunning   = "running"
	StateSucceeded = "succeeded"
	StateFailed    = "failed"
)

// Job は jobs テーブルの 1 行。
type Job struct {
	ID          int64       `db:"id"`
	Queue       string      `db:"queue"`
	Kind        string      `db:"kind"`
	Payload     db.RawJSON  `db:"payload"`
	State       string      `db:"state"`
	Priority    int         `db:"priority"`
	RunAt       db.Time     `db:"run_at"`
	Attempts    int         `db:"attempts"`
	MaxAttempts int         `db:"max_attempts"`
	LastError   *string     `db:"last_error"`
	LockedBy    *string     `db:"locked_by"`
	LockedAt    db.NullTime `db:"locked_at"`
	UniqueKey   *string     `db:"unique_key"`
	CreatedAt   db.Time     `db:"created_at"`
	UpdatedAt   db.Time     `db:"updated_at"`
	FinishedAt  db.NullTime `db:"finished_at"`
}

// Decode は payload を v に読み込む。
func (j *Job) Decode(v any) error { return json.Unmarshal(j.Payload, v) }

// HandlerFunc はジョブ種別ごとの処理。
type HandlerFunc func(ctx context.Context, j *Job) error

// retryError は回数を数えずに延期するエラー。
type retryError struct {
	after time.Duration
	err   error
}

func (e *retryError) Error() string { return fmt.Sprintf("retry after %s: %v", e.after, e.err) }
func (e *retryError) Unwrap() error { return e.err }

// Retry は after 後に再実行する（attempts を増やさない。レート制限など）。
func Retry(after time.Duration, err error) error { return &retryError{after: after, err: err} }

// permanentError は再試行しないエラー。
type permanentError struct{ err error }

func (e *permanentError) Error() string { return "permanent: " + e.err.Error() }
func (e *permanentError) Unwrap() error { return e.err }

// Permanent は再試行せずに失敗（デッドレター）にする。
func Permanent(err error) error { return &permanentError{err: err} }

// IsPermanent は Permanent で包まれたエラーか。
func IsPermanent(err error) bool {
	var p *permanentError
	return errors.As(err, &p)
}

// Options はキューの設定。
type Options struct {
	// Workers はワーカー数（既定 2）。
	Workers int
	// PollInterval はポーリング間隔（既定 2 秒）。Enqueue 直後は待たずに起きる。
	PollInterval time.Duration
	// MaxAttempts は既定の最大試行回数（既定 10）。
	MaxAttempts int
	// BackoffBase / BackoffMax は再試行間隔（base * 2^(attempts-1)、上限 max。既定 30 秒 / 6 時間）。
	BackoffBase, BackoffMax time.Duration
	// NoJitter はバックオフの揺らぎ（±20%）を無効にする（テスト用）。
	NoJitter bool
	// LockTimeout は running のまま放置されたジョブを pending に戻すまでの時間（既定 15 分）。
	LockTimeout time.Duration
	// Queues は処理するキュー名（空なら全キュー）。
	Queues []string
}

// Queue はジョブキュー。
type Queue struct {
	DB     *db.DB
	Logger *slog.Logger
	// Now は現在時刻（nil なら clock.Now）。
	Now  func() time.Time
	Opts Options

	mu       sync.RWMutex
	handlers map[string]HandlerFunc
	wake     chan struct{}
	workerID string
	// claimSeq は取得ごとの連番（locked_by = workerID:連番。同じプロセスで取り直したジョブとも区別する）。
	claimSeq atomic.Int64
	running  sync.WaitGroup
}

// New はキューを作る。
func New(d *db.DB, opts ...Options) *Queue {
	q := &Queue{DB: d, handlers: map[string]HandlerFunc{}, wake: make(chan struct{}, 1)}
	if len(opts) > 0 {
		q.Opts = opts[0]
	}
	host, _ := os.Hostname()
	var b [4]byte
	_, _ = rand.Read(b[:])
	q.workerID = fmt.Sprintf("%s:%d:%s", host, os.Getpid(), hex.EncodeToString(b[:]))
	return q
}

func (q *Queue) now() time.Time {
	if q.Now != nil {
		return q.Now().UTC()
	}
	return clock.Now().UTC()
}

func (q *Queue) logger() *slog.Logger {
	if q.Logger != nil {
		return q.Logger
	}
	return slog.Default()
}

func (q *Queue) maxAttempts() int {
	if q.Opts.MaxAttempts > 0 {
		return q.Opts.MaxAttempts
	}
	return 10
}

// Register はジョブ種別の処理を登録する。
func (q *Queue) Register(kind string, fn HandlerFunc) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.handlers[kind] = fn
}

func (q *Queue) handler(kind string) HandlerFunc {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return q.handlers[kind]
}

// EnqueueOption は Enqueue の追加指定。
type EnqueueOption func(*enqueueOpts)

type enqueueOpts struct {
	queue       string
	priority    int
	runAt       time.Time
	maxAttempts int
	uniqueKey   string
}

// RunAt は実行予定時刻を指定する。
func RunAt(t time.Time) EnqueueOption { return func(o *enqueueOpts) { o.runAt = t } }

// Delay は実行を d だけ遅らせる。
func Delay(d time.Duration) EnqueueOption {
	return func(o *enqueueOpts) { o.runAt = o.runAt.Add(d) }
}

// Priority は優先度（大きいほど先）。
func Priority(p int) EnqueueOption { return func(o *enqueueOpts) { o.priority = p } }

// MaxAttempts は最大試行回数。
func MaxAttempts(n int) EnqueueOption { return func(o *enqueueOpts) { o.maxAttempts = n } }

// OnQueue はキュー名。
func OnQueue(name string) EnqueueOption { return func(o *enqueueOpts) { o.queue = name } }

// UniqueKey は重複投入防止キー（同じキーのジョブが pending / running の間は積まない）。
func UniqueKey(k string) EnqueueOption { return func(o *enqueueOpts) { o.uniqueKey = k } }

// ErrDuplicate は UniqueKey が重複して積まなかったことを示す（Enqueue は id 0 と nil を返す）。
var ErrDuplicate = errors.New("jobs: duplicate unique key")

// Enqueue はジョブを積む。tx が nil なら q.DB を使う（トランザクション中なら tx を渡すと、
// コミットされたときだけジョブが見える）。UniqueKey が重複したら (0, nil)。
func (q *Queue) Enqueue(ctx context.Context, tx db.Queryer, kind string, payload any, opts ...EnqueueOption) (int64, error) {
	if tx == nil {
		tx = q.DB
	}
	now := q.now()
	o := enqueueOpts{queue: "default", runAt: now, maxAttempts: q.maxAttempts()}
	for _, f := range opts {
		f(&o)
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}
	var uk *string
	if o.uniqueKey != "" {
		uk = &o.uniqueKey
		var n int
		if err := tx.Get(ctx, &n, `SELECT COUNT(*) FROM jobs WHERE unique_key = ? AND state IN ('pending', 'running')`, o.uniqueKey); err != nil {
			return 0, err
		}
		if n > 0 {
			return 0, nil
		}
	}
	id, err := tx.InsertReturningID(ctx, `INSERT INTO jobs (queue, kind, payload, state, priority, run_at, attempts, max_attempts, unique_key, created_at, updated_at)
VALUES (?, ?, ?, 'pending', ?, ?, 0, ?, ?, ?, ?)`, o.queue, kind, string(b), o.priority, db.NewTime(o.runAt), o.maxAttempts, uk, db.NewTime(now), db.NewTime(now))
	if err != nil {
		if uk != nil && strings.Contains(strings.ToLower(err.Error()), "unique") {
			return 0, nil
		}
		return 0, err
	}
	q.Notify()
	return id, nil
}

// Notify は待機中のワーカーを起こす。
func (q *Queue) Notify() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// Run は ctx がキャンセルされるまでワーカーを動かす。
func (q *Queue) Run(ctx context.Context) {
	n := q.Opts.Workers
	if n <= 0 {
		n = 2
	}
	if err := q.RecoverStale(ctx); err != nil && ctx.Err() == nil {
		q.logger().Error("jobs: recover stale failed", "err", err)
	}
	for i := 0; i < n; i++ {
		q.running.Add(1)
		go func() {
			defer q.running.Done()
			q.loop(ctx)
		}()
	}
	// プロセスが落ちて running のまま残ったジョブは起動時だけでなく定期的に戻す
	// （起動時点では LockTimeout を過ぎていないジョブが、次の再起動まで失われないように）。
	q.running.Add(1)
	go func() {
		defer q.running.Done()
		t := time.NewTicker(q.lockTimeout() / 2)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := q.RecoverStale(ctx); err != nil && ctx.Err() == nil {
					q.logger().Error("jobs: recover stale failed", "err", err)
				}
			}
		}
	}()
	q.running.Wait()
}

func (q *Queue) lockTimeout() time.Duration {
	if q.Opts.LockTimeout > 0 {
		return q.Opts.LockTimeout
	}
	return 15 * time.Minute
}

func (q *Queue) pollInterval() time.Duration {
	if q.Opts.PollInterval > 0 {
		return q.Opts.PollInterval
	}
	return 2 * time.Second
}

func (q *Queue) loop(ctx context.Context) {
	t := time.NewTicker(q.pollInterval())
	defer t.Stop()
	for {
		for {
			if ctx.Err() != nil {
				return
			}
			ok, err := q.RunOne(ctx)
			if err != nil {
				q.logger().Error("jobs: poll failed", "err", err)
				break
			}
			if !ok {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-q.wake:
		}
	}
}

// RecoverStale は LockTimeout を過ぎた running のジョブを pending に戻す（プロセスが落ちた場合）。
func (q *Queue) RecoverStale(ctx context.Context) error {
	lt := q.lockTimeout()
	now := q.now()
	_, err := q.DB.Exec(ctx, `UPDATE jobs SET state = 'pending', locked_by = NULL, locked_at = NULL, updated_at = ?
WHERE state = 'running' AND locked_at < ?`, db.NewTime(now), db.NewTime(now.Add(-lt)))
	return err
}

// claim は実行可能なジョブを 1 件取って running にする（無ければ nil）。
func (q *Queue) claim(ctx context.Context) (*Job, error) {
	for range 5 {
		now := q.now()
		args := []any{db.NewTime(now)}
		cond := ""
		if len(q.Opts.Queues) > 0 {
			cond = " AND queue IN (" + strings.TrimSuffix(strings.Repeat("?,", len(q.Opts.Queues)), ",") + ")"
			for _, n := range q.Opts.Queues {
				args = append(args, n)
			}
		}
		var ids []int64
		if err := q.DB.Select(ctx, &ids, `SELECT id FROM jobs WHERE state = 'pending' AND run_at <= ?`+cond+
			` ORDER BY priority DESC, run_at, id LIMIT 1`, args...); err != nil {
			return nil, err
		}
		if len(ids) == 0 {
			return nil, nil
		}
		res, err := q.DB.Exec(ctx, `UPDATE jobs SET state = 'running', locked_by = ?, locked_at = ?, attempts = attempts + 1, updated_at = ?
WHERE id = ? AND state = 'pending'`, q.workerID+":"+strconv.FormatInt(q.claimSeq.Add(1), 10), db.NewTime(now), db.NewTime(now), ids[0])
		if err != nil {
			return nil, err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			continue // 他のワーカーが取った
		}
		var j Job
		if err := q.DB.Get(ctx, &j, `SELECT * FROM jobs WHERE id = ?`, ids[0]); err != nil {
			return nil, err
		}
		return &j, nil
	}
	return nil, nil
}

// RunOne は実行可能なジョブを 1 件処理する（処理したら true）。
func (q *Queue) RunOne(ctx context.Context) (bool, error) {
	j, err := q.claim(ctx)
	if err != nil || j == nil {
		return false, err
	}
	stop := q.heartbeat(ctx, j)
	err = q.execute(ctx, j)
	stop()
	if err != nil && ctx.Err() != nil && !IsPermanent(err) {
		// 停止（ctx のキャンセル）で中断されたジョブは失敗として数えず、すぐに再実行できるよう戻す
		err = Retry(0, err)
	}
	// 結果の記録は停止中でも行う（記録できないと running のまま LockTimeout 後に再実行され、二重に実行される）
	fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	return true, q.finish(fctx, j, err)
}

// lockOwner は claim で記録した locked_by（finish・heartbeat はこれが一致する場合だけ更新する）。
func lockOwner(j *Job) string {
	if j.LockedBy == nil {
		return ""
	}
	return *j.LockedBy
}

// heartbeat は実行中のジョブの locked_at を定期的に更新する（LockTimeout より長く動くジョブが RecoverStale で
// pending に戻され、別のワーカーで二重に実行されないように）。戻り値で止める。
func (q *Queue) heartbeat(ctx context.Context, j *Job) (stop func()) {
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		t := time.NewTicker(q.lockTimeout() / 3)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				if _, err := q.DB.Exec(ctx, `UPDATE jobs SET locked_at = ? WHERE id = ? AND locked_by = ? AND state = 'running'`,
					db.NewTime(q.now()), j.ID, lockOwner(j)); err != nil && ctx.Err() == nil {
					q.logger().Warn("jobs: heartbeat failed", "id", j.ID, "err", err)
				}
			}
		}
	}()
	return func() { close(done); <-finished }
}

// RunPending は実行可能なジョブが無くなるまで同期的に処理する（テスト・CLI 用）。処理件数を返す。
func (q *Queue) RunPending(ctx context.Context) (int, error) {
	n := 0
	for {
		ok, err := q.RunOne(ctx)
		if err != nil {
			return n, err
		}
		if !ok {
			return n, nil
		}
		n++
	}
}

func (q *Queue) execute(ctx context.Context, j *Job) (err error) {
	h := q.handler(j.Kind)
	if h == nil {
		return Permanent(fmt.Errorf("jobs: no handler for %q", j.Kind))
	}
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v\n%s", r, debug.Stack())
		}
	}()
	return h(ctx, j)
}

// Backoff は attempts 回目の失敗後の待ち時間。
func (q *Queue) Backoff(attempts int) time.Duration {
	base := q.Opts.BackoffBase
	if base <= 0 {
		base = 30 * time.Second
	}
	maxD := q.Opts.BackoffMax
	if maxD <= 0 {
		maxD = 6 * time.Hour
	}
	// float64 のまま上限と比べる（Duration への変換で溢れると値が処理系依存になる）
	f := float64(base) * math.Pow(2, float64(max(attempts-1, 0)))
	d := maxD
	if f < float64(maxD) {
		d = time.Duration(f)
	}
	if !q.Opts.NoJitter {
		d = time.Duration(float64(d) * (0.8 + 0.4*mrand.Float64()))
	}
	return d
}

func (q *Queue) finish(ctx context.Context, j *Job, err error) error {
	now := q.now()
	if err == nil {
		_, e := q.DB.Exec(ctx, `UPDATE jobs SET state = 'succeeded', locked_by = NULL, locked_at = NULL, last_error = NULL, updated_at = ?, finished_at = ? WHERE id = ? AND locked_by = ?`,
			db.NewTime(now), db.NewTime(now), j.ID, lockOwner(j))
		return e
	}
	msg := err.Error()
	if len(msg) > 4000 {
		msg = msg[:4000]
	}
	var re *retryError
	switch {
	case errors.As(err, &re):
		// 回数を数えずに延期する
		_, e := q.DB.Exec(ctx, `UPDATE jobs SET state = 'pending', locked_by = NULL, locked_at = NULL, attempts = attempts - 1, last_error = ?, run_at = ?, updated_at = ? WHERE id = ? AND locked_by = ?`,
			msg, db.NewTime(now.Add(re.after)), db.NewTime(now), j.ID, lockOwner(j))
		q.logger().Info("jobs: rescheduled", "id", j.ID, "kind", j.Kind, "after", re.after, "err", re.err)
		return e
	case IsPermanent(err) || j.Attempts >= j.MaxAttempts:
		_, e := q.DB.Exec(ctx, `UPDATE jobs SET state = 'failed', locked_by = NULL, locked_at = NULL, last_error = ?, updated_at = ?, finished_at = ? WHERE id = ? AND locked_by = ?`,
			msg, db.NewTime(now), db.NewTime(now), j.ID, lockOwner(j))
		q.logger().Error("jobs: failed", "id", j.ID, "kind", j.Kind, "attempts", j.Attempts, "err", err)
		return e
	default:
		_, e := q.DB.Exec(ctx, `UPDATE jobs SET state = 'pending', locked_by = NULL, locked_at = NULL, last_error = ?, run_at = ?, updated_at = ? WHERE id = ? AND locked_by = ?`,
			msg, db.NewTime(now.Add(q.Backoff(j.Attempts))), db.NewTime(now), j.ID, lockOwner(j))
		q.logger().Warn("jobs: will retry", "id", j.ID, "kind", j.Kind, "attempts", j.Attempts, "err", err)
		return e
	}
}

// Get は id のジョブを返す。
func (q *Queue) Get(ctx context.Context, id int64) (*Job, error) {
	var j Job
	if err := q.DB.Get(ctx, &j, `SELECT * FROM jobs WHERE id = ?`, id); err != nil {
		return nil, err
	}
	return &j, nil
}

// PurgeFinished は finished_at が before より前の succeeded / failed のジョブを消す。
func (q *Queue) PurgeFinished(ctx context.Context, before time.Time) (int64, error) {
	res, err := q.DB.Exec(ctx, `DELETE FROM jobs WHERE state IN ('succeeded', 'failed') AND finished_at < ?`, db.NewTime(before))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
