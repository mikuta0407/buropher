package handler

import (
	"net/http"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/query"
)

func (a *App) ProjectsCopy(c *Req)                           { c.head(http.StatusNotImplemented) }
func (a *App) ProjectsDestroy(c *Req)                        { c.head(http.StatusNotImplemented) }
func (a *App) ProjectsBulkDestroy(c *Req)                    { c.head(http.StatusNotImplemented) }
func (a *App) renderProjectsIndexAPI(c *Req, q *query.Query) { c.head(http.StatusNotImplemented) }
func (a *App) renderProjectsAtom(c *Req, q *query.Query)     { c.head(http.StatusNotImplemented) }
func (a *App) renderProjectShowAPI(c *Req, p *domain.Project, status int) {
	c.head(http.StatusNotImplemented)
}
