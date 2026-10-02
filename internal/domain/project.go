package domain

import (
	"slices"
	"time"

	"github.com/mikuta0407/buropher/internal/permission"
)

// プロジェクトのステータス (Project::STATUS_*)。
const (
	ProjectStatusActive               = 1
	ProjectStatusClosed               = 5
	ProjectStatusArchived             = 9
	ProjectStatusScheduledForDeletion = 10
)

// Project は projects 行 (Redmine Project)。
type Project struct {
	ID                  int64
	ParentID            *int64
	Name                string
	Identifier          string
	Description         string
	Homepage            string
	IsPublic            bool
	Status              int
	InheritMembers      bool
	DefaultVersionID    *int64
	DefaultAssignedToID *int64
	DefaultIssueQueryID *int64
	CreatedAt           time.Time
	UpdatedAt           time.Time

	// EnabledModuleNames は有効なモジュール名 (project_modules)。
	// リポジトリが読み込む。nil は「未読込」ではなく「モジュールなし」として扱う。
	EnabledModuleNames []string
}

// Active は Project#active?。
func (p *Project) Active() bool { return p.Status == ProjectStatusActive }

// Closed は Project#closed?。
func (p *Project) Closed() bool { return p.Status == ProjectStatusClosed }

// Archived は Project#archived?。
func (p *Project) Archived() bool { return p.Status == ProjectStatusArchived }

// ScheduledForDeletion は Project#scheduled_for_deletion?。
func (p *Project) ScheduledForDeletion() bool {
	return p.Status == ProjectStatusScheduledForDeletion
}

// IsRoot は親を持たないなら true (Project#root?)。
func (p *Project) IsRoot() bool { return p.ParentID == nil }

// ModuleEnabled は Project#module_enabled?。
func (p *Project) ModuleEnabled(name string) bool {
	return slices.Contains(p.EnabledModuleNames, name)
}

// AllowedPermissions は有効モジュールに属する権限とモジュール外権限の名前一覧
// (Project#allowed_permissions)。
func (p *Project) AllowedPermissions() []string {
	ps := permission.ModulesPermissions(p.EnabledModuleNames)
	names := make([]string, len(ps))
	for i, x := range ps {
		names[i] = x.Name
	}
	return names
}

// AllowsTo は Project#allows_to?(action) の移植。
// アーカイブ済みは常に false、アクティブでない (クローズ・削除予約) 場合は read 権限のみ、
// さらに権限 (またはアクション) が有効モジュールで許可されていること。
func (p *Project) AllowsTo(a Action) bool {
	if p.Archived() {
		return false
	}
	if !p.Active() && !a.IsRead() {
		return false
	}
	if a.IsPermission() {
		perm := permission.Get(a.Permission)
		if perm == nil {
			// AccessControl に存在しない権限は allowed_permissions に含まれない
			return false
		}
		return perm.Module == "" || p.ModuleEnabled(perm.Module)
	}
	for _, perm := range permission.ModulesPermissions(p.EnabledModuleNames) {
		if perm.Allows(a.Controller, a.Action) {
			return true
		}
	}
	return false
}

// Action は権限判定の対象。Redmine の allowed_to? が受け取る
// 「権限シンボル」または「:controller/:action の Hash」に相当する。
type Action struct {
	Permission string // 権限名 (シンボル形式)。空ならアクション形式
	Controller string
	Action     string
}

// Perm は権限名の Action を返す (例 Perm("view_issues"))。
func Perm(name string) Action { return Action{Permission: name} }

// ControllerAction は controller/action 形式の Action を返す (例 ControllerAction("issues", "index"))。
func ControllerAction(controller, action string) Action {
	return Action{Controller: controller, Action: action}
}

// IsPermission は権限シンボル形式なら true。
func (a Action) IsPermission() bool { return a.Permission != "" }

// IsRead は AccessControl.read_action?(action)。
func (a Action) IsRead() bool {
	if a.IsPermission() {
		return permission.IsReadPermission(a.Permission)
	}
	return permission.IsReadAction(a.Controller, a.Action)
}

// String は表示用の文字列。
func (a Action) String() string {
	if a.IsPermission() {
		return a.Permission
	}
	return a.Controller + "/" + a.Action
}

// EnabledModule は project_modules 行 (旧 enabled_modules)。
type EnabledModule struct {
	ID        int64
	ProjectID int64
	Name      string
}
