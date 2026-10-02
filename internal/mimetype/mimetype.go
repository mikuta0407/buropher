// Package mimetype は Redmine::MimeType（lib/redmine/mime_type.rb）の移植。
//
// Redmine 独自の表（MIME_TYPES）に無い拡張子は MiniMime（mini_mime gem の DB）で引く。
// MiniMime の DB 全体は持たず、添付ファイルでよく使う拡張子だけを miniMime に置く
// （足りなければ mini_mime の lib/db/ext_mime.db から追記すること）。
package mimetype

import (
	"strings"
)

// redmineTypes は Redmine::MimeType::MIME_TYPES（定義順）。
var redmineTypes = [][2]string{
	{"text/plain", "txt,tpl,properties,patch,diff,ini,readme,install,upgrade,sql"},
	{"text/css", "css"},
	{"text/html", "html,htm,xhtml"},
	{"text/jsp", "jsp"},
	{"text/x-c", "c,cpp,cc,h,hh"},
	{"text/x-csharp", "cs"},
	{"text/x-java", "java"},
	{"text/x-html-template", "rhtml"},
	{"text/x-perl", "pl,pm"},
	{"text/x-php", "php,php3,php4,php5"},
	{"text/x-python", "py"},
	{"text/x-ruby", "rb,rbw,ruby,rake,erb"},
	{"text/x-csh", "csh"},
	{"text/x-sh", "sh"},
	{"text/x-textile", "textile"},
	{"text/xml", "xml,xsd,mxml"},
	{"text/yaml", "yml,yaml"},
	{"text/csv", "csv"},
	{"text/x-po", "po"},
	{"image/gif", "gif"},
	{"image/jpeg", "jpg,jpeg,jpe"},
	{"image/png", "png"},
	{"image/tiff", "tiff,tif"},
	{"image/webp", "webp"},
	{"image/x-ms-bmp", "bmp"},
	{"application/javascript", "js"},
	{"application/pdf", "pdf"},
	{"video/mp4", "mp4"},
	{"video/webm", "webm"},
}

// miniMime は MiniMime.lookup_by_extension(ext).content_type の一部（mini_mime 1.1 の ext_mime.db）。
var miniMime = map[string]string{
	"7z":       "application/x-7z-compressed",
	"aac":      "audio/x-aac",
	"avi":      "video/x-msvideo",
	"bz2":      "application/x-bzip2",
	"doc":      "application/msword",
	"docx":     "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	"eml":      "message/rfc822",
	"eps":      "application/postscript",
	"flac":     "audio/x-flac",
	"gz":       "application/gzip",
	"ico":      "image/vnd.microsoft.icon",
	"ics":      "text/calendar",
	"jar":      "application/java-archive",
	"json":     "application/json",
	"m4a":      "audio/mp4",
	"md":       "text/markdown",
	"markdown": "text/markdown",
	"mid":      "audio/midi",
	"mkv":      "video/x-matroska",
	"mov":      "video/quicktime",
	"mp3":      "audio/mpeg",
	"mpeg":     "video/mpeg",
	"mpg":      "video/mpeg",
	"odp":      "application/vnd.oasis.opendocument.presentation",
	"ods":      "application/vnd.oasis.opendocument.spreadsheet",
	"odt":      "application/vnd.oasis.opendocument.text",
	"oga":      "audio/ogg",
	"ogg":      "audio/ogg",
	"ogv":      "video/ogg",
	"ppt":      "application/vnd.ms-powerpoint",
	"pptx":     "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	"ps":       "application/postscript",
	"rar":      "application/x-rar-compressed",
	"rtf":      "application/rtf",
	"svg":      "image/svg+xml",
	"tar":      "application/x-tar",
	"tgz":      "application/x-gtar",
	"ttf":      "font/ttf",
	"wav":      "audio/x-wav",
	"weba":     "audio/webm",
	"wma":      "audio/x-ms-wma",
	"wmv":      "video/x-ms-wmv",
	"woff":     "font/woff",
	"woff2":    "font/woff2",
	"xls":      "application/vnd.ms-excel",
	"xlsx":     "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	"xz":       "application/x-xz",
	"zip":      "application/zip",
}

// extensions は Redmine::MimeType::EXTENSIONS（拡張子 → MIME タイプ。後の定義が優先）。
var extensions = func() map[string]string {
	m := map[string]string{}
	for _, t := range redmineTypes {
		for _, ext := range strings.Split(t[1], ",") {
			m[strings.TrimSpace(ext)] = t[0]
		}
	}
	return m
}()

// rubyExtname は File.extname（Ruby 2.7 以降・非 Windows の挙動）。basename の先頭のドットは
// 拡張子とみなさず、末尾がドットなら "."（例 "a.tar.gz" → ".gz"、".bashrc" → ""、"a." → "."）。
func rubyExtname(name string) string {
	base := name
	if i := strings.LastIndex(strings.TrimRight(name, "/"), "/"); i >= 0 {
		base = name[i+1:]
	}
	base = strings.TrimRight(base, "/")
	trimmed := strings.TrimLeft(base, ".")
	i := strings.LastIndex(trimmed, ".")
	if i <= 0 {
		return ""
	}
	return trimmed[i:]
}

// Extname は File.extname(name)（例 "a.tar.gz" → ".gz"、".bashrc" → ""）。
func Extname(name string) string { return rubyExtname(name) }

// Of は Redmine::MimeType.of(name)。不明なら ""。
func Of(name string) string {
	ext := rubyExtname(name)
	if len(ext) < 2 {
		return ""
	}
	ext = strings.ToLower(ext[1:])
	if t, ok := extensions[ext]; ok {
		return t
	}
	return miniMime[ext]
}

// CSSClassOf は Redmine::MimeType.css_class_of（"text/plain" → "text-plain"）。
func CSSClassOf(name string) string {
	return strings.ReplaceAll(Of(name), "/", "-")
}

// MainMimetypeOf は Redmine::MimeType.main_mimetype_of（"text/plain" → "text"）。
func MainMimetypeOf(name string) string {
	t := Of(name)
	if t == "" {
		return ""
	}
	return strings.SplitN(t, "/", 2)[0]
}

// IsType は Redmine::MimeType.is_type?(type, name)。
func IsType(typ, name string) bool {
	return typ == MainMimetypeOf(name)
}

// ByType は Redmine::MimeType.by_type(type)（MIME_TYPES のうち type/ で始まるもの）。
func ByType(typ string) []string {
	var out []string
	for _, t := range redmineTypes {
		if strings.HasPrefix(t[0], typ+"/") {
			out = append(out, t[0])
		}
	}
	return out
}
