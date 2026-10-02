package handler

import (
	"net/http"

	"github.com/mikuta0407/buropher/internal/domain"
)

func (a *App) ProjectsIndex(c *Req)       { c.head(http.StatusNotImplemented) }
func (a *App) ProjectsCreate(c *Req)      { c.head(http.StatusNotImplemented) }
func (a *App) ProjectsNew(c *Req)         { c.head(http.StatusNotImplemented) }
func (a *App) ProjectsSettings(c *Req)    { c.head(http.StatusNotImplemented) }
func (a *App) ProjectsCopy(c *Req)        { c.head(http.StatusNotImplemented) }
func (a *App) ProjectsUpdate(c *Req)      { c.head(http.StatusNotImplemented) }
func (a *App) ProjectsDestroy(c *Req)     { c.head(http.StatusNotImplemented) }
func (a *App) ProjectsBulkDestroy(c *Req) { c.head(http.StatusNotImplemented) }
func (a *App) renderProjectShowAPI(c *Req, p *domain.Project, status int) {
	c.head(http.StatusNotImplemented)
}
