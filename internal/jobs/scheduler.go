package jobs

import (
	"context"
	"fmt"
	"time"
)

// Schedule は次回実行時刻を決める。
type Schedule interface {
	// Next は after より後の最初の実行時刻。
	Next(after time.Time) time.Time
}

// Every は一定間隔の Schedule。
type Every time.Duration

// Next は after + 間隔。
func (e Every) Next(after time.Time) time.Time { return after.Add(time.Duration(e)) }

// Daily は毎日 Hour:Minute（Location の時刻。nil ならローカル時刻）の Schedule。
type Daily struct {
	Hour, Minute int
	Location     *time.Location
}

// Next は after より後の最初の Hour:Minute。
func (d Daily) Next(after time.Time) time.Time {
	loc := d.Location
	if loc == nil {
		loc = time.Local
	}
	a := after.In(loc)
	t := time.Date(a.Year(), a.Month(), a.Day(), d.Hour, d.Minute, 0, 0, loc)
	if !t.After(a) {
		t = t.AddDate(0, 0, 1)
	}
	return t
}

// ParseDaily は "HH:MM" を Daily にする。
func ParseDaily(s string, loc *time.Location) (Daily, error) {
	var h, m int
	if _, err := fmt.Sscanf(s, "%d:%d", &h, &m); err != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return Daily{}, fmt.Errorf("jobs: invalid time of day %q (want HH:MM)", s)
	}
	return Daily{Hour: h, Minute: m, Location: loc}, nil
}

// Periodic は定期実行するジョブ（実行時刻になると Kind のジョブを UniqueKey 付きで積む）。
type Periodic struct {
	// Name は識別名（UniqueKey "periodic:<Name>" に使う）。
	Name     string
	Kind     string
	Payload  any
	Schedule Schedule
	// RunAtStart は起動直後にも 1 回積む。
	RunAtStart bool
}

// Scheduler は Periodic を時刻どおりに積む（単一プロセスを想定。UniqueKey で二重投入を防ぐ）。
type Scheduler struct {
	Queue *Queue
	Jobs  []Periodic
	// Tick は時刻の確認間隔（既定 30 秒）。
	Tick time.Duration
}

// Run は ctx がキャンセルされるまで動く。
func (s *Scheduler) Run(ctx context.Context) {
	if len(s.Jobs) == 0 {
		return
	}
	tick := s.Tick
	if tick <= 0 {
		tick = 30 * time.Second
	}
	now := s.Queue.now()
	next := make([]time.Time, len(s.Jobs))
	for i, p := range s.Jobs {
		if p.RunAtStart {
			next[i] = now
		} else {
			next[i] = p.Schedule.Next(now)
		}
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		s.fire(ctx, next)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// fire は時刻を過ぎた Periodic を積み、次回時刻を進める。
func (s *Scheduler) fire(ctx context.Context, next []time.Time) {
	now := s.Queue.now()
	for i, p := range s.Jobs {
		if now.Before(next[i]) {
			continue
		}
		if _, err := s.Queue.Enqueue(ctx, nil, p.Kind, p.Payload, UniqueKey("periodic:"+p.Name)); err != nil {
			s.Queue.logger().Error("jobs: schedule failed", "name", p.Name, "err", err)
		}
		next[i] = p.Schedule.Next(now)
	}
}
