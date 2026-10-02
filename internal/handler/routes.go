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
	a.routesWelcome(r)
	a.routesAccount(r)
	a.routesContextMenus(r)
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
	a.routesAttachments(r)
	a.routesWiki(r)
	a.routesWikis(r)
}
