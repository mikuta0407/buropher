// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/repository"
)

// SysController（app/controllers/sys_controller.rb）。リポジトリ管理用の Web サービス
// （reposman.rb や post-receive フックからの /sys/fetch_changesets）。
//
// ApplicationController を継承しないため、ログイン・セッション・CSRF の検証は行わず、
// Setting.sys_api_enabled と Setting.sys_api_key（params[:key]）だけで認可する。
func (a *App) routesSys(r Router) {
	httpx.Route(r, http.MethodGet, "/sys/projects", a.sysHandler(a.SysProjects))
	httpx.Route(r, http.MethodPost, "/sys/projects/{id}/repository", a.sysHandler(a.SysCreateProjectRepository))
	httpx.Route(r, http.MethodGet, "/sys/fetch_changesets", a.sysHandler(a.SysFetchChangesets))
	httpx.Route(r, http.MethodPost, "/sys/fetch_changesets", a.sysHandler(a.SysFetchChangesets))
}

// sysHandler は check_enabled を適用したハンドラ。
func (a *App) sysHandler(fn func(w http.ResponseWriter, r *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := httpx.ParamsOf(r).String("key")
		want := a.Settings.String("sys_api_key")
		// buropher 独自（セキュリティ）: 鍵が未設定（空）なら key なし・空の key で通さない
		if !a.Settings.Bool("sys_api_enabled") || want == "" || subtle.ConstantTimeCompare([]byte(key), []byte(want)) != 1 {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("Access denied. Repository management WS is disabled or key is invalid."))
			return
		}
		fn(w, r)
	}
}

func writeSysJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

// SysProjects は sys#projects（リポジトリモジュールが有効な稼働中のプロジェクトと既定のリポジトリ）。
func (a *App) SysProjects(w http.ResponseWriter, r *http.Request) {
	ps, err := repository.SysProjects(r.Context(), a.DB)
	if err != nil {
		a.logger().Error("sys projects", "err", err)
		httpx.Head(w, r, http.StatusInternalServerError)
		return
	}
	var b strings.Builder
	b.WriteByte('[')
	for i, p := range ps {
		if i > 0 {
			b.WriteByte(',')
		}
		// as_json(:only => ...) は列の順（id, name, is_public, identifier, status）
		b.WriteString(`{"id":` + itoa(p.ID) + `,"name":` + jsonString(p.Name))
		if p.IsPublic {
			b.WriteString(`,"is_public":true`)
		} else {
			b.WriteString(`,"is_public":false`)
		}
		b.WriteString(`,"identifier":` + jsonString(p.Identifier) + `,"status":` + itoa(int64(p.Status)) + `,"repository":`)
		if p.RepoID.Valid {
			b.WriteString(`{"id":` + itoa(p.RepoID.Int64) + `,"url":` + jsonString(p.RepoURL.String) + `}`)
		} else {
			b.WriteString("null")
		}
		b.WriteByte('}')
	}
	b.WriteByte(']')
	writeSysJSON(w, http.StatusOK, b.String())
}

// SysCreateProjectRepository は sys#create_project_repository（Git のみ）。
func (a *App) SysCreateProjectRepository(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, err := repository.FindProject(ctx, a.DB, chi.URLParam(r, "id"))
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			httpx.Head(w, r, http.StatusNotFound)
			return
		}
		httpx.Head(w, r, http.StatusInternalServerError)
		return
	}
	has, err := repository.ProjectHasDefaultRepository(ctx, a.DB, p.ID)
	if err != nil {
		httpx.Head(w, r, http.StatusInternalServerError)
		return
	}
	if has {
		httpx.Head(w, r, http.StatusConflict)
		return
	}
	params := httpx.ParamsOf(r)
	if params.String("vendor") != "Git" {
		// 意図的な差異: Git 以外の SCM は作成できない（D-15）
		httpx.Head(w, r, http.StatusUnprocessableEntity)
		return
	}
	a.logger().Info("Repository for " + p.Name + " was reported to be created by " + httpx.RemoteIP(r) + ".")
	repo := &domain.Repository{ProjectID: p.ID, SCM: "git", Project: p}
	if m := params.Map("repository"); m != nil {
		repo.URL = strings.TrimSpace(m.String("url"))
		repo.Identifier = strings.TrimSpace(m.String("identifier"))
		repo.PathEncoding = m.String("path_encoding")
		repo.Login = m.String("login")
		if v, ok := m.StringOK("is_default"); ok {
			repo.IsDefault = castBool(v)
		}
	}
	if repo.URL == "" || len(repo.URL) > 255 || (repo.Identifier != "" && (!domain.RepositoryIdentifierRe.MatchString(repo.Identifier) ||
		domain.RepositoryIdentifierAllDigits.MatchString(repo.Identifier))) || !a.validRepositoryPath(repo) {
		httpx.Head(w, r, http.StatusUnprocessableEntity)
		return
	}
	taken, err := repository.ScmRepositoryIdentifierTaken(ctx, a.DB, p.ID, repo.Identifier, 0)
	if err != nil || taken {
		httpx.Head(w, r, http.StatusUnprocessableEntity)
		return
	}
	err = a.DB.WithTx(ctx, func(tx *db.Tx) error {
		repos, err := repository.ProjectScmRepositories(ctx, tx, p.ID)
		if err != nil {
			return err
		}
		if len(repos) == 0 {
			repo.IsDefault = true
		}
		if repo.IsDefault {
			if err := repository.ClearDefaultScmRepository(ctx, tx, p.ID); err != nil {
				return err
			}
		}
		repo.CreatedOn = a.now()
		return repository.InsertScmRepository(ctx, tx, repo)
	})
	if err != nil {
		a.logger().Error("sys create repository", "err", err)
		httpx.Head(w, r, http.StatusUnprocessableEntity)
		return
	}
	body, _ := json.Marshal(map[string]any{"repository-git": map[string]any{"id": repo.ID, "url": repo.URL}})
	writeSysJSON(w, http.StatusCreated, string(body))
}

var reDigitsOnly = regexp.MustCompile(`^\d*$`)

// SysFetchChangesets は sys#fetch_changesets（params[:id] のプロジェクト、無ければ全プロジェクト）。
func (a *App) SysFetchChangesets(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ids, err := repository.ActiveRepositoryProjectIDs(ctx, a.DB)
	if err != nil {
		httpx.Head(w, r, http.StatusInternalServerError)
		return
	}
	svc := a.scmService()
	anon, err := repository.AnonymousUser(ctx, a.DB)
	if err != nil {
		httpx.Head(w, r, http.StatusInternalServerError)
		return
	}
	if id := httpx.ParamsOf(r).String("id"); id != "" {
		var target int64
		if reDigitsOnly.MatchString(id) {
			n := httpx.RubyToI(id)
			for _, pid := range ids {
				if pid == n {
					target = pid
				}
			}
		} else if p, err := repository.FindProject(ctx, a.DB, id); err == nil {
			for _, pid := range ids {
				if pid == p.ID {
					target = pid
				}
			}
		}
		if target == 0 {
			httpx.Head(w, r, http.StatusNotFound)
			return
		}
		ids = []int64{target}
	}
	for _, pid := range ids {
		if err := svc.FetchProject(ctx, pid, anon); err != nil {
			a.logger().Error("sys fetch changesets", "project_id", pid, "err", err)
		}
	}
	httpx.Head(w, r, http.StatusOK)
}
