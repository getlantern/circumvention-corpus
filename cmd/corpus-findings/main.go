// corpus-findings extracts structured findings from circumvention-research
// papers using their full PDF/HTML text. The pipeline per paper:
//
//   1. Resolve a PDF URL (paper.url, or fall back to https://arxiv.org/pdf/<arxiv_id>)
//   2. Download to corpus/pdfs/<id>.pdf  (cached; --force re-downloads)
//   3. Extract plain text with pdftotext -layout into corpus/text/<id>.txt
//   4. Build a structured prompt: taxonomy + paper metadata + full text
//   5. Call `claude -p --output-format json` (Sonnet, user's subscription)
//   6. Parse the model's JSON array of findings
//   7. Write one corpus/findings/<id>__<slug>.yaml per finding
//
// Two modes:
//
//	corpus-findings extract --paper <id>      one-shot single-paper extract
//	corpus-findings extract-all [--parallel N --max N]
//	                                            bulk backfill, skips papers
//	                                            that already have findings
//
// The crawler shells out to `corpus-findings extract` for each accepted
// paper so auto-ingest PRs land with both the paper YAML and its findings.
//
// Required tools: pdftotext (poppler), claude (Code CLI), curl.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	classModel       = "claude-sonnet-4-6"
	httpTimeout      = 90 * time.Second
	pdfMaxBytes      = 50 << 20  // 50 MB
	textCapBytes     = 90_000    // chars passed to LLM (~22K tokens; sonnet handles 200K but budget cost)
	textMinBytes     = 800       // below this, treat as "extraction failed"
	pdfTextLayoutFmt = "-layout"
	defaultParallel  = 3
)

// ── Paper struct (subset of the schema we need to extract findings) ──

type Paper struct {
	ID       string   `yaml:"id"`
	Title    string   `yaml:"title"`
	Authors  []string `yaml:"authors,omitempty"`
	Venue    string   `yaml:"venue,omitempty"`
	Year     int      `yaml:"year"`
	ArxivID  string   `yaml:"arxiv_id,omitempty"`
	URL      string   `yaml:"url,omitempty"`
	Abstract string   `yaml:"abstract,omitempty"`
	Notes    string   `yaml:"notes,omitempty"`
}

// finding mirrors schema/finding.schema.json. Only the writable fields.
type finding struct {
	Paper               string   `yaml:"paper" json:"paper"`
	Kind                string   `yaml:"kind" json:"kind"`
	Summary             string   `yaml:"summary" json:"summary"`
	Techniques          []string `yaml:"techniques,omitempty" json:"techniques,omitempty"`
	Defenses            []string `yaml:"defenses,omitempty" json:"defenses,omitempty"`
	Censors             []string `yaml:"censors,omitempty" json:"censors,omitempty"`
	DefenseImplications []string `yaml:"defense_implications,omitempty" json:"defense_implications,omitempty"`
	Section             string   `yaml:"section,omitempty" json:"section,omitempty"`
	ExtractedBy         string   `yaml:"extracted_by,omitempty" json:"extracted_by,omitempty"`
	// Slug suggested by the model. Local field (not persisted).
	SlugSuggestion string `yaml:"-" json:"slug,omitempty"`
}

// ── Subcommand dispatch ──

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "extract":
		extractOneCLI(os.Args[2:])
	case "extract-all":
		extractAllCLI(os.Args[2:])
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n", os.Args[1])
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  corpus-findings extract     --paper <id> [--corpus PATH] [--force]
  corpus-findings extract-all [--corpus PATH] [--parallel N] [--max N] [--force]

extract     — single-paper extract; used by the crawler after each
              accepted paper.
extract-all — bulk backfill; iterates corpus/papers/, skips those that
              already have findings (unless --force), processes the rest
              with bounded concurrency.`)
	os.Exit(2)
}

// ── extract (single-paper) CLI ──

func extractOneCLI(args []string) {
	fs := flag.NewFlagSet("extract", flag.ExitOnError)
	paper := fs.String("paper", "", "paper id (required)")
	corpus := fs.String("corpus", ".", "corpus root")
	force := fs.Bool("force", false, "re-extract even if findings already exist")
	_ = fs.Parse(args)
	if *paper == "" {
		log.Fatal("--paper is required")
	}
	root, err := filepath.Abs(*corpus)
	must(err)
	n, err := extract(context.Background(), root, *paper, *force)
	if err != nil {
		log.Fatalf("extract %s: %v", *paper, err)
	}
	log.Printf("wrote %d findings for %s", n, *paper)
}

// ── extract-all (bulk) CLI ──

func extractAllCLI(args []string) {
	fs := flag.NewFlagSet("extract-all", flag.ExitOnError)
	corpus := fs.String("corpus", ".", "corpus root")
	parallel := fs.Int("parallel", defaultParallel, "concurrent extractions")
	maxN := fs.Int("max", 0, "max papers to process (0 = no limit)")
	force := fs.Bool("force", false, "re-extract papers that already have findings")
	_ = fs.Parse(args)
	root, err := filepath.Abs(*corpus)
	must(err)

	cands, err := findCandidates(root, *force)
	if err != nil {
		log.Fatal(err)
	}
	if *maxN > 0 && len(cands) > *maxN {
		cands = cands[:*maxN]
	}
	log.Printf("processing %d papers with --parallel=%d", len(cands), *parallel)

	sem := make(chan struct{}, *parallel)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var ok, fail, totalFindings int

	for i, id := range cands {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, id string) {
			defer wg.Done()
			defer func() { <-sem }()
			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
			defer cancel()
			n, err := extract(ctx, root, id, false)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				log.Printf("[%d/%d] FAIL %s: %v", idx+1, len(cands), id, err)
				fail++
				return
			}
			if n > 0 {
				log.Printf("[%d/%d] ok   %s: %d findings", idx+1, len(cands), id, n)
				ok++
				totalFindings += n
			} else {
				log.Printf("[%d/%d] skip %s (already has findings)", idx+1, len(cands), id)
			}
		}(i, id)
	}
	wg.Wait()
	log.Printf("done: %d ok, %d failed, %d total findings", ok, fail, totalFindings)
}

// findCandidates returns paper IDs that have no findings yet (or all of
// them if --force). Iterates corpus/papers/*.yaml and corpus/findings/
// once each.
func findCandidates(corpusRoot string, force bool) ([]string, error) {
	hasFindings := map[string]bool{}
	if !force {
		fEntries, err := os.ReadDir(filepath.Join(corpusRoot, "corpus", "findings"))
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		for _, e := range fEntries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
				continue
			}
			id := strings.SplitN(strings.TrimSuffix(e.Name(), ".yaml"), "__", 2)[0]
			hasFindings[id] = true
		}
	}

	pEntries, err := os.ReadDir(filepath.Join(corpusRoot, "corpus", "papers"))
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range pEntries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".yaml")
		if hasFindings[id] {
			continue
		}
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}

// ── Per-paper extraction pipeline ──

// extract runs the full pipeline for one paper. Returns the number of
// findings written. Returns (0, nil) if findings already exist and
// !force (a "skipped" signal). All other errors are returned.
func extract(ctx context.Context, corpusRoot, paperID string, force bool) (int, error) {
	if err := requireTool("pdftotext"); err != nil {
		return 0, err
	}
	if err := requireTool("claude"); err != nil {
		return 0, err
	}

	if !force {
		matches, _ := filepath.Glob(filepath.Join(corpusRoot, "corpus", "findings", paperID+"__*.yaml"))
		if len(matches) > 0 {
			return 0, nil
		}
	}

	paper, err := loadPaper(corpusRoot, paperID)
	if err != nil {
		return 0, err
	}

	url := pdfURL(paper)
	if url == "" {
		return 0, errors.New("no URL or arxiv_id to download")
	}

	pdfPath := filepath.Join(corpusRoot, "corpus", "pdfs", paperID+".pdf")
	if _, err := os.Stat(pdfPath); err != nil {
		if err := downloadPDF(ctx, url, pdfPath); err != nil {
			return 0, fmt.Errorf("download %s: %w", url, err)
		}
	}

	txtPath := filepath.Join(corpusRoot, "corpus", "text", paperID+".txt")
	if _, err := os.Stat(txtPath); err != nil {
		if err := pdfToText(ctx, pdfPath, txtPath); err != nil {
			return 0, fmt.Errorf("pdftotext: %w", err)
		}
	}

	text, err := os.ReadFile(txtPath)
	if err != nil {
		return 0, err
	}
	if len(text) < textMinBytes {
		return 0, fmt.Errorf("extracted text too short (%d bytes); likely a scan or paywall", len(text))
	}
	if len(text) > textCapBytes {
		text = text[:textCapBytes]
	}

	taxRaw, err := os.ReadFile(filepath.Join(corpusRoot, "schema", "taxonomy.yaml"))
	if err != nil {
		return 0, err
	}

	findings, err := callClaude(ctx, paper, string(text), string(taxRaw))
	if err != nil {
		return 0, fmt.Errorf("claude: %w", err)
	}
	if len(findings) == 0 {
		return 0, errors.New("model returned zero findings")
	}

	written := 0
	for _, f := range findings {
		f.Paper = paperID
		f.ExtractedBy = "claude-sonnet-4-6"
		path, err := writeFindingYAML(corpusRoot, paperID, f)
		if err != nil {
			log.Printf("[%s] write %s: %v", paperID, path, err)
			continue
		}
		written++
	}
	return written, nil
}

// pdfURL resolves the URL we'll download the paper from. Prefers an
// explicit url that looks like a PDF; falls back to arxiv ID; finally
// falls back to the paper's url even if it doesn't end in .pdf (some
// papers link to landing pages that redirect to the PDF).
func pdfURL(p Paper) string {
	if p.ArxivID != "" {
		return "https://arxiv.org/pdf/" + p.ArxivID
	}
	if p.URL != "" {
		return p.URL
	}
	return ""
}

func downloadPDF(ctx context.Context, url, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, httpTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, "GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 circumvention-corpus-findings/0.1")
	resp, err := (&http.Client{Timeout: httpTimeout}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	tmp := path + ".part"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	limited := io.LimitReader(resp.Body, pdfMaxBytes)
	if _, err := io.Copy(out, limited); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	out.Close()
	return os.Rename(tmp, path)
}

func pdfToText(ctx context.Context, pdfPath, txtPath string) error {
	if err := os.MkdirAll(filepath.Dir(txtPath), 0o755); err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, "pdftotext", pdfTextLayoutFmt, pdfPath, txtPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%v: %s", err, truncate(stderr.String(), 200))
	}
	return nil
}

// ── Claude call ──

func callClaude(ctx context.Context, paper Paper, fullText, taxonomy string) ([]finding, error) {
	prompt := buildPrompt(paper, fullText, taxonomy)
	cctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(cctx, "claude",
		"-p", prompt,
		"--model", classModel,
		"--output-format", "json",
		"--max-turns", "1",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("claude -p: %v: %s", err, truncate(stderr.String(), 300))
	}
	var envelope struct {
		Result string `json:"result"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		return nil, fmt.Errorf("envelope parse: %w", err)
	}
	text := strings.TrimSpace(envelope.Result)
	text = strings.TrimPrefix(text, "```json")
	text = strings.TrimPrefix(text, "```")
	text = strings.TrimSuffix(text, "```")
	text = strings.TrimSpace(text)
	// The model occasionally emits explanatory prose around the JSON.
	// Find the bracketed array as the canonical payload.
	if i := strings.Index(text, "["); i >= 0 {
		if j := strings.LastIndex(text, "]"); j > i {
			text = text[i : j+1]
		}
	}
	var fs []finding
	if err := json.Unmarshal([]byte(text), &fs); err != nil {
		return nil, fmt.Errorf("findings parse: %w (text starts: %s)", err, truncate(text, 200))
	}
	return fs, nil
}

func buildPrompt(p Paper, body, taxonomy string) string {
	var b strings.Builder
	b.WriteString(`You are extracting structured findings from a censorship-circumvention research paper for a research corpus.

A FINDING is a citation-grade claim — 1-3 sentences with preserved numbers — about what the paper actually demonstrates / measures / proves. NOT a summary; the load-bearing claims that someone designing a circumvention protocol would want to know about.

Output STRICT JSON only — a JSON ARRAY of finding objects. No prose, no markdown, no code fences. Schema per element:

{
  "kind": "detection" | "defense" | "evaluation" | "deployment" | "policy",
  "summary": "1-3 sentences. Specific, with numbers / percentages preserved verbatim.",
  "techniques": ["array of taxonomy ids — required if relevant"],
  "defenses": ["array of taxonomy ids — optional"],
  "censors": ["array of taxonomy ids — optional"],
  "defense_implications": ["1-2 action-oriented bullets for circumvention tool designers"],
  "section": "§4.2 or Table 3 — the paper section the claim came from",
  "slug": "3-6-word kebab-case slug for the filename"
}

Rules:
  - Output 3-5 findings per paper. Quality over quantity.
  - Prioritize claims that quote specific evaluation numbers, concrete protocol design decisions, documented censor behavior measured, or limitations the paper itself flags.
  - Use ONLY taxonomy IDs from the controlled vocabulary below. Don't invent new ones. If no specific id fits, pick the closest broader tag.
  - The "kind" field controls how the corpus indexes the finding:
      detection — censor-side detection technique paper
      defense   — circumvention design / counter-detection
      evaluation — measurement study, deployment metrics
      deployment — primary-source documentation of real-world infra
      policy    — legal/regulatory work
  - "defense_implications" is the highest-leverage field — what concrete change would a circumvention tool designer make in light of this finding?

=== TAXONOMY (controlled vocabulary) ===
`)
	b.WriteString(taxonomy)
	b.WriteString("\n=== PAPER METADATA ===\n")
	fmt.Fprintf(&b, "Title: %s\nAuthors: %s\nVenue: %s\nYear: %d\nID: %s\n",
		p.Title, strings.Join(p.Authors, ", "), p.Venue, p.Year, p.ID)
	if p.Abstract != "" {
		b.WriteString("\nAbstract:\n")
		b.WriteString(p.Abstract)
		b.WriteString("\n")
	}
	if p.Notes != "" {
		b.WriteString("\nTeam notes:\n")
		b.WriteString(p.Notes)
		b.WriteString("\n")
	}
	b.WriteString("\n=== FULL PAPER TEXT (pdftotext output, may be truncated) ===\n")
	b.WriteString(body)
	return b.String()
}

// ── YAML I/O ──

func loadPaper(corpusRoot, id string) (Paper, error) {
	raw, err := os.ReadFile(filepath.Join(corpusRoot, "corpus", "papers", id+".yaml"))
	if err != nil {
		return Paper{}, err
	}
	var p Paper
	if err := yaml.Unmarshal(raw, &p); err != nil {
		return Paper{}, err
	}
	return p, nil
}

func writeFindingYAML(corpusRoot, paperID string, f finding) (string, error) {
	slug := f.SlugSuggestion
	if slug == "" {
		slug = slugFromSummary(f.Summary)
	}
	slug = sanitizeSlug(slug)
	if slug == "" {
		slug = "finding"
	}
	path := filepath.Join(corpusRoot, "corpus", "findings", paperID+"__"+slug+".yaml")
	// If a slug collides with an existing file (multiple findings with
	// similar summaries), append a counter.
	for n := 2; ; n++ {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			break
		}
		path = filepath.Join(corpusRoot, "corpus", "findings",
			fmt.Sprintf("%s__%s-%d.yaml", paperID, slug, n))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return path, err
	}
	// Build a clean record (drop the slug field, ensure paper field is set).
	out := finding{
		Paper:               paperID,
		Kind:                f.Kind,
		Summary:             f.Summary,
		Techniques:          f.Techniques,
		Defenses:            f.Defenses,
		Censors:             f.Censors,
		DefenseImplications: f.DefenseImplications,
		Section:             f.Section,
		ExtractedBy:         "claude-sonnet-4-6",
	}
	buf, err := yaml.Marshal(out)
	if err != nil {
		return path, err
	}
	header := []byte("# Auto-extracted by corpus-findings from full paper text.\n# Review summary + tags before relying.\n")
	full := append(header, buf...)
	return path, os.WriteFile(path, full, 0o644)
}

var slugRE = regexp.MustCompile(`[^a-z0-9]+`)

func sanitizeSlug(s string) string {
	s = strings.ToLower(s)
	s = slugRE.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 60 {
		s = s[:60]
		s = strings.TrimRight(s, "-")
	}
	return s
}

func slugFromSummary(s string) string {
	stop := map[string]bool{
		"a": true, "an": true, "the": true, "of": true, "for": true, "and": true,
		"to": true, "in": true, "on": true, "with": true, "via": true, "is": true,
		"how": true, "what": true, "by": true, "as": true, "from": true, "or": true,
		"are": true, "be": true, "at": true, "this": true, "that": true,
	}
	words := strings.Fields(strings.ToLower(s))
	var keep []string
	for _, w := range words {
		w = sanitizeSlug(w)
		if w == "" || stop[w] {
			continue
		}
		keep = append(keep, w)
		if len(keep) == 5 {
			break
		}
	}
	if len(keep) == 0 {
		return "finding"
	}
	return strings.Join(keep, "-")
}

// ── helpers ──

func requireTool(name string) error {
	if _, err := exec.LookPath(name); err != nil {
		return fmt.Errorf("required tool %q not found in PATH", name)
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func init() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("corpus-findings: ")
}
