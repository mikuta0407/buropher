package issues

import (
	"context"
	"database/sql"
	"math"
	"math/big"
	"regexp"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/domain"
)

func mustRe(s string) *regexp.Regexp { return regexp.MustCompile(s) }

// kbSum は Ruby の Array#sum (Float は Kahan-Babuska の補正付き加算)。
func kbSum(vs []float64) float64 {
	if len(vs) == 0 {
		return 0
	}
	f, c := 0.0, 0.0
	for _, x := range vs {
		t := f + x
		if math.Abs(f) >= math.Abs(x) {
			c += (f - t) + x
		} else {
			c += (x - t) + f
		}
		f = t
	}
	return f + c
}

// TotalEstimatedHours は total_estimated_hours (葉は estimated_hours、親は User.current に見える子孫を含む合計)。
func (e *Env) TotalEstimatedHours(ctx context.Context, iss *Issue) (*float64, error) {
	leaf, err := e.Leaf(ctx, iss)
	if err != nil {
		return nil, err
	}
	if leaf {
		return iss.EstimatedHours, nil
	}
	u, err := e.currentUser(ctx)
	if err != nil {
		return nil, err
	}
	cond, err := e.authz(u).IssueVisibleCondition(ctx, authz.ConditionOptions{})
	if err != nil {
		return nil, err
	}
	root, path, _, err := e.dbPath(ctx, iss.ID)
	if err != nil {
		return nil, err
	}
	var hs []sql.NullFloat64
	if err := e.Q.Select(ctx, &hs, `SELECT issues.estimated_hours FROM issues JOIN projects ON projects.id = issues.project_id
WHERE issues.root_id = ? AND issues.hier_path LIKE ? AND `+cond+` ORDER BY issues.hier_path`, root, path+"%"); err != nil {
		return nil, err
	}
	var vals []float64
	for _, h := range hs {
		if h.Valid {
			vals = append(vals, h.Float64)
		}
	}
	// SQL の SUM は値が無ければ 0.0 (Rails の sum)
	s := 0.0
	for _, v := range vals {
		s += v
	}
	return &s, nil
}

// EstimatedRemainingHours は estimated_remaining_hours。
func (e *Env) EstimatedRemainingHours(ctx context.Context, iss *Issue) (float64, error) {
	dr, err := e.DoneRatio(ctx, iss)
	if err != nil {
		return 0, err
	}
	h := 0.0
	if iss.EstimatedHours != nil {
		h = *iss.EstimatedHours
	}
	return h * float64(100-dr) / 100, nil
}

// SpentHours は spent_hours。
func (e *Env) SpentHours(ctx context.Context, iss *Issue) (float64, error) {
	var h sql.NullFloat64
	err := e.Q.Get(ctx, &h, `SELECT SUM(hours) FROM time_entries WHERE issue_id = ?`, iss.ID)
	return h.Float64, err
}

// TotalSpentHours は total_spent_hours (自身と子孫の工数合計)。
func (e *Env) TotalSpentHours(ctx context.Context, iss *Issue) (float64, error) {
	root, path, ok, err := e.dbPath(ctx, iss.ID)
	if err != nil || !ok {
		return 0, err
	}
	var h sql.NullFloat64
	err = e.Q.Get(ctx, &h, `SELECT SUM(t.hours) FROM time_entries t JOIN issues ON issues.id = t.issue_id
WHERE issues.root_id = ? AND issues.hier_path LIKE ?`, root, path+"%")
	return h.Float64, err
}

// ratFromFloat は Rational(f.to_s)。
func ratFromFloat(f float64) *big.Rat {
	r, ok := new(big.Rat).SetString(RubyFloatToS(f))
	if !ok {
		return new(big.Rat)
	}
	return r
}

// recalculateAttributesFor は recalculate_attributes_for(issue_id): 子から優先度・日付・進捗率を導出して保存する。
func (e *Env) recalculateAttributesFor(ctx context.Context, issueID int64, st *saveState) error {
	p, err := e.Find(ctx, issueID)
	if err != nil || p == nil {
		return err
	}
	if d, err := e.PriorityDerived(ctx, p); err != nil {
		return err
	} else if d {
		var pos sql.NullInt64
		if err := e.Q.Get(ctx, &pos, `SELECT MAX(ip.position) FROM issues c JOIN issue_statuses s ON s.id = c.status_id
JOIN issue_priorities ip ON ip.id = c.priority_id WHERE c.parent_id = ? AND s.is_closed = ?`, p.ID, false); err != nil {
			return err
		}
		if pos.Valid {
			var id int64
			if err := e.Q.Get(ctx, &id, `SELECT id FROM issue_priorities WHERE position = ? ORDER BY id LIMIT 1`, pos.Int64); err != nil && !isNoRows(err) {
				return err
			} else if err == nil {
				p.PriorityID = id
			}
		} else if def, err := e.DefaultPriority(ctx); err != nil {
			return err
		} else if def != nil {
			p.PriorityID = def.ID
		}
	}
	if d, err := e.DatesDerived(ctx, p); err != nil {
		return err
	} else if d {
		var r struct {
			Start sql.NullString `db:"s"`
			Due   sql.NullString `db:"d"`
		}
		if err := e.Q.Get(ctx, &r, `SELECT MIN(start_date) AS s, MAX(due_date) AS d FROM issues WHERE parent_id = ?`, p.ID); err != nil {
			return err
		}
		start, _ := parseDateInput(firstN(r.Start.String, 10))
		due, _ := parseDateInput(firstN(r.Due.String, 10))
		if start != nil && due != nil && due.Before(*start) {
			start, due = due, start
		}
		p.SetStartDate(start)
		p.SetDueDate(due)
	}
	if d, err := e.DoneRatioDerived(ctx, p); err != nil {
		return err
	} else if d {
		skip := false
		if e.useStatusForDoneRatio() {
			s, err := e.StatusOf(ctx, p)
			if err != nil {
				return err
			}
			skip = s != nil && s.DefaultDoneRatio != nil
		}
		if !skip {
			children, err := e.Children(ctx, p)
			if err != nil {
				return err
			}
			if len(children) > 0 {
				totals := make([]float64, len(children))
				var withEst []float64
				for i, c := range children {
					t, err := e.TotalEstimatedHours(ctx, c)
					if err != nil {
						return err
					}
					if t != nil {
						totals[i] = *t
					}
					if totals[i] > 0 {
						withEst = append(withEst, totals[i])
					}
				}
				average := big.NewRat(1, 1)
				if len(withEst) > 0 {
					average = new(big.Rat).Quo(ratFromFloat(kbSum(withEst)), big.NewRat(int64(len(withEst)), 1))
				}
				done := new(big.Rat)
				for i, c := range children {
					est := ratFromFloat(totals[i])
					if est.Sign() <= 0 {
						est = average
					}
					closed, err := e.Closed(ctx, c)
					if err != nil {
						return err
					}
					ratio := 100
					if !closed {
						if ratio, err = e.DoneRatio(ctx, c); err != nil {
							return err
						}
					}
					done.Add(done, new(big.Rat).Mul(est, big.NewRat(int64(ratio), 1)))
				}
				den := new(big.Rat).Mul(average, big.NewRat(int64(len(children)), 1))
				progress := new(big.Rat).Quo(done, den)
				// floor
				q := new(big.Int).Div(progress.Num(), progress.Denom())
				p.DoneRatio = int(q.Int64())
			}
		}
	}
	_, err = e.save(ctx, p, false, st)
	return err
}

func firstN(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

var _ = domain.Issue{}
