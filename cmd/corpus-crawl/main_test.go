package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// foci636Titles is the paper list from net4people/bbs#636 ("FOCI and PETS
// 2026 papers"). When that issue was posted the crawler had already ingested
// 8 of the 14; the other 6 never even reached the LLM classifier because the
// title-only keyword gate dropped them, so they never showed up in the
// rejection cache as a decision anyone could review. Every one of them is
// squarely in scope for this corpus. They are the regression fixture: the
// keyword filter must not silently drop a censorship paper because its title
// avoids the word "censor".
var foci636Titles = []string{
	"Beyond OS Trust Stores: TLS Trust in Russia's Android Ecosystem",
	"An Empirical Study of Backend Infrastructure in Leading Pakistani Mobile Apps",
	"Fountain codes in censorship circumvention rendezvous",
	"Who Carries Tor? Measuring Bandwidth-Weighted Transit Concentration",
	"Gaps in the Record: On the Observability and Documentation of Internet Shutdowns",
	"Insights into an Iranian Internet Shutdown",
	"On Russia's Early Introduction of QUIC SNI Censorship",
	"Who Decides What Stays and Why? Participation, Authority, and Rationales in Chinese Wikipedia's Deletion Discussions",
	"Evaluating connection migration based QUIC censorship circumvention",
	"Obscura: Enabling Ephemeral Proxies for Traffic Encapsulation in WebRTC Media Streams Against Cost-Effective Censors",
	"CensorLess: Cost-Efficient Censorship Circumvention Through Serverless Cloud Functions",
	"Troll Patrol: Anonymous User Reporting of Bridge Censorship",
	"Precarious But Active: A Look At Privacy Behaviors in Chinese Transformative Fandom on a Censored and Surveilled Internet",
	"Maude-HCS: Model Checking the Undetectability-Performance Tradeoffs in Hidden Communication Systems",
}

func TestKeywordFilterAcceptsFOCI636Titles(t *testing.T) {
	for _, title := range foci636Titles {
		if !matchesKeywordsInText(title) {
			t.Errorf("keyword filter drops an in-scope paper: %q", title)
		}
	}
}

// TestKeywordFilterRejectsUnrelated guards the over-matching that the old
// space-padded entries existed to prevent. Word boundaries have to do that
// job now: "tor" must not fire on vector, "ech" must not fire on echo, "dpi"
// must not fire on rapid.
func TestKeywordFilterRejectsUnrelated(t *testing.T) {
	for _, title := range []string{
		"Vector Commitments over Lattices with Sublinear Openings",
		"Echo State Networks for Time Series Prediction",
		"Rapid Prototyping of Actor Frameworks on Multicore Hardware",
		"A Formal Semantics for Gradual Typing",
	} {
		if matchesKeywordsInText(title) {
			t.Errorf("keyword filter accepts unrelated paper: %q", title)
		}
	}
}

func TestNormalizeTitleIgnoresCountryPrefixAndVenueSuffix(t *testing.T) {
	// The three shapes one net4people thread produced across four crawls.
	want := normalizeTitle("Advanced DPI is reassembling TCP fragments to extract SNI on VLESS/WS + CDN")
	for _, variant := range []string{
		"[Iran] Advanced DPI is reassembling TCP fragments to extract SNI on VLESS/WS + CDN",
		"Advanced DPI is reassembling TCP fragments to extract SNI on VLESS/WS + CDN (FOCI 2026)",
		"[Iran] Advanced DPI is reassembling TCP fragments to extract SNI on VLESS/WS + CDN (FOCI 2026)",
	} {
		if got := normalizeTitle(variant); got != want {
			t.Errorf("normalizeTitle(%q)\n = %q\nwant %q", variant, got, want)
		}
	}
}

func TestCanonicalURLAndArxivID(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"https://github.com/net4people/bbs/issues/628", "github.com/net4people/bbs/issues/628"},
		{"http://GitHub.com/net4people/bbs/issues/628/", "github.com/net4people/bbs/issues/628"},
		{"https://www.petsymposium.org/foci/2026/foci-2026-0010.php#top", "petsymposium.org/foci/2026/foci-2026-0010.php"},
	} {
		if got := canonicalURL(tc.in); got != tc.want {
			t.Errorf("canonicalURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	// A versioned abs URL and the bare id are the same paper.
	if a, b := canonicalArxivID("", "https://arxiv.org/abs/2606.10097v1"), canonicalArxivID("2606.10097", ""); a != b {
		t.Errorf("arxiv id mismatch: %q vs %q", a, b)
	}
}

// TestContainsMatchesOnURLAlone is the regression test for the pileup: the
// same net4people thread re-summarized under a different title, which changed
// the derived id too. Only the URL stayed put, and it was the one field the
// old dedup never indexed.
func TestContainsMatchesOnURLAlone(t *testing.T) {
	e := &existingCorpus{
		byID:    map[string]bool{},
		byTitle: map[string]bool{},
		byURL:   map[string]bool{"github.com/net4people/bbs/issues/628": true},
		byArxiv: map[string]bool{},
	}
	c := candidate{
		Title: "Totally different LLM summary of the same thread",
		URL:   "https://github.com/net4people/bbs/issues/628",
	}
	if !e.contains(c) {
		t.Error("candidate sharing only a URL with the corpus was treated as novel")
	}
}

// TestContainsMatchesSecondaryRef covers the net4people shape where the
// stored record's url is the canonical paper URL and the thread URL survives
// only as a source, while the incoming candidate has them the other way round.
func TestContainsMatchesSecondaryRef(t *testing.T) {
	e := &existingCorpus{
		byID:    map[string]bool{},
		byTitle: map[string]bool{},
		byURL:   map[string]bool{"randlab.engineering.ucsc.edu/blogs/iran-allowlist": true},
		byArxiv: map[string]bool{},
	}
	c := candidate{
		Title: "We researched Iran whitelist system before and after May.26 restoration",
		URL:   "https://github.com/net4people/bbs/issues/630",
		Refs:  []string{"url:https://randlab.engineering.ucsc.edu/blogs/iran-allowlist/"},
	}
	if !e.contains(c) {
		t.Error("candidate matching the corpus on a secondary ref was treated as novel")
	}
}

// TestIntraRunDedup covers one paper arriving from two sources in a single
// crawl, which put "On Russia's Early Introduction of QUIC SNI Censorship"
// into the 2026-07-06 batch twice.
func TestIntraRunDedup(t *testing.T) {
	seen := &existingCorpus{
		byID:    map[string]bool{},
		byTitle: map[string]bool{},
		byURL:   map[string]bool{},
		byArxiv: map[string]bool{},
	}
	fromFOCI := candidate{
		Title:   "On Russia's Early Introduction of QUIC SNI Censorship",
		Authors: []string{"Nico Heitmann"},
		Year:    2026,
		URL:     "https://petsymposium.org/foci/2026/foci-2026-0010.php",
		Source:  "foci",
	}
	fromBlog := candidate{
		Title:  "On Russia's Early Introduction of QUIC SNI Censorship",
		Year:   2026,
		URL:    "https://jonsnowwhite.github.io/page/files/foci_26_russia.pdf",
		Source: "paderborn-blog",
	}
	seen.remember(fromFOCI)
	if !seen.contains(fromBlog) {
		t.Error("same paper from a second source in the same run was not collapsed")
	}
}

func TestSanitizeDropsNonTaxonomyTags(t *testing.T) {
	tax := &taxonomy{
		censors:    map[string]bool{"generic": true, "ir": true},
		techniques: map[string]bool{"dns-poisoning": true},
		defenses:   map[string]bool{"pluggable-transport": true},
		evalMethod: map[string]bool{"controlled-deployment": true},
	}
	// The exact classification that turned CI red: pluggable-transport is a
	// defense, and the classifier put it in techniques.
	got := tax.sanitize(classification{
		Censors:           []string{"generic", "atlantis"},
		Techniques:        []string{"dns-poisoning", "pluggable-transport"},
		DefensesDiscussed: []string{"pluggable-transport"},
		EvaluationMethods: []string{"controlled-deployment", "vibes"},
	}, "test")

	if len(got.Techniques) != 1 || got.Techniques[0] != "dns-poisoning" {
		t.Errorf("techniques = %v, want [dns-poisoning]", got.Techniques)
	}
	if len(got.Censors) != 1 || got.Censors[0] != "generic" {
		t.Errorf("censors = %v, want [generic]", got.Censors)
	}
	if len(got.EvaluationMethods) != 1 {
		t.Errorf("evaluation_methods = %v, want [controlled-deployment]", got.EvaluationMethods)
	}
	if len(got.DefensesDiscussed) != 1 {
		t.Errorf("valid defense was dropped: %v", got.DefensesDiscussed)
	}
}

func TestCanonicalDOI(t *testing.T) {
	for _, in := range []string{
		"10.1109/INFCOMW.2019.8845315",
		"doi:10.1109/infcomw.2019.8845315",
		"https://doi.org/10.1109/INFCOMW.2019.8845315",
		"http://dx.doi.org/10.1109/infcomw.2019.8845315/",
	} {
		if got := canonicalDOI(in); got != "10.1109/infcomw.2019.8845315" {
			t.Errorf("canonicalDOI(%q) = %q", in, got)
		}
	}
	for _, in := range []string{"", "arxiv:2609.12242", "https://github.com/net4people/bbs/issues/628", "not a doi"} {
		if got := canonicalDOI(in); got != "" {
			t.Errorf("canonicalDOI(%q) = %q, want empty", in, got)
		}
	}
}

// A record can carry the schema's top-level doi while linking to the
// publisher, so its url never matches the doi.org URL a Crossref candidate
// arrives with. Without DOI indexing the only thing left is the title, and a
// title that differs by a subtitle or casing re-ingests the same paper.
func TestContainsMatchesTopLevelDOI(t *testing.T) {
	root := t.TempDir()
	papers := filepath.Join(root, "corpus", "papers")
	if err := os.MkdirAll(papers, 0o755); err != nil {
		t.Fatal(err)
	}
	yaml := "id: 2019-shapira-flowpic\n" +
		"title: 'FlowPic: Encrypted Internet Traffic Classification is as Easy as Image Recognition'\n" +
		"year: 2019\ndoi: 10.1109/INFCOMW.2019.8845315\n" +
		"url: https://ieeexplore.ieee.org/document/8845315\n" +
		"censors: [generic]\ntechniques: [ml-classifier]\nvisibility: public\n"
	if err := os.WriteFile(filepath.Join(papers, "2019-shapira-flowpic.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	e, err := loadExisting(root)
	if err != nil {
		t.Fatal(err)
	}
	c := candidate{
		Source: "crossref",
		Title:  "FlowPic: Encrypted Internet Traffic Classification",
		URL:    "https://doi.org/10.1109/infcomw.2019.8845315",
		Refs:   []string{"doi:10.1109/infcomw.2019.8845315"},
	}
	if !e.contains(c) {
		t.Error("Crossref candidate matching an existing record's top-level doi was treated as novel")
	}
	other := candidate{Source: "crossref", Title: "Something else entirely", URL: "https://doi.org/10.1109/other"}
	if e.contains(other) {
		t.Error("unrelated DOI matched")
	}
}

func TestWriteYAMLsUsesCandidateSource(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "corpus", "papers"), 0o755); err != nil {
		t.Fatal(err)
	}
	items := []accepted{{
		c: candidate{
			Source: "crossref",
			Title:  "Encrypted Traffic Classification Without a Venue",
			Year:   2026,
			URL:    "https://doi.org/10.1109/tnsm.2021.3071441",
			Refs:   []string{"doi:10.1109/tnsm.2021.3071441"},
		},
		k: classification{IsRelevant: true, Censors: []string{"generic"}, Techniques: []string{"ml-classifier"}},
	}}
	written, err := writeYAMLs(root, items, false)
	if err != nil || len(written) != 1 {
		t.Fatalf("err=%v written=%v", err, written)
	}
	raw, err := os.ReadFile(written[0])
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	if strings.Contains(got, "arXiv preprint") {
		t.Error("a Crossref paper with no venue was labelled an arXiv preprint")
	}
	if !strings.Contains(got, "doi: 10.1109/tnsm.2021.3071441") {
		t.Error("top-level doi not emitted")
	}
	if !strings.Contains(got, "Source: crossref doi:10.1109/tnsm.2021.3071441") {
		t.Errorf("header does not name the real source:\n%s", got[:200])
	}
	if strings.Contains(got, "Source: arXiv") {
		t.Error("header claims arXiv provenance")
	}
}

func TestPRBodyIdentityAndSourceSummary(t *testing.T) {
	arxiv := candidate{Source: "arxiv", ArxivID: "2609.12242", URL: "https://arxiv.org/abs/2609.12242"}
	cross := candidate{Source: "crossref", URL: "https://doi.org/10.1109/x", Refs: []string{"doi:10.1109/x"}}
	bare := candidate{Source: "foci", URL: "https://example.org/paper"}
	if got := identityLine(arxiv); !strings.Contains(got, "**arXiv**: [2609.12242]") {
		t.Errorf("arxiv line: %q", got)
	}
	if got := identityLine(cross); !strings.Contains(got, "**DOI**: [10.1109/x](https://doi.org/10.1109/x)") {
		t.Errorf("crossref line: %q", got)
	}
	if got := identityLine(bare); !strings.Contains(got, "https://example.org/paper") {
		t.Errorf("fallback line: %q", got)
	}
	items := []accepted{{c: arxiv}, {c: cross}, {c: arxiv}}
	if got := sourceSummary(items); got != "Crossref + arXiv cs.CR + cs.NI" {
		t.Errorf("sourceSummary = %q", got)
	}
	if got := sourceSummary(nil); got != "the crawler sources" {
		t.Errorf("empty sourceSummary = %q", got)
	}
}

func TestClassifyPromptIncludesVenue(t *testing.T) {
	c := candidate{Source: "crossref", Title: "Encrypted Traffic Classification", Venue: "IEEE INFOCOM Workshops"}
	got := buildClassifyPrompt(c, "taxonomy:")
	if !strings.Contains(got, "Venue hint: IEEE INFOCOM Workshops") {
		t.Error("venue missing from the prompt the instructions tell the model to use")
	}
	if !strings.Contains(got, "(none — this source provided title-level metadata only)") {
		t.Error("empty abstract not marked explicitly")
	}
	if strings.Contains(buildClassifyPrompt(candidate{Title: "x"}, ""), "Venue hint:") {
		t.Error("empty venue should be omitted")
	}
}

// The intra-run dedup corpus in runWith is built fresh each crawl and
// remember() writes to every index, so a constructor that misses one panics
// on the first candidate carrying that identity — which, once DOIs were
// indexed, meant every Crossref candidate, before classification and even
// under --dry-run.
func TestNewExistingCorpusInitializesEveryIndex(t *testing.T) {
	e := newExistingCorpus()
	c := candidate{
		Source:  "crossref",
		Title:   "Encrypted Traffic Classification",
		URL:     "https://doi.org/10.1109/x",
		Refs:    []string{"doi:10.1109/x"},
		ArxivID: "2609.12242",
	}
	e.remember(c) // must not panic on any nil map
	if !e.contains(c) {
		t.Error("remembered candidate not recognized")
	}
	if !e.byDOI["10.1109/x"] {
		t.Error("DOI index not populated")
	}
}
