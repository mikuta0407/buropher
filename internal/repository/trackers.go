package repository

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
)

type trackerRow struct {
	ID                 int64          `db:"id"`
	Name               string         `db:"name"`
	Description        sql.NullString `db:"description"`
	Position           int            `db:"position"`
	IsInRoadmap        bool           `db:"is_in_roadmap"`
	DefaultStatusID    int64          `db:"default_status_id"`
	DisabledCoreFields string         `db:"disabled_core_fields"`
}

func loadTrackers(ctx context.Context, q db.Queryer, where string, args ...any) ([]*domain.Tracker, error) {
	if where == "" {
		where = "1=1"
	}
	var rows []trackerRow
	if err := q.Select(ctx, &rows, `SELECT id, name, description, position, is_in_roadmap, default_status_id, disabled_core_fields
  FROM trackers WHERE `+where+` ORDER BY position, id`, args...); err != nil {
		return nil, err
	}
	out := make([]*domain.Tracker, 0, len(rows))
	for _, r := range rows {
		t := &domain.Tracker{ID: r.ID, Name: r.Name, Description: r.Description.String, Position: r.Position,
			IsInRoadmap: r.IsInRoadmap, DefaultStatusID: r.DefaultStatusID}
		if r.DisabledCoreFields != "" {
			_ = json.Unmarshal([]byte(r.DisabledCoreFields), &t.DisabledCoreFields)
		}
		out = append(out, t)
	}
	return out, nil
}

// ListTrackers は Tracker.sorted（position 順）。
func ListTrackers(ctx context.Context, q db.Queryer) ([]*domain.Tracker, error) {
	return loadTrackers(ctx, q, "")
}

// TrackersByIDs は Tracker.where(:id => ids)（position 順。存在しない id は無視）。
func TrackersByIDs(ctx context.Context, q db.Queryer, ids []int64) ([]*domain.Tracker, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	where, args, err := db.In(`id IN (?)`, uniqIDs(ids))
	if err != nil {
		return nil, err
	}
	return loadTrackers(ctx, q, where, args...)
}

// GetTracker は Tracker.find(id)。
func GetTracker(ctx context.Context, q db.Queryer, id int64) (*domain.Tracker, error) {
	ts, err := loadTrackers(ctx, q, `id = ?`, id)
	if err != nil {
		return nil, err
	}
	if len(ts) == 0 {
		return nil, ErrNotFound
	}
	return ts[0], nil
}
