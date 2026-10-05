// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

import (
	"github.com/go-chi/chi/v5"
)

// Router はルート登録先（chi.Router）。
type Router = chi.Router

// Routes は config/routes.rb のうち実装済みのルートを登録する。
// r はセッション等のミドルウェアを適用済みのルータ（ルーティング後の CSRF 検証は Handle が行う）。
//
// ルートはコントローラごとのファイルの routesXxx メソッドで登録し、ここではその呼び出しだけを
// 並べる（並列開発での衝突を避けるため。追加するときは 1 行足すだけにすること）。
// 順序は config/routes.rb の記述順に合わせる（chi は登録順に依存しないが、読み比べやすくするため）。
func (a *App) Routes(r Router) {
	a.routesOAuth(r)
	a.routesWelcome(r)
	a.routesAccount(r)
	a.routesContextMenus(r)
	a.routesImports(r)
	a.routesUsers(r)
	a.routesPrincipalMemberships(r)
	a.routesEmailAddresses(r)
	a.routesGroups(r)
	a.routesTrackers(r)
	a.routesIssueStatuses(r)
	a.routesRoles(r)
	a.routesEnumerations(r)
	a.routesProjects(r)
	a.routesMembers(r)
	a.routesProjectEnumerations(r)
	a.routesIssueCategories(r)
	a.routesAdminProjects(r)
	a.routesPreviews(r)
	a.routesCustomFields(r)
	a.routesWorkflows(r)
	a.routesActivities(r)
	a.routesSearch(r)
	a.routesTimelog(r)
	a.routesAttachments(r)
	a.routesWiki(r)
	a.routesWikis(r)
	a.routesIssues(r)
	a.routesIssuesWrite(r)
	a.routesQueries(r)
	a.routesJournals(r)
	a.routesVersions(r)
	a.routesMy(r)
	a.routesGantts(r)
	a.routesCalendars(r)
	a.routesReports(r)
	a.routesSettings(r)
	a.routesAdmin(r)
	a.routesAuthSources(r)
	a.routesNews(r)
	a.routesComments(r)
	a.routesDocuments(r)
	a.routesFiles(r)
	a.routesBoards(r)
	a.routesMessages(r)
	a.routesAttachmentsMore(r)
	a.routesReactions(r)
	a.routesIssueRelationsAndWatchers(r)
	a.routesIssuesBulk(r)
	a.routesJournalsWrite(r)
	a.routesDiscord(r)
	a.routesTwofa(r)
	a.routesMyIdentities(r)
	a.routesRepositories(r)
	a.routesSys(r)
	a.routesMailHandler(r)
	a.routesHelp(r)
	a.routesWebhooks(r)
}
