package handler

import "github.com/mikuta0407/buropher/internal/query"

func (a *App) renderIssuesIndexAPI(c *Req, q *query.Query)  { c.Render404("") }
func (a *App) renderIssuesIndexAtom(c *Req, q *query.Query) { c.Render404("") }
func (a *App) renderIssuesIndexCSV(c *Req, q *query.Query)  { c.Render404("") }
func (a *App) issuesShowAPI(c *Req)                         { c.Render404("") }
func (a *App) issuesShowAtom(c *Req)                        { c.Render404("") }
