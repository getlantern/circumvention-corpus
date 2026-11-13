package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

const (
	siteOrigin = "https://corpus.lantern.io"
	siteURL    = siteOrigin + "/"
	siteName   = "Circumvention Corpus"
	lanternOrg = "https://lantern.io/#organization"

	indexTitle       = "Circumvention Corpus: censorship-circumvention research index"
	indexDescription = "A structured, LLM-callable index of censorship-circumvention research from Lantern: papers tagged by censor, technique and defense, queryable over MCP."
	defaultDesc      = "Circumvention Corpus, from Lantern: censorship-circumvention research tagged by censor, detection technique and defense, with extracted findings and an MCP server."
)

// og.png is rendered from og.html (1200x630) and copied into the site root.
//
//go:embed og.png
var ogPNG []byte

type faqItem struct {
	Q    string
	A    template.HTML
	Slug string
}

// faq is the single source for both the visible FAQ on the home page and
// its FAQPage JSON-LD, so the two can't drift apart.
func (s *site) faq() []faqItem {
	censorbib := 0
	for _, p := range s.papers {
		if slices.Contains(p.Sources, "censorbib") {
			censorbib++
		}
	}
	items := []faqItem{
		{Q: "What is the Circumvention Corpus?", A: template.HTML(
			`The Circumvention Corpus is a structured, LLM-callable index of censorship-circumvention research from <a href="https://lantern.io">Lantern</a>. ` +
				`Each paper is a YAML record with its core metadata, tags from a controlled vocabulary of censors, detection techniques and defenses, and, where available, team notes and extracted findings. ` +
				`An MCP server lets any AI assistant query it. It is a research index, not a paper-hosting site.`)},
		{Q: "What's in the corpus?", A: template.HTML(fmt.Sprintf(
			`%d papers and %d findings extracted from them, tagged against a taxonomy of %d censors, %d detection techniques and %d defenses. `+
				`Each paper record has a title and year, and, where available, authors, venue, DOI or arXiv ID, a link to the canonical source and the abstract. `+
				`This site and the hosted MCP server include only papers marked public.`,
			len(s.papers), len(s.findings), len(s.tax.Censors), len(s.tax.Techniques), len(s.tax.Defenses)))},
		{Q: "How is it different from CensorBib?", A: template.HTML(fmt.Sprintf(
			`<a href="https://censorbib.nymity.ch/">CensorBib</a> is a maintained bibliography of censorship research. `+
				`The corpus builds on it (%d of its papers came from an import of CensorBib's BibTeX file) and adds what a bibliography doesn't have: `+
				`consistent controlled-vocabulary tags, extracted findings where available, and an MCP server so an AI assistant can query it.`, censorbib))},
		{Q: "How does it relate to net4people/bbs and gfw.report?", A: template.HTML(
			`<a href="https://github.com/net4people/bbs">net4people/bbs</a> is the circumvention community's discussion forum, and <a href="https://gfw.report">gfw.report</a> publishes original research. ` +
				`The corpus complements them rather than replacing them: it indexes the field's research under one metadata schema, and its crawler watches both for new work.`)},
		{Q: "How do I query the corpus from an AI assistant?", A: template.HTML(
			`Add the hosted MCP server at <code>https://corpus.lantern.io/mcp</code>. In Claude Code: ` +
				`<code>claude mcp add --transport http -s user circumvention-corpus https://corpus.lantern.io/mcp</code>. ` +
				`Any MCP client can use the same URL. The server exposes <code>search_papers</code>, <code>get_paper</code>, <code>list_taxonomy</code>, <code>find_related</code> and <code>synthesize</code>. ` +
				`See <a href="/use/">how to use the corpus</a>.`)},
		{Q: "Can I run the MCP server locally?", A: template.HTML(
			`Yes. For offline use, or if your network blocks Cloudflare, the Go stdio server in <code>cmd/corpus-mcp</code> reads the YAML records from a local clone of the repository. ` +
				`Install it with <code>go install github.com/getlantern/circumvention-corpus/cmd/corpus-mcp@latest</code>.`)},
		{Q: "What are findings?", A: template.HTML(
			`Findings are short claims extracted from a paper, one to three sentences each, tagged with the techniques, censors and defenses they concern, usually with the section they come from and notes on what a circumvention-tool builder should do about them. ` +
				`Claude extracts them, so check important ones against the paper. Each has its own page under <a href="/findings/">findings</a>.`)},
		{Q: "How does “Ask the corpus” work?", A: template.HTML(
			`The <a href="/ask/">Ask</a> page retrieves the relevant extracted findings, the same retrieval the MCP <code>synthesize</code> tool uses, and Claude writes an answer that cites them inline as <code>(paper_id, §section)</code>. ` +
				`Treat the answer as a starting point and verify each citation against the source paper.`)},
		{Q: "How are new papers added?", A: template.HTML(
			`A crawler polls sources including arXiv, net4people/bbs, gfw.report, PoPETs, FOCI and USENIX Security, asks Claude to propose taxonomy tags and extract findings, and opens a pull request labeled <code>auto-ingest</code>. ` +
				`The pull request merges automatically once CI validates it, and a batch that fails CI waits for a human. ` +
				`Anyone can also add a paper by opening a pull request with a YAML record; see <a href="/contribute/">how to contribute</a>.`)},
		{Q: "Is the corpus comprehensive?", A: template.HTML(
			`No, by design. Curation beats volume: the bar for inclusion is roughly whether a Lantern protocol designer would be slowed down by not having read the paper. Comprehensive coverage of the field is a non-goal.`)},
		{Q: "Can I download the papers from here?", A: template.HTML(
			`No. The corpus doesn't host or redistribute papers. Each record links to the paper's canonical URL, DOI or arXiv entry.`)},
		{Q: "What license is the corpus under?", A: template.HTML(
			`The schema, taxonomy and corpus metadata are CC0, public domain, so you can reuse them freely. That doesn't extend to the papers themselves, which the corpus only links to. ` +
				`The source is at <a href="https://github.com/getlantern/circumvention-corpus">github.com/getlantern/circumvention-corpus</a>.`)},
		{Q: "Who maintains the Circumvention Corpus?", A: template.HTML(
			`<a href="https://lantern.io">Lantern</a>, the free VPN and censorship-circumvention app run by Brave New Software Project, Inc., with contributions from the wider circumvention community. ` +
				`Issues and pull requests are open on GitHub. To get around censorship yourself, <a href="https://lantern.io/download">get Lantern</a>.`)},
	}
	slug := regexp.MustCompile(`[^a-z0-9]+`)
	for i := range items {
		items[i].Slug = strings.Trim(slug.ReplaceAllString(strings.ToLower(items[i].Q), "-"), "-")
	}
	return items
}

var tagRE = regexp.MustCompile(`<[^>]+>`)

func stripTags(s string) string {
	return html.UnescapeString(tagRE.ReplaceAllString(s, ""))
}

// jsonLD builds the home page's schema.org graph. All Lantern sites share
// the Organization @id so search engines see one entity.
func (s *site) jsonLD(faq []faqItem) template.JS {
	org := map[string]any{
		"@type":     "Organization",
		"@id":       lanternOrg,
		"name":      "Lantern",
		"legalName": "Brave New Software Project, Inc.",
		"url":       "https://lantern.io",
		"sameAs":    []string{"https://github.com/getlantern"},
	}
	questions := make([]map[string]any, 0, len(faq))
	for _, f := range faq {
		questions = append(questions, map[string]any{
			"@type":          "Question",
			"name":           f.Q,
			"acceptedAnswer": map[string]any{"@type": "Answer", "text": stripTags(string(f.A))},
		})
	}
	graph := map[string]any{
		"@context": "https://schema.org",
		"@graph": []any{
			org,
			map[string]any{
				"@type":         "WebSite",
				"@id":           siteURL + "#website",
				"url":           siteURL,
				"name":          siteName,
				"alternateName": "circumvention-corpus",
				"publisher":     map[string]any{"@id": lanternOrg},
				"inLanguage":    "en",
			},
			map[string]any{
				"@type":              "WebPage",
				"@id":                siteURL + "#webpage",
				"url":                siteURL,
				"name":               indexTitle,
				"isPartOf":           map[string]any{"@id": siteURL + "#website"},
				"about":              map[string]any{"@id": siteURL + "#dataset"},
				"primaryImageOfPage": siteURL + "og.png",
				"inLanguage":         "en",
			},
			map[string]any{
				"@type":               "Dataset",
				"@id":                 siteURL + "#dataset",
				"name":                siteName,
				"alternateName":       "circumvention-corpus",
				"description":         stripTags(string(faq[0].A)),
				"url":                 siteURL,
				"sameAs":              "https://github.com/getlantern/circumvention-corpus",
				"creator":             map[string]any{"@id": lanternOrg},
				"publisher":           map[string]any{"@id": lanternOrg},
				"license":             "https://creativecommons.org/publicdomain/zero/1.0/",
				"isAccessibleForFree": true,
				"inLanguage":          "en",
				"keywords": []string{
					"censorship circumvention", "internet censorship", "Great Firewall",
					"active probing", "traffic analysis", "pluggable transports", "MCP server",
				},
				"distribution": []map[string]any{
					{
						"@type":          "DataDownload",
						"name":           "Public paper records as JSON",
						"encodingFormat": "application/json",
						"contentUrl":     siteURL + "search-index.json",
					},
					{
						"@type":          "DataDownload",
						"name":           "YAML records, schema and taxonomy (source repository)",
						"encodingFormat": "application/zip",
						"contentUrl":     "https://github.com/getlantern/circumvention-corpus/archive/refs/heads/main.zip",
					},
				},
			},
			map[string]any{
				"@type":      "FAQPage",
				"@id":        siteURL + "#faq",
				"isPartOf":   map[string]any{"@id": siteURL + "#webpage"},
				"mainEntity": questions,
			},
		},
	}
	b, err := json.MarshalIndent(graph, "", " ")
	if err != nil {
		panic(err)
	}
	return template.JS(b)
}

func (s *site) renderNotFound() error {
	return s.writeTo(filepath.Join(s.out, "404.html"), "notfound", map[string]any{
		"Title":   "Page not found — circumvention-corpus",
		"NoIndex": true,
	})
}

func (s *site) renderRobots() error {
	return os.WriteFile(filepath.Join(s.out, "robots.txt"),
		[]byte("User-agent: *\nAllow: /\n\nSitemap: "+siteOrigin+"/sitemap.xml\n"), 0o644)
}

func (s *site) renderSitemap() error {
	lastmod := time.Now().UTC().Format("2006-01-02")
	paths := slices.Clone(s.pages)
	slices.Sort(paths)
	var b strings.Builder
	b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<urlset xmlns=\"http://www.sitemaps.org/schemas/sitemap/0.9\">\n")
	for _, p := range paths {
		fmt.Fprintf(&b, "  <url>\n    <loc>%s</loc>\n    <lastmod>%s</lastmod>\n  </url>\n", html.EscapeString(siteOrigin+p), lastmod)
	}
	b.WriteString("</urlset>\n")
	return os.WriteFile(filepath.Join(s.out, "sitemap.xml"), []byte(b.String()), 0o644)
}
