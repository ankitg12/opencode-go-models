package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
)

const modelsDevURL = "https://catalog.stencil.so/models.json.zstd"

// mdEnrichment fetches the models.dev snapshot and returns per-id
// context/limits/pricing/vision for the opencode-go provider rows — the same
// source other catalogs hydrate from, fetched directly here. Any fetch/decode failure yields
// an empty map; the caller falls back to cached values, never to an error.
func mdEnrichment(ctx context.Context) map[string]enrich {
	out := map[string]enrich{}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsDevURL, nil)
	if err != nil {
		return out
	}
	req.Header.Set("Accept", "application/zstd, application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return out
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK {
		return out
	}
	if dec, derr := zstd.NewReader(nil); derr == nil {
		defer dec.Close()
		if inflated, derr := dec.DecodeAll(raw, nil); derr == nil {
			raw = inflated
		} else if !isJSON(raw) {
			return out
		}
	} else if !isJSON(raw) {
		return out
	}
	var catalog map[string]struct {
		Models map[string]struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			Limit struct {
				Context int64 `json:"context"`
				Output  int64 `json:"output"`
			} `json:"limit"`
			Cost struct {
				Input      *float64 `json:"input"`
				Output     *float64 `json:"output"`
				CacheRead  *float64 `json:"cache_read"`
				CacheWrite *float64 `json:"cache_write"`
			} `json:"cost"`
			Modalities struct {
				Input []string `json:"input"`
			} `json:"modalities"`
		} `json:"models"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&catalog); err != nil {
		return out
	}
	prov, ok := catalog["opencode-go"]
	if !ok {
		return out
	}
	for id, m := range prov.Models {
		if id == "" {
			continue
		}
		e := enrich{Name: m.Name, Ctx: m.Limit.Context, Out: m.Limit.Output}
		if m.Cost.Input != nil {
			e.InPrice = *m.Cost.Input
		}
		if m.Cost.Output != nil {
			e.OutPrice = *m.Cost.Output
		}
		if m.Cost.CacheRead != nil {
			e.CacheRead = *m.Cost.CacheRead
		}
		if m.Cost.CacheWrite != nil {
			e.CacheWrite = *m.Cost.CacheWrite
		}
		for _, in := range m.Modalities.Input {
			if in == "image" {
				e.Vision = true
			}
		}
		out[id] = e
	}
	// Sibling borrow: opencode and opencode-go share one operator with identical
	// endpoint semantics, so the sibling's models.dev rows cover ids missing here.
	if sib, ok := catalog["opencode"]; ok {
		for id, m := range sib.Models {
			if _, have := out[id]; have || id == "" {
				continue
			}
			e := enrich{Name: m.Name, Ctx: m.Limit.Context, Out: m.Limit.Output}
			if m.Cost.Input != nil {
				e.InPrice = *m.Cost.Input
			}
			if m.Cost.Output != nil {
				e.OutPrice = *m.Cost.Output
			}
			if m.Cost.CacheRead != nil {
				e.CacheRead = *m.Cost.CacheRead
			}
			if m.Cost.CacheWrite != nil {
				e.CacheWrite = *m.Cost.CacheWrite
			}
			for _, in := range m.Modalities.Input {
				if in == "image" {
					e.Vision = true
				}
			}
			out[id] = e
		}
	}
	// handPatches covers live ids in neither models.dev provider. Values from
	// https://opencode.ai/docs/go/; models.dev or sibling rows win when present.
	handPatches := map[string]enrich{
		"qwen3.7-max": {Name: "Qwen3.7 Max", Ctx: 1_000_000, Out: 65_536, InPrice: 2.5, OutPrice: 7.5, CacheRead: 0.5, CacheWrite: 3.125},
	}
	for id, e := range handPatches {
		if _, have := out[id]; !have {
			out[id] = e
		}
	}
	return out
}

func isJSON(raw []byte) bool {
	for _, b := range raw {
		switch b {
		case ' ', '\t', '\n', '\r':
			continue
		case '{', '[':
			return true
		default:
			return false
		}
	}
	return false
}

// goUsageWindow is one rolling/weekly/monthly window from /v1/usage.
type goUsageWindow struct {
	Status   string `json:"status"`
	Percent  int    `json:"percent"`
	ResetsAt string `json:"resets_at"`
}

func showUsage(ctx context.Context, key string) error {
	if key == "" {
		return fmt.Errorf("usage needs a key: --key or $OPENCODE_GO_API_KEY")
	}
	ctx, cancel := context.WithTimeout(ctx, httpTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, usageEndpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("usage endpoint returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload struct {
		Usage map[string]goUsageWindow `json:"usage"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return fmt.Errorf("decode usage: %w", err)
	}
	order := []string{"rolling", "weekly", "monthly"}
	fmt.Printf("\nOpenCode Go usage\n\n")
	for _, k := range order {
		w, ok := payload.Usage[k]
		if !ok {
			continue
		}
		fmt.Printf("  %-8s %3d%%  resets %s  (%s)\n", k, w.Percent, w.ResetsAt, w.Status)
	}
	for k, w := range payload.Usage {
		known := false
		for _, o := range order {
			if k == o {
				known = true
			}
		}
		if !known {
			fmt.Printf("  %-8s %3d%%  resets %s  (%s)\n", k, w.Percent, w.ResetsAt, w.Status)
		}
	}
	fmt.Println()
	return nil
}
