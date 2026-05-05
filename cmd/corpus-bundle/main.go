// corpus-bundle reads every paper + finding YAML and the taxonomy and
// emits a single JSON blob that the Cloudflare Worker (functions/mcp/)
// imports at build time. Bundling happens in CI before `wrangler pages
// deploy`, so the Worker ships with the corpus baked in — no R2 reads,
// no fetch-at-runtime, no cold-start penalty.
//
// Output goes to functions/_data/corpus.json by default. The path is
// underscore-prefixed so Cloudflare Pages doesn't expose it as a
// static asset (only Functions can import it).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type paper struct {
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
	References               []string `yaml:"references,omitempty" json:"references,omitempty"`
}

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
	HumanValidatedBy    string   `yaml:"human_validated_by,omitempty" json:"human_validated_by,omitempty"`
}

type bundle struct {
	Generated string         `json:"generated"`
	Papers    []paper        `json:"papers"`
	Findings  []finding      `json:"findings"`
	Taxonomy  map[string]any `json:"taxonomy"`
}

func main() {
	root := flag.String("corpus", ".", "corpus repo root")
	out := flag.String("out", "functions/_data/corpus.json", "output JSON path (relative to corpus root)")
	publicOnly := flag.Bool("public-only", true, "only include visibility=public papers")
	flag.Parse()

	b := bundle{Taxonomy: map[string]any{}}

	// Papers.
	papersDir := filepath.Join(*root, "corpus", "papers")
	entries, err := os.ReadDir(papersDir)
	if err != nil {
		log.Fatalf("read papers dir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		path := filepath.Join(papersDir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			log.Fatalf("read %s: %v", path, err)
		}
		var p paper
		if err := yaml.Unmarshal(raw, &p); err != nil {
			log.Fatalf("parse %s: %v", path, err)
		}
		if *publicOnly && p.Visibility != "public" {
			continue
		}
		b.Papers = append(b.Papers, p)
	}
	sort.SliceStable(b.Papers, func(i, j int) bool { return b.Papers[i].ID < b.Papers[j].ID })

	// Findings (optional — directory may not exist on every commit).
	findingsDir := filepath.Join(*root, "corpus", "findings")
	if fEntries, err := os.ReadDir(findingsDir); err == nil {
		for _, e := range fEntries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
				continue
			}
			path := filepath.Join(findingsDir, e.Name())
			raw, err := os.ReadFile(path)
			if err != nil {
				log.Fatalf("read %s: %v", path, err)
			}
			var f finding
			if err := yaml.Unmarshal(raw, &f); err != nil {
				log.Fatalf("parse %s: %v", path, err)
			}
			b.Findings = append(b.Findings, f)
		}
		sort.SliceStable(b.Findings, func(i, j int) bool {
			if b.Findings[i].Paper != b.Findings[j].Paper {
				return b.Findings[i].Paper < b.Findings[j].Paper
			}
			return b.Findings[i].Summary < b.Findings[j].Summary
		})
	}

	// Taxonomy.
	taxRaw, err := os.ReadFile(filepath.Join(*root, "schema", "taxonomy.yaml"))
	if err != nil {
		log.Fatalf("read taxonomy: %v", err)
	}
	if err := yaml.Unmarshal(taxRaw, &b.Taxonomy); err != nil {
		log.Fatalf("parse taxonomy: %v", err)
	}

	b.Generated = "build"

	outPath := filepath.Join(*root, *out)
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		log.Fatalf("mkdir: %v", err)
	}
	js, err := json.Marshal(b)
	if err != nil {
		log.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(outPath, js, 0o644); err != nil {
		log.Fatalf("write: %v", err)
	}
	fmt.Printf("wrote %s — %d papers, %d findings, %d bytes\n",
		outPath, len(b.Papers), len(b.Findings), len(js))
}
