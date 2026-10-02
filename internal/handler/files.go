package handler

import (
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/mikuta0407/buropher/internal/apibuilder"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/query"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/view"
)

// FilesController（app/controllers/files_controller.rb）。
//
//	menu_item :files
//	before_action :find_project_by_project_id
//	before_action :authorize
//	accept_api_auth :index, :create
var FilesController = &Controller{Name: "files", MainMenu: true}

// routesFiles は files コントローラのルートを登録する（resources :files, :only => [:index, :new, :create]）。
func (a *App) routesFiles(r Router) {
	f := FilesController
	a.Handle(r, http.MethodGet, "/projects/{project_id}/files", f, "index", a.FilesIndex, FindProjectByProjectID(), Authorize(), AcceptAPIAuth())
	a.Handle(r, http.MethodPost, "/projects/{project_id}/files", f, "create", a.FilesCreate, FindProjectByProjectID(), Authorize(), AcceptAPIAuth())
	a.Handle(r, http.MethodGet, "/projects/{project_id}/files/new", f, "new", a.FilesNew, FindProjectByProjectID(), Authorize())
}

// ---------------------------------------------------------------- SortHelper

// sortState は SortHelper（sort_init / sort_update / sort_clause）の状態。
type sortState struct {
	Criteria query.SortCriteria
	columns  map[string][]string
}

var reSortFixedOrder = regexp.MustCompile(`(?i) (asc|desc)$`)

// sortUpdate は sort_init(def) と sort_update(columns)（params[:sort] || session[sort_name] || 既定）。
func (c *Req) sortUpdate(def query.SortCriteria, columns map[string][]string) *sortState {
	name := c.Controller.Name + "_" + c.Action + "_sort"
	var crit query.SortCriteria
	sess := c.Session()
	switch {
	case c.Params().Present("sort"):
		crit = query.ParseSortCriteria(c.Params().String("sort"))
	case sess != nil && sess.Has(name) && sess.GetString(name) != "":
		crit = query.ParseSortCriteria(sess.GetString(name))
	default:
		crit = query.ParseSortCriteria(def.ToParam())
	}
	if sess != nil {
		sess.Set(name, crit.ToParam())
	}
	return &sortState{Criteria: crit, columns: columns}
}

// Clause は sort_clause（ORDER BY の断片の列。空なら nil）。
func (s *sortState) Clause() []string {
	var out []string
	for _, kv := range s.Criteria {
		cols, ok := s.columns[kv[0]]
		if !ok {
			continue
		}
		for _, col := range cols {
			if reSortFixedOrder.MatchString(col) {
				out = append(out, col)
			} else {
				out = append(out, col+" "+strings.ToUpper(kv[1]))
			}
		}
	}
	return out
}

// ---------------------------------------------------------------- actions

// fileContainer は index の @containers の 1 要素（プロジェクトまたはバージョン）。
type fileContainer struct {
	Version     *domain.Version
	Attachments []*domain.Attachment
}

// FilesIndex は files#index。
func (a *App) FilesIndex(c *Req) {
	st := c.sortUpdate(query.SortCriteria{{"filename", "asc"}}, map[string][]string{
		"filename":   {"attachments.filename"},
		"created_on": {"attachments.created_at"},
		"size":       {"attachments.filesize"},
		"downloads":  {"attachments.downloads"},
	})
	order := st.Clause()
	patts, err := repository.SortedContainerAttachments(c.Ctx(), a.DB, domain.AttachmentContainerProject, c.Project.ID, order)
	if err != nil {
		a.internalError(c, "project files", err)
		return
	}
	containers := []fileContainer{{Attachments: patts}}
	versions, err := repository.ProjectOwnVersions(c.Ctx(), a.DB, c.Project.ID)
	if err != nil {
		a.internalError(c, "versions", err)
		return
	}
	// @project.versions....to_a.sort.reverse
	sort.SliceStable(versions, func(i, j int) bool { return domain.CompareVersions(versions[i], versions[j]) > 0 })
	for _, v := range versions {
		atts, err := repository.SortedContainerAttachments(c.Ctx(), a.DB, domain.AttachmentContainerVersion, v.ID, order)
		if err != nil {
			a.internalError(c, "version files", err)
			return
		}
		containers = append(containers, fileContainer{Version: v, Attachments: atts})
	}
	switch httpx.Negotiate(c.R, "html", "xml", "json") {
	case "html":
		opts := RenderOptions{}
		if httpx.IsXHR(c.R) {
			opts.Layout = view.NoLayout
		}
		c.Render("files/index", map[string]any{
			"Containers":    containers,
			"Sort":          st.Criteria,
			"DeleteAllowed": c.AllowedTo(domain.Perm("manage_files"), c.Project),
			"ManageAllowed": c.AllowedTo(domain.Perm("manage_files"), c.Project),
		}, opts)
	case "xml", "json":
		c.RenderAPI(0, func(b apibuilder.Builder) {
			b.Array("files", nil, func() {
				for _, ct := range containers {
					for _, att := range ct.Attachments {
						b.Object("file", func() {
							renderAPIAttachmentAttributes(c, b, att)
							if ct.Version != nil {
								b.Attrs("version", apibuilder.A("id", ct.Version.ID, "name", ct.Version.Name))
							}
							b.Value("digest", nilIfEmptyString(att.Digest))
							b.Value("downloads", att.Downloads)
						})
					}
				}
			})
		})
	default:
		respondNotAcceptable(c)
	}
}

// FilesNew は files#new。
func (a *App) FilesNew(c *Req) {
	a.renderFilesNew(c, nil)
}

func (a *App) renderFilesNew(c *Req, opts *RenderOptions) {
	versions, err := repository.ProjectOwnVersions(c.Ctx(), a.DB, c.Project.ID)
	if err != nil {
		a.internalError(c, "versions", err)
		return
	}
	// @project.versions.sorted（Version.sorted は effective_date, name の順。期日なしは後ろ）
	sort.SliceStable(versions, func(i, j int) bool { return domain.CompareVersions(versions[i], versions[j]) < 0 })
	o := RenderOptions{}
	if opts != nil {
		o = *opts
	}
	c.Render("files/new", map[string]any{"Versions": versions}, o)
}

// FilesCreate は files#create。
func (a *App) FilesCreate(c *Req) {
	p := c.Params()
	versionID := p.String("version_id")
	if versionID == "" {
		if f := p.Map("file"); f != nil {
			versionID = f.String("version_id")
		}
	}
	kind, cid := domain.AttachmentContainerProject, c.Project.ID
	found := true
	if strings.TrimSpace(versionID) != "" {
		// @project.versions.find_by_id(version_id)（無ければ container は nil になり、添付は保存されない）
		v, err := repository.ProjectOwnVersion(c.Ctx(), a.DB, c.Project.ID, httpx.RubyToI(versionID))
		if err != nil {
			found = false
		} else {
			kind, cid = domain.AttachmentContainerVersion, v.ID
		}
	}
	// params[:attachments] || (params[:file] && params[:file][:token] && params)
	var atts any = paramAttachments(c)
	if atts == nil {
		if f := p.Map("file"); f != nil && f.Present("token") {
			atts = []any{f}
		}
	}
	var files []*domain.Attachment
	if found {
		res, err := a.AttachmentStore.AttachFiles(c.Ctx(), a.DB, kind, cid, atts, c.User, c.Loc)
		if err != nil {
			a.internalError(c, "attach files", err)
			return
		}
		c.AttachFilesWarning(res)
		files = res.Files
	}
	api := httpx.IsAPIRequest(c.R)
	if len(files) > 0 {
		a.notify(c, "file_added", "attachments_added", files)
		if api {
			c.RenderAPIOK()
			return
		}
		c.Flash().SetNotice(c.L("label_file_added"))
		c.Redirect("/projects/" + c.Project.Identifier + "/files")
		return
	}
	if api {
		httpx.Head(c.W, c.R, http.StatusBadRequest)
		c.Halt()
		return
	}
	c.Flash().Now("error", c.L("label_attachment")+" "+c.L("activerecord.errors.messages.invalid"))
	a.renderFilesNew(c, nil)
}
