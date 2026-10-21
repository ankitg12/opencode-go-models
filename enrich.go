package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// ompEnrichment shells out to `omp models --json` when available and returns
// per-id context/limits/pricing/vision for the opencode-go provider rows.
// Absent/broken omp (non-OMP machines, CI) yields an empty map — the caller
// falls back to cached values, never to an error.
func ompEnrichment(ctx context.Context) map[string]enrich {
	out := map[string]enrich{}
	omp, err := exec.LookPath("omp")
	if err != nil {
		return out
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, omp, "models", "--json")
	raw, err := cmd.Output()
	if err != nil {
		return out
	}
	var payload struct {
		Models []struct {
			Provider      string   `json:"provider"`
			ID            string   `json:"id"`
			Name          string   `json:"name"`
			ContextWindow *int64   `json:"contextWindow"`
			MaxTokens     *int64   `json:"maxTokens"`
			Input         []string `json:"input"`
			Cost          struct {
				Input      *float64 `json:"input"`
				Output     *float64 `json:"output"`
				CacheRead  *float64 `json:"cacheRead"`
				CacheWrite *float64 `json:"cacheWrite"`
			} `json:"cost"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return out
	}
	for _, m := range payload.Models {
		if m.Provider != "opencode-go" || m.ID == "" {
			continue
		}
		e := enrich{Name: m.Name}
		if m.ContextWindow != nil {
			e.Ctx = *m.ContextWindow
		}
		if m.MaxTokens != nil {
			e.Out = *m.MaxTokens
		}
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
		for _, in := range m.Input {
			if in == "image" {
				e.Vision = true
			}
		}
		out[m.ID] = e
	}
	return out
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
