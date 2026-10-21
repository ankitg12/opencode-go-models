package main

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"opencode-go-models/internal/db"
)

// applyFilters: default view hides zero-price rows (free/preview). --all
// restores them, --free shows only them.
func applyFilters(models []db.Model, showAll, oneM bool, family string, freeOnly bool) []db.Model {
	out := make([]db.Model, 0, len(models))
	for _, m := range models {
		free := (m.PriceInput == nil || *m.PriceInput == 0) && (m.PriceOutput == nil || *m.PriceOutput == 0)
		if freeOnly && !free {
			continue
		}
		if !showAll && !freeOnly && free {
			continue
		}
		if oneM && (m.ContextLength == nil || *m.ContextLength < 1_000_000) {
			continue
		}
		if family != "" && !strings.HasPrefix(strings.ToLower(m.ID), strings.ToLower(family)) {
			continue
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// latestModels keeps the newest generation per family. Generation = trailing
// numeric token (gpt-5.6-luna vs gpt-6-luna, kimi-k2.6 vs k2.7-code vs k3,
// qwen3.6/3.7/3.8, mimo-v2.5/v2.6, glm-5.1/5.2/5.3, minimax-m2.5/m2.7/m3,
// deepseek-v4/v4.1, hy3/hy4-preview). Non-versioned ids (beam-free, omen-alpha,
// step-5-preview) are kept as their own family of one.
func latestModels(models []db.Model) []db.Model {
	best := map[string]db.Model{}
	for _, m := range models {
		fam := familyKey(m.ID)
		cur, ok := best[fam]
		if !ok || compareVersion(m.ID, cur.ID) > 0 {
			// Preview rows lose to same-family stable ids: hy4-preview is the
			// future, not the latest shippable.
			if ok && strings.HasSuffix(strings.ToLower(m.ID), "-preview") && !strings.HasSuffix(strings.ToLower(cur.ID), "-preview") {
				continue
			}
			best[fam] = m
		}
	}
	out := make([]db.Model, 0, len(best))
	for _, m := range best {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// familyKey groups ids by vendor/family so --latest keeps one winner per
// family: claude-*, deepseek-*, gemini-*, glm-*, gpt-*, grok-*, hy*, kimi-k*,
// longcat-*, mimo-*, minimax-*, muse-spark-*, qwen*, step-*. The one exception
// is kimi: k2 and k3 are distinct lines, so they group separately.
func familyKey(id string) string {
	s := strings.ToLower(id)
	for _, suf := range []string{"-free", "-preview", "-contributor"} {
		s = strings.TrimSuffix(s, suf)
	}
	first := strings.SplitN(s, "-", 2)[0]
	// Dotted vendors (qwen3.7-max, qwen3.8-flash) are one family: qwen3.
	if i := strings.Index(first, "."); i > 0 {
		first = first[:i]
	}
	if strings.HasPrefix(first, "kimi") {
		segs := strings.Split(s, "-")
		if len(segs) > 1 {
			return "kimi-" + stripMajor(segs[1])
		}
	}
	if first == "muse" {
		return "muse-spark"
	}
	return first
}

// stripMajor reduces a versioned token to vendor+major (k2.7 -> k2).
func stripMajor(tok string) string {
	out := ""
	for i := 0; i < len(tok); i++ {
		if tok[i] == '.' {
			break
		}
		out += string(tok[i])
	}
	return out
}

func compareVersion(a, b string) int {
	na, nb := numRuns(a), numRuns(b)
	for i := 0; i < len(na) && i < len(nb); i++ {
		if na[i] != nb[i] {
			if na[i] < nb[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(na) < len(nb):
		return -1
	case len(na) > len(nb):
		return 1
	default:
		return strings.Compare(a, b)
	}
}

func isVersionToken(s string) bool {
	if s == "" {
		return false
	}
	if strings.HasPrefix(s, "v") && len(s) > 1 && isDigit(s[1]) {
		return true
	}
	return containsDigit(s) && !isWord(s)
}

func isWord(s string) bool {
	for _, r := range s {
		if (r < 'a' || r > 'z') && r != '.' {
			return false
		}
	}
	return true
}

func containsDigit(s string) bool {
	for _, r := range s {
		if r >= '0' && r <= '9' {
			return true
		}
	}
	return false
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

func numRuns(s string) []float64 {
	var runs []float64
	cur := ""
	flush := func() {
		if cur != "" {
			if f, err := strconv.ParseFloat(cur, 64); err == nil {
				runs = append(runs, f)
			}
			cur = ""
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= '0' && c <= '9') || c == '.' {
			cur += string(c)
		} else {
			flush()
		}
	}
	flush()
	return runs
}

func sortModels(models []db.Model, by string) {
	price := func(m db.Model, in bool) float64 {
		var p *float64
		if in {
			p = m.PriceInput
		} else {
			p = m.PriceOutput
		}
		if p == nil {
			return -1
		}
		return *p
	}
	sort.SliceStable(models, func(i, j int) bool {
		switch by {
		case "name":
			return deref(models[i].Name) < deref(models[j].Name)
		case "ctx":
			return deref(models[i].ContextLength) < deref(models[j].ContextLength)
		case "output":
			return deref(models[i].MaxOutputTokens) < deref(models[j].MaxOutputTokens)
		case "input-price":
			return price(models[i], true) < price(models[j], true)
		case "output-price":
			return price(models[i], false) < price(models[j], false)
		case "seen":
			return deref(models[i].FirstSeen) < deref(models[j].FirstSeen)
		default:
			return models[i].ID < models[j].ID
		}
	})
}

func renderView(w io.Writer, models []db.Model, total int, fetchedAt string, latest bool, sortBy string) {
	title := "paid models"
	if latest {
		title = "latest " + title
	}
	fmt.Fprintf(w, "\nOpenCode Go · %d %s · fetched %s\n\n", len(models), title, prettyTime(fetchedAt))
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "  MODEL\tNAME\tCTX\tOUT\tIN/OUT $\tCACHE $\tVISION\tFIRST SEEN")
	for _, m := range models {
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			m.ID, deref(m.Name), tokenStr(m.ContextLength), tokenStr(m.MaxOutputTokens),
			priceStr(m), cacheStr(m), visionMark(m), seenStr(m))
	}
	tw.Flush()
	if len(models) < total {
		fmt.Fprintf(w, "\n%d shown · %d hidden (free/preview) · --all to include, --free for only them\n", len(models), total-len(models))
	}
	fmt.Fprintln(w)
}

func priceStr(m db.Model) string {
	if m.PriceInput == nil && m.PriceOutput == nil {
		return "—"
	}
	return fmt.Sprintf("%s/%s", money(m.PriceInput), money(m.PriceOutput))
}

func cacheStr(m db.Model) string {
	if m.PriceCacheRead == nil && m.PriceCacheCreate == nil {
		return "—"
	}
	return fmt.Sprintf("%s/%s", money(m.PriceCacheRead), money(m.PriceCacheCreate))
}

func money(price *float64) string {
	if price == nil {
		return "—"
	}
	return "$" + strconv.FormatFloat(*price, 'f', -1, 64)
}

func visionMark(m db.Model) string {
	if m.CapVision != nil && *m.CapVision == 1 {
		return "👁"
	}
	return ""
}

func seenStr(m db.Model) string {
	if m.FirstSeen == nil || *m.FirstSeen == "" {
		return "—"
	}
	t, err := time.Parse(fetchedTimeFormat, *m.FirstSeen)
	if err != nil {
		return *m.FirstSeen
	}
	return t.UTC().Format("2006-01-02")
}
