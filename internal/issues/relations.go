package issues

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/domain"
)

type relationRow struct {
	ID           int64         `db:"id"`
	IssueFromID  int64         `db:"issue_from_id"`
	IssueToID    int64         `db:"issue_to_id"`
	RelationType string        `db:"relation_type"`
	Delay        sql.NullInt64 `db:"delay"`
}

func (r *relationRow) relation() *domain.IssueRelation {
	rel := &domain.IssueRelation{ID: r.ID, IssueFromID: r.IssueFromID, IssueToID: r.IssueToID, RelationType: r.RelationType}
	if r.Delay.Valid {
		d := int(r.Delay.Int64)
		rel.Delay = &d
	}
	return rel
}

func (e *Env) loadRelations(ctx context.Context, where string, args ...any) ([]*domain.IssueRelation, error) {
	var rows []relationRow
	if err := e.Q.Select(ctx, &rows, `SELECT id, issue_from_id, issue_to_id, relation_type, delay FROM issue_relations WHERE `+where+` ORDER BY id`, args...); err != nil {
		return nil, err
	}
	out := make([]*domain.IssueRelation, len(rows))
	for i := range rows {
		out[i] = rows[i].relation()
	}
	return out, nil
}

// relationsFrom は relations_from。
func (e *Env) relationsFrom(ctx context.Context, issueID int64) ([]*domain.IssueRelation, error) {
	return e.loadRelations(ctx, `issue_from_id = ?`, issueID)
}

// relationsTo は relations_to。
func (e *Env) relationsTo(ctx context.Context, issueID int64) ([]*domain.IssueRelation, error) {
	return e.loadRelations(ctx, `issue_to_id = ?`, issueID)
}

// relationsOf は issue.relations (from / to の両方、IssueRelation#<=> 順)。
func (e *Env) relationsOf(ctx context.Context, issueID int64) ([]*domain.IssueRelation, error) {
	rs, err := e.loadRelations(ctx, `issue_from_id = ? OR issue_to_id = ?`, issueID, issueID)
	if err != nil {
		return nil, err
	}
	slices.SortStableFunc(rs, domain.CompareRelations)
	return rs, nil
}

// Relations は issue.relations。
func (e *Env) Relations(ctx context.Context, iss *Issue) ([]*domain.IssueRelation, error) {
	if iss.ID == 0 {
		return nil, nil
	}
	return e.relationsOf(ctx, iss.ID)
}

// VisibleRelations は Issue.load_visible_relations の 1 件分 (相手が user に見える関連)。
func (e *Env) VisibleRelations(ctx context.Context, iss *Issue, u *domain.User) ([]*domain.IssueRelation, error) {
	rs, err := e.Relations(ctx, iss)
	if err != nil {
		return nil, err
	}
	var out []*domain.IssueRelation
	for _, r := range rs {
		other, err := e.Find(ctx, r.OtherIssueID(iss.ID))
		if err != nil {
			return nil, err
		}
		if other == nil {
			continue
		}
		ok, err := e.Visible(ctx, other, u)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, r)
		}
	}
	return out, nil
}

// FindRelation は find_relation(relation_id) (このチケットの関連のみ。無ければ nil)。
func (e *Env) FindRelation(ctx context.Context, iss *Issue, relationID int64) (*domain.IssueRelation, error) {
	rs, err := e.loadRelations(ctx, `id = ? AND (issue_to_id = ? OR issue_from_id = ?)`, relationID, iss.ID, iss.ID)
	if err != nil || len(rs) == 0 {
		return nil, err
	}
	return rs[0], nil
}

// Duplicates は duplicates (このチケットを duplicates しているチケット)。
func (e *Env) Duplicates(ctx context.Context, iss *Issue) ([]*Issue, error) {
	rs, err := e.relationsTo(ctx, iss.ID)
	if err != nil {
		return nil, err
	}
	var out []*Issue
	for _, r := range rs {
		if r.RelationType == domain.RelationDuplicates {
			x, err := e.Find(ctx, r.IssueFromID)
			if err != nil {
				return nil, err
			}
			if x != nil {
				out = append(out, x)
			}
		}
	}
	return out, nil
}

// Blocks は blocks?(other) (このチケットが other を (推移的に) ブロックしているか)。
func (e *Env) Blocks(ctx context.Context, iss *Issue, otherID int64) (bool, error) {
	all := []int64{iss.ID}
	last := []int64{iss.ID}
	for len(last) > 0 {
		var cur []int64
		for _, id := range last {
			rs, err := e.relationsFrom(ctx, id)
			if err != nil {
				return false, err
			}
			for _, r := range rs {
				if r.RelationType == domain.RelationBlocks && !containsID(cur, r.IssueToID) {
					cur = append(cur, r.IssueToID)
				}
			}
		}
		cur = slices.DeleteFunc(cur, func(id int64) bool { return containsID(last, id) || containsID(all, id) })
		if containsID(cur, otherID) {
			return true, nil
		}
		last = cur
		all = append(all, cur...)
	}
	return false, nil
}

// WouldReschedule は would_reschedule?(other) (このチケットの日付が変わると other が再スケジュールされうるか)。
func (e *Env) WouldReschedule(ctx context.Context, iss *Issue, otherID int64) (bool, error) {
	start := iss.ID
	all := []int64{start}
	last := []int64{start}
	for len(last) > 0 {
		var cur []int64
		add := func(id int64) {
			if !containsID(cur, id) {
				cur = append(cur, id)
			}
		}
		for _, id := range last {
			if id == 0 {
				continue
			}
			rs, err := e.relationsFrom(ctx, id)
			if err != nil {
				return false, err
			}
			for _, r := range rs {
				if r.RelationType == domain.RelationPrecedes {
					add(r.IssueToID)
				}
			}
			var leaves []int64
			root, path, ok, err := e.dbPath(ctx, id)
			if err != nil {
				return false, err
			}
			if ok {
				if err := e.Q.Select(ctx, &leaves, `SELECT id FROM issues WHERE root_id = ? AND hier_path LIKE ? AND id <> ?
AND NOT EXISTS (SELECT 1 FROM issues c WHERE c.parent_id = issues.id) ORDER BY hier_path`, root, path+"%", id); err != nil {
					return false, err
				}
				for _, l := range leaves {
					add(l)
				}
				for _, a := range pathIDs(path, id) {
					ars, err := e.relationsFrom(ctx, a)
					if err != nil {
						return false, err
					}
					for _, r := range ars {
						if r.RelationType == domain.RelationPrecedes {
							add(r.IssueToID)
						}
					}
				}
			}
		}
		cur = slices.DeleteFunc(cur, func(id int64) bool { return containsID(last, id) || containsID(all, id) })
		if containsID(cur, otherID) {
			return true, nil
		}
		last = cur
		all = append(all, cur...)
	}
	return false, nil
}

// successorSoonestStart は IssueRelation#successor_soonest_start。
func (e *Env) successorSoonestStart(ctx context.Context, r *domain.IssueRelation, from *Issue) (*time.Time, error) {
	if r.RelationType != domain.RelationPrecedes || r.Delay == nil {
		return nil, nil
	}
	if from == nil {
		var err error
		if from, err = e.Find(ctx, r.IssueFromID); err != nil || from == nil {
			return nil, err
		}
	}
	base := from.DueDate
	if base == nil {
		base = from.StartDate
	}
	if base == nil {
		return nil, nil
	}
	t := e.AddWorkingDays(*base, 1+*r.Delay)
	return &t, nil
}

// SoonestStart は soonest_start (先行チケットと (日付が派生する場合の) 親から決まる最も早い開始日)。
func (e *Env) SoonestStart(ctx context.Context, iss *Issue) (*time.Time, error) {
	if iss.soonestStartStub != nil {
		return iss.soonestStartStub, nil
	}
	var dates []time.Time
	if iss.ID != 0 {
		rs, err := e.relationsTo(ctx, iss.ID)
		if err != nil {
			return nil, err
		}
		for _, r := range rs {
			d, err := e.successorSoonestStart(ctx, r, nil)
			if err != nil {
				return nil, err
			}
			if d != nil {
				dates = append(dates, *d)
			}
		}
	}
	var p *Issue
	if iss.parentIssueSet {
		p = iss.parentIssue
	} else if iss.ParentID != nil {
		var err error
		if p, err = e.Find(ctx, *iss.ParentID); err != nil {
			return nil, err
		}
	}
	if p != nil && (e.Settings == nil || e.Settings.String("parent_issue_dates") == "derived") {
		d, err := e.SoonestStart(ctx, p)
		if err != nil {
			return nil, err
		}
		if d != nil {
			dates = append(dates, *d)
		}
	}
	if len(dates) == 0 {
		return nil, nil
	}
	m := dates[0]
	for _, d := range dates[1:] {
		if d.After(m) {
			m = d
		}
	}
	return &m, nil
}

// RescheduleOn は reschedule_on(date): 開始日を date (稼働日) にし、稼働日数を保って期日を動かす。
func (e *Env) RescheduleOn(iss *Issue, date time.Time) {
	wd := e.WorkingDuration(iss)
	date = e.NextWorkingDate(date)
	iss.SetStartDate(&date)
	due := e.AddWorkingDays(date, wd)
	iss.SetDueDate(&due)
}

// rescheduleOnBang は reschedule_on!(date, journal)。
func (e *Env) rescheduleOnBang(ctx context.Context, iss *Issue, date time.Time, journal *Journal, st *saveState) error {
	leaf, err := e.Leaf(ctx, iss)
	if err != nil {
		return err
	}
	derived, err := e.DatesDerived(ctx, iss)
	if err != nil {
		return err
	}
	if leaf || !derived {
		if iss.StartDate == nil || !iss.StartDate.Equal(date) {
			if iss.StartDate != nil && iss.StartDate.After(date) {
				ss, err := e.SoonestStart(ctx, iss)
				if err != nil {
					return err
				}
				if ss != nil && ss.After(date) {
					date = *ss
				}
			}
			if journal != nil {
				u, err := e.UserByID(ctx, journal.UserID)
				if err != nil {
					return err
				}
				if _, err := e.InitJournal(ctx, iss, u, ""); err != nil {
					return err
				}
			}
			e.RescheduleOn(iss, date)
			if _, err := e.save(ctx, iss, true, st); err != nil {
				if !errors.Is(err, ErrStale) {
					return err
				}
				if err := e.Reload(ctx, iss); err != nil {
					return err
				}
				e.RescheduleOn(iss, date)
				if _, err := e.save(ctx, iss, true, st); err != nil {
					return err
				}
			}
		}
		return nil
	}
	leaves, err := e.Leaves(ctx, iss)
	if err != nil {
		return err
	}
	for _, l := range leaves {
		if l.StartDate != nil {
			if (iss.StartDate != nil && iss.StartDate.Equal(*l.StartDate)) || date.After(*l.StartDate) {
				if err := e.rescheduleOnBang(ctx, l, date, nil, st); err != nil {
					return err
				}
			}
		} else if err := e.rescheduleOnBang(ctx, l, date, nil, st); err != nil {
			return err
		}
	}
	return nil
}

// RescheduleOnAndSave は reschedule_on!(date) を単独で実行する。
func (e *Env) RescheduleOnAndSave(ctx context.Context, iss *Issue, date time.Time) (*SaveResult, error) {
	st := &saveState{result: &SaveResult{}}
	err := e.inTx(ctx, func() error {
		if err := e.rescheduleOnBang(ctx, iss, dateOnly(date), nil, st); err != nil {
			return err
		}
		return e.runCommitCallbacks(ctx, st)
	})
	return st.result, err
}

// setIssueToDates は IssueRelation#set_issue_to_dates(journal)。from は関連元チケット (nil なら読み込む)、
// to は再スケジュールするチケット (nil なら読み込む)。
func (e *Env) setIssueToDatesWith(ctx context.Context, r *domain.IssueRelation, from, to *Issue, journal *Journal, st *saveState) error {
	ss, err := e.successorSoonestStart(ctx, r, from)
	if err != nil || ss == nil {
		return err
	}
	if to == nil {
		if to, err = e.Find(ctx, r.IssueToID); err != nil || to == nil {
			return err
		}
	}
	return e.rescheduleOnBang(ctx, to, *ss, journal, st)
}

func (e *Env) setIssueToDates(ctx context.Context, r *domain.IssueRelation, from *Issue, journal *Journal, st *saveState) error {
	// 関連元は DB から読み直したもの (Redmine の relation.issue_from も別インスタンス)
	return e.setIssueToDatesWith(ctx, r, nil, nil, journal, st)
}

// ---------------------------------------------------------------- 関連の作成・削除

// RelationParams は関連の作成パラメータ (IssueRelation#safe_attributes=)。
type RelationParams struct {
	// IssueToID は "12" / "#12" / "12, 13" (複数は ApiController 以外の IssueRelationsController#create)。
	IssueToID    string
	RelationType string
	Delay        string
}

// NewRelation は IssueRelation.new(issue_from: from) + safe_attributes= (関連先は user に見えるものだけ)。
// 戻り値の Relation は CreateRelation で保存する。
func (e *Env) NewRelation(ctx context.Context, from *Issue, p RelationParams, u *domain.User) (*Relation, error) {
	r := &Relation{From: from}
	r.RelationType = p.RelationType
	if r.RelationType == "" {
		r.RelationType = domain.RelationRelates
	}
	if strings.TrimSpace(p.Delay) != "" {
		r.delayRaw = p.Delay
		if n := castID(p.Delay); n != nil {
			d := int(*n)
			r.Delay = &d
		}
	}
	if m := parentIDRe.FindStringSubmatch(strings.TrimSpace(p.IssueToID)); m != nil {
		id, _ := strconv.ParseInt(m[1], 10, 64)
		to, err := e.Find(ctx, id)
		if err != nil {
			return nil, err
		}
		if to != nil {
			ok, err := e.Visible(ctx, to, u)
			if err != nil {
				return nil, err
			}
			if ok {
				r.To = to
			}
		}
	}
	return r, nil
}

// Relation は作成中の関連 (関連元・先のチケットのインスタンスを保持する)。
type Relation struct {
	domain.IssueRelation
	From, To *Issue
	delayRaw string
	Errors   domain.ValidationErrors
}

// ValidateRelation は IssueRelation の検証。
func (e *Env) ValidateRelation(ctx context.Context, r *Relation) (bool, error) {
	r.Errors = domain.ValidationErrors{}
	if r.From == nil {
		r.Errors.Add("issue_from", "blank", nil)
	}
	if r.To == nil {
		r.Errors.Add("issue_to", "blank", nil)
	}
	if r.RelationType == "" {
		r.Errors.Add("relation_type", "blank", nil)
	}
	if _, ok := domain.RelationTypes[r.RelationType]; !ok {
		r.Errors.Add("relation_type", "inclusion", nil)
	}
	if r.delayRaw != "" && !numericRe.MatchString(strings.TrimSpace(r.delayRaw)) {
		r.Errors.Add("delay", "not_a_number", nil)
	}
	if r.From != nil && r.To != nil {
		var n int
		if err := e.Q.Get(ctx, &n, `SELECT COUNT(*) FROM issue_relations WHERE issue_from_id = ? AND issue_to_id = ? AND id <> ?`,
			r.From.ID, r.To.ID, r.ID); err != nil {
			return false, err
		}
		if n > 0 {
			r.Errors.Add("issue_to_id", "taken", nil)
		}
		errs, err := e.validateIssueRelation(ctx, &r.IssueRelation, r.From, r.To)
		if err != nil {
			return false, err
		}
		r.Errors.List = append(r.Errors.List, errs.List...)
	}
	return !r.Errors.Any(), nil
}

var numericRe = mustRe(`^[+-]?(\d+\.?\d*|\.\d+)([eE][+-]?\d+)?$`)

// validateIssueRelation は validate_issue_relation。
func (e *Env) validateIssueRelation(ctx context.Context, r *domain.IssueRelation, from, to *Issue) (domain.ValidationErrors, error) {
	var errs domain.ValidationErrors
	if from.ID == to.ID {
		errs.Add("issue_to_id", "invalid", nil)
	}
	if from.ProjectID != to.ProjectID && (e.Settings == nil || !e.Settings.Bool("cross_project_issue_relations")) {
		errs.Add("issue_to_id", "not_same_project", nil)
	}
	circ, err := e.circularDependency(ctx, r.RelationType, from, to, r.ID)
	if err != nil {
		return errs, err
	}
	if circ {
		errs.Add("base", "circular_dependency", nil)
	}
	d1, err := e.IsDescendantOf(ctx, from, to)
	if err != nil {
		return errs, err
	}
	d2, err := e.IsAncestorOf(ctx, from, to)
	if err != nil {
		return errs, err
	}
	if d1 || d2 {
		errs.Add("base", "cant_link_an_issue_with_a_descendant", nil)
	}
	return errs, nil
}

// circularDependency は circular_dependency?。
func (e *Env) circularDependency(ctx context.Context, typ string, from, to *Issue, selfID int64) (bool, error) {
	switch typ {
	case domain.RelationFollows:
		return e.WouldReschedule(ctx, from, to.ID)
	case domain.RelationPrecedes:
		return e.WouldReschedule(ctx, to, from.ID)
	case domain.RelationBlocked:
		return e.Blocks(ctx, from, to.ID)
	case domain.RelationBlocks:
		return e.Blocks(ctx, to, from.ID)
	case domain.RelationRelates:
		var n int
		err := e.Q.Get(ctx, &n, `SELECT COUNT(*) FROM issue_relations WHERE issue_from_id = ? AND issue_to_id = ?`, to.ID, from.ID)
		return n > 0, err
	}
	return false, nil
}

// relationValid は保存済み関連の valid? (update_nested_set_attributes_on_parent_change で使う)。
func (e *Env) relationValid(ctx context.Context, r *domain.IssueRelation) (bool, error) {
	from, err := e.Find(ctx, r.IssueFromID)
	if err != nil {
		return false, err
	}
	to, err := e.Find(ctx, r.IssueToID)
	if err != nil {
		return false, err
	}
	if from == nil || to == nil {
		return false, nil
	}
	errs, err := e.validateIssueRelation(ctx, r, from, to)
	if err != nil {
		return false, err
	}
	return !errs.Any(), nil
}

// InitRelationJournals は IssueRelation#init_journals(user)。
func (e *Env) InitRelationJournals(ctx context.Context, r *Relation, u *domain.User) error {
	for _, iss := range []*Issue{r.From, r.To} {
		if iss != nil {
			if _, err := e.InitJournal(ctx, iss, u, ""); err != nil {
				return err
			}
		}
	}
	return nil
}

// CreateRelation は関連を検証して保存する (handle_issue_order による反転と後続の再スケジュール、
// 両チケットへのジャーナル記録 (current_journal があれば) を含む)。検証エラーなら false。
func (e *Env) CreateRelation(ctx context.Context, r *Relation) (bool, *SaveResult, error) {
	st := &saveState{result: &SaveResult{}}
	ok := false
	err := e.inTx(ctx, func() error {
		return e.savepoint(ctx, func() error {
			var err error
			ok, err = e.createRelation(ctx, r, st)
			if err != nil {
				return err
			}
			if !ok {
				return errRollback
			}
			return e.runCommitCallbacks(ctx, st)
		})
	})
	if errors.Is(err, errRollback) {
		return false, st.result, nil
	}
	if err != nil {
		return false, nil, err
	}
	return ok, st.result, nil
}

func (e *Env) createRelation(ctx context.Context, r *Relation, st *saveState) (bool, error) {
	ok, err := e.ValidateRelation(ctx, r)
	if err != nil || !ok {
		return false, err
	}
	// handle_issue_order: reverse_if_needed
	if t := domain.RelationTypes[r.RelationType]; t.Reverse != "" {
		r.From, r.To = r.To, r.From
		r.RelationType = t.Reverse
	} else if r.RelationType == domain.RelationRelates && r.From.ID > r.To.ID {
		r.From, r.To = r.To, r.From
	}
	if r.RelationType == domain.RelationPrecedes {
		if r.Delay == nil {
			z := 0
			r.Delay = &z
		}
	} else {
		r.Delay = nil
	}
	r.IssueFromID, r.IssueToID = r.From.ID, r.To.ID
	// set_issue_to_dates (journal 引数なし。関連先インスタンスの current_journal に記録される)
	if err := e.setIssueToDatesWith(ctx, &r.IssueRelation, r.From, r.To, nil, st); err != nil {
		return false, err
	}
	var delay any
	if r.Delay != nil {
		delay = *r.Delay
	}
	id, err := e.Q.InsertReturningID(ctx, `INSERT INTO issue_relations (issue_from_id, issue_to_id, relation_type, delay) VALUES (?, ?, ?, ?)`,
		r.IssueFromID, r.IssueToID, r.RelationType, delay)
	if err != nil {
		return false, err
	}
	r.ID = id
	// after_create: relation_added
	for _, iss := range []*Issue{r.From, r.To} {
		if j := iss.currentJournal; j != nil {
			j.JournalizeRelation(&r.IssueRelation, iss.ID, true)
			if _, err := e.saveJournal(ctx, j, iss, st); err != nil {
				return false, err
			}
		}
	}
	return true, nil
}

// DestroyRelation は関連を削除する。user が nil でなければ両チケットのジャーナルに記録する
// (IssueRelationsController#destroy の init_journals + destroy)。
func (e *Env) DestroyRelation(ctx context.Context, rel *domain.IssueRelation, u *domain.User) (*SaveResult, error) {
	st := &saveState{result: &SaveResult{}}
	err := e.inTx(ctx, func() error {
		var issues []*Issue
		for _, id := range []int64{rel.IssueFromID, rel.IssueToID} {
			iss, err := e.Find(ctx, id)
			if err != nil {
				return err
			}
			if iss != nil && u != nil {
				if _, err := e.InitJournal(ctx, iss, u, ""); err != nil {
					return err
				}
			}
			issues = append(issues, iss)
		}
		if _, err := e.Q.Exec(ctx, `DELETE FROM issue_relations WHERE id = ?`, rel.ID); err != nil {
			return err
		}
		for _, iss := range issues {
			if iss != nil && iss.currentJournal != nil {
				iss.currentJournal.JournalizeRelation(rel, iss.ID, false)
				if _, err := e.saveJournal(ctx, iss.currentJournal, iss, st); err != nil {
					return err
				}
			}
		}
		return e.runCommitCallbacks(ctx, st)
	})
	return st.result, err
}

// destroyRelation はジャーナルを付けずに関連を削除する (関連先のインスタンスに current_journal が無い場合)。
func (e *Env) destroyRelation(ctx context.Context, rel *domain.IssueRelation, st *saveState) error {
	_, err := e.Q.Exec(ctx, `DELETE FROM issue_relations WHERE id = ?`, rel.ID)
	return err
}

// RelationDeletable は IssueRelation#deletable?(user)。
func (e *Env) RelationDeletable(ctx context.Context, rel *domain.IssueRelation, u *domain.User) (bool, error) {
	from, err := e.Find(ctx, rel.IssueFromID)
	if err != nil {
		return false, err
	}
	to, err := e.Find(ctx, rel.IssueToID)
	if err != nil {
		return false, err
	}
	for _, x := range []*Issue{from, to} {
		if x != nil {
			ok, err := e.Visible(ctx, x, u)
			if err != nil || !ok {
				return false, err
			}
		}
	}
	if from == nil {
		return true, nil
	}
	p, err := e.ProjectOf(ctx, from)
	if err != nil {
		return false, err
	}
	if ok, err := e.allowedTo(ctx, u, "manage_issue_relations", p); err != nil || ok {
		return ok, err
	}
	if to == nil {
		return true, nil
	}
	p, err = e.ProjectOf(ctx, to)
	if err != nil {
		return false, err
	}
	return e.allowedTo(ctx, u, "manage_issue_relations", p)
}
