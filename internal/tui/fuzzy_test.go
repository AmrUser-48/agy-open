package tui

import "testing"

func TestFuzzyMatchScoreRanksPrefixesAndUsefulTypoMatches(t *testing.T) {
	values := []string{"/config", "/context", "/conversation", "/copy", "/clear"}
	ranked := fuzzyRank(values, "/com")
	if len(ranked) == 0 {
		t.Fatal("fuzzy completion returned no candidates")
	}
	if got := values[ranked[0]]; got != "/config" {
		t.Fatalf("best /com completion = %q, want /config", got)
	}
	if fuzzyMatchScore("NVIDIA API Catalog", "nvda") < 0 {
		t.Fatal("fuzzy subsequence should match NVIDIA")
	}
	if fuzzyMatchScore("Groq API", "grqo") < 0 {
		t.Fatal("one-edit typo should match Groq")
	}
	if fuzzyMatchScore("Groq API", "azure") >= 0 {
		t.Fatal("unrelated query should not match")
	}
}

func TestFuzzyRankPreservesOriginalOrderForEmptyQuery(t *testing.T) {
	values := []string{"alpha", "beta", "gamma"}
	got := fuzzyRank(values, "")
	if len(got) != len(values) {
		t.Fatalf("rank count = %d, want %d", len(got), len(values))
	}
	for i, index := range got {
		if index != i {
			t.Fatalf("ranked index %d = %d, want stable index %d", i, index, i)
		}
	}
}
