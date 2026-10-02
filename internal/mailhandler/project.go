package mailhandler

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/repository"
)

// このファイルは新しいチケットの対象プロジェクトの決定（target_project）。

// projectFromReceiverAddresses は get_project_from_receiver_addresses（To / Cc / Bcc のうち
// project_from_subaddress のサブアドレス "user+<識別子>@domain" からプロジェクトを決める）。
func (r *receiver) projectFromReceiverAddresses(ctx context.Context, q db.Queryer) (*domain.Project, error) {
	parts := strings.Split(r.opts.projectFromSubaddress, "@")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return nil, nil
	}
	local, dom := parts[0], parts[1]
	re := regexp.MustCompile(`\A` + regexp.QuoteMeta(local) + `\+([^+]+)\z`)
	for _, field := range [][]Address{r.email.To(), r.email.Cc(), r.email.Bcc()} {
		for _, a := range field {
			if !strings.EqualFold(a.Domain, dom) {
				continue
			}
			m := re.FindStringSubmatch(a.Local)
			if m == nil {
				continue
			}
			p, err := findProjectByIdentifier(ctx, q, &m[1])
			if err != nil {
				return nil, err
			}
			if p != nil {
				return p, nil
			}
		}
	}
	return nil, nil
}

// findProjectByIdentifier は Project.find_by_identifier（無ければ nil）。
func findProjectByIdentifier(ctx context.Context, q db.Queryer, identifier *string) (*domain.Project, error) {
	if identifier == nil {
		return nil, nil
	}
	p, err := repository.FindProjectByIdentifier(ctx, q, *identifier)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, nil
	}
	return p, err
}

// targetProject は target_project（サブアドレス → 本文の Project: キーワード → 既定のプロジェクトの順）。
func (r *receiver) targetProject(ctx context.Context, q db.Queryer) (*domain.Project, error) {
	target, err := r.projectFromReceiverAddresses(ctx, q)
	if err != nil {
		return nil, err
	}
	if target == nil {
		if target, err = findProjectByIdentifier(ctx, q, r.getKeyword(sym("project"), "", nil)); err != nil {
			return nil, err
		}
	}
	if target == nil {
		// 不正なキーワードなら既定のプロジェクト
		if def := r.opts.issue["project"]; strings.TrimSpace(def) != "" {
			if target, err = findProjectByIdentifier(ctx, q, &def); err != nil {
				return nil, err
			}
		}
	}
	if target == nil {
		return nil, missingInformation("Unable to determine target project")
	}
	return target, nil
}
