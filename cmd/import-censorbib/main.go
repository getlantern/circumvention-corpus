// import-censorbib is a one-shot importer that ingests the CensorBib
// BibTeX file (https://github.com/NullHypothesis/censorbib) into the
// corpus's YAML format. Heuristic tagging only — every imported record
// is marked sources: [censorbib] and core: false so the team can spot
// what came from the bulk import vs. what's been hand-curated. Re-running
// the importer is safe: existing YAML files (matched by title) are
// skipped.
//
// Usage:
//
//	go run ./cmd/import-censorbib --bib /tmp/censorbib.bib --corpus . [--limit 50]
//
// The importer writes one YAML per accepted record to corpus/papers/.
// After running, run `go test ./...` to validate the corpus integrity
// (every tag must resolve in the taxonomy).
package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// bibEntry is the parsed shape of a single @type{key, field = value, ...}
// block from the BibTeX file.
type bibEntry struct {
	Type   string
	Key    string
	Fields map[string]string
}

func main() {
	bibPath := flag.String("bib", "", "Path to the CensorBib references.bib file.")
	corpusDir := flag.String("corpus", ".", "Path to the circumvention-corpus repo root.")
	limit := flag.Int("limit", 0, "If > 0, stop after this many newly-written records (useful for incremental commits).")
	dryRun := flag.Bool("dry-run", false, "Parse + plan but don't write any files.")
	flag.Parse()
	if *bibPath == "" {
		log.Fatal("--bib is required")
	}

	raw, err := os.ReadFile(*bibPath)
	if err != nil {
		log.Fatal(err)
	}
	entries, err := parseBibTeX(string(raw))
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("parsed %d BibTeX entries", len(entries))

	// Build a set of existing titles (normalized) so we don't write
	// duplicates against the hand-curated entries already in the corpus.
	existingTitles, existingIDs, err := loadExistingFingerprints(*corpusDir)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("corpus already has %d papers", len(existingTitles))

	written := 0
	skipped := 0
	for _, e := range entries {
		title := cleanField(e.Fields["title"])
		if title == "" {
			skipped++
			continue
		}
		if _, dup := existingTitles[normalizeTitle(title)]; dup {
			skipped++
			continue
		}
		paper, ok := bibToPaper(e)
		if !ok {
			skipped++
			continue
		}
		// Avoid id collisions (different titles, same first-author / year /
		// first-word produce the same slug). Append -bibkey-suffix when
		// needed.
		base := paper["id"].(string)
		id := base
		for i := 2; ; i++ {
			if _, dup := existingIDs[id]; !dup {
				break
			}
			id = fmt.Sprintf("%s-%d", base, i)
		}
		paper["id"] = id
		existingIDs[id] = true
		existingTitles[normalizeTitle(title)] = true

		path := filepath.Join(*corpusDir, "corpus", "papers", id+".yaml")
		if *dryRun {
			log.Printf("[dry-run] would write %s", path)
		} else {
			if err := writeYAML(path, paper); err != nil {
				log.Fatalf("write %s: %v", path, err)
			}
		}
		written++
		if *limit > 0 && written >= *limit {
			log.Printf("hit --limit %d, stopping", *limit)
			break
		}
	}
	log.Printf("written %d, skipped %d (duplicates or unparseable)", written, skipped)
}

// parseBibTeX parses a CensorBib-style BibTeX file. The format here is
// regular enough that a tiny state-machine parser handles it:
// @type{key, field = {value}, field = {value or "value"} } with optional
// trailing commas. Comments / preamble blocks are ignored.
func parseBibTeX(s string) ([]*bibEntry, error) {
	var out []*bibEntry
	i := 0
	for i < len(s) {
		// Skip whitespace and find the next entry.
		for i < len(s) && s[i] != '@' {
			i++
		}
		if i >= len(s) {
			break
		}
		// Read type up to '{'.
		i++
		typeStart := i
		for i < len(s) && s[i] != '{' {
			i++
		}
		if i >= len(s) {
			break
		}
		entryType := strings.ToLower(strings.TrimSpace(s[typeStart:i]))
		i++ // past '{'
		// Skip "@string" / "@preamble" / "@comment" blocks.
		if entryType == "string" || entryType == "preamble" || entryType == "comment" {
			depth := 1
			for i < len(s) && depth > 0 {
				if s[i] == '{' {
					depth++
				} else if s[i] == '}' {
					depth--
				}
				i++
			}
			continue
		}
		// Read key up to ','.
		keyStart := i
		for i < len(s) && s[i] != ',' && s[i] != '}' {
			i++
		}
		if i >= len(s) {
			break
		}
		key := strings.TrimSpace(s[keyStart:i])
		fields := map[string]string{}
		// Read fields.
		for i < len(s) && s[i] != '}' {
			// Skip leading separator/whitespace.
			for i < len(s) && (s[i] == ',' || s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r') {
				i++
			}
			if i >= len(s) || s[i] == '}' {
				break
			}
			// Field name.
			fieldStart := i
			for i < len(s) && s[i] != '=' && s[i] != '}' {
				i++
			}
			if i >= len(s) || s[i] == '}' {
				break
			}
			name := strings.ToLower(strings.TrimSpace(s[fieldStart:i]))
			i++ // past '='
			// Skip whitespace.
			for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r') {
				i++
			}
			if i >= len(s) {
				break
			}
			// Field value: { ... }, " ... ", or unquoted (number).
			value := ""
			switch s[i] {
			case '{':
				i++
				start := i
				depth := 1
				for i < len(s) && depth > 0 {
					switch s[i] {
					case '{':
						depth++
					case '}':
						depth--
					}
					if depth > 0 {
						i++
					}
				}
				value = s[start:i]
				if i < len(s) {
					i++ // past closing '}'
				}
			case '"':
				i++
				start := i
				for i < len(s) && s[i] != '"' {
					i++
				}
				value = s[start:i]
				if i < len(s) {
					i++ // past closing '"'
				}
			default:
				start := i
				for i < len(s) && s[i] != ',' && s[i] != '}' && s[i] != '\n' {
					i++
				}
				value = strings.TrimSpace(s[start:i])
			}
			fields[name] = value
		}
		if i < len(s) && s[i] == '}' {
			i++
		}
		if key != "" {
			out = append(out, &bibEntry{Type: entryType, Key: key, Fields: fields})
		}
	}
	return out, nil
}

// loadExistingFingerprints walks corpus/papers/ and returns
// (normalizedTitle → true, id → true) maps used for dedup.
func loadExistingFingerprints(corpusDir string) (titles, ids map[string]bool, err error) {
	titles = map[string]bool{}
	ids = map[string]bool{}
	dir := filepath.Join(corpusDir, "corpus", "papers")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, nil, err
		}
		var p struct {
			ID    string `yaml:"id"`
			Title string `yaml:"title"`
		}
		if err := yaml.Unmarshal(raw, &p); err != nil {
			return nil, nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		if p.ID != "" {
			ids[p.ID] = true
		}
		if p.Title != "" {
			titles[normalizeTitle(p.Title)] = true
		}
	}
	return titles, ids, nil
}

// bibToPaper converts a parsed BibTeX entry into the YAML map shape we
// write out. Returns ok=false if mandatory fields (title, year, an
// extractable first author) aren't present.
func bibToPaper(e *bibEntry) (map[string]any, bool) {
	title := cleanField(e.Fields["title"])
	if title == "" {
		return nil, false
	}
	year, _ := strconv.Atoi(cleanField(e.Fields["year"]))
	if year < 1990 || year > 2100 {
		return nil, false
	}
	authors := splitAuthors(cleanField(e.Fields["author"]))
	if len(authors) == 0 {
		return nil, false
	}
	venue := cleanField(e.Fields["booktitle"])
	if venue == "" {
		venue = cleanField(e.Fields["journal"])
	}
	// CensorBib's @misc and @techreport entries often have venue in the
	// `howpublished` or `institution` field.
	if venue == "" {
		venue = cleanField(e.Fields["howpublished"])
	}
	if venue == "" {
		venue = cleanField(e.Fields["institution"])
	}

	id := makeID(authors[0], year, title, e.Key)

	censors := guessCensors(title)
	techniques := guessTechniques(title)
	defenses := guessDefenses(title)

	paper := map[string]any{
		"id":         id,
		"title":      title,
		"authors":    authors,
		"year":       year,
		"censors":    censors,
		"techniques": techniques,
		"visibility": "public",
		"core":       false,
		"sources":    []string{"censorbib", "bibtex:" + e.Key},
		"date_added": "2026-05-04",
		"added_by":   "censorbib-import",
	}
	if venue != "" {
		paper["venue"] = venue
	}
	if u := cleanField(e.Fields["url"]); u != "" {
		paper["url"] = u
	}
	if d := cleanField(e.Fields["doi"]); d != "" {
		paper["doi"] = d
	}
	if a := cleanField(e.Fields["abstract"]); a != "" {
		paper["abstract"] = a
	}
	if len(defenses) > 0 {
		paper["defenses_discussed"] = defenses
	}
	return paper, true
}

// cleanField strips BibTeX-isms: nested braces around acronyms, LaTeX
// escape sequences for accents, and excess whitespace. Best-effort only.
var (
	bracesRE   = regexp.MustCompile(`[{}]`)
	multiWsRE  = regexp.MustCompile(`\s+`)
	latexAccRE = regexp.MustCompile(`\\['"^` + "`" + `~]\{?(.)\}?`)
	latexCmdRE = regexp.MustCompile(`\\[a-zA-Z]+\{([^}]*)\}`)
)

func cleanField(s string) string {
	if s == "" {
		return ""
	}
	s = strings.TrimSpace(s)
	s = latexAccRE.ReplaceAllString(s, "$1")
	s = latexCmdRE.ReplaceAllString(s, "$1")
	s = bracesRE.ReplaceAllString(s, "")
	s = multiWsRE.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

func splitAuthors(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, " and ")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		// "Last, First" → "First Last"
		if idx := strings.Index(p, ","); idx > 0 {
			last := strings.TrimSpace(p[:idx])
			first := strings.TrimSpace(p[idx+1:])
			if first != "" && last != "" {
				p = first + " " + last
			}
		}
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// makeID produces a stable id of the form YYYY-firstauthorlastname-firstwordoftitle.
// Lowercased, slugified.
func makeID(firstAuthor string, year int, title, _bibKey string) string {
	last := lastNameOf(firstAuthor)
	first := firstSignificantWordOf(title)
	return fmt.Sprintf("%d-%s-%s", year, slug(last), slug(first))
}

func lastNameOf(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "anon"
	}
	parts := strings.Fields(name)
	return parts[len(parts)-1]
}

var stopWords = map[string]bool{
	"a": true, "an": true, "the": true, "and": true, "or": true, "of": true,
	"on": true, "in": true, "for": true, "to": true, "with": true, "from": true,
	"is": true, "are": true, "by": true, "via": true, "at": true, "as": true,
	"how": true, "what": true, "why": true, "do": true, "does": true,
}

func firstSignificantWordOf(title string) string {
	for _, w := range strings.Fields(title) {
		w = strings.ToLower(strings.Trim(w, ".,;:?!()[]{}\"'"))
		if w == "" || stopWords[w] {
			continue
		}
		return w
	}
	return "paper"
}

var slugRE = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	s = strings.ToLower(s)
	s = slugRE.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "x"
	}
	return s
}

func normalizeTitle(t string) string {
	t = strings.ToLower(t)
	t = slugRE.ReplaceAllString(t, " ")
	t = multiWsRE.ReplaceAllString(t, " ")
	return strings.TrimSpace(t)
}

// Heuristic tagging — conservative on purpose. The corpus integrity test
// catches taxonomy IDs that don't exist; everything emitted here must
// resolve. Matches are case-insensitive; titles are messy but fine for
// keyword spotting.
func contains(haystack, needle string) bool {
	return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}

func guessCensors(title string) []string {
	out := []string{}
	add := func(s string) {
		for _, x := range out {
			if x == s {
				return
			}
		}
		out = append(out, s)
	}
	if contains(title, "China") || contains(title, "GFW") || contains(title, "Great Firewall") || contains(title, "Chinese") {
		add("cn")
	}
	if contains(title, "Iran") {
		add("ir")
	}
	if contains(title, "Russia") || contains(title, "Roskomnadzor") || contains(title, "TSPU") {
		add("ru")
	}
	if contains(title, "Belarus") {
		add("by")
	}
	if contains(title, "Cuba") {
		add("cu")
	}
	if contains(title, "Venezuela") {
		add("ve")
	}
	if contains(title, "Turkmenistan") {
		add("tm")
	}
	if contains(title, "Korea") {
		add("kp")
	}
	if contains(title, "Kazakhstan") {
		add("kz")
	}
	if contains(title, "Uzbekistan") {
		add("uz")
	}
	if contains(title, "UAE") || contains(title, "Emirates") {
		add("ae")
	}
	if contains(title, "Saudi") {
		add("sa")
	}
	if len(out) == 0 {
		out = append(out, "generic")
	}
	return out
}

func guessTechniques(title string) []string {
	out := []string{}
	add := func(s string) {
		for _, x := range out {
			if x == s {
				return
			}
		}
		out = append(out, s)
	}
	if contains(title, "DNS") {
		add("dns-poisoning")
	}
	if contains(title, "SNI") {
		add("sni-blocking")
	}
	if contains(title, "ESNI") || contains(title, "ECH") {
		add("esni-eh-blocking")
	}
	if contains(title, "active prob") {
		add("active-probing")
	}
	if contains(title, "fingerprint") {
		add("tls-fingerprint")
	}
	if contains(title, "QUIC") || contains(title, "HTTP/3") {
		add("http3-quic-block")
	}
	if contains(title, "RST") || contains(title, "reset") {
		add("rst-injection")
	}
	if contains(title, "machine learning") || contains(title, "classifier") || contains(title, "ML ") {
		add("ml-classifier")
	}
	if contains(title, "fully encrypted") || contains(title, "entropy") {
		add("fully-encrypted-detect")
	}
	if contains(title, "flow correlation") || contains(title, "traffic analysis") || contains(title, "website fingerprint") {
		add("flow-correlation")
	}
	if len(out) == 0 {
		out = append(out, "dpi")
	}
	return out
}

func guessDefenses(title string) []string {
	out := []string{}
	add := func(s string) {
		for _, x := range out {
			if x == s {
				return
			}
		}
		out = append(out, s)
	}
	if contains(title, "domain front") || contains(title, "fronting") {
		add("domain-fronting")
	}
	if contains(title, "Snowflake") || contains(title, "WebRTC") {
		add("webrtc-pluggable")
	}
	if contains(title, "decoy") || contains(title, "refraction") {
		add("decoy-routing")
	}
	if contains(title, "DNS tunnel") || contains(title, "DNSTT") || contains(title, "iodine") {
		add("dns-tunneling")
	}
	if contains(title, "AMP") {
		add("amp-cache")
	}
	if contains(title, "meek") {
		add("meek")
	}
	if contains(title, "obfs4") || contains(title, "obfs3") || contains(title, "obfsproxy") {
		add("obfs4")
	}
	if contains(title, "Shadowsocks") {
		add("shadowsocks")
	}
	if contains(title, "VLESS") {
		add("vless")
	}
	if contains(title, "VMess") {
		add("vmess")
	}
	if contains(title, "Trojan") {
		add("trojan")
	}
	if contains(title, "Hysteria") {
		add("hysteria2")
	}
	if contains(title, "REALITY") {
		add("reality")
	}
	if contains(title, "AnyTLS") {
		add("anytls")
	}
	if contains(title, "AmneziaWG") || contains(title, "Amnezia") {
		add("amnezia-wg")
	}
	if contains(title, "Geneva") {
		add("geneva")
	}
	if contains(title, "WATER") {
		add("water-wasm")
	}
	if contains(title, "ECH") || contains(title, "ESNI") {
		add("ech-esni")
	}
	return out
}

// writeYAML emits a paper YAML in the same shape the hand-curated entries
// use. We hand-format the file rather than use yaml.Marshal directly so
// the field order matches the existing convention (id / title / authors /
// venue / year / ... / visibility / metadata / sources at end).
func writeYAML(path string, p map[string]any) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	defer w.Flush()

	keys := []string{
		"id", "title", "authors", "venue", "year",
		"doi", "arxiv_id", "url", "abstract",
		"censors", "techniques",
		"defenses_discussed", "defenses_evaluated_against", "evaluation_methods",
		"core", "visibility",
		"date_added", "added_by",
		"sources", "references",
	}
	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	ordered := map[string]any{}
	// yaml.Marshal sorts map keys alphabetically by default. To preserve
	// our preferred field order, build an ordered yaml.Node tree.
	root := &yaml.Node{Kind: yaml.MappingNode}
	for _, k := range keys {
		v, ok := p[k]
		if !ok {
			continue
		}
		keyNode := &yaml.Node{Kind: yaml.ScalarNode, Value: k}
		valueNode := &yaml.Node{}
		if err := valueNode.Encode(v); err != nil {
			return err
		}
		// Force string scalars to use literal style when they contain newlines
		// (the abstract field). Otherwise YAML dumps them as quoted single
		// lines which are unreadable.
		if s, ok := v.(string); ok && strings.Contains(s, "\n") {
			valueNode.Style = yaml.LiteralStyle
		}
		root.Content = append(root.Content, keyNode, valueNode)
	}
	_ = ordered
	return enc.Encode(root)
}
