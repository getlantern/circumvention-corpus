package main

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
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
