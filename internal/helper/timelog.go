package helper

// このファイルは工数画面（handler の timelog*.go）がハンドラ側で使う ApplicationHelper の公開版。

// BackURLOf は back_url（params[:back_url]、無ければ Referer を CGI.unescape したもの）。
func BackURLOf(p *Page) string { return backURL(p) }
