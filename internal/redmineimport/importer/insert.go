package importer

import (
	"context"
	"fmt"
	"strings"

	"github.com/mikuta0407/buropher/internal/db"
)

// maxParams は 1 文あたりのプレースホルダ数の上限(SQLite 32766 / PG 65535 より十分小さく)。
const (
	maxParams    = 30000
	maxBatchRows = 500
)

// inserter は複数行 INSERT でまとめて書き込む。
// PG では DEFERRABLE でない FK は文ごとに検査されるため、依存先テーブルの inserter は
// 先に flush しておくこと(テーブル単位で close すれば足りる)。
type inserter struct {
	ctx    context.Context
	tx     *db.Tx
	table  string
	cols   []string
	batch  int
	args   []any
	rows   int
	total  int64
	prefix string
	tuple  string
	onDone func(n int64)
}

func newInserter(ctx context.Context, tx *db.Tx, table string, cols ...string) *inserter {
	b := maxParams / len(cols)
	if b > maxBatchRows {
		b = maxBatchRows
	}
	if b < 1 {
		b = 1
	}
	return &inserter{
		ctx: ctx, tx: tx, table: table, cols: cols, batch: b,
		prefix: "INSERT INTO " + table + " (" + strings.Join(cols, ", ") + ") VALUES ",
		tuple:  "(" + strings.TrimSuffix(strings.Repeat("?, ", len(cols)), ", ") + ")",
	}
}

// add は 1 行を追加する(値の個数は列数と一致させる)。
func (in *inserter) add(vals ...any) error {
	if len(vals) != len(in.cols) {
		return fmt.Errorf("importer: %s: %d values for %d columns", in.table, len(vals), len(in.cols))
	}
	in.args = append(in.args, vals...)
	in.rows++
	if in.rows >= in.batch {
		return in.flush()
	}
	return nil
}

func (in *inserter) flush() error {
	if in.rows == 0 {
		return nil
	}
	var sb strings.Builder
	sb.Grow(len(in.prefix) + in.rows*(len(in.tuple)+2))
	sb.WriteString(in.prefix)
	for i := 0; i < in.rows; i++ {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(in.tuple)
	}
	if _, err := in.tx.Exec(in.ctx, sb.String(), in.args...); err != nil {
		// SQLite は文の失敗でトランザクションが中断しないので、1 行ずつ再実行して原因行を特定する
		// (どのみちロールバックするので途中の行が入っても問題ない)。
		if in.tx.Dialect().Name() == db.SQLite {
			n := len(in.cols)
			for i := 0; i < in.rows; i++ {
				vals := in.args[i*n : (i+1)*n]
				if _, rerr := in.tx.Exec(in.ctx, in.prefix+in.tuple, vals...); rerr != nil {
					return fmt.Errorf("importer: insert into %s (%s) values %v: %w", in.table, strings.Join(in.cols, ", "), vals, rerr)
				}
			}
		}
		return fmt.Errorf("importer: insert into %s: %w", in.table, err)
	}
	in.total += int64(in.rows)
	in.rows = 0
	in.args = in.args[:0]
	return nil
}

// close は残りを書き込み、挿入総数を返す。
func (in *inserter) close() (int64, error) {
	if err := in.flush(); err != nil {
		return in.total, err
	}
	if in.onDone != nil {
		in.onDone(in.total)
	}
	return in.total, nil
}
