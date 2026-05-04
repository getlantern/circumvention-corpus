// retag-papers expands the heuristic tags on already-imported papers,
// using a much richer keyword→tag map than the initial import-censorbib
// pass had. Designed to be safely re-runnable: it never overwrites
// hand-curated entries, never narrows tags, and treats the existing
// per-paper tags as a baseline to enrich.
//
// The expected loop is: edit the keyword maps in this file, run, eyeball
// the diff, commit. The corpus integrity test catches typos against the
// taxonomy.
//
// Usage:
//
//	go run ./cmd/retag-papers --corpus . [--dry-run]
package main

import (
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

type paper struct {
	ID                       string   `yaml:"id"`
	Title                    string   `yaml:"title"`
	Notes                    string   `yaml:"notes,omitempty"`
	Core                     bool     `yaml:"core,omitempty"`
	Censors                  []string `yaml:"censors"`
	Techniques               []string `yaml:"techniques"`
	DefensesDiscussed        []string `yaml:"defenses_discussed,omitempty"`
	DefensesEvaluatedAgainst []string `yaml:"defenses_evaluated_against,omitempty"`
	Sources                  []string `yaml:"sources,omitempty"`
}

func main() {
	corpusDir := flag.String("corpus", ".", "Path to the circumvention-corpus repo root.")
	dryRun := flag.Bool("dry-run", false, "If true, print proposed changes without writing.")
	flag.Parse()

	dir := filepath.Join(*corpusDir, "corpus", "papers")
	entries, err := os.ReadDir(dir)
	if err != nil {
		log.Fatal(err)
	}

	var (
		updated, skippedHand, skippedNoChange int
		censorAdds                             = map[string]int{}
		techniqueAdds                          = map[string]int{}
		defenseAdds                            = map[string]int{}
	)

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			log.Fatal(err)
		}
		var p paper
		if err := yaml.Unmarshal(raw, &p); err != nil {
			log.Fatalf("%s: %v", path, err)
		}
		// Skip hand-curated papers — anything with non-empty notes or
		// core: true is presumed to have human-vetted tags. We don't want
		// the heuristic to clobber them, even by addition.
		if p.Notes != "" || p.Core || !slices.Contains(p.Sources, "censorbib") {
			skippedHand++
			continue
		}

		text := strings.ToLower(p.Title)
		newCensors := mergeAndDropGeneric(p.Censors, guessCensors(text), "generic")
		newTech := mergeAndDropGeneric(p.Techniques, guessTechniques(text), "dpi")
		newDef := mergeNoGeneric(p.DefensesDiscussed, guessDefenses(text))

		censorChanged := !sameSet(p.Censors, newCensors)
		techChanged := !sameSet(p.Techniques, newTech)
		defChanged := !sameSet(p.DefensesDiscussed, newDef)
		if !censorChanged && !techChanged && !defChanged {
			skippedNoChange++
			continue
		}

		// Track tag-adoption stats for the run summary.
		for _, c := range newCensors {
			if !slices.Contains(p.Censors, c) {
				censorAdds[c]++
			}
		}
		for _, t := range newTech {
			if !slices.Contains(p.Techniques, t) {
				techniqueAdds[t]++
			}
		}
		for _, d := range newDef {
			if !slices.Contains(p.DefensesDiscussed, d) {
				defenseAdds[d]++
			}
		}

		if *dryRun {
			fmt.Printf("[dry-run] %s\n  censors:    %v -> %v\n  techniques: %v -> %v\n",
				p.ID, p.Censors, newCensors, p.Techniques, newTech)
			if defChanged {
				fmt.Printf("  defenses:   %v -> %v\n", p.DefensesDiscussed, newDef)
			}
			updated++
			continue
		}
		if err := patchYAML(path, newCensors, newTech, newDef); err != nil {
			log.Fatalf("patch %s: %v", path, err)
		}
		updated++
	}

	log.Printf("updated %d, skipped %d hand-curated, skipped %d no-change",
		updated, skippedHand, skippedNoChange)
	if len(censorAdds) > 0 {
		log.Printf("censor tag adoptions:    %v", sortedMap(censorAdds))
	}
	if len(techniqueAdds) > 0 {
		log.Printf("technique tag adoptions: %v", sortedMap(techniqueAdds))
	}
	if len(defenseAdds) > 0 {
		log.Printf("defense tag adoptions:   %v", sortedMap(defenseAdds))
	}
}

// patchYAML edits a paper YAML in place by rewriting just the three tag
// fields. Goes through yaml.Node so existing field order, comments, and
// formatting are preserved.
func patchYAML(path string, censors, techniques, defenses []string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return err
	}
	if len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("not a mapping at root")
	}
	doc := root.Content[0]

	setSeq(doc, "censors", censors)
	setSeq(doc, "techniques", techniques)
	if len(defenses) > 0 {
		setSeq(doc, "defenses_discussed", defenses)
	}

	out, err := yaml.Marshal(&root)
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}

// setSeq replaces or inserts a flow-style sequence value for the given
// key on a mapping node. Insertion happens at end-of-mapping; for already-
// present fields we replace the value in place to preserve key order.
func setSeq(doc *yaml.Node, key string, values []string) {
	for i := 0; i < len(doc.Content); i += 2 {
		if doc.Content[i].Value == key {
			doc.Content[i+1] = seqNode(values)
			return
		}
	}
	doc.Content = append(doc.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, seqNode(values))
}

func seqNode(values []string) *yaml.Node {
	n := &yaml.Node{Kind: yaml.SequenceNode, Style: yaml.FlowStyle}
	for _, v := range values {
		n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: v})
	}
	return n
}

func mergeAndDropGeneric(existing, additions []string, generic string) []string {
	out := []string{}
	for _, x := range existing {
		out = append(out, x)
	}
	for _, a := range additions {
		if !slices.Contains(out, a) {
			out = append(out, a)
		}
	}
	// If we now have something more specific than the generic placeholder,
	// drop the generic.
	if len(out) > 1 && slices.Contains(out, generic) {
		filtered := make([]string, 0, len(out)-1)
		for _, x := range out {
			if x != generic {
				filtered = append(filtered, x)
			}
		}
		out = filtered
	}
	return out
}

func mergeNoGeneric(existing, additions []string) []string {
	out := []string{}
	for _, x := range existing {
		out = append(out, x)
	}
	for _, a := range additions {
		if !slices.Contains(out, a) {
			out = append(out, a)
		}
	}
	return out
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for _, x := range a {
		if !slices.Contains(b, x) {
			return false
		}
	}
	return true
}

func sortedMap(m map[string]int) []string {
	type kv struct {
		k string
		v int
	}
	pairs := make([]kv, 0, len(m))
	for k, v := range m {
		pairs = append(pairs, kv{k, v})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].v != pairs[j].v {
			return pairs[i].v > pairs[j].v
		}
		return pairs[i].k < pairs[j].k
	})
	out := make([]string, len(pairs))
	for i, p := range pairs {
		out[i] = fmt.Sprintf("%s=%d", p.k, p.v)
	}
	return out
}

// hit returns true if any of the keywords is a substring (case-insensitive)
// of the title — title is already lowered by the caller.
func hit(title string, keywords ...string) bool {
	for _, k := range keywords {
		if strings.Contains(title, strings.ToLower(k)) {
			return true
		}
	}
	return false
}

func guessCensors(t string) []string {
	out := []string{}
	add := func(s string) {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	if hit(t, "china", "gfw", "great firewall", "chinese", "henan") {
		add("cn")
	}
	if hit(t, "iran", "iranian") {
		add("ir")
	}
	if hit(t, "russia", "russian", "roskomnadzor", "tspu", "rkn") {
		add("ru")
	}
	if hit(t, "belarus", "belarusian") {
		add("by")
	}
	if hit(t, "cuba") {
		add("cu")
	}
	if hit(t, "venezuela") {
		add("ve")
	}
	if hit(t, "turkmenistan") {
		add("tm")
	}
	if hit(t, "north korea", "dprk") {
		add("kp")
	}
	if hit(t, "kazakhstan") {
		add("kz")
	}
	if hit(t, "uzbekistan") {
		add("uz")
	}
	if hit(t, "uae", "emirates") {
		add("ae")
	}
	if hit(t, "saudi") {
		add("sa")
	}
	if hit(t, "pakistan") {
		add("pk")
	}
	if hit(t, "myanmar", "burma") {
		add("mm")
	}
	if len(out) == 0 {
		out = append(out, "generic")
	}
	return out
}

func guessTechniques(t string) []string {
	out := []string{}
	add := func(s string) {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	if hit(t, "dns ", "dns injection", "dns poisoning", "dns censorship", "dns filter", "domain name") {
		add("dns-poisoning")
	}
	if hit(t, " sni ", "sni-based", "server name indication") {
		add("sni-blocking")
	}
	if hit(t, "esni", "ech ", "encrypted client hello", "encrypted sni") {
		add("esni-eh-blocking")
	}
	if hit(t, "active prob") {
		add("active-probing")
	}
	if hit(t, "fingerprint") {
		add("tls-fingerprint")
	}
	if hit(t, "quic", "http/3", "http3") {
		add("http3-quic-block")
	}
	if hit(t, " rst ", "tcp reset", "rst injection") {
		add("rst-injection")
	}
	if hit(t, "machine learning", "classifier", "deep learning", "neural") {
		add("ml-classifier")
	}
	if hit(t, "fully encrypted", "high entropy", "randomness") {
		add("fully-encrypted-detect")
	}
	if hit(t, "flow correlation", "traffic analysis") {
		add("flow-correlation")
	}
	if hit(t, "website fingerprint", "site fingerprint") {
		add("website-fingerprint")
	}
	if hit(t, "throttl", "shaping", "rate limit") {
		add("throttling")
	}
	if hit(t, "ip-block", "ip block", "blocklist", "blacklist", "ip filter") {
		add("ip-blocking")
	}
	if hit(t, "port block", "port filter") {
		add("port-blocking")
	}
	if hit(t, "bgp", "route hijack", "as-level", "as level") {
		add("bgp-hijack")
	}
	if hit(t, "ooni", "iclab", "encore", "iris", "quack", "censored planet", "satellite", "augur") {
		add("measurement-platform")
	}
	if hit(t, "middlebox") {
		add("middlebox-interference")
	}
	if hit(t, "packet injection", "blockpage", "injected") {
		add("packet-injection")
	}
	// Any paper about "censorship" or "filtering" generally is at least
	// about DPI in the broad sense, so the [dpi] default stays useful.
	if len(out) == 0 {
		out = append(out, "dpi")
	}
	return out
}

func guessDefenses(t string) []string {
	out := []string{}
	add := func(s string) {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	// Domain fronting / Meek family
	if hit(t, "domain front", "fronting", "collateral freedom") {
		add("domain-fronting")
	}
	if hit(t, "meek") {
		add("meek")
	}
	// AMP
	if hit(t, " amp ", "amp cache", "champa") {
		add("amp-cache")
	}
	// WebRTC family
	if hit(t, "snowflake") {
		add("webrtc-pluggable")
	}
	if hit(t, "webrtc") {
		add("webrtc-pluggable")
	}
	// Decoy / refraction lineage
	if hit(t, "decoy", "refraction") {
		add("decoy-routing")
	}
	if hit(t, "telex") {
		add("telex")
		add("decoy-routing")
	}
	if hit(t, "tapdance") {
		add("tapdance")
		add("decoy-routing")
	}
	if hit(t, "conjure") {
		add("conjure")
		add("decoy-routing")
	}
	if hit(t, "slitheen", "rebound") {
		add("decoy-routing")
	}
	// DNS tunneling
	if hit(t, "dns tunnel", "dnstt", "iodine") {
		add("dns-tunneling")
	}
	// obfsproxy lineage
	if hit(t, "obfs4", "obfs3", "obfs2", "obfsproxy") {
		add("obfs4")
	}
	if hit(t, "scramblesuit") {
		add("scramblesuit")
	}
	// Format-transform / programmable
	if hit(t, "marionette", "format-transform", "format transform", "fte") {
		add("format-transform")
		add("marionette")
	}
	if hit(t, "proteus", "turbo tunnel", "programmable protocol") {
		add("meta-resistance")
	}
	if hit(t, "cloak") {
		add("cloak")
	}
	// V2Ray / common modern protocols
	if hit(t, "shadowsocks") {
		add("shadowsocks")
	}
	if hit(t, "vless") {
		add("vless")
	}
	if hit(t, "vmess") {
		add("vmess")
	}
	if hit(t, "trojan") {
		add("trojan")
	}
	if hit(t, "hysteria") {
		add("hysteria2")
	}
	if hit(t, "reality") {
		add("reality")
	}
	if hit(t, "anytls") {
		add("anytls")
	}
	if hit(t, "amnezia") {
		add("amnezia-wg")
	}
	if hit(t, "geneva") {
		add("geneva")
	}
	if hit(t, "water-wasm", " water ", "wasm proxy") {
		add("water-wasm")
	}
	if hit(t, "ech", "esni") {
		add("ech-esni")
	}
	if hit(t, "stegano") {
		add("steganography")
	}
	// Tor / bridges
	if hit(t, " tor ", "tor:", "onion") {
		add("tor")
	}
	if hit(t, "bridge", "private relay") {
		add("bridges")
	}
	if hit(t, "pluggable transport") {
		add("pluggable-transport")
	}
	return out
}
