package main

import (
	"testing"

	"opencode-go-models/internal/db"
)

func mk(id string) db.Model { return db.Model{ID: id} }

func TestFamilyKey(t *testing.T) {
	cases := map[string]string{
		"gpt-5.6-luna": "gpt", "gpt-6-luna": "gpt",
		"grok-4.6": "grok", "grok-4.7": "grok",
		"qwen3.7-max": "qwen3", "qwen3.8-flash": "qwen3",
		"kimi-k2.6": "kimi-k2", "kimi-k2.7-code": "kimi-k2", "kimi-k3": "kimi-k3",
		"mimo-v2.5": "mimo", "mimo-v2.6-pro": "mimo",
		"hy3": "hy3", "hy4-preview": "hy4",
		"beam-free": "beam", "omen-alpha": "omen",
		"muse-spark-1.3-contributor": "muse-spark",
	}
	for id, want := range cases {
		if got := familyKey(id); got != want {
			t.Errorf("familyKey(%q) = %q, want %q", id, got, want)
		}
	}
}

func TestLatestKeepsNewestPerFamily(t *testing.T) {
	in := []db.Model{mk("gpt-5.6-luna"), mk("gpt-6-luna"), mk("grok-4.6"), mk("grok-4.7")}
	got := latestModels(in)
	ids := map[string]bool{}
	for _, m := range got {
		ids[m.ID] = true
	}
	if len(got) != 2 || !ids["gpt-6-luna"] || !ids["grok-4.7"] {
		t.Errorf("latest = %v, want [gpt-6-luna grok-4.7]", ids)
	}
}

func TestStableBeatsPreview(t *testing.T) {
	in := []db.Model{mk("mimo-v2.6-flash"), mk("mimo-v2.6-pro")}
	got := latestModels(in)
	if len(got) != 1 || got[0].ID != "mimo-v2.6-pro" {
		t.Errorf("latest = %v, want [mimo-v2.6-pro]", got)
	}
}

func TestFreeFilter(t *testing.T) {
	zero := 0.0
	paid := db.Model{ID: "gpt-6-luna", PriceInput: &zero}
	paid.PriceInput = ptr(0.1)
	free := db.Model{ID: "beam-free", PriceInput: &zero, PriceOutput: &zero}
	in := []db.Model{paid, free}
	if got := applyFilters(in, false, false, "", false); len(got) != 1 || got[0].ID != "gpt-6-luna" {
		t.Errorf("default filter = %v, want paid only", got)
	}
	if got := applyFilters(in, false, false, "", true); len(got) != 1 || got[0].ID != "beam-free" {
		t.Errorf("--free = %v, want free only", got)
	}
}

func ptr(f float64) *float64 { return &f }
