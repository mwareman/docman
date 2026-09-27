package srv

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
)

// This file holds the small Markdown renderer behind DocMan's help pages and
// API reference, and GET /api, which hands the reference to machines as raw
// Markdown and sends browsers to the formatted copy under /help.
//
// The renderer covers exactly the Markdown the documentation uses rather than
// pulling in a full CommonMark implementation.

// handleAPIDoc serves the API reference. It is deliberately readable without
// signing in: it documents the shape of the API and contains nothing about this
// particular host.
func (s *Server) handleAPIDoc(w http.ResponseWriter, r *http.Request) {
	if len(s.cfg.APIDoc) == 0 {
		fail(w, http.StatusNotFound, "no API documentation is embedded in this build")
		return
	}

	format := strings.ToLower(r.URL.Query().Get("format"))
	raw := format == "raw" || format == "md" || format == "markdown" || strings.HasSuffix(r.URL.Path, ".md")
	if !raw && format != "html" {
		// curl and friends send */*; browsers ask for text/html explicitly.
		raw = !strings.Contains(r.Header.Get("Accept"), "text/html")
	}

	if !raw {
		http.Redirect(w, r, "/help/api", http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(s.cfg.APIDoc)
	}
}

// etagFor is a strong validator for a rendered document.
func etagFor(body []byte) string {
	sum := sha256.Sum256(body)
	return `"` + hex.EncodeToString(sum[:16]) + `"`
}

// ---------- markdown ----------

// docSection is one heading's worth of a document as plain text, which is
// what the help search index is built from.
type docSection struct {
	Heading string
	Anchor  string
	Level   int
	text    strings.Builder
}

// Text is the section's prose with the Markdown stripped.
func (d *docSection) Text() string { return strings.TrimSpace(d.text.String()) }

// renderedMarkdown is the output of renderMarkdownDoc.
type renderedMarkdown struct {
	Title    string
	TOC      string // links to the level 2 and 3 headings
	Body     string
	Sections []*docSection
}

type mdWriter struct {
	out      strings.Builder
	toc      strings.Builder
	title    string
	slugs    map[string]int
	listKind byte // 'u', 'o' or 0 when no list is open
	liOpen   bool
	inPara   bool
	sections []*docSection
}

// addText records prose for the search index under the current heading.
func (w *mdWriter) addText(markdown string) {
	if len(w.sections) == 0 {
		w.sections = append(w.sections, &docSection{})
	}
	cur := w.sections[len(w.sections)-1]
	cur.text.WriteString(plainInline(markdown))
	cur.text.WriteByte(' ')
}

// renderMarkdown converts a document and returns its parts.
func renderMarkdown(src []byte) (title, toc, body string) {
	doc := renderMarkdownDoc(src)
	return doc.Title, doc.TOC, doc.Body
}

// renderMarkdownDoc converts the documentation subset DocMan uses: ATX
// headings, paragraphs, fenced code, unordered and ordered lists, pipe tables,
// "> " callouts, and inline code, bold, italics and links.
func renderMarkdownDoc(src []byte) *renderedMarkdown {
	w := &mdWriter{slugs: map[string]int{}}
	lines := strings.Split(strings.ReplaceAll(string(src), "\r\n", "\n"), "\n")

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		// Callout: consecutive "> " lines. A leading **Note:**, **Tip:** or
		// **Warning:** picks the style.
		if strings.HasPrefix(trimmed, ">") {
			var quoted []string
			for i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), ">") {
				q := strings.TrimPrefix(strings.TrimSpace(lines[i]), ">")
				quoted = append(quoted, strings.TrimPrefix(q, " "))
				i++
			}
			i--
			w.closeBlocks()
			w.callout(quoted)
			continue
		}

		// Fenced code.
		if strings.HasPrefix(trimmed, "```") {
			lang := strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
			var code strings.Builder
			i++
			for i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), "```") {
				code.WriteString(lines[i])
				code.WriteByte('\n')
				i++
			}
			w.closeBlocks()
			w.out.WriteString(`<div class="doc-code"><pre><code`)
			if lang != "" {
				w.out.WriteString(` class="lang-` + escapeAttr(lang) + `"`)
			}
			w.out.WriteString(`>`)
			w.out.WriteString(escapeText(code.String()))
			w.out.WriteString("</code></pre></div>\n")
			continue
		}

		if trimmed == "" {
			w.closeParagraph()
			continue
		}

		// Heading.
		if level, text, ok := heading(trimmed); ok {
			w.closeBlocks()
			slug := w.slug(text)
			if level == 1 && w.title == "" {
				w.title = text
			}
			w.sections = append(w.sections, &docSection{Heading: plainInline(text), Anchor: slug, Level: level})
			level10 := strconv.Itoa(level)
			w.out.WriteString("<h" + level10 + ` id="` + escapeAttr(slug) + `">`)
			w.out.WriteString(`<a class="doc-anchor" href="#` + escapeAttr(slug) + `">#</a>`)
			w.out.WriteString(inlineMarkdown(text))
			w.out.WriteString("</h" + level10 + ">\n")
			if level == 2 || level == 3 {
				w.toc.WriteString(`    <a class="lvl` + level10 + `" href="#` + escapeAttr(slug) + `">`)
				w.toc.WriteString(escapeText(plainInline(text)))
				w.toc.WriteString("</a>\n")
			}
			continue
		}

		// Table: a pipe row followed by a separator row.
		if strings.HasPrefix(trimmed, "|") && i+1 < len(lines) && isTableSeparator(lines[i+1]) {
			w.closeBlocks()
			headers := splitRow(trimmed)
			aligns := rowAlignments(lines[i+1])
			i += 2
			w.out.WriteString(`<div class="doc-table"><table><thead><tr>`)
			for c, cell := range headers {
				w.out.WriteString("<th" + alignAttr(aligns, c) + ">" + inlineMarkdown(cell) + "</th>")
			}
			w.out.WriteString("</tr></thead><tbody>")
			for i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), "|") {
				w.out.WriteString("<tr>")
				for c, cell := range splitRow(strings.TrimSpace(lines[i])) {
					w.out.WriteString("<td" + alignAttr(aligns, c) + ">" + inlineMarkdown(cell) + "</td>")
					w.addText(cell)
				}
				w.out.WriteString("</tr>")
				i++
			}
			i--
			w.out.WriteString("</tbody></table></div>\n")
			continue
		}

		// Lists. An item stays open so an indented line after it continues it.
		if item, ok := unorderedItem(trimmed); ok {
			w.openList('u')
			w.openItem(item)
			continue
		}
		if item, ok := orderedItem(trimmed); ok {
			if w.listKind != 'o' {
				// A numbered list interrupted by a code block carries on counting.
				w.closeParagraph()
				w.closeList()
				w.listKind = 'o'
				if n, _ := strconv.Atoi(trimmed[:strings.IndexByte(trimmed, '.')]); n > 1 {
					w.out.WriteString(`<ol start="` + strconv.Itoa(n) + `">` + "\n")
				} else {
					w.out.WriteString("<ol>\n")
				}
			}
			w.openItem(item)
			continue
		}
		if w.liOpen && !w.inPara && line != trimmed {
			w.out.WriteString(" " + inlineMarkdown(trimmed))
			w.addText(trimmed)
			continue
		}

		// Paragraph text, with soft line breaks collapsed to spaces.
		if w.listKind != 0 {
			w.closeList()
		}
		if !w.inPara {
			w.out.WriteString("<p>")
			w.inPara = true
		} else {
			w.out.WriteString(" ")
		}
		w.out.WriteString(inlineMarkdown(trimmed))
		w.addText(trimmed)
	}
	w.closeBlocks()
	return &renderedMarkdown{Title: w.title, TOC: w.toc.String(), Body: w.out.String(), Sections: w.sections}
}

func (w *mdWriter) openItem(item string) {
	w.closeItem()
	w.out.WriteString("<li>" + inlineMarkdown(item))
	w.liOpen = true
	w.addText(item)
}

func (w *mdWriter) closeItem() {
	if w.liOpen {
		w.out.WriteString("</li>\n")
		w.liOpen = false
	}
}

// callout renders a run of "> " lines as a highlighted box.
func (w *mdWriter) callout(lines []string) {
	kind := "note"
	if len(lines) > 0 {
		first := strings.ToLower(lines[0])
		switch {
		case strings.HasPrefix(first, "**tip"):
			kind = "tip"
		case strings.HasPrefix(first, "**warning"), strings.HasPrefix(first, "**important"), strings.HasPrefix(first, "**caution"):
			kind = "warn"
		}
	}
	w.out.WriteString(`<div class="doc-callout ` + kind + `">`)
	inPara, inList := false, false
	closeAll := func() {
		if inPara {
			w.out.WriteString("</p>")
			inPara = false
		}
		if inList {
			w.out.WriteString("</li></ul>")
			inList = false
		}
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch item, isItem := unorderedItem(trimmed); {
		case trimmed == "":
			closeAll()
		case isItem:
			if inPara {
				w.out.WriteString("</p>")
				inPara = false
			}
			if inList {
				w.out.WriteString("</li><li>")
			} else {
				w.out.WriteString("<ul><li>")
				inList = true
			}
			w.out.WriteString(inlineMarkdown(item))
			w.addText(item)
		case inList:
			w.out.WriteString(" " + inlineMarkdown(trimmed))
			w.addText(trimmed)
		default:
			if inPara {
				w.out.WriteString(" ")
			} else {
				w.out.WriteString("<p>")
				inPara = true
			}
			w.out.WriteString(inlineMarkdown(trimmed))
			w.addText(trimmed)
		}
	}
	closeAll()
	w.out.WriteString("</div>\n")
}

func (w *mdWriter) openList(kind byte) {
	w.closeParagraph()
	if w.listKind == kind {
		return
	}
	w.closeList()
	w.listKind = kind
	if kind == 'o' {
		w.out.WriteString("<ol>\n")
	} else {
		w.out.WriteString("<ul>\n")
	}
}

func (w *mdWriter) closeList() {
	w.closeItem()
	switch w.listKind {
	case 'o':
		w.out.WriteString("</ol>\n")
	case 'u':
		w.out.WriteString("</ul>\n")
	}
	w.listKind = 0
}

func (w *mdWriter) closeParagraph() {
	if w.inPara {
		w.out.WriteString("</p>\n")
		w.inPara = false
	}
}

func (w *mdWriter) closeBlocks() {
	w.closeParagraph()
	w.closeList()
}

func (w *mdWriter) slug(text string) string {
	var b strings.Builder
	lastDash := true
	for _, r := range strings.ToLower(text) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		slug = "section"
	}
	w.slugs[slug]++
	if n := w.slugs[slug]; n > 1 {
		slug = slug + "-" + strconv.Itoa(n)
	}
	return slug
}

func heading(line string) (int, string, bool) {
	level := 0
	for level < len(line) && line[level] == '#' {
		level++
	}
	if level == 0 || level > 6 || level >= len(line) || line[level] != ' ' {
		return 0, "", false
	}
	return level, strings.TrimSpace(line[level+1:]), true
}

func unorderedItem(line string) (string, bool) {
	if len(line) > 2 && (line[0] == '-' || line[0] == '*') && line[1] == ' ' {
		return strings.TrimSpace(line[2:]), true
	}
	return "", false
}

func orderedItem(line string) (string, bool) {
	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	if i == 0 || i+1 >= len(line) || line[i] != '.' || line[i+1] != ' ' {
		return "", false
	}
	return strings.TrimSpace(line[i+2:]), true
}

func isTableSeparator(line string) bool {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "|") {
		return false
	}
	seen := false
	for _, r := range trimmed {
		switch r {
		case '-':
			seen = true
		case '|', ':', ' ':
		default:
			return false
		}
	}
	return seen
}

func splitRow(line string) []string {
	trimmed := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(line), "|"), "|")
	parts := strings.Split(trimmed, "|")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		out = append(out, strings.TrimSpace(part))
	}
	return out
}

func rowAlignments(line string) []string {
	out := []string{}
	for _, cell := range splitRow(line) {
		switch {
		case strings.HasPrefix(cell, ":") && strings.HasSuffix(cell, ":"):
			out = append(out, "center")
		case strings.HasSuffix(cell, ":"):
			out = append(out, "right")
		default:
			out = append(out, "")
		}
	}
	return out
}

func alignAttr(aligns []string, index int) string {
	if index >= len(aligns) || aligns[index] == "" {
		return ""
	}
	return ` style="text-align:` + aligns[index] + `"`
}

// inlineMarkdown handles code spans, bold and links. Everything else is
// escaped, so the document can never inject markup.
func inlineMarkdown(text string) string {
	var b strings.Builder
	for i := 0; i < len(text); {
		switch {
		case text[i] == '`':
			if end := strings.IndexByte(text[i+1:], '`'); end >= 0 {
				b.WriteString("<code>")
				b.WriteString(escapeText(text[i+1 : i+1+end]))
				b.WriteString("</code>")
				i += end + 2
				continue
			}
		case strings.HasPrefix(text[i:], "**"):
			if end := strings.Index(text[i+2:], "**"); end >= 0 {
				b.WriteString("<strong>")
				b.WriteString(inlineMarkdown(text[i+2 : i+2+end]))
				b.WriteString("</strong>")
				i += end + 4
				continue
			}
		case text[i] == '*':
			if end := italicEnd(text, i); end > 0 {
				b.WriteString("<em>")
				b.WriteString(inlineMarkdown(text[i+1 : end]))
				b.WriteString("</em>")
				i = end + 1
				continue
			}
		case text[i] == '[':
			if label, href, width, ok := link(text[i:]); ok {
				b.WriteString(`<a href="` + escapeAttr(href) + `"`)
				if strings.HasPrefix(href, "http") {
					b.WriteString(` rel="noreferrer"`)
				}
				b.WriteString(">")
				b.WriteString(inlineMarkdown(label))
				b.WriteString("</a>")
				i += width
				continue
			}
		}
		b.WriteString(escapeByte(text[i]))
		i++
	}
	return b.String()
}

// italicEnd finds the closing * of an *emphasis* run opening at i, or -1. The
// text either side of the markers must not be a space, so a lone asterisk
// such as "container.*" stays literal.
func italicEnd(text string, i int) int {
	if i+2 >= len(text) || text[i+1] == ' ' || text[i+1] == '*' {
		return -1
	}
	if i > 0 && isWordByte(text[i-1]) {
		return -1
	}
	for j := i + 2; j < len(text); j++ {
		if text[j] == '`' {
			return -1
		}
		if text[j] == '*' && text[j-1] != ' ' {
			if j+1 < len(text) && isWordByte(text[j+1]) {
				continue
			}
			return j
		}
	}
	return -1
}

func isWordByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_'
}

// plainInline strips inline Markdown, leaving the words a reader sees.
func plainInline(text string) string {
	var b strings.Builder
	for i := 0; i < len(text); {
		switch {
		case text[i] == '`':
			if end := strings.IndexByte(text[i+1:], '`'); end >= 0 {
				b.WriteString(text[i+1 : i+1+end])
				i += end + 2
				continue
			}
		case strings.HasPrefix(text[i:], "**"):
			i += 2
			continue
		case text[i] == '*':
			if end := italicEnd(text, i); end > 0 {
				b.WriteString(plainInline(text[i+1 : end]))
				i = end + 1
				continue
			}
		case text[i] == '[':
			if label, _, width, ok := link(text[i:]); ok {
				b.WriteString(plainInline(label))
				i += width
				continue
			}
		}
		b.WriteByte(text[i])
		i++
	}
	return b.String()
}

// link parses a leading [label](href) and reports how many bytes it spanned.
func link(text string) (label, href string, width int, ok bool) {
	closeLabel := strings.IndexByte(text, ']')
	if closeLabel < 0 || closeLabel+1 >= len(text) || text[closeLabel+1] != '(' {
		return "", "", 0, false
	}
	closeHref := strings.IndexByte(text[closeLabel+2:], ')')
	if closeHref < 0 {
		return "", "", 0, false
	}
	label = text[1:closeLabel]
	href = strings.TrimSpace(text[closeLabel+2 : closeLabel+2+closeHref])
	if !safeHref(href) {
		return "", "", 0, false
	}
	return label, href, closeLabel + 2 + closeHref + 1, true
}

// safeHref keeps javascript: and data: URLs out of the rendered page.
func safeHref(href string) bool {
	if href == "" {
		return false
	}
	if strings.HasPrefix(href, "#") || strings.HasPrefix(href, "/") || strings.HasPrefix(href, "./") {
		return true
	}
	lower := strings.ToLower(href)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "mailto:") {
		return true
	}
	// A bare relative path such as API.md is fine; anything with a scheme is not.
	return !strings.Contains(href, ":")
}

func escapeByte(c byte) string {
	switch c {
	case '&':
		return "&amp;"
	case '<':
		return "&lt;"
	case '>':
		return "&gt;"
	case '"':
		return "&#34;"
	case '\'':
		return "&#39;"
	}
	// Written back as a single byte so multi-byte UTF-8 survives intact.
	return string([]byte{c})
}

func escapeText(text string) string {
	var b strings.Builder
	b.Grow(len(text))
	for i := 0; i < len(text); i++ {
		b.WriteString(escapeByte(text[i]))
	}
	return b.String()
}

func escapeAttr(text string) string { return escapeText(text) }
