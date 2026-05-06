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
	arxivAPI       = "https://export.arxiv.org/api/query"
	arxivCat       = "cs.CR"
	arxivLimit     = 200
	gfwReportFeed  = "https://gfw.report/index.xml"
	classModel     = "claude-haiku-4-5"
	httpTimeout    = 30 * time.Second
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
	// "tor " (with space) was too broad — matched "vec[tor ]Commitments",
	// "ac[tor ]frameworks", etc. Use multi-word phrases that are
	// specific to the Tor anonymity network.
	"tor browser", "tor bridge", "tor relay", "tor network", "tor cell",
	"onion router", "onion routing", "onion service", "anonymity network",
	"snowflake", "obfs4", "scramblesuit",
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
	corpus      string
	source      string // "arxiv", "net4people", or "all"
	maxN        int    // cap on accepted (relevant) papers per run — keeps PRs reviewable
	maxClassify int    // safety bound on how many candidates we send to the LLM
	windowDays  int
	dryRun      bool
	noPR        bool
	reclassify  bool // ignore the rejection cache and re-classify everything
}

func runOnce(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	opts := runOptions{}
	fs.StringVar(&opts.corpus, "corpus", ".", "path to circumvention-corpus repo root")
	fs.StringVar(&opts.source, "source", "all", "source: arxiv, net4people, gfw-report, popets, foci, usenix-sec, ermao, paderborn, paderborn-blog, or all")
	fs.IntVar(&opts.maxN, "max", 12, "max ACCEPTED papers per PR (cap applied after classification)")
	fs.IntVar(&opts.maxClassify, "max-classify", 80, "safety bound on classifier calls per run")
	fs.IntVar(&opts.windowDays, "window-days", 30, "look back this many days")
	fs.BoolVar(&opts.dryRun, "dry-run", false, "don't write YAML files; print what would happen")
	fs.BoolVar(&opts.noPR, "no-pr", false, "write YAMLs but skip opening a PR")
	fs.BoolVar(&opts.reclassify, "reclassify", false, "ignore the rejection cache and re-classify everything")
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

	rejected, err := loadRejectStore(root)
	if err != nil {
		return nil, fmt.Errorf("load reject cache: %w", err)
	}
	if opts.reclassify {
		log.Printf("--reclassify set: ignoring %d cached rejections this run", len(rejected.seen))
	} else {
		log.Printf("loaded %d cached rejections (will skip re-classifying these)", len(rejected.seen))
	}

	taxRaw, err := os.ReadFile(filepath.Join(root, "schema", "taxonomy.yaml"))
	if err != nil {
		return nil, fmt.Errorf("read taxonomy: %w", err)
	}

	since := time.Now().AddDate(0, 0, -opts.windowDays)
	source := strings.ToLower(strings.TrimSpace(opts.source))
	if source == "" {
		source = "all"
	}

	var cands []candidate
	if source == "arxiv" || source == "all" {
		ax, err := fetchArxiv(ctx, since)
		if err != nil {
			// Same best-effort posture as the other sources: arXiv's
			// API is aggressively rate-limited (HTTP 429 within seconds
			// of consecutive runs) and shouldn't take the whole crawl
			// down with it.
			log.Printf("fetch arxiv: %v (continuing)", err)
		} else {
			log.Printf("fetched %d from arXiv cs.CR (%d-day window)", len(ax), opts.windowDays)
			cands = append(cands, ax...)
		}
	}
	if source == "net4people" || source == "all" {
		np, err := fetchNet4People(ctx, since)
		if err != nil {
			// net4people is best-effort; if it fails, log and continue
			// so an arXiv outage / GH-API rate limit doesn't kill the run.
			log.Printf("fetch net4people: %v (continuing)", err)
		} else {
			log.Printf("fetched %d from net4people reading group", len(np))
			cands = append(cands, np...)
		}
	}
	if source == "gfw-report" || source == "all" {
		gr, err := fetchGFWReport(ctx, since)
		if err != nil {
			log.Printf("fetch gfw.report: %v (continuing)", err)
		} else {
			log.Printf("fetched %d from gfw.report", len(gr))
			cands = append(cands, gr...)
		}
	}
	if source == "popets" || source == "all" {
		// PoPETs publishes 4 issues per year. Pull the current year's
		// index page (which lists all 4 issues' papers in one document)
		// AND the previous year's, since the previous year's last
		// issue often lands in early next year.
		thisYear := time.Now().Year()
		for _, y := range []int{thisYear, thisYear - 1} {
			pp, err := fetchPETSymposiumProceedings(ctx, "PoPETs", fmt.Sprintf("https://petsymposium.org/popets/%d/", y), y, "popets")
			if err != nil {
				log.Printf("fetch PoPETs %d: %v (continuing)", y, err)
				continue
			}
			log.Printf("fetched %d from PoPETs %d", len(pp), y)
			cands = append(cands, pp...)
		}
	}
	if source == "foci" || source == "all" {
		thisYear := time.Now().Year()
		for _, y := range []int{thisYear, thisYear - 1} {
			f, err := fetchPETSymposiumProceedings(ctx, "FOCI", fmt.Sprintf("https://petsymposium.org/foci/%d/", y), y, "foci")
			if err != nil {
				log.Printf("fetch FOCI %d: %v (continuing)", y, err)
				continue
			}
			log.Printf("fetched %d from FOCI %d", len(f), y)
			cands = append(cands, f...)
		}
	}
	if source == "ermao" || source == "all" {
		em, err := fetchErmaoNet(ctx, since)
		if err != nil {
			log.Printf("fetch ermao.net: %v (continuing)", err)
		} else {
			log.Printf("fetched %d from ermao.net", len(em))
			cands = append(cands, em...)
		}
	}
	if source == "paderborn" || source == "all" {
		pb, err := fetchPaderbornSyssec(ctx, since)
		if err != nil {
			log.Printf("fetch Paderborn syssec publications: %v (continuing)", err)
		} else {
			log.Printf("fetched %d from Paderborn syssec publications", len(pb))
			cands = append(cands, pb...)
		}
	}
	if source == "paderborn-blog" || source == "all" {
		pb, err := fetchPaderbornBlog(ctx, since)
		if err != nil {
			log.Printf("fetch Paderborn syssec blog: %v (continuing)", err)
		} else {
			log.Printf("fetched %d from Paderborn syssec blog", len(pb))
			cands = append(cands, pb...)
		}
	}
	if source == "usenix-sec" || source == "all" {
		// USENIX Security uses a 2-digit-year URL slug. Each year has
		// up to 2 cycles (Cycle 1, Cycle 2) plus an aggregated
		// technical-sessions page. We try both cycles for the current
		// and previous year — broken cycles 404 and we skip cleanly.
		thisYear := time.Now().Year()
		for _, y := range []int{thisYear, thisYear - 1} {
			yy := y % 100
			for _, cycle := range []string{"cycle1-accepted-papers", "cycle2-accepted-papers"} {
				url := fmt.Sprintf("https://www.usenix.org/conference/usenixsecurity%02d/%s", yy, cycle)
				us, err := fetchUSENIXSecurity(ctx, url, y)
				if err != nil {
					log.Printf("fetch USENIX Sec %d %s: %v (continuing)", y, cycle, err)
					continue
				}
				log.Printf("fetched %d from USENIX Security '%02d %s", len(us), yy, cycle)
				cands = append(cands, us...)
			}
		}
	}
	res := &runResult{Considered: len(cands)}

	// Curation tier:
	//   net4people + gfw.report — every entry is human-curated research, so
	//     skip the keyword filter entirely.
	//   arxiv — has full abstract on the API; we keyword-match title+abstract.
	//   popets / foci / usenix-sec — proceedings pages give us full abstracts
	//     too, but those abstracts mention many keywords ("Tor", "fingerprint",
	//     etc.) tangentially in unrelated work. Match on title only — a paper
	//     whose title doesn't contain a circumvention keyword is almost
	//     certainly not circumvention research, regardless of abstract content.
	curatedSources := map[string]bool{"net4people": true, "gfw-report": true, "ermao": true, "paderborn": true, "paderborn-blog": true}
	titleOnlySources := map[string]bool{"popets": true, "foci": true, "usenix-sec": true}
	kept := make([]candidate, 0, len(cands))
	for _, c := range cands {
		switch {
		case curatedSources[c.Source]:
			kept = append(kept, c)
		case titleOnlySources[c.Source]:
			if matchesKeywordsInText(c.Title) {
				kept = append(kept, c)
			}
		default:
			if matchesKeywords(c) {
				kept = append(kept, c)
			}
		}
	}
	res.Filtered = len(kept)
	log.Printf("%d passed keyword filter", len(kept))

	novel := make([]candidate, 0, len(kept))
	skippedRejected := 0
	for _, c := range kept {
		if existing.contains(c) {
			continue
		}
		if !opts.reclassify && rejected.Has(c.identityKey()) {
			skippedRejected++
			continue
		}
		novel = append(novel, c)
	}
	res.Novel = len(novel)
	if skippedRejected > 0 {
		log.Printf("%d are novel (%d skipped via rejection cache)", len(novel), skippedRejected)
	} else {
		log.Printf("%d are novel", len(novel))
	}

	// Apply the classifier safety bound BEFORE classification so we don't
	// run away on a future high-volume source. Note: this is NOT --max;
	// --max caps the *accepted* set after classification so we don't
	// undercount because the classifier rejected most of the first N.
	if len(novel) > opts.maxClassify {
		log.Printf("capping classifier input at --max-classify=%d (was %d)", opts.maxClassify, len(novel))
		novel = novel[:opts.maxClassify]
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
			if len(out) >= opts.maxN {
				break
			}
			continue
		}
		k, err := classifyWithClaude(ctx, root, c, string(taxRaw))
		ident := c.ArxivID
		if ident == "" {
			ident = c.URL
		}
		if err != nil {
			log.Printf("classify %s: %v (skipping)", ident, err)
			continue
		}
		if !k.IsRelevant {
			log.Printf("not-relevant: %s — %s", ident, k.Reason)
			rejected.Add(c, k.Reason)
			continue
		}
		// For net4people candidates the LLM has filled in authors/venue/
		// year/abstract from the issue body — patch those onto the
		// candidate so the YAML writer sees them.
		if c.Source == "net4people" {
			if k.Title != "" {
				c.Title = k.Title
			}
			if len(k.Authors) > 0 {
				c.Authors = k.Authors
			}
			if k.Venue != "" {
				c.Venue = k.Venue
			}
			if k.Year != 0 {
				c.Year = k.Year
			}
			if k.CleanAbstract != "" {
				c.Abstract = k.CleanAbstract
			}
			if k.CanonicalURL != "" {
				// Prefer the actual paper URL; keep the GH issue URL in Refs.
				c.URL = k.CanonicalURL
			}
		}
		log.Printf("✓ %s — censors=%v techniques=%v", ident, k.Censors, k.Techniques)
		out = append(out, accepted{c: c, k: k})
		// Stop classifying once we have maxN accepted — saves API calls
		// without losing relevant papers (we only stop AFTER acceptance).
		if len(out) >= opts.maxN {
			log.Printf("hit --max=%d accepted; stopping classifier early", opts.maxN)
			break
		}
	}
	res.Accepted = len(out)

	// Persist the rejection cache regardless of whether anything was
	// accepted — even an all-reject run is valuable cache data we want
	// to keep.
	rejectPath := ""
	if !opts.dryRun {
		rp, err := rejected.Save()
		if err != nil {
			log.Printf("warning: failed to save rejection cache: %v", err)
		} else if rp != "" {
			rejectPath = rp
			log.Printf("saved rejection cache (%d total entries)", len(rejected.seen))
		}
	}

	if len(out) == 0 {
		log.Println("classifier rejected all; nothing to write")
		// Still try to commit the rejection-cache delta so future runs
		// don't re-pay for these classifications. Only do this if we
		// actually have a delta and aren't in dry-run / no-pr mode.
		if rejectPath != "" && !opts.noPR && !opts.dryRun {
			if err := commitRejectCacheOnly(ctx, root, rejectPath, len(rejected.seen)); err != nil {
				log.Printf("warning: couldn't commit rejection cache: %v", err)
			}
		}
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

	// Extract findings from each accepted paper's full text. Best-effort:
	// PDF download failures, paywalled servers, scan-only PDFs etc. are
	// logged and skipped — the YAML still ships even if no findings
	// could be extracted. Findings get committed alongside the YAML in
	// the same auto-ingest PR.
	findingFiles := extractFindingsForAccepted(ctx, root, out)

	// Include the rejection cache in the PR if it was updated this run.
	prFiles := append([]string{}, written...)
	prFiles = append(prFiles, findingFiles...)
	if rejectPath != "" {
		prFiles = append(prFiles, rejectPath)
	}
	prURL, err := openPR(ctx, root, out, prFiles)
	if err != nil {
		log.Printf("PR creation failed (YAMLs are written, push manually): %v", err)
	} else {
		res.PRURL = prURL
		log.Printf("PR opened: %s", prURL)
	}
	return res, nil
}

// extractFindingsForAccepted runs the corpus-findings tool for each
// paper we just accepted, returning the file paths of the new finding
// YAMLs so they can be added to the auto-ingest PR. Findings extraction
// is best-effort: failures (PDF unreachable, scan-only PDF, paywall,
// LLM error) are logged but don't abort the run. The YAML still ships
// even if no findings can be extracted.
//
// Locating the binary: prefer a sibling binary at the same directory
// as this corpus-crawl binary (the wrapper builds both into the repo
// root); fall back to PATH lookup; if neither is found, skip findings
// extraction entirely with a warning.
func extractFindingsForAccepted(ctx context.Context, root string, items []accepted) []string {
	bin := locateFindingsBinary(root)
	if bin == "" {
		log.Printf("findings: corpus-findings binary not found; skipping findings extraction")
		return nil
	}
	var out []string
	for _, a := range items {
		id := proposeID(a.c)
		// Snapshot the existing findings dir so we can diff after the run.
		before := snapshotFindings(root, id)
		fctx, cancel := context.WithTimeout(ctx, 7*time.Minute)
		cmd := exec.CommandContext(fctx, bin, "extract", "--paper", id, "--corpus", root)
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			log.Printf("findings: %s extraction failed: %v (continuing)", id, err)
		}
		cancel()
		after := snapshotFindings(root, id)
		for f := range after {
			if !before[f] {
				out = append(out, f)
			}
		}
	}
	if len(out) > 0 {
		log.Printf("findings: extracted %d new finding(s) across %d papers", len(out), len(items))
	}
	return out
}

func locateFindingsBinary(root string) string {
	// Sibling binary in the corpus-crawl directory (the launchd wrapper
	// builds both via `go build ./cmd/corpus-crawl/` AND we ship a
	// build of corpus-findings the same way — checked first).
	for _, candidate := range []string{
		filepath.Join(root, "corpus-findings"),
		filepath.Join(filepath.Dir(os.Args[0]), "corpus-findings"),
	} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	if path, err := exec.LookPath("corpus-findings"); err == nil {
		return path
	}
	return ""
}

func snapshotFindings(root, paperID string) map[string]bool {
	matches, _ := filepath.Glob(filepath.Join(root, "corpus", "findings", paperID+"__*.yaml"))
	out := make(map[string]bool, len(matches))
	for _, m := range matches {
		out[m] = true
	}
	return out
}

// commitRejectCacheOnly pushes a small commit on main containing only
// the rejection-cache update — no PR, since there's nothing for a human
// to review. Used when a run produced no accepted papers but did burn
// LLM cycles classifying things we'd like to remember.
func commitRejectCacheOnly(ctx context.Context, root, rejectPath string, totalEntries int) error {
	run := func(args ...string) error {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("git %v: %v\n%s", args, err, string(out))
		}
		return nil
	}
	_ = run("config", "user.name", "circumvention-corpus-bot")
	_ = run("config", "user.email", "bot@lantern.io")
	if err := run("checkout", "main"); err != nil {
		return err
	}
	if err := run("add", rejectPath); err != nil {
		return err
	}
	// Skip the commit if there's nothing staged (e.g., the cache was
	// already up to date on disk relative to what's committed).
	cmd := exec.CommandContext(ctx, "git", "diff", "--cached", "--quiet")
	cmd.Dir = root
	if cmd.Run() == nil {
		log.Printf("rejection cache already up to date on main; nothing to commit")
		return nil
	}
	if err := run("commit", "-m", fmt.Sprintf("auto-ingest: cache %d rejection(s)", totalEntries)); err != nil {
		return err
	}
	if err := run("push", "origin", "main"); err != nil {
		return err
	}
	log.Printf("pushed rejection-cache update to main")
	return nil
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
	Source   string // "arxiv" or "net4people" — drives downstream prompt + sources field
	ArxivID  string
	Title    string
	Abstract string
	Authors  []string
	URL      string
	Venue    string
	Year     int
	Updated  time.Time
	// Refs are extra source URLs (e.g. for net4people, the GH issue URL)
	// that get appended to the YAML's `sources` field.
	Refs []string
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
			Source:   "arxiv",
			ArxivID:  arxivIDFromURL(e.ID),
			Title:    cleanText(e.Title),
			Abstract: cleanText(e.Summary),
			URL:      strings.Replace(e.ID, "http://", "https://", 1),
			Venue:    "arXiv preprint",
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

// ── net4people/bbs source ──────────────────────────────────────────
//
// net4people is a curated forum where the circumvention community
// flags new papers — academic conf papers, journal articles, NGO
// reports, MSc theses, and other primary sources. The "reading group"
// label marks issues that discuss a specific paper. Body format is
// fairly regular: title, authors, URL, summary written by the
// community member who posted it.
//
// We pull all reading-group issues since `since` via the GitHub API
// and create one candidate per issue. Authors and venue are extracted
// by the LLM during classification (the bodies aren't structured
// enough for regex parsing to reliably catch e.g. "Article 19" vs
// "Vincent Brussee" vs five-author conference papers).

type ghIssue struct {
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	HTMLURL   string    `json:"html_url"`
	CreatedAt time.Time `json:"created_at"`
	Labels    []struct {
		Name string `json:"name"`
	} `json:"labels"`
	State string `json:"state"`
}

func fetchNet4People(ctx context.Context, since time.Time) ([]candidate, error) {
	cctx, cancel := context.WithTimeout(ctx, httpTimeout)
	defer cancel()
	// Reading-group label only — high-precision (every issue with that
	// label discusses a specific paper or report). We pull both open
	// and closed issues; closed often means "merged into another thread"
	// not "no longer relevant."
	endpoint := "https://api.github.com/repos/net4people/bbs/issues"
	q := fmt.Sprintf("?labels=reading%%20group&state=all&since=%s&per_page=100",
		since.UTC().Format(time.RFC3339))
	req, err := http.NewRequestWithContext(cctx, "GET", endpoint+q, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "circumvention-corpus-crawl/0.1")
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := (&http.Client{Timeout: httpTimeout}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("github API: status %d: %s", resp.StatusCode, string(body))
	}
	var issues []ghIssue
	if err := json.NewDecoder(resp.Body).Decode(&issues); err != nil {
		return nil, err
	}
	out := make([]candidate, 0, len(issues))
	for _, iss := range issues {
		if iss.CreatedAt.Before(since) {
			continue
		}
		// Title in the issue is usually the paper's title verbatim. We
		// preserve it as-is and let the classifier extract structured
		// metadata from the body.
		out = append(out, candidate{
			Source:   "net4people",
			Title:    cleanText(iss.Title),
			Abstract: cleanText(truncate(iss.Body, 4000)), // 4 KB body is plenty for context
			URL:      iss.HTMLURL,
			Updated:  iss.CreatedAt,
			Year:     iss.CreatedAt.Year(),
			Refs:     []string{fmt.Sprintf("url:%s", iss.HTMLURL)},
		})
	}
	return out, nil
}

// ── gfw.report source ──────────────────────────────────────────────
//
// gfw.report publishes editorially-curated GFW research — measurement
// write-ups, leak analyses, blocking-event reports. The RSS feed at
// /index.xml is clean RSS 2.0; description fields contain the full
// HTML body. Every post is corpus-relevant, so we skip the keyword
// filter (see runWith). The classifier still runs to extract author
// list, venue, and tag the post against the taxonomy.

type rssItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	GUID        string `xml:"guid"`
	PubDate     string `xml:"pubDate"`
	Description string `xml:"description"`
}

type rssChannel struct {
	XMLName xml.Name  `xml:"rss"`
	Items   []rssItem `xml:"channel>item"`
}

func fetchGFWReport(ctx context.Context, since time.Time) ([]candidate, error) {
	cctx, cancel := context.WithTimeout(ctx, httpTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, "GET", gfwReportFeed, nil)
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
		return nil, fmt.Errorf("gfw.report feed: status %d: %s", resp.StatusCode, string(body))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var feed rssChannel
	if err := xml.Unmarshal(body, &feed); err != nil {
		return nil, fmt.Errorf("parse gfw.report feed: %w", err)
	}
	out := make([]candidate, 0, len(feed.Items))
	// gfw.report posts often have multiple URL variants for the same
	// piece — English at /en/ and Chinese at /zh/. We keep only English
	// (heuristic: link ends in /en/ OR contains no /zh/ marker).
	for _, it := range feed.Items {
		if strings.Contains(it.Link, "/zh/") {
			continue
		}
		// pubDate parsing: RSS uses RFC1123Z. Fall back to a few common
		// alternatives if that fails.
		var pub time.Time
		for _, layout := range []string{time.RFC1123Z, time.RFC1123, time.RFC822Z, time.RFC822} {
			if t, err := time.Parse(layout, it.PubDate); err == nil {
				pub = t
				break
			}
		}
		// Skip items without a parseable pubDate. gfw.report's feed
		// includes a placeholder entry (no pubDate, generic "Great
		// Firewall Report - GFW Report" title) that's evidently a
		// categorization/index marker rather than a real post.
		if pub.IsZero() {
			continue
		}
		if pub.Before(since) {
			continue
		}
		out = append(out, candidate{
			Source:   "gfw-report",
			Title:    cleanText(it.Title),
			Abstract: cleanText(stripHTML(truncate(it.Description, 6000))),
			URL:      it.Link,
			Venue:    "gfw.report (research blog)",
			Updated:  pub,
			Year:     yearOrNow(pub),
			Refs:     []string{"url:" + it.Link},
		})
	}
	return out, nil
}

// stripHTML is a coarse HTML-tag remover. We don't need full HTML
// parsing — the LLM consumes the result and tolerates some leftover
// markup. Just removes <tag> sequences and decodes a few entities.
func stripHTML(s string) string {
	// Drop tags.
	s = regexp.MustCompile(`<[^>]+>`).ReplaceAllString(s, " ")
	// Decode common entities.
	s = strings.NewReplacer(
		"&amp;", "&",
		"&lt;", "<",
		"&gt;", ">",
		"&quot;", `"`,
		"&#34;", `"`,
		"&#39;", "'",
		"&rsquo;", "'",
		"&lsquo;", "'",
		"&ldquo;", `"`,
		"&rdquo;", `"`,
		"&nbsp;", " ",
		"&mdash;", "—",
		"&ndash;", "–",
	).Replace(s)
	return s
}

func yearOrNow(t time.Time) int {
	if t.IsZero() {
		return time.Now().Year()
	}
	return t.Year()
}

// ── ermao.net source ──────────────────────────────────────────────
//
// ermao.net is a Chinese-language circumvention blog with operator-
// side commentary on enforcement events. Most posts are commercial
// VPN-broker reviews (not corpus-relevant), but some document major
// enforcement waves and outage cascades from a perspective the
// academic measurement papers don't capture.
//
// No RSS feed; we use sitemap.xml as the discovery channel. Each
// candidate post is pre-fetched with `wick fetch --format markdown`
// (residential IP + JS rendering for the VuePress SPA) and the body
// is passed to the LLM classifier. The Chinese-language relevance
// gate works because Sonnet handles zh natively.
//
// We treat ermao as curated (skip keyword filter) since English
// keywords don't match Chinese titles; the LLM does all the
// filtering.

const ermaoSitemap = "https://www.ermao.net/sitemap.xml"

type sitemapURL struct {
	Loc     string `xml:"loc"`
	Lastmod string `xml:"lastmod"`
}
type sitemapURLSet struct {
	XMLName xml.Name     `xml:"urlset"`
	URLs    []sitemapURL `xml:"url"`
}

func fetchErmaoNet(ctx context.Context, since time.Time) ([]candidate, error) {
	cctx, cancel := context.WithTimeout(ctx, httpTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, "GET", ermaoSitemap, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 circumvention-corpus-crawl/0.1")
	resp, err := (&http.Client{Timeout: httpTimeout}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("ermao sitemap: status %d: %s", resp.StatusCode, string(body))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var set sitemapURLSet
	if err := xml.Unmarshal(body, &set); err != nil {
		return nil, fmt.Errorf("parse ermao sitemap: %w", err)
	}

	// Filter to post URLs that look like content (skip index/landing
	// pages, friends/stats/airport directories, scamvpn slop, etc.)
	// and skip anything older than `since`.
	type kept struct{ url string; lastmod time.Time }
	var posts []kept
	for _, u := range set.URLs {
		if !ermaoIsPost(u.Loc) {
			continue
		}
		var lm time.Time
		if u.Lastmod != "" {
			if t, err := time.Parse(time.RFC3339, u.Lastmod); err == nil {
				lm = t
			}
		}
		if !lm.IsZero() && lm.Before(since) {
			continue
		}
		posts = append(posts, kept{url: u.Loc, lastmod: lm})
	}
	// Cap so a wide --window-days doesn't trigger 100+ wick fetches.
	const ermaoCap = 30
	if len(posts) > ermaoCap {
		posts = posts[:ermaoCap]
	}

	out := make([]candidate, 0, len(posts))
	for _, p := range posts {
		md, err := wickFetch(ctx, p.url, "markdown")
		if err != nil {
			log.Printf("ermao: wick fetch %s: %v (skipping)", p.url, err)
			continue
		}
		title, body := ermaoTitleAndBody(string(md))
		if title == "" {
			title = p.url
		}
		out = append(out, candidate{
			Source:   "ermao",
			Title:    title,
			Abstract: body,
			URL:      p.url,
			Venue:    "ermao.net (Chinese-language circumvention blog)",
			Updated:  p.lastmod,
			Year:     yearOrNow(p.lastmod),
			Refs:     []string{"url:" + p.url},
		})
	}
	return out, nil
}

func ermaoIsPost(url string) bool {
	// Keep blog posts and articles; skip taxonomy/index pages and
	// known low-signal categories (scamvpn = scam-VPN call-outs,
	// airport = commercial-broker reviews, friends/stats/posts =
	// directory pages).
	for _, prefix := range []string{
		"https://www.ermao.net/blog/tags",
		"https://www.ermao.net/blog/categories",
		"https://www.ermao.net/blog/archives",
		"https://www.ermao.net/blog/",  // careful — this is a path-prefix, but only the root /blog/ is the index
	} {
		if url == strings.TrimSuffix(prefix, "/") || url == prefix+"/" {
			return false
		}
	}
	if strings.Contains(url, "/scamvpn/") || strings.Contains(url, "/airport/") ||
		strings.Contains(url, "/friends/") || strings.Contains(url, "/stats/") ||
		strings.Contains(url, "/posts/") || strings.HasSuffix(url, "/blog/") ||
		strings.HasSuffix(url, "/blog/tags/") || strings.HasSuffix(url, "/blog/categories/") ||
		strings.HasSuffix(url, "/blog/archives/") {
		return false
	}
	return strings.Contains(url, "/blog/") || strings.Contains(url, "/article/") ||
		strings.Contains(url, "/news/")
}

// ermaoTitleAndBody pulls the post title and body from wick fetch's
// markdown output. Wick output looks like:
//
//	Title: <page-title>
//	Status: 200 | Time: 252ms
//
//	# <heading>
//	...body...
//
// We use the first H1 line as the title (cleaner than the wick
// "Title:" header which often duplicates the site name) and return
// the body (capped to keep prompt size bounded).
func ermaoTitleAndBody(md string) (string, string) {
	const bodyCap = 5000
	lines := strings.Split(md, "\n")
	var title, body string
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if title == "" && strings.HasPrefix(t, "# ") {
			title = strings.TrimSpace(strings.TrimPrefix(t, "# "))
			body = strings.Join(lines[i+1:], "\n")
			break
		}
	}
	body = strings.TrimSpace(body)
	if len(body) > bodyCap {
		body = body[:bodyCap]
	}
	return title, body
}

// ── PoPETs / FOCI proceedings source ──────────────────────────────
//
// petsymposium.org renders both PoPETs (privacy) and FOCI (circumvention)
// issue/proceedings pages in a consistent markdown-able format:
//
//   *   **[Title](paper-page.php)** \[[PDF](pdf-url)\] (artifact info)
//        *Author1 (Affiliation), Author2 (Affiliation), ...*
//
// We fetch via wick (residential IP, browser-grade) and parse with a
// regex tailored to that format. PoPETs publishes 4 issues per year;
// the current-year index lists all of them. FOCI is annual.
//
// Most PoPETs papers aren't circumvention-relevant (privacy/crypto/ML
// dominate), so candidates flow through the keyword filter before
// classification — see runWith. FOCI papers are usually all relevant
// but go through the same gate for safety.

var (
	petsPaperRE  = regexp.MustCompile(`(?ms)^\*\s+\*\*\[([^\]]+)\]\(([^)]+)\)\*\*[^\n]*\n\s+\*([^*\n]+)\*`)
	petsAffixRE  = regexp.MustCompile(`\s*\([^)]*\)`)
	petsAuthorRE = regexp.MustCompile(`\s*,\s*`)
)

func fetchPETSymposiumProceedings(ctx context.Context, venuePrefix, url string, year int, sourceName string) ([]candidate, error) {
	md, err := wickFetch(ctx, url, "markdown")
	if err != nil {
		return nil, err
	}
	matches := petsPaperRE.FindAllStringSubmatch(string(md), -1)
	if len(matches) == 0 {
		return nil, fmt.Errorf("no papers parsed from %s (page format may have changed)", url)
	}
	out := make([]candidate, 0, len(matches))
	for _, m := range matches {
		title := strings.TrimSpace(m[1])
		paperURL := strings.TrimSpace(m[2])
		authorsLine := strings.TrimSpace(m[3])

		// Skip non-paper entries (Editors' Introduction, prefaces).
		lt := strings.ToLower(title)
		if strings.Contains(lt, "editors' introduction") ||
			strings.Contains(lt, "editor's introduction") ||
			strings.Contains(lt, "preface") {
			continue
		}

		// Resolve the URL: petsymposium.org links can be relative
		// (../2026/popets-2026-0014.php). Prefix with the index URL's
		// origin if needed.
		if !strings.HasPrefix(paperURL, "http") {
			// Strip ../ and join with the symposium origin.
			cleaned := strings.TrimPrefix(paperURL, "../")
			paperURL = "https://petsymposium.org/" + sourceName + "/" + cleaned
			if strings.HasPrefix(cleaned, sourceName+"/") {
				paperURL = "https://petsymposium.org/" + cleaned
			}
		}

		// Strip "(Affiliation)" parens from the authors line, then split.
		clean := petsAffixRE.ReplaceAllString(authorsLine, "")
		var authors []string
		for _, a := range petsAuthorRE.Split(clean, -1) {
			a = strings.TrimSpace(a)
			if a != "" {
				authors = append(authors, a)
			}
		}

		out = append(out, candidate{
			Source:   sourceName,
			Title:    title,
			Authors:  authors,
			URL:      paperURL,
			Venue:    fmt.Sprintf("%s %d", venuePrefix, year),
			Year:     year,
			Refs:     []string{"url:" + paperURL},
		})
	}
	return out, nil
}

// ── USENIX Security source ────────────────────────────────────────
//
// USENIX Security publishes accepted papers as a Drupal-rendered HTML
// page. Each paper section follows a regular pattern in markdown form
// (after wick conversion):
//
//   ## [Title](/conference/usenixsecurityNN/presentation/slug)
//
//   Author1, *Affiliation;* Author2, *Affiliation; ...*
//
//   Short Presentation
//   Available Media
//   [icon links...]
//
//   Abstract paragraph(s)
//
// We split on top-level ## headings, then heuristically extract title,
// authors, and abstract from each section.

// usenixHeadingRE matches the top-level "## [Title](url)" line that
// starts each paper section. Go's RE2 doesn't support lookahead, so
// we use this just to find section starts and slice the body manually.
var usenixHeadingRE = regexp.MustCompile(`(?m)^## \[([^\]]+)\]\(([^)]+)\)`)

func fetchUSENIXSecurity(ctx context.Context, url string, year int) ([]candidate, error) {
	md, err := wickFetch(ctx, url, "markdown")
	if err != nil {
		return nil, err
	}
	text := string(md)

	// Find all paper section starts.
	starts := usenixHeadingRE.FindAllStringSubmatchIndex(text, -1)
	if len(starts) == 0 {
		return nil, fmt.Errorf("no papers parsed from %s", url)
	}

	out := make([]candidate, 0, len(starts))
	for i, s := range starts {
		// Title is captures 2..3, URL is captures 4..5.
		title := strings.TrimSpace(text[s[2]:s[3]])
		paperURL := strings.TrimSpace(text[s[4]:s[5]])
		// Body is everything from end-of-heading-line until the next
		// section's start (or EOF for the last paper).
		bodyStart := s[1]
		bodyEnd := len(text)
		if i+1 < len(starts) {
			bodyEnd = starts[i+1][0]
		}
		body := text[bodyStart:bodyEnd]

		// Skip the page-header section (the ## with the page title is
		// also matched by our regex). USENIX header is usually the URL
		// path matching the page itself or a fragment selector.
		if strings.HasPrefix(paperURL, "#") || strings.Contains(strings.ToLower(title), "switcher") {
			continue
		}

		// Resolve relative URLs.
		if strings.HasPrefix(paperURL, "/") {
			paperURL = "https://www.usenix.org" + paperURL
		}

		authors, abstract := extractUSENIXAuthorsAbstract(body)

		out = append(out, candidate{
			Source:   "usenix-sec",
			Title:    title,
			Authors:  authors,
			Abstract: abstract,
			URL:      paperURL,
			Venue:    fmt.Sprintf("USENIX Security %d", year),
			Year:     year,
			Refs:     []string{"url:" + paperURL},
		})
	}
	return out, nil
}

// extractUSENIXAuthorsAbstract pulls the authors line and abstract
// paragraph out of a USENIX paper section body. Authors are the first
// non-empty line containing italic markers (the affiliation marks);
// the abstract is the longest paragraph in the section (skipping
// metadata lines like "Short Presentation", "Available Media").
func extractUSENIXAuthorsAbstract(body string) ([]string, string) {
	lines := strings.Split(body, "\n")

	// Find authors: first non-empty line with italics that isn't a
	// metadata bullet.
	var authorsLine string
	for _, l := range lines {
		lt := strings.TrimSpace(l)
		if lt == "" {
			continue
		}
		// Skip known metadata lines.
		if strings.HasPrefix(lt, "[!") || strings.HasPrefix(lt, "![") {
			continue
		}
		ll := strings.ToLower(lt)
		if ll == "available media" || ll == "short presentation" || ll == "long presentation" ||
			ll == "video" || ll == "slides" || strings.HasPrefix(ll, "view mode") {
			continue
		}
		if strings.Contains(lt, "*") {
			authorsLine = lt
			break
		}
	}

	// Parse authors: each entry is "Name, *Affiliation*" (semicolon-
	// or comma-separated). Strip italic-wrapped affiliations and split
	// on author boundaries.
	var authors []string
	if authorsLine != "" {
		// Strip *...* italic blocks (affiliations).
		clean := regexp.MustCompile(`\*[^*]*\*`).ReplaceAllString(authorsLine, "")
		// Split by , or ; — each entry is one author name.
		for _, a := range regexp.MustCompile(`[,;]`).Split(clean, -1) {
			a = strings.TrimSpace(a)
			a = strings.TrimSuffix(a, ",")
			a = strings.Trim(a, "; ")
			if a == "" {
				continue
			}
			// Reject obvious junk (single chars, all-non-letters).
			if len(a) < 2 || !regexp.MustCompile(`[A-Za-z]`).MatchString(a) {
				continue
			}
			authors = append(authors, a)
		}
	}

	// Find abstract: longest paragraph that isn't the authors line and
	// isn't an icon-link block. We split on blank lines.
	var paragraphs []string
	var cur []string
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			if len(cur) > 0 {
				paragraphs = append(paragraphs, strings.TrimSpace(strings.Join(cur, " ")))
				cur = cur[:0]
			}
		} else {
			cur = append(cur, l)
		}
	}
	if len(cur) > 0 {
		paragraphs = append(paragraphs, strings.TrimSpace(strings.Join(cur, " ")))
	}

	abstract := ""
	for _, p := range paragraphs {
		if p == authorsLine || strings.Contains(p, "![") || strings.HasPrefix(p, "[!") {
			continue
		}
		if len(p) < 80 {
			// Too short to be an abstract.
			continue
		}
		if len(p) > len(abstract) {
			abstract = p
		}
	}
	abstract = strings.TrimSpace(abstract)
	abstract = cleanText(abstract)

	return authors, abstract
}

// wickFetch shells out to `wick fetch <url>` for browser-grade page
// fetching. Used by the (future) JS-rendered conference-page scrapers
// — PETS / FOCI / USENIX / NDSS / IMC. arXiv's plain XML API uses
// net/http above; wick's markdown/html/text output formats would
// ────────────────── Paderborn upb-syssec ──────────────────
// The University of Paderborn's System Security group (Niklas Niere,
// Juraj Somorovsky, Robert Merget, Felix Lange, Sven Hebrok) is one of
// the strongest TLS-layer-circumvention research outfits operating
// today. Their canonical publications page lives at jonsnowwhite.de
// (Niklas's personal site, listing all his and the group's
// circumvention papers with abstracts). Their group blog at
// upb-syssec.github.io carries technical posts that don't always
// graduate to a paper but document concrete censor behavior.
//
// We treat both as curated (skip keyword filter) since the entire
// catalog is on-topic for circumvention research.

const (
	paderbornPubsURL = "https://www.jonsnowwhite.de/publications/"
	paderbornBlogURL = "https://upb-syssec.github.io/blog/"
)

// paderbornPubHeadingRE matches a publication heading on the
// jonsnowwhite.de publications page. The page uses an "✏" prefix to
// mark first-author papers; we strip it. Each H2 starts a publication
// block; the line directly under is "*Venue*, Year".
var (
	paderbornPubHeadingRE = regexp.MustCompile(`(?m)^## (?:✏)?(.+)$`)
	paderbornPubVenueRE   = regexp.MustCompile(`(?m)^\*([^*]+)\*,\s*(\d{4})\s*$`)
	paderbornPubLinkRE    = regexp.MustCompile(`\[Download Paper\]\(\s*<?\s*(\S+?)\s*>?\s*\)`)
)

func fetchPaderbornSyssec(ctx context.Context, since time.Time) ([]candidate, error) {
	md, err := wickFetch(ctx, paderbornPubsURL, "markdown")
	if err != nil {
		return nil, err
	}
	text := string(md)

	// Trim the page to the "## Conference Papers" section onward — the
	// header above is bio/links, not publications.
	if i := strings.Index(text, "## Conference Papers"); i >= 0 {
		text = text[i:]
	}

	starts := paderbornPubHeadingRE.FindAllStringSubmatchIndex(text, -1)
	var out []candidate
	for i, m := range starts {
		titleStart, titleEnd := m[2], m[3]
		title := strings.TrimSpace(text[titleStart:titleEnd])
		// Skip section dividers ("Conference Papers", "Workshop Papers", etc.).
		if !strings.ContainsAny(title, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ") || len(title) < 8 {
			continue
		}
		// Slice from this heading to the next.
		blockStart := m[1]
		blockEnd := len(text)
		if i+1 < len(starts) {
			blockEnd = starts[i+1][0]
		}
		block := text[blockStart:blockEnd]

		venue, year := "", 0
		if vm := paderbornPubVenueRE.FindStringSubmatch(block); len(vm) == 3 {
			venue = strings.TrimSpace(vm[1])
			fmt.Sscanf(vm[2], "%d", &year)
		}
		if year == 0 {
			continue // not a real publication entry
		}
		// Date filter — at the year granularity we have, anything from
		// the year of `since` or later is included.
		if year < since.Year() {
			continue
		}

		// Abstract: everything between "Abstract" and the next heading
		// or "[Download Paper]".
		abstract := ""
		if i := strings.Index(block, "Abstract"); i >= 0 {
			tail := block[i+len("Abstract"):]
			if j := strings.Index(tail, "[Download"); j >= 0 {
				tail = tail[:j]
			}
			abstract = strings.TrimSpace(tail)
		}

		url := ""
		if lm := paderbornPubLinkRE.FindStringSubmatch(block); len(lm) == 2 {
			url = strings.Trim(lm[1], "<> ")
		}
		if url == "" {
			// Fall back to the publications page anchor — better than nothing.
			url = paderbornPubsURL
		}

		out = append(out, candidate{
			Source:   "paderborn",
			Title:    title,
			Abstract: abstract,
			URL:      url,
			Venue:    venue,
			Year:     year,
			Updated:  time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC),
			Refs:     []string{"url:" + url, "url:" + paderbornPubsURL},
		})
	}
	return out, nil
}

// paderbornBlogEntryRE matches the upb-syssec blog index format —
// each post is "### [Title](/blog/YYYY/slug/)" then a one-line summary
// then a "N min read   ·   Month DD, YYYY" line.
var (
	paderbornBlogEntryRE = regexp.MustCompile(`(?m)^\s*(?:\*\s+)?### \[([^\]]+)\]\(([^)]+)\)\s*$`)
	paderbornBlogDateRE  = regexp.MustCompile(`\b([A-Z][a-z]+) (\d{1,2}), (\d{4})\b`)
)

func fetchPaderbornBlog(ctx context.Context, since time.Time) ([]candidate, error) {
	md, err := wickFetch(ctx, paderbornBlogURL, "markdown")
	if err != nil {
		return nil, err
	}
	text := string(md)
	starts := paderbornBlogEntryRE.FindAllStringSubmatchIndex(text, -1)
	var out []candidate
	for i, m := range starts {
		title := strings.TrimSpace(text[m[2]:m[3]])
		href := strings.TrimSpace(text[m[4]:m[5]])
		if !strings.HasPrefix(href, "/blog/") {
			continue
		}
		blockStart := m[1]
		blockEnd := len(text)
		if i+1 < len(starts) {
			blockEnd = starts[i+1][0]
		}
		block := text[blockStart:blockEnd]

		// One-line summary is the first non-empty paragraph after the heading.
		summary := ""
		for _, line := range strings.Split(block, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if strings.HasPrefix(line, "[") || strings.Contains(line, "min read") {
				continue
			}
			summary = line
			break
		}

		// Date.
		var posted time.Time
		if dm := paderbornBlogDateRE.FindStringSubmatch(block); len(dm) == 4 {
			t, err := time.Parse("January 2, 2006", dm[0])
			if err == nil {
				posted = t
			}
		}
		if !posted.IsZero() && posted.Before(since) {
			continue
		}

		fullURL := "https://upb-syssec.github.io" + href
		out = append(out, candidate{
			Source:   "paderborn-blog",
			Title:    title,
			Abstract: summary,
			URL:      fullURL,
			Venue:    "upb-syssec blog (Paderborn University, System Security group)",
			Year:     yearOrNow(posted),
			Updated:  posted,
			Refs:     []string{"url:" + fullURL},
		})
	}
	return out, nil
}

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
	// For net4people candidates the classifier also extracts these
	// (the GH issue title is the paper title but authors/venue/year
	// live in the body). Empty for arXiv candidates which have these
	// from the API directly.
	Authors          []string `json:"authors,omitempty"`
	Venue            string   `json:"venue,omitempty"`
	Year             int      `json:"year,omitempty"`
	Title            string   `json:"title,omitempty"`             // override if the issue title differs from paper title
	CleanAbstract    string   `json:"clean_abstract,omitempty"`    // 1-3 sentence abstract LLM derives from body
	CanonicalURL     string   `json:"canonical_url,omitempty"`     // first paper URL found in body
}

// classifyWithClaude shells out to `claude -p` to classify a paper. We
// pass the full taxonomy YAML in the prompt and ask for strict JSON
// output. claude -p uses the user's logged-in Claude subscription, so
// no API key is needed — this is the load-bearing reason the crawler
// lives on the user's residential machine instead of in CI.
func classifyWithClaude(ctx context.Context, corpusRoot string, c candidate, taxonomy string) (classification, error) {
	prompt := buildClassifyPrompt(c, taxonomy)
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	args := []string{
		"-p", prompt,
		"--model", classModel,
		"--output-format", "json",
	}
	args = append(args, claudeMCPArgs(corpusRoot)...)
	cmd := exec.CommandContext(ctx, "claude", args...)
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
	b.WriteString(`You are a research librarian curating a censorship-circumvention research corpus. Given a paper's title and surrounding context, decide whether it is relevant and propose taxonomy tags from the controlled vocabulary below.

RELEVANCE — be GENEROUS. A paper is relevant if it studies any of:
  (a) censorship measurement (network-level or app-level)
  (b) censor capabilities or detection techniques (DPI, fingerprinting, ML classifiers, active probing, traffic analysis)
  (c) circumvention protocol design or evaluation
  (d) anonymity systems / Tor / VPN ecosystem studies
  (e) traffic-analysis attacks or defenses on encrypted tunnels
  (f) NGO / advocacy / journalism reports on censorship infrastructure or policy
  (g) primary-source measurements of internet shutdowns, blocking events, or surveillance regimes
  (h) MSc / PhD theses, mailing-list crossposts, blog write-ups that materially advance the field
  (i) policy, legal, or regulatory work that affects circumvention tooling

NOT relevant: generic crypto/cryptanalysis, ML adversarial robustness without a censorship angle, IoT auth, smart-contract security, web bot detection without a proxy/VPN angle, biometric authentication, fraud detection unrelated to circumvention.

Default to is_relevant=true if uncertain. The human reviewer will tighten in PR review; we'd rather catch a borderline paper than miss it.

Use ONLY taxonomy IDs from the vocabulary below. Pick the closest broader tag if no exact match exists. Tags can be empty arrays if nothing applies.

Output STRICT JSON. No prose, no markdown, no code fences. Schema:
{
  "is_relevant": boolean,
  "reason": "1 sentence",
  "censors": ["array of censor ids"],
  "techniques": ["array of technique ids — REQUIRED, at least one"],
  "defenses_discussed": ["array of defense ids, optional"],
  "evaluation_methods": ["array, optional"],
  "title": "the paper's actual title — NO venue or year suffix",
  "authors": ["author full names extracted from the body, in order"],
  "venue": "publication venue (e.g. 'USENIX Security', 'PoPETs', 'Article 19 report', 'arXiv preprint')",
  "year": integer,
  "clean_abstract": "1-3 sentence summary of what the paper does",
  "canonical_url": "the first non-github paper URL in the body",
  "notes": "1 sentence on Lantern relevance, or empty"
}

CRITICAL formatting rules:
  - "title" must be ONLY the paper's actual title. Do not append "(FOCI 2026)", "(Journal of X 2026)", "(USENIX Security 2025)", or any venue/year suffix. The venue goes in its own "venue" field; the year in "year". Many net4people issue titles have these suffixes — strip them.
  - "techniques" must contain at least one taxonomy ID for any relevant paper. If no specific detection technique fits, use the broadest applicable tag (e.g., "ip-blocking" for IP/geo-based blocking like sanctions, "measurement-platform" for measurement studies that don't study a specific technique, "keyword-filtering" for content-based blocking). Do NOT default to "dpi" unless the paper actually studies DPI.

For arXiv candidates: title/authors/year/abstract are already correct in the input — you can skip those fields in your response (we keep what we have). Just give relevance + tags + notes.

For net4people candidates: the input "Abstract" is actually the GitHub issue body and contains the metadata you need to extract (authors line, URL link, summary). Extract all the metadata fields above.

=== TAXONOMY ===
`)
	b.WriteString(taxonomy)
	b.WriteString("\n=== PAPER ===\n")
	fmt.Fprintf(&b, "Source: %s\nTitle: %s\n", c.Source, c.Title)
	if len(c.Authors) > 0 {
		fmt.Fprintf(&b, "Authors: %s\n", strings.Join(c.Authors, ", "))
	}
	if c.ArxivID != "" {
		fmt.Fprintf(&b, "ArXiv: %s\n", c.ArxivID)
	}
	if c.URL != "" {
		fmt.Fprintf(&b, "URL: %s\n", c.URL)
	}
	if c.Year != 0 {
		fmt.Fprintf(&b, "Year hint: %d\n", c.Year)
	}
	b.WriteString("\n--- Abstract / body ---\n")
	b.WriteString(c.Abstract)
	b.WriteString("\n")
	return b.String()
}

// ── rejection cache ───────────────────────────────────────────────
//
// The crawler classifies the same arXiv papers every run since the
// arXiv recent-submissions API doesn't give us a "since-token." Without
// a cache, we re-pay the LLM cost for every recurring not-relevant
// paper — dozens per week, mostly cs.CR malware/IoT/ML-adversarial work.
//
// Rejections are persisted to .crawl-state/rejected.json at the repo
// root. The file commits with the auto-ingest PR so the cache survives
// machine moves and is auditable — if the LLM is rejecting things it
// shouldn't, the file shows what + why + when.
//
// Identity key: "arxiv:<id>" for arxiv candidates, "url:<url>" for
// net4people candidates. Stable across re-fetches.
//
// To re-evaluate everything (e.g., after broadening relevance criteria
// or upgrading the model), pass --reclassify.

type rejection struct {
	Key        string `json:"key"`
	Title      string `json:"title"`
	Source     string `json:"source"`
	Reason     string `json:"reason,omitempty"`
	RejectedAt string `json:"rejected_at"`
}

type rejectStore struct {
	path  string
	seen  map[string]rejection
	dirty bool
}

func loadRejectStore(corpusRoot string) (*rejectStore, error) {
	path := filepath.Join(corpusRoot, ".crawl-state", "rejected.json")
	rs := &rejectStore{path: path, seen: map[string]rejection{}}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return rs, nil
	}
	if err != nil {
		return nil, err
	}
	var items []rejection
	if err := json.Unmarshal(raw, &items); err != nil {
		// Tolerate a malformed file rather than blocking the run.
		log.Printf("warning: rejected.json is malformed (%v); starting fresh", err)
		return rs, nil
	}
	for _, it := range items {
		rs.seen[it.Key] = it
	}
	return rs, nil
}

func (rs *rejectStore) Has(key string) bool {
	if rs == nil {
		return false
	}
	_, ok := rs.seen[key]
	return ok
}

func (rs *rejectStore) Add(c candidate, reason string) {
	if rs == nil {
		return
	}
	key := c.identityKey()
	if key == "" {
		return
	}
	rs.seen[key] = rejection{
		Key:        key,
		Title:      c.Title,
		Source:     c.Source,
		Reason:     reason,
		RejectedAt: time.Now().UTC().Format("2006-01-02"),
	}
	rs.dirty = true
}

// Save writes the rejection cache, sorted for stable git diffs. Returns
// the path written (empty if nothing was dirty).
func (rs *rejectStore) Save() (string, error) {
	if rs == nil || !rs.dirty {
		return "", nil
	}
	items := make([]rejection, 0, len(rs.seen))
	for _, v := range rs.seen {
		items = append(items, v)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].RejectedAt != items[j].RejectedAt {
			return items[i].RejectedAt > items[j].RejectedAt
		}
		return items[i].Key < items[j].Key
	})
	if err := os.MkdirAll(filepath.Dir(rs.path), 0o755); err != nil {
		return "", err
	}
	buf, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return "", err
	}
	buf = append(buf, '\n')
	if err := os.WriteFile(rs.path, buf, 0o644); err != nil {
		return "", err
	}
	return rs.path, nil
}

// identityKey returns a stable identifier across re-fetches.
func (c candidate) identityKey() string {
	if c.ArxivID != "" {
		return "arxiv:" + c.ArxivID
	}
	if c.URL != "" {
		return "url:" + c.URL
	}
	return ""
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
		venue := a.c.Venue
		if venue == "" {
			venue = "arXiv preprint"
		}
		sources := []string{}
		if a.c.ArxivID != "" {
			sources = append(sources, "arxiv:"+a.c.ArxivID)
		}
		sources = append(sources, a.c.Refs...)
		y := paperYAML{
			ID:      id,
			Title:   stripVenueSuffix(a.c.Title),
			Authors: a.c.Authors,
			Venue:   venue,
			Year:    a.c.Year,
			ArxivID: a.c.ArxivID,
			URL:     a.c.URL,
			Abstract: a.c.Abstract,
			// Censors defaults to ["generic"] when not specific — that's
			// always a valid taxonomy ID. Techniques does NOT default:
			// previously we defaulted to ["dpi"] which silently mis-tagged
			// papers like sanctions-driven blocking as DPI work. Better to
			// emit empty techniques and let the integrity test fail visibly
			// in PR review, prompting the human to add the right tag.
			Censors:           defaultIfEmpty(a.k.Censors, []string{"generic"}),
			Techniques:        a.k.Techniques,
			DefensesDiscussed: a.k.DefensesDiscussed,
			EvaluationMethods: a.k.EvaluationMethods,
			Core:              false,
			Notes:             noteWithProvenance(a.k.Notes),
			Visibility:        "public",
			DateAdded:         time.Now().UTC().Format("2006-01-02"),
			AddedBy:           "corpus-crawl-bot",
			Sources:           sources,
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

// openPR commits prFiles (new YAMLs + optionally the rejection cache)
// on a fresh auto-ingest branch and opens a PR labeled `auto-ingest`.
// `accepted` count comes from the items slice (one per new YAML).
func openPR(ctx context.Context, root string, items []accepted, prFiles []string) (string, error) {
	if err := requireTool("gh"); err != nil {
		return "", err
	}
	// Number of paper YAMLs (everything in prFiles except the rejection
	// cache) drives the commit/PR title text.
	numPapers := len(items)

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
	args := append([]string{"add"}, prFiles...)
	if err := run("git", args...); err != nil {
		return "", err
	}
	commitMsg := fmt.Sprintf("Auto-ingest: %d new paper%s from arXiv cs.CR\n\nProposed by corpus-crawl. Review tags + notes before merging.",
		numPapers, pluralS(numPapers))
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

	title := fmt.Sprintf("Auto-ingest: %d new paper%s (%s)", numPapers, pluralS(numPapers), time.Now().UTC().Format("2006-01-02"))
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
			corpus:      *corpus,
			maxN:        intOrDefault(r.URL.Query().Get("max"), 12),
			maxClassify: intOrDefault(r.URL.Query().Get("max_classify"), 80),
			windowDays:  intOrDefault(r.URL.Query().Get("window_days"), 14),
			dryRun:      r.URL.Query().Get("dry_run") == "1",
			noPR:        r.URL.Query().Get("no_pr") == "1",
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

	mux.HandleFunc("/ask", func(w http.ResponseWriter, r *http.Request) {
		askHandler(w, r, *authToken, *corpus)
	})

	log.Printf("corpus-crawl serve listening on %s", *listen)
	srv := &http.Server{Addr: *listen, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

// ────────────────── /ask — corpus query → cited LLM answer ──────────────────
//
// POST /ask { "question": "..." } returns the synthesize-bundle plus an LLM
// answer that cites the bundle's findings inline. Internally:
//   1. Fetch the structured bundle from corpus.lantern.io/mcp's synthesize
//      tool (one HTTP round trip — reuses the deployed MCP, no algorithm
//      duplication).
//   2. Format an LLM-friendly text prompt over the findings.
//   3. Shell out to `claude -p` (auth via the user's macOS keychain — same
//      as the findings-extraction backfill). Bound CPU time at 90s.
//   4. Return { question, answer, bundle } as JSON.
//
// Auth: same Bearer scheme as /crawl. Public visitors hit a
// rate-limited Cloudflare Pages Function that holds the token and
// proxies here, so the end-user form is anonymous.

const askMCPEndpoint = "https://corpus.lantern.io/mcp"

type askRequest struct {
	Question   string   `json:"question"`
	Censors    []string `json:"censors,omitempty"`
	Techniques []string `json:"techniques,omitempty"`
	Defenses   []string `json:"defenses,omitempty"`
	Limit      int      `json:"limit,omitempty"`
}

type askResponse struct {
	Question string          `json:"question"`
	Answer   string          `json:"answer"`
	Bundle   json.RawMessage `json:"bundle"`
	Elapsed  string          `json:"elapsed_ms"`
}

func askHandler(w http.ResponseWriter, r *http.Request, authToken, corpusRoot string) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", 405)
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+authToken {
		http.Error(w, "unauthorized", 401)
		return
	}
	defer r.Body.Close()
	var req askRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 16*1024)).Decode(&req); err != nil {
		http.Error(w, "bad json: "+err.Error(), 400)
		return
	}
	q := strings.TrimSpace(req.Question)
	if q == "" {
		http.Error(w, "question required", 400)
		return
	}
	if len(q) > 500 {
		http.Error(w, "question too long (max 500 chars)", 400)
		return
	}
	if req.Limit <= 0 {
		req.Limit = 30
	}

	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	start := time.Now()

	bundle, err := fetchSynthesizeBundle(ctx, req)
	if err != nil {
		log.Printf("/ask synthesize fetch: %v", err)
		http.Error(w, "synthesize backend unavailable: "+err.Error(), 502)
		return
	}

	prompt, foundN := formatSynthesisPrompt(q, bundle)
	if foundN == 0 {
		// No findings matched — short-circuit; don't burn an LLM call.
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(askResponse{
			Question: q,
			Answer:   "No findings in the corpus match this question yet. Try a broader phrasing, or browse /findings/ to see what's there.",
			Bundle:   bundle,
			Elapsed:  time.Since(start).Round(time.Millisecond).String(),
		})
		return
	}

	answer, err := runClaudeAsk(ctx, corpusRoot, prompt)
	if err != nil {
		log.Printf("/ask claude: %v", err)
		http.Error(w, "claude execution failed: "+err.Error(), 502)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(askResponse{
		Question: q,
		Answer:   strings.TrimSpace(answer),
		Bundle:   bundle,
		Elapsed:  time.Since(start).Round(time.Millisecond).String(),
	})
}

// fetchSynthesizeBundle calls the public corpus.lantern.io/mcp endpoint
// and returns the synthesize tool's structured result. Reusing the
// deployed MCP means there's exactly one source of truth for the
// retrieval algorithm.
func fetchSynthesizeBundle(ctx context.Context, req askRequest) (json.RawMessage, error) {
	rpcReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "synthesize",
			"arguments": map[string]any{
				"question":   req.Question,
				"censors":    req.Censors,
				"techniques": req.Techniques,
				"defenses":   req.Defenses,
				"limit":      req.Limit,
			},
		},
	}
	body, err := json.Marshal(rpcReq)
	if err != nil {
		return nil, err
	}
	hreq, err := http.NewRequestWithContext(ctx, "POST", askMCPEndpoint, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	hreq.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(hreq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("mcp status %d: %s", resp.StatusCode, string(raw))
	}
	var rpc struct {
		Result *struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &rpc); err != nil {
		return nil, fmt.Errorf("decode mcp envelope: %w", err)
	}
	if rpc.Error != nil {
		return nil, fmt.Errorf("mcp error: %s", rpc.Error.Message)
	}
	if rpc.Result == nil || len(rpc.Result.Content) == 0 {
		return nil, fmt.Errorf("mcp empty result")
	}
	return json.RawMessage(rpc.Result.Content[0].Text), nil
}

type bundleFinding struct {
	ID                  string   `json:"id"`
	Paper               string   `json:"paper"`
	Summary             string   `json:"summary"`
	Section             string   `json:"section"`
	Techniques          []string `json:"techniques"`
	Censors             []string `json:"censors"`
	Defenses            []string `json:"defenses"`
	DefenseImplications []string `json:"defense_implications"`
	PaperTitle          string   `json:"paper_title"`
	PaperAuthors        []string `json:"paper_authors"`
	PaperYear           int      `json:"paper_year"`
	PaperVenue          string   `json:"paper_venue"`
	PaperURL            string   `json:"paper_url"`
}

type bundleShape struct {
	Findings []bundleFinding `json:"findings"`
	Counts   struct {
		MatchedFindings int `json:"matched_findings"`
		MatchedPapers   int `json:"matched_papers"`
	} `json:"counts"`
}

// formatSynthesisPrompt renders the bundle as a compact text prompt
// (much friendlier for `claude -p` than raw JSON). Returns the prompt
// and the count of findings included.
func formatSynthesisPrompt(question string, bundle json.RawMessage) (string, int) {
	var b bundleShape
	if err := json.Unmarshal(bundle, &b); err != nil {
		return "", 0
	}
	var sb strings.Builder
	sb.WriteString("You are answering a research question using extracted findings from the circumvention-corpus, a structured-metadata corpus of censorship-circumvention research.\n\n")
	sb.WriteString("Question: ")
	sb.WriteString(question)
	sb.WriteString("\n\n")
	sb.WriteString(fmt.Sprintf("%d matching findings (cite each inline as (paper_id, §section)):\n\n", b.Counts.MatchedFindings))
	for i, f := range b.Findings {
		fmt.Fprintf(&sb, "[%d] %s — %d", i+1, f.Paper, f.PaperYear)
		if f.PaperVenue != "" {
			fmt.Fprintf(&sb, " · %s", f.PaperVenue)
		}
		if f.Section != "" {
			fmt.Fprintf(&sb, " · %s", f.Section)
		}
		sb.WriteString("\n")
		fmt.Fprintf(&sb, "    %s\n", strings.ReplaceAll(strings.TrimSpace(f.Summary), "\n", " "))
		if len(f.DefenseImplications) > 0 {
			for _, di := range f.DefenseImplications {
				fmt.Fprintf(&sb, "    → %s\n", strings.ReplaceAll(strings.TrimSpace(di), "\n", " "))
			}
		}
		if len(f.Techniques) > 0 || len(f.Censors) > 0 {
			sb.WriteString("    tags: ")
			if len(f.Techniques) > 0 {
				fmt.Fprintf(&sb, "techniques=%s ", strings.Join(f.Techniques, ","))
			}
			if len(f.Censors) > 0 {
				fmt.Fprintf(&sb, "censors=%s", strings.Join(f.Censors, ","))
			}
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}
	sb.WriteString("Produce a concise structured answer in markdown:\n")
	sb.WriteString("**What's known** — claims supported by multiple findings.\n")
	sb.WriteString("**What's contested or limited** — where findings diverge or come from a single paper.\n")
	sb.WriteString("**Open questions** — gaps the matched findings don't address.\n\n")
	sb.WriteString("Cite every claim inline as (paper_id, §section). Be specific. ")
	sb.WriteString("If matched findings are few (< 3), say so explicitly. Do not invent citations or summarize papers not in the list above.\n")
	return sb.String(), b.Counts.MatchedFindings
}

// runClaudeAsk shells out to `claude -p` with the prompt on stdin.
// Reuses the same approach as the findings backfill: launchd
// (CWD = corpusRoot) means the macOS keychain is unlocked so the
// OAuth token resolves. Output of `claude -p` is the assistant
// response text.
func runClaudeAsk(ctx context.Context, corpusRoot, prompt string) (string, error) {
	bin := locateClaude()
	if bin == "" {
		return "", fmt.Errorf("claude binary not found in $PATH or ~/.claude/local/")
	}
	cctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, bin, "-p", "--max-turns", "1", "--permission-mode", "bypassPermissions")
	cmd.Dir = corpusRoot
	cmd.Stdin = strings.NewReader(prompt)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%w (stderr: %s)", err, strings.TrimSpace(errBuf.String()))
	}
	return out.String(), nil
}

func locateClaude() string {
	for _, p := range []string{
		"/opt/homebrew/bin/claude",
		"/usr/local/bin/claude",
		os.ExpandEnv("$HOME/.claude/local/claude"),
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if path, err := exec.LookPath("claude"); err == nil {
		return path
	}
	return ""
}

// ── helpers ───────────────────────────────────────────────────────

func matchesKeywords(c candidate) bool {
	return matchesKeywordsInText(c.Title + " " + c.Abstract)
}

func matchesKeywordsInText(s string) bool {
	hay := strings.ToLower(s)
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

// venueSuffixRE matches "(Venue Year)" and "(Venue YYYY)" trailing
// suffixes that the LLM tends to copy from net4people issue titles
// (e.g., "Title here (FOCI 2026)"). Stripped before normalization
// so dedup correctly matches the same paper across sources, AND
// stripped from the YAML title field itself so the corpus stores
// the actual paper title (venue lives in the venue field).
var venueSuffixRE = regexp.MustCompile(`\s*\([^)]*\b(19|20)\d{2}\)\s*$`)

func stripVenueSuffix(t string) string {
	return strings.TrimSpace(venueSuffixRE.ReplaceAllString(t, ""))
}

func normalizeTitle(t string) string {
	t = stripVenueSuffix(t)
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

// claudeMCPArgs returns per-invocation flags for `claude -p` that let
// the LLM call MCP tools (wick_fetch, search_papers, etc.) mid-turn.
// We do NOT pass --mcp-config — claude -p inherits the user's persisted
// MCP servers from ~/.claude.json (registered via `claude mcp add`).
// Required registrations on the runtime host:
//
//	claude mcp add wick wick mcp                                  # stdio
//	claude mcp add --transport http circumvention-corpus \
//	    https://corpus.lantern.io/mcp                            # HTTP
func claudeMCPArgs(_ string) []string {
	allowed := strings.Join([]string{
		"mcp__wick__wick_fetch",
		"mcp__wick__wick_search",
		"mcp__circumvention-corpus__search_papers",
		"mcp__circumvention-corpus__get_paper",
		"mcp__circumvention-corpus__list_taxonomy",
		"mcp__circumvention-corpus__find_related",
	}, ",")
	return []string{
		"--max-turns", "5",
		"--permission-mode", "bypassPermissions",
		"--allowed-tools", allowed,
	}
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
