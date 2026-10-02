package handler

import (
	"net/http"
	"regexp"
	"strconv"

	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/textformat/redmine"
	"github.com/mikuta0407/buropher/internal/view"
)

// PreviewsController（app/controllers/previews_controller.rb）。
var PreviewsController = &Controller{Name: "previews"}

// routesPreviews は previews コントローラのルートを登録する。
func (a *App) routesPreviews(r Router) {
	methods := []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch}
	for _, m := range methods {
		// match '/news/preview', :controller => 'previews', :action => 'news', :via => [:get, :post, :put, :patch]
		a.Handle(r, m, "/news/preview", PreviewsController, "news", a.PreviewsNews,
			Before(previewFindProject), Before(findAttachments))
		// match '/issues/preview', :to => 'previews#issue', :via => [:get, :post, :put, :patch]
		a.Handle(r, m, "/issues/preview", PreviewsController, "issue", a.PreviewsIssue,
			Before(previewFindProject), Before(findAttachments))
		// match '/preview/text', :to => 'previews#text', :via => [:get, :post, :put, :patch]
		a.Handle(r, m, "/preview/text", PreviewsController, "text", a.PreviewsText, Before(findAttachments))
	}
}

// previewFindProject は PreviewsController#find_project（params[:issue][:project_id] || params[:project_id]）。
func previewFindProject(c *Req) {
	id := c.Params().String("issue", "project_id")
	if id == "" {
		id = c.Params().String("project_id")
	}
	c.FindProject(id)
}

var reAttachmentToken = regexp.MustCompile(`^(\d+)\.([0-9a-f]+)$`)

// findAttachments は ApplicationController#find_attachments（params[:attachments][*][:token] の未紐付け添付）。
func findAttachments(c *Req) {
	c.Attachments = nil
	atts := c.Params().Map("attachments")
	if atts == nil {
		return
	}
	for _, k := range atts.Keys() {
		token := atts.String(k, "token")
		m := reAttachmentToken.FindStringSubmatch(token)
		if m == nil {
			continue
		}
		id, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil {
			continue
		}
		a, err := repository.FindAttachmentByToken(c.Ctx(), c.App.DB, id, m[2])
		if err == nil {
			c.Attachments = append(c.Attachments, a)
		}
	}
}

// renderPreview は render :partial => 'common/preview'。
func (a *App) renderPreview(c *Req, previewed *redmine.Object) {
	data := map[string]any{"Attachments": c.Attachments, "Previewed": previewed}
	if v, ok := c.Params().Get("text"); ok && v != nil {
		data["Text"] = c.Params().String("text")
	}
	c.Render("common/_preview", data, RenderOptions{Layout: view.NoLayout})
}

// PreviewsIssue は previews#issue。
func (a *App) PreviewsIssue(c *Req) {
	var previewed *redmine.Object
	if c.Params().Present("issue_id") {
		st := redmine.NewDBStore(c.Ctx(), a.DB, c.Authz())
		if id := c.Params().Int("issue_id"); id != 0 {
			if is, err := st.VisibleIssue(id); err == nil && is != nil {
				previewed, _ = st.LoadObject("issue", is.ID)
			}
		}
	}
	a.renderPreview(c, previewed)
}

// PreviewsNews は previews#news。
func (a *App) PreviewsNews(c *Req) {
	var previewed *redmine.Object
	if c.Params().Present("id") {
		st := redmine.NewDBStore(c.Ctx(), a.DB, c.Authz())
		if id := c.Params().Int("id"); id != 0 {
			if n, err := st.VisibleNamed("news", id); err == nil && n != nil {
				previewed, _ = st.LoadObject("news", n.ID)
			}
		}
	}
	a.renderPreview(c, previewed)
}

// PreviewsText は previews#text。
func (a *App) PreviewsText(c *Req) {
	a.renderPreview(c, nil)
}
