// corpus-mcp is the MCP server for the circumvention research corpus.
//
// It exposes the corpus over stdio as an MCP server, with the tools
// documented in the project README. v0 reads YAML files from disk at
// startup and serves from in-memory indexes; that's sufficient for a
// few hundred papers and avoids any operational burden for the seed
// deployment. A Postgres-backed implementation will become worthwhile
// once the corpus crosses ~1000 papers or full-text search becomes a
// bottleneck.
//
// Usage:
//
//	corpus-mcp --corpus /path/to/circumvention-corpus
//
// Pass --visibility=public to expose only public records (used by the
// public-facing endpoint). The default lets all visibility levels
// through, which is what the local Lantern-team Claude Code instances
// want.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

const (
	serverName    = "circumvention-corpus"
	serverVersion = "0.3.0"
	protocolRev   = "2024-11-05"
)

// Paper mirrors schema/paper.schema.json. Only the fields the MCP layer
// reads are typed; unknown fields are ignored on load (extra fields can
// be added to YAMLs without rebuilding the server).
type Paper struct {
	ID                       string   `yaml:"id" json:"id"`
	Title                    string   `yaml:"title" json:"title"`
	Authors                  []string `yaml:"authors,omitempty" json:"authors,omitempty"`
	Venue                    string   `yaml:"venue,omitempty" json:"venue,omitempty"`
	Year                     int      `yaml:"year" json:"year"`
	DOI                      string   `yaml:"doi,omitempty" json:"doi,omitempty"`
	ArxivID                  string   `yaml:"arxiv_id,omitempty" json:"arxiv_id,omitempty"`
	URL                      string   `yaml:"url,omitempty" json:"url,omitempty"`
	Abstract                 string   `yaml:"abstract,omitempty" json:"abstract,omitempty"`
	Censors                  []string `yaml:"censors" json:"censors"`
	Techniques               []string `yaml:"techniques" json:"techniques"`
	DefensesDiscussed        []string `yaml:"defenses_discussed,omitempty" json:"defenses_discussed,omitempty"`
	DefensesEvaluatedAgainst []string `yaml:"defenses_evaluated_against,omitempty" json:"defenses_evaluated_against,omitempty"`
	EvaluationMethods        []string `yaml:"evaluation_methods,omitempty" json:"evaluation_methods,omitempty"`
	Core                     bool     `yaml:"core,omitempty" json:"core,omitempty"`
	Notes                    string   `yaml:"notes,omitempty" json:"notes,omitempty"`
	Visibility               string   `yaml:"visibility" json:"visibility"`
	EmbargoUntil             string   `yaml:"embargo_until,omitempty" json:"embargo_until,omitempty"`
	RedistributionTerms      string   `yaml:"redistribution_terms,omitempty" json:"redistribution_terms,omitempty"`
	SharedBy                 string   `yaml:"shared_by,omitempty" json:"shared_by,omitempty"`
	SharedOn                 string   `yaml:"shared_on,omitempty" json:"shared_on,omitempty"`
	DateAdded                string   `yaml:"date_added,omitempty" json:"date_added,omitempty"`
	AddedBy                  string   `yaml:"added_by,omitempty" json:"added_by,omitempty"`
	Sources                  []string `yaml:"sources,omitempty" json:"sources,omitempty"`
	References               []string `yaml:"references,omitempty" json:"references,omitempty"`

	// findingsText holds the concatenated text of every extracted finding
	// for this paper (summary + defense_implications + section). Built once
	// at load time so search_papers can match queries against the rich
	// findings text without re-reading the findings/ directory per query.
	findingsText string `yaml:"-" json:"-"`
}

// Finding mirrors corpus/findings/*.yaml. Findings carry the concrete,
// queryable detail that abstracts often gloss over (e.g. "Iran blocks
// 443 unconditionally on 2025-10-12") — folding them into search_papers
// means a single query surfaces papers via their findings, not just
// their abstracts. Synthesize uses the structured fields directly.
type Finding struct {
	ID                  string   `yaml:"-" json:"id"`
	Paper               string   `yaml:"paper" json:"paper"`
	Kind                string   `yaml:"kind,omitempty" json:"kind,omitempty"`
	Summary             string   `yaml:"summary" json:"summary"`
	Techniques          []string `yaml:"techniques,omitempty" json:"techniques,omitempty"`
	Censors             []string `yaml:"censors,omitempty" json:"censors,omitempty"`
	Defenses            []string `yaml:"defenses,omitempty" json:"defenses,omitempty"`
	DefenseImplications []string `yaml:"defense_implications,omitempty" json:"defense_implications,omitempty"`
	Section             string   `yaml:"section,omitempty" json:"section,omitempty"`
	ExtractedBy         string   `yaml:"extracted_by,omitempty" json:"extracted_by,omitempty"`
}

// store holds the loaded corpus. Searches run over the in-memory slice;
// the corpus is small enough that a linear scan is fine well past v0.
type store struct {
	mu       sync.RWMutex
	papers   []*Paper
	byID     map[string]*Paper
	findings []*Finding
	// taxonomy is loaded raw and passed through to list_taxonomy callers
	// without parsing — clients want the whole document, not individual fields.
	taxonomy map[string]any
	// publicOnly clamps every read to visibility=public (the public-facing
	// endpoint). When false, all visibility tiers are returned.
	publicOnly bool
}

func loadStore(corpusDir string, publicOnly bool) (*store, error) {
	s := &store{byID: map[string]*Paper{}, publicOnly: publicOnly}

	papersDir := filepath.Join(corpusDir, "corpus", "papers")
	entries, err := os.ReadDir(papersDir)
	if err != nil {
		return nil, fmt.Errorf("read papers dir %s: %w", papersDir, err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		path := filepath.Join(papersDir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		var p Paper
		if err := yaml.Unmarshal(raw, &p); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		if p.ID == "" {
			return nil, fmt.Errorf("%s: missing id", path)
		}
		if _, dup := s.byID[p.ID]; dup {
			return nil, fmt.Errorf("%s: duplicate paper id %s", path, p.ID)
		}
		s.byID[p.ID] = &p
		s.papers = append(s.papers, &p)
	}

	// Load findings: concatenate their searchable text onto each paper for
	// the search haystack AND keep structured copies on the store for the
	// synthesize tool. A missing findings dir is fine; broken individual
	// files are logged but don't abort load.
	findingsDir := filepath.Join(corpusDir, "corpus", "findings")
	if fEntries, err := os.ReadDir(findingsDir); err == nil {
		var b strings.Builder
		for _, fe := range fEntries {
			if fe.IsDir() || !strings.HasSuffix(fe.Name(), ".yaml") {
				continue
			}
			fpath := filepath.Join(findingsDir, fe.Name())
			fraw, ferr := os.ReadFile(fpath)
			if ferr != nil {
				log.Printf("findings: read %s: %v", fpath, ferr)
				continue
			}
			var f Finding
			if ferr := yaml.Unmarshal(fraw, &f); ferr != nil {
				log.Printf("findings: parse %s: %v", fpath, ferr)
				continue
			}
			// Derive the finding id from the filename (stable across
			// runs, matches what the curation tooling generates).
			f.ID = strings.TrimSuffix(fe.Name(), ".yaml")
			p, ok := s.byID[f.Paper]
			if !ok {
				continue
			}
			s.findings = append(s.findings, &f)
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
		}
	}

	// Load taxonomy as raw map for list_taxonomy.
	taxPath := filepath.Join(corpusDir, "schema", "taxonomy.yaml")
	taxRaw, err := os.ReadFile(taxPath)
	if err != nil {
		return nil, fmt.Errorf("read taxonomy: %w", err)
	}
	if err := yaml.Unmarshal(taxRaw, &s.taxonomy); err != nil {
		return nil, fmt.Errorf("parse taxonomy: %w", err)
	}

	return s, nil
}

// visible returns true if this paper should be served given the current
// visibility clamp. The public-facing endpoint runs with publicOnly=true,
// so non-public records are never even visited; the local instance runs
// with publicOnly=false and serves everything.
func (s *store) visible(p *Paper) bool {
	if !s.publicOnly {
		return true
	}
	return p.Visibility == "public"
}

// search executes a simple keyword search over title / abstract / notes
// plus exact-match filters on tag fields. Ranking: papers matching all
// filters first, then by core==true, then by year desc.
func (s *store) search(query string, filters searchFilters, limit int) []*Paper {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 {
		limit = 20
	}
	q := strings.ToLower(strings.TrimSpace(query))

	var matches []*Paper
	for _, p := range s.papers {
		if !s.visible(p) {
			continue
		}
		if !filters.match(p) {
			continue
		}
		if q != "" && !textMatch(p, q) {
			continue
		}
		matches = append(matches, p)
	}

	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Core != matches[j].Core {
			return matches[i].Core
		}
		return matches[i].Year > matches[j].Year
	})

	if len(matches) > limit {
		matches = matches[:limit]
	}
	return matches
}

func textMatch(p *Paper, q string) bool {
	if strings.Contains(strings.ToLower(p.Title), q) {
		return true
	}
	if strings.Contains(strings.ToLower(p.Abstract), q) {
		return true
	}
	if strings.Contains(strings.ToLower(p.Notes), q) {
		return true
	}
	for _, a := range p.Authors {
		if strings.Contains(strings.ToLower(a), q) {
			return true
		}
	}
	// Findings text — concatenated per paper at store load time so
	// queries like "iran 443 unconditional" match a paper via its
	// extracted-finding text even when the abstract is generic.
	if strings.Contains(strings.ToLower(p.findingsText), q) {
		return true
	}
	return false
}

type searchFilters struct {
	Censors    []string
	Techniques []string
	Defenses   []string
	YearMin    int
	YearMax    int
	Venue      string
	CoreOnly   bool
}

func (f searchFilters) match(p *Paper) bool {
	if len(f.Censors) > 0 && !anyOverlap(f.Censors, p.Censors) {
		return false
	}
	if len(f.Techniques) > 0 && !anyOverlap(f.Techniques, p.Techniques) {
		return false
	}
	if len(f.Defenses) > 0 {
		all := append(append([]string{}, p.DefensesDiscussed...), p.DefensesEvaluatedAgainst...)
		if !anyOverlap(f.Defenses, all) {
			return false
		}
	}
	if f.YearMin > 0 && p.Year < f.YearMin {
		return false
	}
	if f.YearMax > 0 && p.Year > f.YearMax {
		return false
	}
	if f.Venue != "" && !strings.EqualFold(f.Venue, p.Venue) {
		return false
	}
	if f.CoreOnly && !p.Core {
		return false
	}
	return true
}

func anyOverlap(a, b []string) bool {
	for _, x := range a {
		if slices.Contains(b, x) {
			return true
		}
	}
	return false
}

func (s *store) get(id string) (*Paper, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.byID[id]
	if !ok || !s.visible(p) {
		return nil, false
	}
	return p, true
}

// findRelated returns papers that share at least one tag in the given
// dimension. mode: "same_technique", "same_censor", "same_defense".
func (s *store) findRelated(id, mode string, limit int) []*Paper {
	s.mu.RLock()
	defer s.mu.RUnlock()
	src, ok := s.byID[id]
	if !ok || !s.visible(src) {
		return nil
	}
	if limit <= 0 {
		limit = 10
	}
	var pool []string
	switch mode {
	case "same_technique", "":
		pool = src.Techniques
	case "same_censor":
		pool = src.Censors
	case "same_defense":
		pool = append(append([]string{}, src.DefensesDiscussed...), src.DefensesEvaluatedAgainst...)
	default:
		return nil
	}
	type scored struct {
		p     *Paper
		score int
	}
	var out []scored
	for _, p := range s.papers {
		if p.ID == id || !s.visible(p) {
			continue
		}
		var bag []string
		switch mode {
		case "same_technique", "":
			bag = p.Techniques
		case "same_censor":
			bag = p.Censors
		case "same_defense":
			bag = append(append([]string{}, p.DefensesDiscussed...), p.DefensesEvaluatedAgainst...)
		}
		score := 0
		for _, t := range pool {
			if slices.Contains(bag, t) {
				score++
			}
		}
		if score > 0 {
			out = append(out, scored{p, score})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		if out[i].p.Core != out[j].p.Core {
			return out[i].p.Core
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

// synthesisFinding is a finding enriched with the parent paper's
// citation metadata. The caller (an LLM) writes the synthesized answer
// from these — every claim it produces should be citable back to a
// paper_id + section.
type synthesisFinding struct {
	*Finding
	PaperTitle   string   `json:"paper_title"`
	PaperAuthors []string `json:"paper_authors,omitempty"`
	PaperYear    int      `json:"paper_year"`
	PaperVenue   string   `json:"paper_venue,omitempty"`
	PaperURL     string   `json:"paper_url,omitempty"`
}

type synthesisBundle struct {
	Question              string                  `json:"question"`
	Findings              []synthesisFinding      `json:"findings"`
	Grouped               synthesisGroups         `json:"grouped"`
	PapersWithoutFindings []*Paper                `json:"papers_without_findings,omitempty"`
	Counts                map[string]int          `json:"counts"`
	Synthesis             map[string]string       `json:"synthesis_hint"`
	Coverage              map[string]any          `json:"coverage"`
}

type synthesisGroups struct {
	ByTechnique map[string][]string `json:"by_technique,omitempty"`
	ByCensor    map[string][]string `json:"by_censor,omitempty"`
	ByYear      map[string][]string `json:"by_year,omitempty"`
}

// synthesize retrieves every finding relevant to the question, attaches
// paper-level citation metadata, and returns a structured bundle the
// caller can synthesize from. Lexical match: a finding matches when
// every token of the question appears somewhere in summary +
// defense_implications + section + parent paper title/abstract.
func (s *store) synthesize(question string, censors, techniques, defenses []string, limit int) synthesisBundle {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 {
		limit = 30
	}
	q := strings.ToLower(strings.TrimSpace(question))
	tokens := tokenize(q)

	type scored struct {
		f     synthesisFinding
		score int
	}
	var pool []scored
	matchedPaperIDs := map[string]bool{}
	for _, f := range s.findings {
		p, ok := s.byID[f.Paper]
		if !ok || !s.visible(p) {
			continue
		}
		if len(censors) > 0 && !anyOverlap(censors, f.Censors) && !anyOverlap(censors, p.Censors) {
			continue
		}
		if len(techniques) > 0 && !anyOverlap(techniques, f.Techniques) && !anyOverlap(techniques, p.Techniques) {
			continue
		}
		if len(defenses) > 0 {
			paperDef := append(append([]string{}, p.DefensesDiscussed...), p.DefensesEvaluatedAgainst...)
			if !anyOverlap(defenses, f.Defenses) && !anyOverlap(defenses, paperDef) {
				continue
			}
		}
		// Score = number of question content-tokens that hit this finding's
		// haystack. Switching from strict AND ("every token must hit") to
		// ranked OR-with-floor ("at least one token hits, sort by hit count")
		// is what makes natural-language questions actually return things —
		// otherwise queries like "how does TLS record fragmentation evade SNI
		// censorship" require all six tokens in a single finding's text,
		// which is unrealistic.
		score := 0
		if len(tokens) > 0 {
			hay := strings.ToLower(strings.Join([]string{
				f.Summary,
				strings.Join(f.DefenseImplications, " "),
				f.Section,
				p.Title,
				p.Abstract,
			}, " "))
			for _, t := range tokens {
				if strings.Contains(hay, t) {
					score++
				}
			}
			if score == 0 {
				continue
			}
		}
		pool = append(pool, scored{
			f: synthesisFinding{
				Finding:      f,
				PaperTitle:   p.Title,
				PaperAuthors: p.Authors,
				PaperYear:    p.Year,
				PaperVenue:   p.Venue,
				PaperURL:     p.URL,
			},
			score: score,
		})
		matchedPaperIDs[f.Paper] = true
	}

	// Sort: highest score first, then most recent year, then id for
	// determinism. With no question tokens, score is 0 across the board
	// and we fall through to recency-only — the same behavior as before.
	sort.SliceStable(pool, func(i, j int) bool {
		if pool[i].score != pool[j].score {
			return pool[i].score > pool[j].score
		}
		if pool[i].f.PaperYear != pool[j].f.PaperYear {
			return pool[i].f.PaperYear > pool[j].f.PaperYear
		}
		return pool[i].f.ID < pool[j].f.ID
	})
	if len(pool) > limit {
		pool = pool[:limit]
	}
	matched := make([]synthesisFinding, len(pool))
	for i, sc := range pool {
		matched[i] = sc.f
	}
	// Recompute matched-paper set from the truncated slice so the
	// counts match the findings actually returned.
	matchedPaperIDs = map[string]bool{}
	for _, f := range matched {
		matchedPaperIDs[f.Paper] = true
	}

	groups := synthesisGroups{
		ByTechnique: map[string][]string{},
		ByCensor:    map[string][]string{},
		ByYear:      map[string][]string{},
	}
	for _, f := range matched {
		for _, t := range f.Techniques {
			groups.ByTechnique[t] = append(groups.ByTechnique[t], f.ID)
		}
		for _, c := range f.Censors {
			groups.ByCensor[c] = append(groups.ByCensor[c], f.ID)
		}
		groups.ByYear[fmt.Sprintf("%d", f.PaperYear)] = append(groups.ByYear[fmt.Sprintf("%d", f.PaperYear)], f.ID)
	}

	// Papers that match the question lexically (title/abstract/notes) but
	// have no extracted findings yet — surfacing them tells the caller
	// "this paper looks relevant but its specific claims haven't been
	// extracted; you may want to read it directly."
	var orphans []*Paper
	if len(tokens) > 0 {
		for _, p := range s.papers {
			if !s.visible(p) || matchedPaperIDs[p.ID] {
				continue
			}
			hay := strings.ToLower(p.Title + " " + p.Abstract + " " + p.Notes)
			ok := true
			for _, t := range tokens {
				if !strings.Contains(hay, t) {
					ok = false
					break
				}
			}
			if !ok {
				continue
			}
			// Skip if this paper has any findings at all — its absence
			// from `matched` means the findings didn't match the question,
			// not that the paper is unextracted.
			hasFindings := false
			for _, f := range s.findings {
				if f.Paper == p.ID {
					hasFindings = true
					break
				}
			}
			if hasFindings {
				continue
			}
			orphans = append(orphans, p)
			if len(orphans) >= 10 {
				break
			}
		}
	}

	// Coverage stats so the caller can flag low-evidence answers.
	totalPapers := 0
	papersWithFindings := map[string]bool{}
	for _, p := range s.papers {
		if !s.visible(p) {
			continue
		}
		totalPapers++
	}
	for _, f := range s.findings {
		if p, ok := s.byID[f.Paper]; ok && s.visible(p) {
			papersWithFindings[f.Paper] = true
		}
	}

	return synthesisBundle{
		Question: question,
		Findings: matched,
		Grouped:  groups,
		PapersWithoutFindings: orphans,
		Counts: map[string]int{
			"matched_findings":          len(matched),
			"matched_papers":            len(matchedPaperIDs),
			"papers_without_findings":   len(orphans),
			"total_findings_in_corpus":  len(s.findings),
			"total_papers_in_corpus":    totalPapers,
			"papers_with_any_findings":  len(papersWithFindings),
		},
		Synthesis: map[string]string{
			"role":   "You are answering a research question using extracted findings from a censorship-circumvention research corpus.",
			"format": "Produce a structured answer with three sections: (1) What's known — claims supported by multiple findings, (2) What's contested or uncertain — where findings diverge or are limited to one paper, (3) Open questions — gaps the literature hasn't addressed. Cite every claim inline as (paper_id, §section) using the IDs in the findings array.",
			"caveats": "If counts.matched_findings is low (< 3), say so explicitly — the corpus may not have strong evidence on this question yet. If papers_without_findings is non-empty, mention that those papers may contain relevant claims that haven't been extracted yet.",
		},
		Coverage: map[string]any{
			"corpus_size_papers":   totalPapers,
			"papers_with_findings": len(papersWithFindings),
			"total_findings":       len(s.findings),
		},
	}
}

func tokenize(s string) []string {
	var out []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			tok := cur.String()
			cur.Reset()
			// Strip English stopwords + question words. Without this,
			// natural questions like "what does the literature say about
			// active probing" force an AND-match on "what / does / the /
			// literature / say" — none of which appear in finding
			// summaries — and the result set collapses to empty. Keeping
			// content tokens only restores the intent ("active probing").
			if stopwords[tok] {
				return
			}
			out = append(out, tok)
		}
	}
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			cur.WriteRune(r)
		default:
			flush()
		}
	}
	flush()
	return out
}

// stopwords are the English function-words and question-shaped words
// we drop before AND-matching. Conservative list — only words that are
// near-zero signal as content tokens. We keep nouns/verbs/adjectives
// even when they're common ("paper", "research", "show") because they
// can still be load-bearing for some queries.
var stopwords = map[string]bool{
	// articles, prepositions, conjunctions
	"a": true, "an": true, "the": true, "and": true, "or": true, "but": true,
	"of": true, "in": true, "on": true, "at": true, "to": true, "from": true,
	"for": true, "with": true, "by": true, "as": true, "into": true, "onto": true,
	"upon": true, "out": true, "over": true, "under": true, "than": true,
	"so": true, "if": true, "then": true, "is": true, "are": true, "was": true,
	"were": true, "be": true, "been": true, "being": true, "am": true,
	"have": true, "has": true, "had": true, "having": true,
	"do": true, "does": true, "did": true, "doing": true, "done": true,
	// pronouns
	"i": true, "me": true, "my": true, "we": true, "us": true, "our": true,
	"you": true, "your": true, "he": true, "him": true, "his": true,
	"she": true, "her": true, "they": true, "them": true, "their": true,
	"it": true, "its": true,
	// question / framing words that almost never appear in findings
	"what": true, "which": true, "who": true, "whom": true, "whose": true,
	"how": true, "when": true, "where": true, "why": true,
	"this": true, "that": true, "these": true, "those": true,
	"there": true, "here": true, "any": true, "some": true, "all": true,
	"can": true, "could": true, "will": true, "would": true, "should": true,
	"may": true, "might": true, "must": true,
	"say": true, "says": true, "said": true, "tell": true, "tells": true, "told": true,
	"about": true, "literature": true, "research": true, "papers": true, "paper": true,
	"finding": true, "findings": true, "show": true, "shows": true, "showed": true,
	"know": true, "known": true, "knows": true, "think": true,
	"please": true, "explain": true, "describe": true, "describes": true,
	"summarize": true, "summarise": true, "summary": true,
	"give": true, "gives": true, "list": true, "lists": true,
}

// JSON-RPC envelope (subset of the spec we use here).
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

var tools = []tool{
	{
		Name: "search_papers",
		Description: "Full-text + tag-filter search over the corpus. Returns ranked paper records with id, title, year, venue, abstract, and tags. Use to find papers about a specific censor, technique, or defense.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query":      map[string]any{"type": "string", "description": "Free-text search over title, abstract, authors, and team notes."},
				"censors":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Filter to papers covering any of these censors (taxonomy IDs, e.g. cn / ir / ru)."},
				"techniques": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Filter to papers covering any of these detection techniques."},
				"defenses":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Filter to papers covering any of these defenses."},
				"year_min":   map[string]any{"type": "integer"},
				"year_max":   map[string]any{"type": "integer"},
				"venue":      map[string]any{"type": "string"},
				"core_only":  map[string]any{"type": "boolean", "description": "If true, only return papers marked as 'core' (must-read)."},
				"limit":      map[string]any{"type": "integer", "default": 20},
			},
		},
	},
	{
		Name:        "get_paper",
		Description: "Return the full paper record for a single id.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"id"},
			"properties": map[string]any{
				"id": map[string]any{"type": "string"},
			},
		},
	},
	{
		Name:        "list_taxonomy",
		Description: "Return the controlled-vocabulary taxonomy (censors, techniques, defenses, etc.). Use this before issuing a search to discover the canonical IDs to filter on.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"category": map[string]any{
					"type":        "string",
					"description": "Optional: return only one section of the taxonomy. One of censors, techniques, defenses, evaluation_methods, visibility_levels.",
				},
			},
		},
	},
	{
		Name:        "find_related",
		Description: "Return papers that share tags with the given paper. mode = same_technique (default), same_censor, or same_defense.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"id"},
			"properties": map[string]any{
				"id":    map[string]any{"type": "string"},
				"mode":  map[string]any{"type": "string", "enum": []string{"same_technique", "same_censor", "same_defense"}},
				"limit": map[string]any{"type": "integer", "default": 10},
			},
		},
	},
	{
		Name:        "reload_corpus",
		Description: "Re-scan the corpus directory and reload all paper/finding YAMLs from disk. Use this when a recently-added paper isn't being found by search_papers/get_paper (typically right after a PR is merged), so subsequent queries can see it without restarting the server. Returns the new paper/finding counts.",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	},
	{
		Name: "synthesize",
		Description: "Answer a research question by retrieving every relevant extracted finding (across all papers) along with full paper provenance, grouped by technique / censor / year. The caller is expected to be an LLM that turns this material into a synthesized answer with citations of the form (paper_id, §section). Use this when the user asks 'what does the literature say about X', 'what's known about Y', 'what's contested', or wants a defense recommendation backed by citations. Prefer this over search_papers when the question is about a phenomenon rather than a specific paper.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"question"},
			"properties": map[string]any{
				"question":   map[string]any{"type": "string", "description": "The research question to synthesize an answer to. Examples: 'What does the literature say about Iran SNI-based blocking?', 'What's the evidence on GFW active probing?', 'Which defenses have been evaluated against fully-encrypted traffic detection?'"},
				"censors":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional taxonomy filter — only consider findings tagged with at least one of these censors."},
				"techniques": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional taxonomy filter — only consider findings tagged with at least one of these techniques."},
				"defenses":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional taxonomy filter — only consider findings tagged with at least one of these defenses."},
				"limit":      map[string]any{"type": "integer", "default": 30, "description": "Max findings to return (most recent first)."},
			},
		},
	},
}

// server wraps the in-memory store with the args needed to rebuild it.
// The request loop is single-threaded so we can swap srv.s without locks;
// callers see either the old store or the new one, never a partial.
type server struct {
	s          *store
	corpusDir  string
	publicOnly bool
}

func (srv *server) handle(req rpcRequest) rpcResponse {
	if req.Method == "tools/call" {
		var p struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(req.Params, &p); err == nil && p.Name == "reload_corpus" {
			return srv.handleReload(req.ID)
		}
	}
	return srv.s.handle(req)
}

func (srv *server) handleReload(id json.RawMessage) rpcResponse {
	resp := rpcResponse{JSONRPC: "2.0", ID: id}
	news, err := loadStore(srv.corpusDir, srv.publicOnly)
	if err != nil {
		resp.Result = map[string]any{
			"isError": true,
			"content": []any{map[string]any{"type": "text", "text": fmt.Sprintf("reload failed: %v", err)}},
		}
		return resp
	}
	prevPapers := len(srv.s.papers)
	prevFindings := len(srv.s.findings)
	srv.s = news
	msg := fmt.Sprintf("reloaded from %s: %d papers (was %d), %d findings (was %d)",
		srv.corpusDir, len(news.papers), prevPapers, len(news.findings), prevFindings)
	resp.Result = map[string]any{
		"content": []any{map[string]any{"type": "text", "text": msg}},
	}
	return resp
}

func (s *store) handle(req rpcRequest) rpcResponse {
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		resp.Result = map[string]any{
			"protocolVersion": protocolRev,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": serverName, "version": serverVersion},
		}
	case "tools/list":
		resp.Result = map[string]any{"tools": tools}
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			resp.Error = &rpcError{Code: -32602, Message: err.Error()}
			return resp
		}
		out, err := s.callTool(p.Name, p.Arguments)
		if err != nil {
			resp.Result = map[string]any{
				"isError": true,
				"content": []any{map[string]any{"type": "text", "text": err.Error()}},
			}
			return resp
		}
		resp.Result = map[string]any{
			"content": []any{map[string]any{"type": "text", "text": out}},
		}
	case "notifications/initialized":
		// MCP convention: this is a notification (no id). No response needed,
		// but our caller is built to swallow empty responses gracefully.
		return rpcResponse{}
	default:
		resp.Error = &rpcError{Code: -32601, Message: "method not found: " + req.Method}
	}
	return resp
}

func (s *store) callTool(name string, raw json.RawMessage) (string, error) {
	switch name {
	case "search_papers":
		var args struct {
			Query      string   `json:"query"`
			Censors    []string `json:"censors"`
			Techniques []string `json:"techniques"`
			Defenses   []string `json:"defenses"`
			YearMin    int      `json:"year_min"`
			YearMax    int      `json:"year_max"`
			Venue      string   `json:"venue"`
			CoreOnly   bool     `json:"core_only"`
			Limit      int      `json:"limit"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", err
		}
		results := s.search(args.Query, searchFilters{
			Censors:    args.Censors,
			Techniques: args.Techniques,
			Defenses:   args.Defenses,
			YearMin:    args.YearMin,
			YearMax:    args.YearMax,
			Venue:      args.Venue,
			CoreOnly:   args.CoreOnly,
		}, args.Limit)
		return jsonString(map[string]any{"papers": results, "count": len(results)})
	case "get_paper":
		var args struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", err
		}
		p, ok := s.get(args.ID)
		if !ok {
			return "", fmt.Errorf("paper not found: %s", args.ID)
		}
		return jsonString(p)
	case "list_taxonomy":
		var args struct {
			Category string `json:"category"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", err
		}
		if args.Category == "" {
			return jsonString(s.taxonomy)
		}
		section, ok := s.taxonomy[args.Category]
		if !ok {
			return "", fmt.Errorf("unknown taxonomy category: %s", args.Category)
		}
		return jsonString(map[string]any{args.Category: section})
	case "find_related":
		var args struct {
			ID    string `json:"id"`
			Mode  string `json:"mode"`
			Limit int    `json:"limit"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", err
		}
		results := s.findRelated(args.ID, args.Mode, args.Limit)
		return jsonString(map[string]any{"papers": results, "count": len(results)})
	case "synthesize":
		var args struct {
			Question   string   `json:"question"`
			Censors    []string `json:"censors"`
			Techniques []string `json:"techniques"`
			Defenses   []string `json:"defenses"`
			Limit      int      `json:"limit"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", err
		}
		bundle := s.synthesize(args.Question, args.Censors, args.Techniques, args.Defenses, args.Limit)
		return jsonString(bundle)
	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}

func jsonString(v any) (string, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func main() {
	corpusDir := flag.String("corpus", ".", "Path to the circumvention-corpus repo root.")
	publicOnly := flag.Bool("public-only", false, "If true, serve only visibility=public records. Used by the public-facing endpoint.")
	flag.Parse()

	abs, err := filepath.Abs(*corpusDir)
	if err != nil {
		log.Fatal(err)
	}
	s, err := loadStore(abs, *publicOnly)
	if err != nil {
		log.Fatal(err)
	}
	visBanner := "all visibility tiers"
	if *publicOnly {
		visBanner = "public only"
	}
	log.Printf("corpus-mcp v%s loaded %d papers from %s (%s)", serverVersion, len(s.papers), abs, visBanner)

	srv := &server{s: s, corpusDir: abs, publicOnly: *publicOnly}

	in := bufio.NewReader(os.Stdin)
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()
	enc := json.NewEncoder(out)

	for {
		line, err := in.ReadString('\n')
		if err == io.EOF {
			return
		}
		if err != nil {
			log.Printf("read: %v", err)
			return
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			log.Printf("parse: %v", err)
			continue
		}
		resp := srv.handle(req)
		// notifications/initialized produces a zero-value response we suppress.
		if resp.JSONRPC == "" {
			continue
		}
		if err := enc.Encode(resp); err != nil {
			log.Printf("write: %v", err)
			return
		}
		if err := out.Flush(); err != nil {
			log.Printf("flush: %v", err)
			return
		}
	}
}
