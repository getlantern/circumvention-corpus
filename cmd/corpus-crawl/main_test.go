package main

import "testing"

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

func TestUSENIXFinalProgramCoverage(t *testing.T) {
	const base = "https://www.usenix.org/conference/usenixsecurity26/"
	urls := usenixSecurityURLs(2026)
	found := false
	for _, url := range urls {
		if url == base+"technical-sessions" {
			found = true
		}
	}
	if !found {
		t.Fatal("missing final program: cycle 2 can be unavailable")
	}
	const program = `## [Breaking the Boundaries: Analyzing QUIC Frame-Packet Interactions With QUIC-Attacker](/conference/usenixsecurity26/presentation/erinola)

Nurullah Erinola, Marcel Maehren, Marcus Brinkmann, and Jörg Schwenk, *Ruhr University Bochum*

We develop probes to explore how different QUIC server implementations handle the coalescence and fragmentation of payloads, covering both valid and invalid combinations of datagrams, packets, and frames.

## [Technical Analysis of the Geedge Networks Firewall Source Code Leak](/conference/usenixsecurity26/presentation/ablove)

Anna Ablove, *University of Michigan;* Johnnie Walker, *GFW Report*

In this paper, we analyze the source code from this leak, focusing on Geedge Networks' flagship product, the Tiangou Secure Gateway (TSG) firewall.
`
	papers, err := parseUSENIXSecurity(program, base+"technical-sessions", 2026)
	if err != nil {
		t.Fatal(err)
	}
	if len(papers) != 2 {
		t.Fatalf("parsed %d papers, want 2", len(papers))
	}
	for i, slug := range []string{"erinola", "ablove"} {
		p := papers[i]
		if p.URL != base+"presentation/"+slug || p.Year != 2026 || len(p.Authors) == 0 || p.Abstract == "" {
			t.Errorf("incomplete record: %+v", p)
		}
		if !matchesKeywordsInText(p.Title) {
			t.Errorf("title gate drops %q", p.Title)
		}
	}
	existing := &existingCorpus{byID: map[string]bool{}, byTitle: map[string]bool{}, byURL: map[string]bool{}, byArxiv: map[string]bool{}}
	existing.remember(papers[0])
	if !existing.contains(papers[0]) {
		t.Fatal("same paper on a cycle page must deduplicate")
	}
}
