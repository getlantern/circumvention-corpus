package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestCorpusIntegrity validates that every paper YAML has a well-formed
// schema and only references taxonomy IDs that actually exist. Run on
// every PR to catch typos in the controlled vocabulary before merge.
func TestCorpusIntegrity(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	s, err := loadStore(root, false)
	if err != nil {
		t.Fatalf("loadStore: %v", err)
	}
	if len(s.papers) == 0 {
		t.Fatal("no papers loaded")
	}

	censorIDs := taxonomyIDs(t, s, "censors")
	techniqueIDs := taxonomyIDs(t, s, "techniques")
	defenseIDs := taxonomyIDs(t, s, "defenses")
	evalIDs := taxonomyIDs(t, s, "evaluation_methods")

	for _, p := range s.papers {
		t.Run(p.ID, func(t *testing.T) {
			if p.ID == "" {
				t.Errorf("missing id")
			}
			if p.Title == "" {
				t.Errorf("missing title")
			}
			if p.Year == 0 {
				t.Errorf("missing year")
			}
			if p.Visibility == "" {
				t.Errorf("missing visibility")
			} else if !slices.Contains([]string{"public", "community", "internal", "embargoed"}, p.Visibility) {
				t.Errorf("invalid visibility: %q", p.Visibility)
			}
			if p.Visibility == "embargoed" && p.EmbargoUntil == "" {
				t.Errorf("visibility=embargoed requires embargo_until")
			}
			if len(p.Censors) == 0 {
				t.Errorf("missing censors")
			}
			for _, c := range p.Censors {
				if !slices.Contains(censorIDs, c) {
					t.Errorf("unknown censor id %q (not in taxonomy)", c)
				}
			}
			if len(p.Techniques) == 0 {
				t.Errorf("missing techniques")
			}
			for _, x := range p.Techniques {
				if !slices.Contains(techniqueIDs, x) {
					t.Errorf("unknown technique id %q (not in taxonomy)", x)
				}
			}
			for _, d := range p.DefensesDiscussed {
				if !slices.Contains(defenseIDs, d) {
					t.Errorf("unknown defense id %q (not in taxonomy)", d)
				}
			}
			for _, d := range p.DefensesEvaluatedAgainst {
				if !slices.Contains(defenseIDs, d) {
					t.Errorf("unknown defense id (evaluated_against) %q", d)
				}
				if !slices.Contains(p.DefensesDiscussed, d) {
					t.Errorf("defense %q in defenses_evaluated_against but not in defenses_discussed", d)
				}
			}
			for _, e := range p.EvaluationMethods {
				if !slices.Contains(evalIDs, e) {
					t.Errorf("unknown evaluation_method id %q", e)
				}
			}
			for _, ref := range p.References {
				if _, ok := s.byID[ref]; !ok {
					t.Errorf("references unknown paper id: %q", ref)
				}
			}
			// Filename should match the id (so the directory listing
			// visually agrees with the canonical id).
			if !strings.Contains(p.ID, "-") {
				t.Errorf("id should follow YYYY-author-slug convention")
			}
		})
	}
}

// TestFindingsIntegrity validates each YAML in corpus/findings/ parses
// cleanly, references a known paper, and only uses taxonomy IDs that
// exist. Findings are extracted-claim records; broken references make
// them silently invisible to future tooling, so we catch them in CI.
func TestFindingsIntegrity(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	s, err := loadStore(root, false)
	if err != nil {
		t.Fatalf("loadStore: %v", err)
	}

	censorIDs := taxonomyIDs(t, s, "censors")
	techniqueIDs := taxonomyIDs(t, s, "techniques")
	defenseIDs := taxonomyIDs(t, s, "defenses")
	validKinds := []string{"detection", "defense", "evaluation", "deployment", "policy"}

	findingsDir := filepath.Join(root, "corpus", "findings")
	entries, err := os.ReadDir(findingsDir)
	if err != nil {
		if os.IsNotExist(err) {
			t.Skip("no findings directory yet")
		}
		t.Fatal(err)
	}

	type finding struct {
		Paper                string   `yaml:"paper"`
		Kind                 string   `yaml:"kind"`
		Summary              string   `yaml:"summary"`
		Techniques           []string `yaml:"techniques"`
		Defenses             []string `yaml:"defenses"`
		Censors              []string `yaml:"censors"`
		DefenseImplications  []string `yaml:"defense_implications"`
	}

	count := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		count++
		path := filepath.Join(findingsDir, e.Name())
		t.Run(e.Name(), func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			var f finding
			if err := yaml.Unmarshal(raw, &f); err != nil {
				t.Fatalf("parse: %v", err)
			}
			if f.Paper == "" {
				t.Errorf("missing paper id")
			} else if _, ok := s.byID[f.Paper]; !ok {
				t.Errorf("references unknown paper id %q", f.Paper)
			}
			if f.Kind == "" {
				t.Errorf("missing kind")
			} else if !slices.Contains(validKinds, f.Kind) {
				t.Errorf("invalid kind %q (expected one of %v)", f.Kind, validKinds)
			}
			if strings.TrimSpace(f.Summary) == "" {
				t.Errorf("missing summary")
			}
			for _, c := range f.Censors {
				if !slices.Contains(censorIDs, c) {
					t.Errorf("unknown censor id %q", c)
				}
			}
			for _, x := range f.Techniques {
				if !slices.Contains(techniqueIDs, x) {
					t.Errorf("unknown technique id %q", x)
				}
			}
			for _, d := range f.Defenses {
				if !slices.Contains(defenseIDs, d) {
					t.Errorf("unknown defense id %q", d)
				}
			}
		})
	}
	if count == 0 {
		t.Skip("no finding YAMLs found")
	}
}

// TestPapersHaveFindings flags papers without any extracted findings.
// It does not fail CI — some papers are legitimately unfetchable
// (paywalled PDFs, dead links from old censorbib imports) and the
// extractor will never produce findings for them. The point is
// visibility: every PR check surfaces the current backlog so it doesn't
// silently grow. Run `corpus-findings extract-all --min-year=0` (or fix
// the URL) to drain it.
func TestPapersHaveFindings(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	s, err := loadStore(root, false)
	if err != nil {
		t.Fatalf("loadStore: %v", err)
	}

	findingsDir := filepath.Join(root, "corpus", "findings")
	entries, err := os.ReadDir(findingsDir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	has := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		id := strings.SplitN(strings.TrimSuffix(e.Name(), ".yaml"), "__", 2)[0]
		has[id] = true
	}

	var missing []string
	for _, p := range s.papers {
		if !has[p.ID] {
			missing = append(missing, p.ID)
		}
	}
	slices.Sort(missing)

	if len(missing) == 0 {
		return
	}
	t.Logf("%d/%d papers have no findings (advisory — not failing CI):", len(missing), len(s.papers))
	for _, id := range missing {
		t.Logf("  - %s", id)
	}
	t.Logf("To extract: ./corpus-findings extract --paper <id> --corpus .")
	t.Logf("To bulk-backfill: ./corpus-findings extract-all --min-year=0 --corpus .")
}

func taxonomyIDs(t *testing.T, s *store, category string) []string {
	t.Helper()
	section, ok := s.taxonomy[category].(map[string]any)
	if !ok {
		t.Fatalf("taxonomy missing or wrong shape: %s", category)
	}
	ids := make([]string, 0, len(section))
	for k := range section {
		ids = append(ids, k)
	}
	return ids
}
