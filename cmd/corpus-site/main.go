// corpus-site renders the circumvention corpus as a static website
// suitable for hosting on Cloudflare Pages, GitHub Pages, or anywhere
// else that serves static files.
//
// It reuses the same YAML loading and Paper struct as the MCP server —
// the controlled vocabulary, visibility filtering, and validation rules
// stay in one place. No npm, no JS framework, no node_modules. Total
// build time on the seed corpus is well under a second.
//
// Usage:
//
//	corpus-site --corpus . --out dist
//
// Pass --base-url=https://corpus.lantern.io if the site will be served
// from a non-root path (currently it isn't, but leaving the option
// open keeps preview deploys clean).
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Paper mirrors the YAML records — same shape as the MCP server uses,
// duplicated here rather than imported to keep the two binaries
// independent. (If they diverge later, the integrity test will catch
// it on the YAML side.)
type Paper struct {
	ID                       string   `yaml:"id"`
	Title                    string   `yaml:"title"`
	Authors                  []string `yaml:"authors,omitempty"`
	Venue                    string   `yaml:"venue,omitempty"`
	Year                     int      `yaml:"year"`
	DOI                      string   `yaml:"doi,omitempty"`
	ArxivID                  string   `yaml:"arxiv_id,omitempty"`
	URL                      string   `yaml:"url,omitempty"`
	Abstract                 string   `yaml:"abstract,omitempty"`
	Censors                  []string `yaml:"censors"`
	Techniques               []string `yaml:"techniques"`
	DefensesDiscussed        []string `yaml:"defenses_discussed,omitempty"`
	DefensesEvaluatedAgainst []string `yaml:"defenses_evaluated_against,omitempty"`
	EvaluationMethods        []string `yaml:"evaluation_methods,omitempty"`
	Core                     bool     `yaml:"core,omitempty"`
	Notes                    string   `yaml:"notes,omitempty"`
	Visibility               string   `yaml:"visibility"`
	EmbargoUntil             string   `yaml:"embargo_until,omitempty"`
	RedistributionTerms      string   `yaml:"redistribution_terms,omitempty"`
	DateAdded                string   `yaml:"date_added,omitempty"`
	AddedBy                  string   `yaml:"added_by,omitempty"`
	Sources                  []string `yaml:"sources,omitempty"`
	References               []string `yaml:"references,omitempty"`

	// findingsText holds the concatenated text of every extracted finding
	// for this paper. Folded into the client-side search index so a query
	// hits a paper via its findings, not just its abstract — e.g. "Iran
	// 443 unconditional" matches a paper whose abstract is generic but
	// whose findings document the specific event.
	findingsText string `yaml:"-"`
}

// Finding mirrors corpus/findings/*.yaml. Its ID is the filename stem
// (e.g. 2025-fan-wallbleed__memory-disclosure) and serves as a stable
// permalink: /findings/<id>/.
type Finding struct {
	ID                  string   `yaml:"-"`
	Paper               string   `yaml:"paper"`
	Kind                string   `yaml:"kind,omitempty"`
	Summary             string   `yaml:"summary"`
	Techniques          []string `yaml:"techniques,omitempty"`
	Censors             []string `yaml:"censors,omitempty"`
	Defenses            []string `yaml:"defenses,omitempty"`
	DefenseImplications []string `yaml:"defense_implications,omitempty"`
	Section             string   `yaml:"section,omitempty"`
	ExtractedBy         string   `yaml:"extracted_by,omitempty"`
}

type taxonomyEntry struct {
	Name     string   `yaml:"name"`
	Synonyms []string `yaml:"synonyms,omitempty"`
	Notes    string   `yaml:"notes,omitempty"`
}

type taxonomy struct {
	Censors           map[string]taxonomyEntry `yaml:"censors"`
	Techniques        map[string]taxonomyEntry `yaml:"techniques"`
	Defenses          map[string]taxonomyEntry `yaml:"defenses"`
	EvaluationMethods map[string]taxonomyEntry `yaml:"evaluation_methods"`
	VisibilityLevels  map[string]taxonomyEntry `yaml:"visibility_levels"`
}

func main() {
	corpusDir := flag.String("corpus", ".", "Path to the circumvention-corpus repo root.")
	outDir := flag.String("out", "dist", "Output directory for the rendered site.")
	publicOnly := flag.Bool("public-only", true, "If true (default), render only visibility=public papers. The public Pages site should always run with this on; flip off only when generating an internal-team mirror.")
	flag.Parse()

	papers, err := loadPapers(*corpusDir, *publicOnly)
	if err != nil {
		log.Fatal(err)
	}
	findings, err := loadFindings(*corpusDir, papers)
	if err != nil {
		log.Fatal(err)
	}
	tax, err := loadTaxonomy(*corpusDir)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		log.Fatal(err)
	}

	// Asset version stamp — read from GITHUB_SHA in CI, fall back to a
	// per-second timestamp for local dev. Used to cache-bust the
	// stylesheet and search.js URLs across deploys.
	assetVersion := os.Getenv("GITHUB_SHA")
	if assetVersion == "" {
		assetVersion = fmt.Sprintf("dev%d", time.Now().Unix())
	}
	if len(assetVersion) > 12 {
		assetVersion = assetVersion[:12]
	}

	site := &site{
		out:             *outDir,
		papers:          papers,
		byID:            map[string]*Paper{},
		findings:        findings,
		findingsByID:    map[string]*Finding{},
		findingsByPaper: map[string][]*Finding{},
		tax:             tax,
		template:        mustTemplates(),
		assetVersion:    assetVersion,
	}
	for _, p := range papers {
		site.byID[p.ID] = p
	}
	for _, f := range findings {
		site.findingsByID[f.ID] = f
		site.findingsByPaper[f.Paper] = append(site.findingsByPaper[f.Paper], f)
	}

	if err := site.render(); err != nil {
		log.Fatal(err)
	}
	log.Printf("rendered %d papers and %d findings to %s/", len(papers), len(findings), *outDir)
}

func loadPapers(corpusDir string, publicOnly bool) ([]*Paper, error) {
	dir := filepath.Join(corpusDir, "corpus", "papers")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []*Paper
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		var p Paper
		if err := yaml.Unmarshal(raw, &p); err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		if publicOnly && p.Visibility != "public" {
			continue
		}
		out = append(out, &p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Year != out[j].Year {
			return out[i].Year > out[j].Year
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// loadFindings reads corpus/findings/*.yaml. For every finding it
// (a) appends the searchable text onto the matching paper's
// findingsText so the search bar can hit findings via the haystack,
// and (b) returns the structured Finding records — drives /findings/,
// per-finding permalinks, and findings sections on paper / tag pages.
//
// Findings whose `paper` field doesn't resolve to a known paper id
// are dropped (with a log) — they would render as orphan permalinks
// otherwise. A missing findings dir is fine; bad individual files
// are logged and skipped.
func loadFindings(corpusDir string, papers []*Paper) ([]*Finding, error) {
	byID := map[string]*Paper{}
	for _, p := range papers {
		byID[p.ID] = p
	}
	dir := filepath.Join(corpusDir, "corpus", "findings")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []*Finding
	var b strings.Builder
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		raw, rerr := os.ReadFile(filepath.Join(dir, e.Name()))
		if rerr != nil {
			log.Printf("findings: read %s: %v", e.Name(), rerr)
			continue
		}
		var f Finding
		if uerr := yaml.Unmarshal(raw, &f); uerr != nil {
			log.Printf("findings: parse %s: %v", e.Name(), uerr)
			continue
		}
		f.ID = strings.TrimSuffix(e.Name(), ".yaml")
		p, ok := byID[f.Paper]
		if !ok {
			log.Printf("findings: %s references unknown paper %q — skipping", f.ID, f.Paper)
			continue
		}
		b.Reset()
		b.WriteString(f.Summary)
		b.WriteByte(' ')
		for _, di := range f.DefenseImplications {
			b.WriteString(di)
			b.WriteByte(' ')
		}
		b.WriteString(f.Section)
		if p.findingsText != "" {
			p.findingsText += " "
		}
		p.findingsText += b.String()
		out = append(out, &f)
	}
	return out, nil
}

func loadTaxonomy(corpusDir string) (*taxonomy, error) {
	raw, err := os.ReadFile(filepath.Join(corpusDir, "schema", "taxonomy.yaml"))
	if err != nil {
		return nil, err
	}
	var t taxonomy
	if err := yaml.Unmarshal(raw, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

type site struct {
	out             string
	papers          []*Paper
	byID            map[string]*Paper
	findings        []*Finding
	findingsByID    map[string]*Finding
	findingsByPaper map[string][]*Finding
	tax             *taxonomy
	template        pageTemplates
	assetVersion    string // appended as ?v=... to /style.css and /search.js
}

func (s *site) render() error {
	if err := s.copyStatic(); err != nil {
		return err
	}
	if err := s.renderIndex(); err != nil {
		return err
	}
	if err := s.renderPapersIndex(); err != nil {
		return err
	}
	for _, p := range s.papers {
		if err := s.renderPaper(p); err != nil {
			return err
		}
	}
	if err := s.renderTagPages("censors", s.tax.Censors, func(p *Paper) []string { return p.Censors }); err != nil {
		return err
	}
	if err := s.renderTagPages("techniques", s.tax.Techniques, func(p *Paper) []string { return p.Techniques }); err != nil {
		return err
	}
	if err := s.renderTagPages("defenses", s.tax.Defenses, func(p *Paper) []string {
		all := append([]string{}, p.DefensesDiscussed...)
		for _, d := range p.DefensesEvaluatedAgainst {
			if !slices.Contains(all, d) {
				all = append(all, d)
			}
		}
		return all
	}); err != nil {
		return err
	}
	if err := s.renderTaxonomy(); err != nil {
		return err
	}
	if err := s.renderFindingsIndex(); err != nil {
		return err
	}
	for _, f := range s.findings {
		if err := s.renderFinding(f); err != nil {
			return err
		}
	}
	if err := s.renderUse(); err != nil {
		return err
	}
	if err := s.renderContribute(); err != nil {
		return err
	}
	return s.renderSearchIndex()
}

// renderSearchIndex emits a single JSON document containing the
// searchable fields for every paper: id, title, authors, abstract,
// notes, tags. The client-side search bar fetches it once and runs
// in-memory filtering. At ~600KB for a few hundred papers it's well
// inside the budget — Pagefind would gzip smaller but adds a build
// dependency we don't need yet.
func (s *site) renderSearchIndex() error {
	type record struct {
		ID         string   `json:"id"`
		Title      string   `json:"title"`
		Authors    []string `json:"authors,omitempty"`
		Venue      string   `json:"venue,omitempty"`
		Year       int      `json:"year,omitempty"`
		Abstract   string   `json:"abstract,omitempty"`
		Notes      string   `json:"notes,omitempty"`
		Findings   string   `json:"findings,omitempty"`
		Censors    []string `json:"censors,omitempty"`
		Techniques []string `json:"techniques,omitempty"`
		Defenses   []string `json:"defenses,omitempty"`
		Core       bool     `json:"core,omitempty"`
	}
	out := make([]record, 0, len(s.papers))
	for _, p := range s.papers {
		all := append([]string{}, p.DefensesDiscussed...)
		for _, d := range p.DefensesEvaluatedAgainst {
			if !slices.Contains(all, d) {
				all = append(all, d)
			}
		}
		out = append(out, record{
			ID:         p.ID,
			Title:      p.Title,
			Authors:    p.Authors,
			Venue:      p.Venue,
			Year:       p.Year,
			Abstract:   p.Abstract,
			Notes:      p.Notes,
			Findings:   p.findingsText,
			Censors:    p.Censors,
			Techniques: p.Techniques,
			Defenses:   all,
			Core:       p.Core,
		})
	}
	f, err := os.Create(filepath.Join(s.out, "search-index.json"))
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	return enc.Encode(out)
}

func (s *site) renderUse() error {
	return s.writeFile("use", "use", map[string]any{
		"Title": "Use the corpus — circumvention-corpus",
	})
}

// writeFile injects the build-time AssetVersion into the page data so
// the layout's <link rel="stylesheet" href="/style.css?v={{.AssetVersion}}">
// busts CDN/browser caches whenever the build changes. Without this,
// users continue to see the old CSS after a deploy until they hard
// reload — which has bitten us once already (z-index fix on the search
// dropdown didn't take effect for users with cached style.css).
func (s *site) writeFile(rel string, name string, data any) error {
	if m, ok := data.(map[string]any); ok {
		if _, present := m["AssetVersion"]; !present {
			m["AssetVersion"] = s.assetVersion
		}
		if _, present := m["PaperCount"]; !present {
			m["PaperCount"] = len(s.papers)
		}
		if _, present := m["FindingsCount"]; !present {
			m["FindingsCount"] = len(s.findings)
		}
	}
	dir := filepath.Join(s.out, rel)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.Create(filepath.Join(dir, "index.html"))
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	defer w.Flush()
	t, ok := s.template[name]
	if !ok {
		return fmt.Errorf("unknown page template: %s", name)
	}
	return t.ExecuteTemplate(w, "layout", data)
}

func (s *site) renderIndex() error {
	core := []*Paper{}
	for _, p := range s.papers {
		if p.Core {
			core = append(core, p)
		}
	}
	recent := s.papers
	if len(recent) > 10 {
		recent = recent[:10]
	}
	return s.writeFile(".", "index", map[string]any{
		"Title":  "circumvention-corpus",
		"Core":   core,
		"Recent": recent,
		"Counts": map[string]int{
			"papers":     len(s.papers),
			"censors":    len(s.tax.Censors),
			"techniques": len(s.tax.Techniques),
			"defenses":   len(s.tax.Defenses),
		},
	})
}

func (s *site) renderPapersIndex() error {
	return s.writeFile("papers", "papers_index", map[string]any{
		"Title":  "All papers — circumvention-corpus",
		"Papers": s.papers,
	})
}

func (s *site) renderPaper(p *Paper) error {
	related := relatedPapers(p, s.papers, 8)
	refResolved := []*Paper{}
	for _, ref := range p.References {
		if r, ok := s.byID[ref]; ok {
			refResolved = append(refResolved, r)
		}
	}
	return s.writeFile(filepath.Join("papers", p.ID), "paper", map[string]any{
		"Title":      p.Title + " — circumvention-corpus",
		"Paper":      p,
		"Tax":        s.tax,
		"Related":    related,
		"References": refResolved,
		"Findings":   s.findingsByPaper[p.ID],
	})
}

// renderFindingsIndex emits /findings/ — a single page listing every
// finding with paper context and tag pills. Sorted by paper year desc,
// then alphabetical, so the freshest findings rise to the top. With
// ~1200 entries this comes out to ~700KB of HTML; if it ever crosses
// 5000 we should paginate or shard by year.
func (s *site) renderFindingsIndex() error {
	rows := s.findingRows(s.findings)
	techCounts := map[string]int{}
	censorCounts := map[string]int{}
	yearCounts := map[int]int{}
	for _, f := range s.findings {
		for _, t := range f.Techniques {
			techCounts[t]++
		}
		for _, c := range f.Censors {
			censorCounts[c]++
		}
		if p, ok := s.byID[f.Paper]; ok {
			yearCounts[p.Year]++
		}
	}
	return s.writeFile("findings", "findings_index", map[string]any{
		"Title":         "Findings — circumvention-corpus",
		"Rows":          rows,
		"Tax":           s.tax,
		"FindingsCount": len(s.findings),
		"TechCounts":    techCounts,
		"CensorCounts":  censorCounts,
		"YearCounts":    yearCounts,
	})
}

// renderFinding emits /findings/<id>/ — the per-finding permalink page.
// Shows the full summary, paper context, defense implications, taxonomy
// pills, and links to other findings from the same paper or sharing the
// same techniques.
func (s *site) renderFinding(f *Finding) error {
	p, ok := s.byID[f.Paper]
	if !ok {
		return nil
	}
	// Sibling findings = other findings extracted from the same paper.
	var siblings []*Finding
	for _, sf := range s.findingsByPaper[f.Paper] {
		if sf.ID != f.ID {
			siblings = append(siblings, sf)
		}
	}
	// Related findings = findings from other papers that share at least
	// one technique. Capped to a handful, recency-biased.
	var related []*Finding
	techSet := map[string]bool{}
	for _, t := range f.Techniques {
		techSet[t] = true
	}
	if len(techSet) > 0 {
		type scored struct {
			f     *Finding
			score int
			year  int
		}
		var pool []scored
		for _, other := range s.findings {
			if other.ID == f.ID || other.Paper == f.Paper {
				continue
			}
			score := 0
			for _, t := range other.Techniques {
				if techSet[t] {
					score++
				}
			}
			if score == 0 {
				continue
			}
			y := 0
			if op, ok := s.byID[other.Paper]; ok {
				y = op.Year
			}
			pool = append(pool, scored{other, score, y})
		}
		sort.SliceStable(pool, func(i, j int) bool {
			if pool[i].score != pool[j].score {
				return pool[i].score > pool[j].score
			}
			return pool[i].year > pool[j].year
		})
		for i := 0; i < len(pool) && i < 6; i++ {
			related = append(related, pool[i].f)
		}
	}
	return s.writeFile(filepath.Join("findings", f.ID), "finding", map[string]any{
		"Title":    truncate(f.Summary, 80) + " — circumvention-corpus",
		"Finding":  f,
		"Paper":    p,
		"Tax":      s.tax,
		"Siblings": siblings,
		"Related":  related,
	})
}

// findingsForTag returns every finding tagged with tagID under category
// (censors / techniques / defenses). A finding contributes via its own
// tag list — we don't fall back to the parent paper's tags here, since
// /techniques/sni-blocking/ should list findings that actually discuss
// SNI blocking, not findings whose parent paper happens to mention it.
func (s *site) findingsForTag(category, tagID string) []*Finding {
	var out []*Finding
	for _, f := range s.findings {
		var bag []string
		switch category {
		case "censors":
			bag = f.Censors
		case "techniques":
			bag = f.Techniques
		case "defenses":
			bag = f.Defenses
		default:
			return nil
		}
		if slices.Contains(bag, tagID) {
			out = append(out, f)
		}
	}
	return out
}

// findingRows returns findings annotated with their parent paper for
// row rendering, sorted by paper year desc then finding id.
func (s *site) findingRows(in []*Finding) []findingRow {
	rows := make([]findingRow, 0, len(in))
	for _, f := range in {
		p := s.byID[f.Paper]
		if p == nil {
			continue
		}
		rows = append(rows, findingRow{Finding: f, Paper: p})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Paper.Year != rows[j].Paper.Year {
			return rows[i].Paper.Year > rows[j].Paper.Year
		}
		return rows[i].Finding.ID < rows[j].Finding.ID
	})
	return rows
}

type findingRow struct {
	Finding *Finding
	Paper   *Paper
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	cut := s[:n]
	if i := strings.LastIndex(cut, " "); i > n/2 {
		cut = cut[:i]
	}
	return cut + "…"
}

func (s *site) renderTagPages(category string, entries map[string]taxonomyEntry, extract func(*Paper) []string) error {
	// One page per tag — but skip tags with zero papers AND zero findings.
	// They clutter the URL space and the content is empty; they re-appear
	// as soon as a paper or finding picks up that tag.
	for tagID, entry := range entries {
		matches := []*Paper{}
		for _, p := range s.papers {
			if slices.Contains(extract(p), tagID) {
				matches = append(matches, p)
			}
		}
		findingMatches := s.findingsForTag(category, tagID)
		if len(matches) == 0 && len(findingMatches) == 0 {
			continue
		}
		if err := s.writeFile(filepath.Join(category, tagID), "tag", map[string]any{
			"Title":    entry.Name + " — " + category + " — circumvention-corpus",
			"Category": category,
			"TagID":    tagID,
			"Entry":    entry,
			"Papers":   matches,
			"Findings": s.findingRows(findingMatches),
		}); err != nil {
			return err
		}
	}
	// And an index of the category itself.
	type tagRow struct {
		ID    string
		Entry taxonomyEntry
		Count int
	}
	rows := make([]tagRow, 0, len(entries))
	for id, e := range entries {
		count := 0
		for _, p := range s.papers {
			if slices.Contains(extract(p), id) {
				count++
			}
		}
		rows = append(rows, tagRow{ID: id, Entry: e, Count: count})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Count != rows[j].Count {
			return rows[i].Count > rows[j].Count
		}
		return rows[i].ID < rows[j].ID
	})
	return s.writeFile(category, "tag_index", map[string]any{
		"Title":    strings.Title(category) + " — circumvention-corpus",
		"Category": category,
		"Rows":     rows,
	})
}

func (s *site) renderTaxonomy() error {
	return s.writeFile("taxonomy", "taxonomy", map[string]any{
		"Title": "Taxonomy — circumvention-corpus",
		"Tax":   s.tax,
	})
}

func (s *site) renderContribute() error {
	return s.writeFile("contribute", "contribute", map[string]any{
		"Title": "Contribute — circumvention-corpus",
	})
}

func relatedPapers(src *Paper, all []*Paper, limit int) []*Paper {
	type scored struct {
		p     *Paper
		score int
	}
	var out []scored
	for _, p := range all {
		if p.ID == src.ID {
			continue
		}
		score := 0
		for _, t := range src.Techniques {
			if slices.Contains(p.Techniques, t) {
				score += 2
			}
		}
		for _, c := range src.Censors {
			if slices.Contains(p.Censors, c) {
				score++
			}
		}
		for _, d := range src.DefensesDiscussed {
			if slices.Contains(p.DefensesDiscussed, d) {
				score++
			}
		}
		if score > 0 {
			out = append(out, scored{p, score})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		return out[i].p.Year > out[j].p.Year
	})
	if len(out) > limit {
		out = out[:limit]
	}
	res := make([]*Paper, len(out))
	for i, s := range out {
		res[i] = s.p
	}
	return res
}

func (s *site) copyStatic() error {
	if err := os.WriteFile(filepath.Join(s.out, "style.css"), []byte(styleCSS), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(s.out, "search.js"), []byte(searchJS), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.out, "favicon.svg"), []byte(faviconSVG), 0o644)
}
