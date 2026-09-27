package srv

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"strings"
	"sync"
)

// The help system at /help: the user guide, written as Markdown pages in the
// help/ directory, and the API reference, which is API.md split into one page
// per "## " section. Everything is rendered once, on first request, and served
// from memory. Like /api it needs no sign-in: it describes DocMan, not the
// host it runs on.

// helpGuide lists the user guide in reading order. Page titles come from each
// file's "# " heading; Nav is the shorter label used in the sidebar.
var helpGuide = []struct {
	Section string
	Pages   []struct{ Slug, Nav, File string }
}{
	{"Getting started", []struct{ Slug, Nav, File string }{
		{"", "Welcome", "index.md"},
		{"getting-started", "Setting up DocMan", "getting-started.md"},
		{"overview", "The Overview page", "overview.md"},
	}},
	{"Managing containers", []struct{ Slug, Nav, File string }{
		{"containers", "The container list", "containers.md"},
		{"deploy", "Deploying a container", "deploy.md"},
		{"container", "Working with a container", "container.md"},
		{"logs", "Logs", "logs.md"},
		{"statistics", "Statistics and processes", "statistics.md"},
		{"console", "The console", "console.md"},
		{"configuration", "Changing configuration", "configuration.md"},
		{"updating", "Updating containers", "updating.md"},
		{"addresses", "Fixed addresses", "addresses.md"},
	}},
	{"Images and storage", []struct{ Slug, Nav, File string }{
		{"images", "Images", "images.md"},
		{"volumes", "Volumes", "volumes.md"},
	}},
	{"Access and settings", []struct{ Slug, Nav, File string }{
		{"sign-in", "Signing in", "sign-in.md"},
		{"settings", "Settings", "settings.md"},
	}},
	{"Administration", []struct{ Slug, Nav, File string }{
		{"administration", "Install, upgrade, back up", "administration.md"},
		{"troubleshooting", "Troubleshooting", "troubleshooting.md"},
	}},
}

const apiSection = "API reference"

type helpPage struct {
	Slug    string
	Section string
	Nav     string
	Title   string
	Source  []byte
	doc     *renderedMarkdown
	html    []byte
	etag    string
	indexed bool // part of the search index
}

func (p *helpPage) URL() string {
	if p.Slug == "" {
		return "/help"
	}
	return "/help/" + p.Slug
}

type helpSite struct {
	pages  []*helpPage // reading order
	bySlug map[string]*helpPage
	index  []byte // search.json
	etag   string
}

var (
	helpOnce sync.Once
	helpData *helpSite
)

func (s *Server) help() *helpSite {
	helpOnce.Do(func() { helpData = buildHelp(s.cfg.HelpFS, s.cfg.APIDoc) })
	return helpData
}

func buildHelp(guide fs.FS, apiDoc []byte) *helpSite {
	site := &helpSite{bySlug: map[string]*helpPage{}}
	add := func(p *helpPage) {
		p.doc = renderMarkdownDoc(p.Source)
		p.Title = p.doc.Title
		if p.Title == "" {
			p.Title = p.Nav
		}
		site.pages = append(site.pages, p)
		site.bySlug[p.Slug] = p
	}

	for _, section := range helpGuide {
		for _, entry := range section.Pages {
			var src []byte
			if guide != nil {
				src, _ = fs.ReadFile(guide, entry.File)
			}
			if len(src) == 0 {
				src = []byte("# " + entry.Nav + "\n\nThis page is missing from this build.\n")
			}
			add(&helpPage{Slug: entry.Slug, Section: section.Section, Nav: entry.Nav, Source: src, indexed: true})
		}
	}

	if len(apiDoc) > 0 {
		for _, part := range splitAPIDoc(apiDoc) {
			add(&helpPage{Slug: part.slug, Section: apiSection, Nav: part.nav, Source: part.src, indexed: true})
		}
		// The whole reference on one page, for find-in-page and printing. It is
		// left out of the search index, which already covers every part.
		add(&helpPage{Slug: "api/all", Section: apiSection, Nav: "All on one page", Source: apiDoc})
	}

	for i, p := range site.pages {
		var prev, next *helpPage
		if i > 0 {
			prev = site.pages[i-1]
		}
		if i+1 < len(site.pages) && site.pages[i+1].Slug != "api/all" {
			next = site.pages[i+1]
		}
		if p.Slug == "api/all" {
			prev, next = nil, nil
		}
		p.html = []byte(renderHelpPage(site, p, prev, next))
		p.etag = etagFor(p.html)
	}
	site.index = buildSearchIndex(site)
	site.etag = etagFor(site.index)
	return site
}

type apiPart struct {
	slug, nav string
	src       []byte
}

// splitAPIDoc cuts API.md into an overview (everything before the first
// "## ") and one page per "## " section. Each section's headings move up a
// level so it reads as a page of its own.
func splitAPIDoc(src []byte) []apiPart {
	lines := strings.Split(strings.ReplaceAll(string(src), "\r\n", "\n"), "\n")
	var parts []apiPart
	var cur []string
	curNav, curSlug := "Overview", "api"
	flush := func() {
		body := strings.TrimSpace(strings.Join(cur, "\n"))
		if body != "" {
			parts = append(parts, apiPart{slug: curSlug, nav: curNav, src: []byte(body + "\n")})
		}
		cur = nil
	}
	inFence := false
	seen := map[string]int{}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
		}
		if !inFence && strings.HasPrefix(line, "## ") {
			flush()
			curNav = plainInline(strings.TrimSpace(line[3:]))
			curSlug = "api/" + slugify(curNav)
			if n := seen[curSlug]; n > 0 {
				curSlug += "-" + string(rune('1'+n))
			}
			seen[curSlug]++
			cur = append(cur, "# "+strings.TrimSpace(line[3:]))
			continue
		}
		if !inFence && len(parts) == 0 && curSlug == "api" && strings.HasPrefix(line, "# ") {
			cur = append(cur, "# API reference")
			continue
		}
		if !inFence && curSlug != "api" && strings.HasPrefix(line, "###") {
			line = line[1:]
		}
		cur = append(cur, line)
	}
	flush()
	return parts
}

func slugify(text string) string {
	w := &mdWriter{slugs: map[string]int{}}
	return w.slug(text)
}

// ---------- search ----------

type searchEntry struct {
	URL     string `json:"u"`
	Page    string `json:"p"`
	Section string `json:"s"`
	Heading string `json:"h,omitempty"`
	Text    string `json:"t"`
}

func buildSearchIndex(site *helpSite) []byte {
	entries := []searchEntry{}
	for _, p := range site.pages {
		if !p.indexed {
			continue
		}
		for _, sec := range p.doc.Sections {
			text := sec.Text()
			if text == "" && sec.Heading == "" {
				continue
			}
			url := p.URL()
			heading := sec.Heading
			if sec.Level > 1 && sec.Anchor != "" {
				url += "#" + sec.Anchor
			} else {
				heading = ""
			}
			if len(text) > 1500 {
				text = text[:1500]
			}
			entries = append(entries, searchEntry{URL: url, Page: p.Title, Section: p.Section, Heading: heading, Text: text})
		}
	}
	out, _ := json.Marshal(entries)
	return out
}

// ---------- serving ----------

func (s *Server) handleHelp(w http.ResponseWriter, r *http.Request) {
	site := s.help()
	slug := strings.Trim(r.PathValue("page"), "/")

	if r.URL.Path == "/help/" {
		http.Redirect(w, r, "/help", http.StatusMovedPermanently)
		return
	}
	if slug == "search.json" {
		serveCached(w, r, "application/json; charset=utf-8", site.index, site.etag)
		return
	}

	raw := strings.HasSuffix(slug, ".md")
	slug = strings.TrimSuffix(slug, ".md")
	if slug == "index" {
		slug = ""
	}
	switch strings.ToLower(r.URL.Query().Get("format")) {
	case "raw", "md", "markdown":
		raw = true
	}

	page := site.bySlug[slug]
	if page == nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusNotFound)
		missing := &helpPage{Slug: slug, Section: "", Nav: "Not found", Title: "Page not found"}
		missing.doc = renderMarkdownDoc([]byte("# Page not found\n\nThere is no help page at `" +
			strings.ReplaceAll(r.URL.Path, "`", "") + "`. Use the menu or the search box to find what you need, " +
			"or start from the [welcome page](/help).\n"))
		_, _ = w.Write([]byte(renderHelpPage(site, missing, nil, nil)))
		return
	}
	if raw {
		serveCached(w, r, "text/markdown; charset=utf-8", page.Source, etagFor(page.Source))
		return
	}
	serveCached(w, r, "text/html; charset=utf-8", page.html, page.etag)
}

func serveCached(w http.ResponseWriter, r *http.Request, contentType string, body []byte, etag string) {
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("Cache-Control", "no-cache")
	h.Set("ETag", etag)
	if match := r.Header.Get("If-None-Match"); match != "" && strings.Contains(match, etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

// ---------- page template ----------

func renderHelpPage(site *helpSite, page *helpPage, prev, next *helpPage) string {
	var b strings.Builder
	title := page.Title
	if page.Slug == "" {
		title = "Help"
	}
	b.WriteString(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="dark light">
<title>`)
	b.WriteString(escapeText(title))
	b.WriteString(` — DocMan help</title>
<link rel="icon" href="/icon.svg" type="image/svg+xml">
<link rel="stylesheet" href="/css/app.css">
<link rel="stylesheet" href="/css/docs.css">
<script src="/js/theme.js"></script>
<script src="/js/help.js" defer></script>
</head>
<body class="doc help">
<a class="help-skip" href="#content">Skip to content</a>
<header class="doc-top">
  <a class="doc-brand" href="/help">
    <svg viewBox="0 0 32 32" aria-hidden="true">
      <rect width="32" height="32" rx="8" fill="currentColor" opacity=".1"/>
      <path d="M6 12.5 16 7l10 5.5v7L16 25 6 19.5z" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linejoin="round"/>
      <path d="M16 13.2 21 16v3.2L16 22l-5-2.8V16z" fill="currentColor" opacity=".8"/>
    </svg>
    <span>DocMan</span>
  </a>
  <span class="doc-sep">/</span>
  <a class="doc-where" href="/help">Help</a>
  <div class="help-search" role="search">
    <svg class="ico" viewBox="0 0 24 24" aria-hidden="true"><circle cx="11" cy="11" r="6.5"/><path d="M16 16l4.5 4.5"/></svg>
    <input id="help-q" type="search" placeholder="Search help" aria-label="Search help" autocomplete="off" spellcheck="false" aria-controls="help-results" aria-expanded="false">
    <kbd class="help-kbd" aria-hidden="true">/</kbd>
    <div id="help-results" class="help-results" role="listbox" hidden></div>
  </div>
  <a class="btn sm primary" href="/">Open DocMan</a>
</header>
<div class="help-shell">
  <details class="help-nav-wrap" open>
    <summary>Menu</summary>
    <nav class="help-nav" aria-label="Help pages">
`)
	b.WriteString(renderHelpNav(site, page))
	b.WriteString(`    </nav>
  </details>
  <main class="doc-body" id="content">
`)
	if page.Section != "" {
		b.WriteString(`    <div class="help-crumb">` + escapeText(page.Section) + `</div>` + "\n")
	}
	if page.doc.TOC != "" {
		b.WriteString(`    <details class="help-inline-toc"><summary>On this page</summary><nav>` + "\n")
		b.WriteString(page.doc.TOC)
		b.WriteString(`    </nav></details>` + "\n")
	}
	b.WriteString(page.doc.Body)
	if prev != nil || next != nil {
		b.WriteString(`    <nav class="help-pager" aria-label="Previous and next page">`)
		if prev != nil {
			b.WriteString(`<a class="prev" href="` + prev.URL() + `"><span>Previous</span>` + escapeText(prev.Title) + `</a>`)
		} else {
			b.WriteString(`<span></span>`)
		}
		if next != nil {
			b.WriteString(`<a class="next" href="` + next.URL() + `"><span>Next</span>` + escapeText(next.Title) + `</a>`)
		}
		b.WriteString("</nav>\n")
	}
	if page.Source != nil {
		b.WriteString(`    <div class="help-foot"><a href="` + page.URL() + `?format=raw">View this page as Markdown</a></div>` + "\n")
	}
	b.WriteString(`  </main>
  <aside class="help-toc doc-toc" aria-label="On this page">
`)
	if page.doc.TOC != "" {
		b.WriteString(`    <div class="doc-toc-title">On this page</div>` + "\n")
		b.WriteString(page.doc.TOC)
	}
	b.WriteString(`  </aside>
</div>
</body>
</html>
`)
	return b.String()
}

func renderHelpNav(site *helpSite, current *helpPage) string {
	var b strings.Builder
	section := ""
	open := false
	for _, p := range site.pages {
		if p.Section != section {
			if open {
				b.WriteString("      </div></details>\n")
			}
			section = p.Section
			// Guide sections stay open; the long API list opens on API pages.
			expanded := section != apiSection || current.Section == apiSection
			b.WriteString(`      <details class="help-nav-section"`)
			if expanded {
				b.WriteString(" open")
			}
			b.WriteString("><summary>" + escapeText(section) + "</summary><div>\n")
			open = true
		}
		cls := ""
		aria := ""
		if p == current || (current != nil && p.Slug == current.Slug) {
			cls = ` class="on"`
			aria = ` aria-current="page"`
		}
		b.WriteString(`        <a href="` + p.URL() + `"` + cls + aria + `>` + escapeText(p.Nav) + "</a>\n")
	}
	if open {
		b.WriteString("      </div></details>\n")
	}
	return b.String()
}
