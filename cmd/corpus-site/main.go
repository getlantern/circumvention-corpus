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
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

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
	tax, err := loadTaxonomy(*corpusDir)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		log.Fatal(err)
	}

	site := &site{
		out:      *outDir,
		papers:   papers,
		byID:     map[string]*Paper{},
		tax:      tax,
		template: mustTemplates(),
	}
	for _, p := range papers {
		site.byID[p.ID] = p
	}

	if err := site.render(); err != nil {
		log.Fatal(err)
	}
	log.Printf("rendered %d papers to %s/", len(papers), *outDir)
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
	out      string
	papers   []*Paper
	byID     map[string]*Paper
	tax      *taxonomy
	template pageTemplates
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
	if err := s.renderUse(); err != nil {
		return err
	}
	return s.renderContribute()
}

func (s *site) renderUse() error {
	return s.writeFile("use", "use", map[string]any{
		"Title": "Use the corpus — circumvention-corpus",
	})
}

func (s *site) writeFile(rel string, name string, data any) error {
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
	})
}

func (s *site) renderTagPages(category string, entries map[string]taxonomyEntry, extract func(*Paper) []string) error {
	// One page per tag — but skip tags with zero papers. They clutter the
	// URL space and the content is empty; they re-appear as soon as a
	// paper picks up that tag.
	for tagID, entry := range entries {
		matches := []*Paper{}
		for _, p := range s.papers {
			if slices.Contains(extract(p), tagID) {
				matches = append(matches, p)
			}
		}
		if len(matches) == 0 {
			continue
		}
		if err := s.writeFile(filepath.Join(category, tagID), "tag", map[string]any{
			"Title":    entry.Name + " — " + category + " — circumvention-corpus",
			"Category": category,
			"TagID":    tagID,
			"Entry":    entry,
			"Papers":   matches,
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
	return os.WriteFile(filepath.Join(s.out, "style.css"), []byte(styleCSS), 0o644)
}
