// corpus-crawl polls research-paper sources for circumvention-relevant
// work and writes proposed YAML stubs to corpus/papers/, then opens a
// PR labeled `auto-ingest` for human review.
//
// Designed to run on a residential-IP machine (afisk@mini) rather than
// GitHub Actions: arXiv / USENIX / NDSS routinely throttle datacenter
// IPs, and `wick fetch` (browser-grade, JS-capable) handles fragile
// conference proceedings pages that curl/net-http can't. Classification
// shells out to `claude -p` so it reuses the user's Claude subscription
// and doesn't require an ANTHROPIC_API_KEY secret anywhere.
//
// Subcommands:
//
//	corpus-crawl run [--corpus PATH] [--max N] [--window-days D] [--dry-run] [--no-pr]
//	    Run one crawl synchronously. Used by launchd cron and by serve mode.
//
//	corpus-crawl serve --listen :8788 [--auth-token TOKEN]
//	    HTTP server with POST /crawl that triggers a run. Sits behind a
//	    Cloudflare Tunnel (crawl.lantern.io) so triggers can come from
//	    anywhere. Requires Bearer-token auth.
//
// Sources (v1): arXiv cs.CR via the standard query API. Add PETS / FOCI /
// USENIX scrapers in later versions; the wick fetcher already handles
// the page-loading layer so adding a source is mostly pattern-match
// + tag-extraction code.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
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
	"time"

	"gopkg.in/yaml.v3"
)

const (
	arxivAPI    = "https://export.arxiv.org/api/query"
	arxivCat    = "cs.CR"
	arxivLimit  = 200
	classModel  = "claude-haiku-4-5"
	httpTimeout = 30 * time.Second
)

// keywords gates which arXiv submissions are interesting enough to send
// to the LLM classifier. False positives are fine — the LLM rejects
// non-relevant ones in the next stage and a human gates the final PR.
var keywords = []string{
	"censor", "circumvent", "blocking", "blocklist", "throttl",
	"deep packet inspection", " dpi ", "dpi-", "protocol obfuscat",
	"great firewall", "gfw", "iran ", "russia ", "china's", "chinese internet",
	"belarus", "kazakh", "myanmar", "turkmen", "saudi", "uae ", "egypt", "north kore",
	"active probing", "fingerprint", "ja3", "ja4", "clienthello",
	"sni-based", "sni filter", "dns injection", "dns poison", "rst injection",
	"middlebox", "traffic analysis", "website fingerprint", "flow correlat",
	"tls fingerprint", "fully encrypted", "fully-encrypted", "entropy detect",
	"tor ", "tor bridge", "snowflake", "obfs4", "scramblesuit",
	"shadowsocks", "v2ray", "vless", "vmess", "trojan", "reality",
	"hysteria", "amneziawg", "wireguard", "kindling",
	"refraction", "decoy routing", "tapdance", "conjure", "telex",
	"domain fronting", "domain front", "fronted",
	"meek ", "meek-", "esni", "ech ", "encrypted clienthello",
	"steganograph", "marionette", "format-transforming",
	"ooni", "censored planet", "iclab", "iris ", "net4people",
	"geneva",
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "run":
		runOnce(os.Args[2:])
	case "serve":
		serve(os.Args[2:])
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n", os.Args[1])
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  corpus-crawl run   [--corpus PATH] [--max N] [--window-days D] [--dry-run] [--no-pr]
  corpus-crawl serve --listen ADDR [--auth-token TOKEN] [--corpus PATH]

run    — execute one crawl synchronously (used by launchd)
serve  — HTTP server with POST /crawl (used by Cloudflare Tunnel)`)
	os.Exit(2)
}

// runOptions are the knobs run-mode reads from flags or HTTP query
// params. Kept in a struct so the HTTP handler can call runWith() with
// the same shape that runOnce() uses.
type runOptions struct {
	corpus     string
	maxN       int
	windowDays int
	dryRun     bool
	noPR       bool
}

func runOnce(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	opts := runOptions{}
	fs.StringVar(&opts.corpus, "corpus", ".", "path to circumvention-corpus repo root")
	fs.IntVar(&opts.maxN, "max", 10, "max papers to ingest per run")
	fs.IntVar(&opts.windowDays, "window-days", 14, "look back this many days")
	fs.BoolVar(&opts.dryRun, "dry-run", false, "don't write YAML files; print what would happen")
	fs.BoolVar(&opts.noPR, "no-pr", false, "write YAMLs but skip opening a PR")
	_ = fs.Parse(args)

	res, err := runWith(context.Background(), opts)
	if err != nil {
		log.Fatalf("crawl failed: %v", err)
	}
	log.Printf("done — wrote %d new YAML(s); pr=%s", len(res.Written), res.PRURL)
}

// runResult summarizes a single crawl. Returned both from runOnce and
// from the HTTP handler.
type runResult struct {
	Considered int      `json:"considered"`
	Filtered   int      `json:"filtered"`
	Novel      int      `json:"novel"`
	Accepted   int      `json:"accepted"`
	Written    []string `json:"written"`
	PRURL      string   `json:"pr_url,omitempty"`
}

func runWith(ctx context.Context, opts runOptions) (*runResult, error) {
	root, err := filepath.Abs(opts.corpus)
	if err != nil {
		return nil, err
	}

	if err := requireTool("wick"); err != nil && !opts.dryRun {
		return nil, err
	}
	if err := requireTool("claude"); err != nil && !opts.dryRun {
		return nil, err
	}

	existing, err := loadExisting(root)
	if err != nil {
		return nil, fmt.Errorf("load existing corpus: %w", err)
	}
	log.Printf("loaded %d existing paper records for dedup", len(existing.byID))

	taxRaw, err := os.ReadFile(filepath.Join(root, "schema", "taxonomy.yaml"))
	if err != nil {
		return nil, fmt.Errorf("read taxonomy: %w", err)
	}

	since := time.Now().AddDate(0, 0, -opts.windowDays)
	cands, err := fetchArxiv(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("fetch arxiv: %w", err)
	}
	log.Printf("fetched %d candidates from arXiv cs.CR (%d-day window)", len(cands), opts.windowDays)
	res := &runResult{Considered: len(cands)}

	kept := make([]candidate, 0, len(cands))
	for _, c := range cands {
		if matchesKeywords(c) {
			kept = append(kept, c)
		}
	}
	res.Filtered = len(kept)
	log.Printf("%d passed keyword filter", len(kept))

	novel := make([]candidate, 0, len(kept))
	for _, c := range kept {
		if existing.contains(c) {
			continue
		}
		novel = append(novel, c)
	}
	res.Novel = len(novel)
	log.Printf("%d are novel", len(novel))

	if len(novel) > opts.maxN {
		log.Printf("capping at --max=%d", opts.maxN)
		novel = novel[:opts.maxN]
	}
	if len(novel) == 0 {
		log.Println("nothing to ingest")
		return res, nil
	}

	var out []accepted
	for _, c := range novel {
		if opts.dryRun {
			log.Printf("DRY RUN: would classify %q (%s)", truncate(c.Title, 60), c.ArxivID)
			out = append(out, accepted{c: c, k: classification{IsRelevant: true, Censors: []string{"generic"}, Techniques: []string{"dpi"}}})
			continue
		}
		k, err := classifyWithClaude(ctx, c, string(taxRaw))
		if err != nil {
			log.Printf("classify %s: %v (skipping)", c.ArxivID, err)
			continue
		}
		if !k.IsRelevant {
			log.Printf("not-relevant: %s — %s", c.ArxivID, k.Reason)
			continue
		}
		log.Printf("✓ %s — censors=%v techniques=%v", c.ArxivID, k.Censors, k.Techniques)
		out = append(out, accepted{c: c, k: k})
	}
	res.Accepted = len(out)

	if len(out) == 0 {
		log.Println("classifier rejected all; nothing to write")
		return res, nil
	}

	written, err := writeYAMLs(root, out, opts.dryRun)
	if err != nil {
		return res, err
	}
	res.Written = written

	if opts.dryRun || opts.noPR || len(written) == 0 {
		return res, nil
	}

	prURL, err := openPR(ctx, root, out, written)
	if err != nil {
		log.Printf("PR creation failed (YAMLs are written, push manually): %v", err)
	} else {
		res.PRURL = prURL
		log.Printf("PR opened: %s", prURL)
	}
	return res, nil
}

// ── arXiv fetch ────────────────────────────────────────────────────

type arxivAuthor struct {
	Name string `xml:"name"`
}

type arxivEntry struct {
	ID        string        `xml:"id"`
	Updated   string        `xml:"updated"`
	Published string        `xml:"published"`
	Title     string        `xml:"title"`
	Summary   string        `xml:"summary"`
	Authors   []arxivAuthor `xml:"author"`
}

type arxivFeed struct {
	XMLName xml.Name     `xml:"feed"`
	Entries []arxivEntry `xml:"entry"`
}

type candidate struct {
	ArxivID  string
	Title    string
	Abstract string
	Authors  []string
	URL      string
	Year     int
	Updated  time.Time
}

// accepted pairs a candidate with its successful classification — the
// shape passed to YAML writers, PR builders, etc.
type accepted struct {
	c candidate
	k classification
}

func fetchArxiv(ctx context.Context, since time.Time) ([]candidate, error) {
	// arXiv has a plain XML query API — wick is overkill (and outputs
	// markdown by default, which destroys the XML). The whole point of
	// running this crawler on the mini is that *the source IP* is
	// residential; we don't need a wick browser shell for plain XML.
	// wick is reserved for the JS-rendered HTML conference pages we'll
	// add later (PETS, USENIX, NDSS, IMC).
	q := fmt.Sprintf("?search_query=cat:%s&start=0&max_results=%d&sortBy=submittedDate&sortOrder=descending",
		arxivCat, arxivLimit)
	cctx, cancel := context.WithTimeout(ctx, httpTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, "GET", arxivAPI+q, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "circumvention-corpus-crawl/0.1 (https://github.com/getlantern/circumvention-corpus)")
	resp, err := (&http.Client{Timeout: httpTimeout}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("arxiv API: status %d: %s", resp.StatusCode, string(body))
	}
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var feed arxivFeed
	if err := xml.Unmarshal(out, &feed); err != nil {
		return nil, fmt.Errorf("parse arxiv feed: %w", err)
	}
	var cands []candidate
	for _, e := range feed.Entries {
		updated, _ := time.Parse(time.RFC3339, e.Updated)
		if !updated.IsZero() && updated.Before(since) {
			continue
		}
		c := candidate{
			ArxivID:  arxivIDFromURL(e.ID),
			Title:    cleanText(e.Title),
			Abstract: cleanText(e.Summary),
			URL:      strings.Replace(e.ID, "http://", "https://", 1),
			Updated:  updated,
		}
		for _, a := range e.Authors {
			c.Authors = append(c.Authors, strings.TrimSpace(a.Name))
		}
		if pub, err := time.Parse(time.RFC3339, e.Published); err == nil {
			c.Year = pub.Year()
		}
		cands = append(cands, c)
	}
	return cands, nil
}

// wickFetch shells out to `wick fetch <url>` for browser-grade page
// fetching. Used by the (future) JS-rendered conference-page scrapers
// — PETS / FOCI / USENIX / NDSS / IMC. arXiv's plain XML API uses
// net/http above; wick's markdown/html/text output formats would
// mangle it.
//
// The "format" argument controls what wick extracts: "html" for raw
// HTML scraping, "markdown" for human-friendly text, "text" for
// unstructured plaintext.
func wickFetch(ctx context.Context, url, format string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, httpTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "wick", "fetch", "--format", format, url)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("wick fetch %s: %v: %s", url, err, stderr.String())
	}
	return stdout.Bytes(), nil
}

// ── classification ─────────────────────────────────────────────────

type classification struct {
	IsRelevant        bool     `json:"is_relevant"`
	Reason            string   `json:"reason,omitempty"`
	Censors           []string `json:"censors"`
	Techniques        []string `json:"techniques"`
	DefensesDiscussed []string `json:"defenses_discussed,omitempty"`
	EvaluationMethods []string `json:"evaluation_methods,omitempty"`
	Notes             string   `json:"notes,omitempty"`
}

// classifyWithClaude shells out to `claude -p` to classify a paper. We
// pass the full taxonomy YAML in the prompt and ask for strict JSON
// output. claude -p uses the user's logged-in Claude subscription, so
// no API key is needed — this is the load-bearing reason the crawler
// lives on the user's residential machine instead of in CI.
func classifyWithClaude(ctx context.Context, c candidate, taxonomy string) (classification, error) {
	prompt := buildClassifyPrompt(c, taxonomy)
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "claude",
		"-p", prompt,
		"--model", classModel,
		"--output-format", "json",
		"--max-turns", "1",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return classification{}, fmt.Errorf("claude -p: %v: %s", err, truncate(stderr.String(), 400))
	}

	// claude --output-format json returns:
	//   { "type": "result", "subtype": "success", "result": "<text>", ... }
	var envelope struct {
		Result string `json:"result"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		return classification{}, fmt.Errorf("parse claude json envelope: %w", err)
	}
	text := strings.TrimSpace(envelope.Result)
	text = strings.TrimPrefix(text, "```json")
	text = strings.TrimPrefix(text, "```")
	text = strings.TrimSuffix(text, "```")
	text = strings.TrimSpace(text)

	var k classification
	if err := json.Unmarshal([]byte(text), &k); err != nil {
		return classification{}, fmt.Errorf("parse classification: %w (text: %s)", err, truncate(text, 200))
	}
	return k, nil
}

func buildClassifyPrompt(c candidate, taxonomy string) string {
	var b strings.Builder
	b.WriteString(`You are a research librarian curating a censorship-circumvention research corpus.
Given a paper's title, authors, and abstract, decide whether it is relevant to the corpus, and propose taxonomy tags from the controlled vocabulary below.

A paper is RELEVANT if it studies (a) censorship measurement, (b) censor capabilities or detection techniques, (c) circumvention protocol design, (d) evaluation of circumvention systems, (e) traffic analysis attacks/defenses on encrypted tunnels, or (f) policy/legal work that materially affects circumvention tooling. Generic crypto, cryptanalysis, ML adversarial robustness, or unrelated infosec is NOT relevant.

Use ONLY the taxonomy IDs below. Do not invent new ones. If a paper is relevant but no existing tag fits exactly, pick the closest broader tag.

Output STRICT JSON only. No prose, no markdown, no code fences. Schema:
{
  "is_relevant": boolean,
  "reason": "1 sentence",
  "censors": ["array of censor ids"],
  "techniques": ["array of technique ids"],
  "defenses_discussed": ["array of defense ids, optional"],
  "evaluation_methods": ["array, optional"],
  "notes": "1 sentence on Lantern relevance, or empty"
}

=== TAXONOMY ===
`)
	b.WriteString(taxonomy)
	b.WriteString("\n=== PAPER ===\n")
	fmt.Fprintf(&b, "Title: %s\nAuthors: %s\nArXiv: %s\n\nAbstract:\n%s\n",
		c.Title, strings.Join(c.Authors, ", "), c.ArxivID, c.Abstract)
	return b.String()
}

// ── existing-corpus dedup ─────────────────────────────────────────

type existingCorpus struct {
	byID    map[string]bool
	byTitle map[string]bool
}

func (e *existingCorpus) contains(c candidate) bool {
	if e.byID[proposeID(c)] {
		return true
	}
	if e.byTitle[normalizeTitle(c.Title)] {
		return true
	}
	return false
}

func loadExisting(root string) (*existingCorpus, error) {
	dir := filepath.Join(root, "corpus", "papers")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := &existingCorpus{byID: map[string]bool{}, byTitle: map[string]bool{}}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		var p struct {
			ID    string `yaml:"id"`
			Title string `yaml:"title"`
		}
		if err := yaml.Unmarshal(raw, &p); err != nil {
			continue
		}
		if p.ID != "" {
			out.byID[p.ID] = true
		}
		if p.Title != "" {
			out.byTitle[normalizeTitle(p.Title)] = true
		}
	}
	return out, nil
}

// ── YAML writing ───────────────────────────────────────────────────

type paperYAML struct {
	ID                string   `yaml:"id"`
	Title             string   `yaml:"title"`
	Authors           []string `yaml:"authors,omitempty"`
	Venue             string   `yaml:"venue,omitempty"`
	Year              int      `yaml:"year"`
	ArxivID           string   `yaml:"arxiv_id,omitempty"`
	URL               string   `yaml:"url,omitempty"`
	Abstract          string   `yaml:"abstract,omitempty"`
	Censors           []string `yaml:"censors"`
	Techniques        []string `yaml:"techniques"`
	DefensesDiscussed []string `yaml:"defenses_discussed,omitempty"`
	EvaluationMethods []string `yaml:"evaluation_methods,omitempty"`
	Core              bool     `yaml:"core"`
	Notes             string   `yaml:"notes,omitempty"`
	Visibility        string   `yaml:"visibility"`
	DateAdded         string   `yaml:"date_added"`
	AddedBy           string   `yaml:"added_by"`
	Sources           []string `yaml:"sources,omitempty"`
}

func writeYAMLs(root string, items []accepted, dryRun bool) ([]string, error) {
	var written []string
	for _, a := range items {
		id := proposeID(a.c)
		path := filepath.Join(root, "corpus", "papers", id+".yaml")
		if _, err := os.Stat(path); err == nil {
			log.Printf("skip %s — file already exists", path)
			continue
		}
		y := paperYAML{
			ID:                id,
			Title:             a.c.Title,
			Authors:           a.c.Authors,
			Venue:             "arXiv preprint",
			Year:              a.c.Year,
			ArxivID:           a.c.ArxivID,
			URL:               a.c.URL,
			Abstract:          a.c.Abstract,
			Censors:           defaultIfEmpty(a.k.Censors, []string{"generic"}),
			Techniques:        defaultIfEmpty(a.k.Techniques, []string{"dpi"}),
			DefensesDiscussed: a.k.DefensesDiscussed,
			EvaluationMethods: a.k.EvaluationMethods,
			Core:              false,
			Notes:             noteWithProvenance(a.k.Notes),
			Visibility:        "public",
			DateAdded:         time.Now().UTC().Format("2006-01-02"),
			AddedBy:           "corpus-crawl-bot",
			Sources:           []string{"arxiv:" + a.c.ArxivID},
		}
		if dryRun {
			log.Printf("DRY RUN: would write %s", path)
			continue
		}
		buf, err := yaml.Marshal(y)
		if err != nil {
			return written, err
		}
		header := []byte(fmt.Sprintf("# Auto-ingested by corpus-crawl. Review and tighten the tags +\n# notes before merging. Source: arXiv %s\n", a.c.ArxivID))
		full := append(header, buf...)
		if err := os.WriteFile(path, full, 0o644); err != nil {
			return written, err
		}
		written = append(written, path)
		log.Printf("wrote %s", path)
	}
	return written, nil
}

// ── PR creation (gh CLI) ───────────────────────────────────────────

func openPR(ctx context.Context, root string, items []accepted, written []string) (string, error) {
	if err := requireTool("gh"); err != nil {
		return "", err
	}
	branch := fmt.Sprintf("auto-ingest/%s", time.Now().UTC().Format("2006-01-02-150405"))

	run := func(name string, args ...string) error {
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%s %v: %v\n%s", name, args, err, string(out))
		}
		return nil
	}

	// Configure git identity for the bot if not set.
	_ = run("git", "config", "user.name", "circumvention-corpus-bot")
	_ = run("git", "config", "user.email", "bot@lantern.io")

	if err := run("git", "checkout", "-b", branch); err != nil {
		return "", err
	}
	args := append([]string{"add"}, written...)
	if err := run("git", args...); err != nil {
		return "", err
	}
	commitMsg := fmt.Sprintf("Auto-ingest: %d new paper%s from arXiv cs.CR\n\nProposed by corpus-crawl. Review tags + notes before merging.",
		len(written), pluralS(len(written)))
	if err := run("git", "commit", "-m", commitMsg); err != nil {
		return "", err
	}
	if err := run("git", "push", "-u", "origin", branch); err != nil {
		return "", err
	}

	body := buildPRBody(items)
	bodyFile := filepath.Join(root, ".pr-body.md")
	if err := os.WriteFile(bodyFile, []byte(body), 0o644); err != nil {
		return "", err
	}
	defer os.Remove(bodyFile)

	title := fmt.Sprintf("Auto-ingest: %d new paper%s (%s)", len(written), pluralS(len(written)), time.Now().UTC().Format("2006-01-02"))
	cmd := exec.CommandContext(ctx, "gh", "pr", "create",
		"--title", title,
		"--body-file", bodyFile,
		"--label", "auto-ingest",
		"--head", branch,
		"--base", "main",
	)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("gh pr create: %v\n%s", err, string(out))
	}
	url := strings.TrimSpace(string(out))
	// Reset to main so the next run starts clean.
	_ = run("git", "checkout", "main")
	return url, nil
}

func buildPRBody(items []accepted) string {
	var b strings.Builder
	b.WriteString("# Auto-ingest from arXiv cs.CR\n\n")
	fmt.Fprintf(&b, "Ingested %d candidate paper%s. Each is a stub committed to `corpus/papers/`. Please:\n\n", len(items), pluralS(len(items)))
	b.WriteString("- [ ] Read each abstract and confirm circumvention-relevance\n")
	b.WriteString("- [ ] Tighten tags (the LLM tends to over-tag — drop tags that don't really apply)\n")
	b.WriteString("- [ ] Replace the auto-generated `notes` with a real team note if interesting\n")
	b.WriteString("- [ ] Set `core: true` for any load-bearing addition\n")
	b.WriteString("- [ ] Drop entries that aren't worth corpus-keeping\n\n---\n\n")

	sort.SliceStable(items, func(i, j int) bool {
		return len(items[i].k.Techniques) > len(items[j].k.Techniques)
	})

	for _, it := range items {
		fmt.Fprintf(&b, "## %s\n\n", it.c.Title)
		fmt.Fprintf(&b, "- **arXiv**: [%s](%s)\n", it.c.ArxivID, it.c.URL)
		fmt.Fprintf(&b, "- **Authors**: %s\n", strings.Join(it.c.Authors, ", "))
		fmt.Fprintf(&b, "- **Proposed id**: `%s`\n", proposeID(it.c))
		fmt.Fprintf(&b, "- **Tags**: censors=`%s` techniques=`%s`",
			strings.Join(it.k.Censors, ","), strings.Join(it.k.Techniques, ","))
		if len(it.k.DefensesDiscussed) > 0 {
			fmt.Fprintf(&b, " defenses=`%s`", strings.Join(it.k.DefensesDiscussed, ","))
		}
		b.WriteString("\n")
		if it.k.Reason != "" {
			fmt.Fprintf(&b, "- **Why relevant** (LLM): %s\n", it.k.Reason)
		}
		b.WriteString("\n")
	}
	b.WriteString("\n---\n*Generated by `cmd/corpus-crawl` running on the residential mini. See `.github/workflows/` for the deploy pipeline that runs after this merges.*\n")
	return b.String()
}

// ── HTTP serve mode ───────────────────────────────────────────────

func serve(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	listen := fs.String("listen", ":8788", "listen address")
	authToken := fs.String("auth-token", os.Getenv("CORPUS_CRAWL_TOKEN"), "shared bearer token (or env CORPUS_CRAWL_TOKEN)")
	corpus := fs.String("corpus", ".", "path to circumvention-corpus repo root")
	_ = fs.Parse(args)
	if *authToken == "" {
		log.Fatal("--auth-token (or env CORPUS_CRAWL_TOKEN) is required for serve mode")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("/crawl", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST required", 405)
			return
		}
		got := r.Header.Get("Authorization")
		want := "Bearer " + *authToken
		if got != want {
			http.Error(w, "unauthorized", 401)
			return
		}
		opts := runOptions{
			corpus:     *corpus,
			maxN:       intOrDefault(r.URL.Query().Get("max"), 10),
			windowDays: intOrDefault(r.URL.Query().Get("window_days"), 14),
			dryRun:     r.URL.Query().Get("dry_run") == "1",
			noPR:       r.URL.Query().Get("no_pr") == "1",
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Minute)
		defer cancel()
		log.Printf("HTTP /crawl: max=%d window=%d dry_run=%v", opts.maxN, opts.windowDays, opts.dryRun)
		res, err := runWith(ctx, opts)
		if err != nil {
			log.Printf("crawl error: %v", err)
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(res)
	})

	log.Printf("corpus-crawl serve listening on %s", *listen)
	srv := &http.Server{Addr: *listen, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

// ── helpers ───────────────────────────────────────────────────────

func matchesKeywords(c candidate) bool {
	hay := strings.ToLower(c.Title + " " + c.Abstract)
	for _, k := range keywords {
		if strings.Contains(hay, k) {
			return true
		}
	}
	return false
}

func proposeID(c candidate) string {
	year := c.Year
	if year == 0 {
		year = time.Now().Year()
	}
	last := "anon"
	if len(c.Authors) > 0 {
		parts := strings.Fields(c.Authors[0])
		if len(parts) > 0 {
			last = parts[len(parts)-1]
		}
	}
	last = slugify(last)
	if last == "" {
		last = "anon"
	}
	return fmt.Sprintf("%d-%s-%s", year, last, titleSlug(c.Title))
}

// titleSlug returns the first 3 meaningful words of a title joined
// by dashes. We split BEFORE slugifying so word boundaries are
// preserved — slugify() collapses all non-alphanumerics into dashes,
// which would turn the whole title into a single dash-joined word
// and break Fields-based splitting downstream.
func titleSlug(title string) string {
	stop := map[string]bool{
		"a": true, "an": true, "the": true, "of": true, "for": true, "and": true,
		"to": true, "in": true, "on": true, "with": true, "via": true, "is": true,
		"how": true, "what": true, "when": true, "where": true, "why": true, "by": true,
		"as": true, "from": true, "or": true, "are": true, "be": true, "at": true,
	}
	words := strings.Fields(strings.ToLower(title))
	var keep []string
	for _, w := range words {
		clean := slugify(w)
		if clean == "" || stop[clean] {
			continue
		}
		keep = append(keep, clean)
		if len(keep) == 3 {
			break
		}
	}
	if len(keep) == 0 {
		return "untitled"
	}
	return strings.Join(keep, "-")
}

var slugRE = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	s = strings.ToLower(s)
	s = slugRE.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

func normalizeTitle(t string) string {
	t = strings.ToLower(t)
	t = slugRE.ReplaceAllString(t, " ")
	return strings.Join(strings.Fields(t), " ")
}

func arxivIDFromURL(s string) string {
	s = strings.TrimSpace(s)
	for _, p := range []string{"http://arxiv.org/abs/", "https://arxiv.org/abs/", "http://arxiv.org/", "https://arxiv.org/"} {
		s = strings.TrimPrefix(s, p)
	}
	if i := strings.LastIndex(s, "v"); i > 4 {
		rest := s[i+1:]
		isDigits := rest != "" && strings.IndexFunc(rest, func(r rune) bool { return r < '0' || r > '9' }) == -1
		if isDigits {
			s = s[:i]
		}
	}
	return s
}

func cleanText(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = regexp.MustCompile(`\s+`).ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

func defaultIfEmpty(s []string, fallback []string) []string {
	out := make([]string, 0, len(s))
	for _, x := range s {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	if len(out) == 0 {
		return fallback
	}
	return out
}

func noteWithProvenance(modelNote string) string {
	prefix := "Auto-ingested via corpus-crawl. Tags proposed by Claude Haiku 4.5; review and tighten before relying."
	if strings.TrimSpace(modelNote) == "" {
		return prefix
	}
	return prefix + " " + strings.TrimSpace(modelNote)
}

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

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func intOrDefault(s string, def int) int {
	if s == "" {
		return def
	}
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil || n <= 0 {
		return def
	}
	return n
}

func init() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("corpus-crawl: ")
}

// _unused keeps goimports happy if io ever drops out of the file.
var _ = io.Discard
