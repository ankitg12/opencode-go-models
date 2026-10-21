package main

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	_ "modernc.org/sqlite"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"opencode-go-models/internal/db"
)

const (
	modelsEndpoint = "https://opencode.ai/zen/go/v1/models"
	usageEndpoint  = "https://opencode.ai/inference/go/v1/usage"

	// cacheTTL bounds staleness: docs carry no release feed, so the cache is
	// the change detector. 6h keeps --latest honest without hammering.
	cacheTTL          = 6 * time.Hour
	fetchedTimeFormat = "2006-01-02T15:04:05.000000-07:00"
	httpTimeout       = 30 * time.Second
	toolVersion       = "0.1.0"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		refresh    = flag.Bool("refresh", false, "fetch the live catalog and rewrite the cache before viewing")
		all        = flag.Bool("all", false, "show free/preview rows too (default hides zero-price rows)")
		oneM       = flag.Bool("1m", false, "only models with at least a 1M context window")
		latest     = flag.Bool("latest", false, "keep only the newest generation of each model family")
		family     = flag.String("family", "", "filter by model family prefix (for example gpt, claude, qwen)")
		free       = flag.Bool("free", false, "only zero-price (free/preview) models")
		usage      = flag.Bool("usage", false, "show subscription usage windows instead of the catalog")
		sortBy     = flag.String("sort", "", "sort globally by name|ctx|output|input-price|output-price|seen")
		limit      = flag.Int("limit", 0, "limit the number of models to show (0 for unlimited)")
		limitShort = flag.Int("n", 0, "alias for --limit")
		jsonOut    = flag.Bool("json", false, "print the full (filtered) catalog as JSON")
		save       = flag.String("save", "", "also write a JSON snapshot to this path (implies --refresh)")
		sqlQuery   = flag.String("sql", "", "run a read-only SQL query against the cache and print a table")
		dbPath     = flag.String("db", "", "SQLite cache path (default ~/Notes/opencode-go-models.db)")
		apiKey     = flag.String("key", "", "OpenCode Go API key (default $OPENCODE_GO_API_KEY; catalog fetch works without one)")
		cached     = flag.Bool("cached", false, "never fetch; render the cache as-is even if stale")
		showVer    = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()

	if *showVer {
		fmt.Println("opencode-go-models", toolVersion)
		return nil
	}
	if *sortBy != "" && !validSort(*sortBy) {
		return fmt.Errorf("invalid --sort %q (want name|ctx|output|input-price|output-price|seen)", *sortBy)
	}
	effectiveLimit := *limit
	if effectiveLimit <= 0 && *limitShort > 0 {
		effectiveLimit = *limitShort
	}
	if effectiveLimit < 0 {
		return fmt.Errorf("invalid --limit %d (must be non-negative)", effectiveLimit)
	}
	selectedDBPath := *dbPath
	if selectedDBPath == "" {
		selectedDBPath = defaultDBPath()
	}

	key := strings.TrimSpace(*apiKey)
	if key == "" {
		key = strings.TrimSpace(os.Getenv("OPENCODE_GO_API_KEY"))
	}

	ctx := context.Background()
	doRefresh := *refresh || *save != ""

	if *sqlQuery != "" && !doRefresh {
		return runQuery(selectedDBPath, *sqlQuery)
	}

	if *usage {
		return showUsage(ctx, key)
	}

	var models []db.Model
	fetchedAt := time.Now().UTC().Format(fetchedTimeFormat)
	autoRefresh := false

	if !doRefresh {
		loaded, err := readCache(ctx, selectedDBPath)
		switch {
		case err != nil && *cached:
			return err
		case err != nil:
			doRefresh = true // no usable cache yet: fetch rather than fail
		default:
			models = loaded
			if len(models) > 0 && models[0].FetchedAt != nil {
				fetchedAt = *models[0].FetchedAt
			}
			if !*cached && cacheStale(fetchedAt, fetchedTimeFormat, cacheTTL) {
				fmt.Fprintf(os.Stderr, "cache older than %s — refreshing\n", cacheTTL)
				doRefresh, autoRefresh = true, true
			}
		}
	}

	if doRefresh {
		fetched, err := fetchCatalog(ctx, selectedDBPath, key)
		switch {
		case err != nil && autoRefresh && len(models) > 0:
			fmt.Fprintf(os.Stderr, "refresh failed (%v) — showing cache from %s\n", err, prettyTime(fetchedAt))
		case err != nil:
			return err
		default:
			models = fetched
			if len(models) > 0 && models[0].FetchedAt != nil {
				fetchedAt = *models[0].FetchedAt
			}
			fmt.Fprintf(os.Stderr, "refreshed %d models → %s\n", len(models), selectedDBPath)
		}
	}

	if *save != "" {
		if err := writeJSONSnapshot(*save, models, fetchedAt); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "wrote JSON snapshot → %s\n", *save)
	}

	shown := applyFilters(models, *all, *oneM, *family, *free)
	freeTotal := 0
	for _, m := range models {
		free := (m.PriceInput == nil || *m.PriceInput == 0) && (m.PriceOutput == nil || *m.PriceOutput == 0)
		if free {
			freeTotal++
		}
	}
	if *latest {
		shown = latestModels(shown)
	}
	if *sortBy != "" {
		sortModels(shown, *sortBy)
	}
	limited := 0
	if effectiveLimit > 0 && effectiveLimit < len(shown) {
		limited = len(shown) - effectiveLimit
		shown = shown[:effectiveLimit]
	}
	if *sqlQuery != "" { // --sql WITH --refresh: fetch happened, now query.
		return runQuery(selectedDBPath, *sqlQuery)
	}
	if *jsonOut {
		return printJSON(os.Stdout, shown, fetchedAt)
	}
	renderView(os.Stdout, shown, len(models), freeTotal, limited, fetchedAt, *latest, *sortBy)
	return nil
}

// cacheStale reports whether a cached fetched-at timestamp is older than the
// TTL. An unparseable timestamp counts as stale: better a needless fetch than
// a silently ancient catalog.
func cacheStale(fetchedAt, timeFmt string, ttl time.Duration) bool {
	t, err := time.Parse(timeFmt, fetchedAt)
	if err != nil {
		return true
	}
	return time.Since(t) > ttl
}

func validSort(s string) bool {
	switch s {
	case "", "name", "ctx", "output", "input-price", "output-price", "seen":
		return true
	}
	return false
}

//go:embed sql/schema.sql
var schemaDDL string

func openDB(path string, readOnly bool) (*sql.DB, error) {
	dsn := "file:" + path
	if readOnly {
		dsn += "?mode=ro"
	}
	return sql.Open("sqlite", dsn)
}

func defaultDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, "Notes", "opencode-go-models.db")
}

func strPtr(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func deref[T any](value *T) T {
	if value == nil {
		var zero T
		return zero
	}
	return *value
}

func prettyTime(value string) string {
	t, err := time.Parse(fetchedTimeFormat, value)
	if err != nil {
		return value
	}
	return t.UTC().Format("2006-01-02 15:04 UTC")
}

func tokenStr(value *int64) string {
	if value == nil || *value <= 0 {
		return "—"
	}
	n := *value
	switch {
	case n >= 1_000_000 && n%1_000_000 == 0:
		return fmt.Sprintf("%dM", n/1_000_000)
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000.0)
	case n >= 1_000 && n%1_000 == 0:
		return fmt.Sprintf("%dK", n/1_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fK", float64(n)/1_000.0)
	default:
		return strconv.FormatInt(n, 10)
	}
}

// --- catalog fetch (no key needed) + OMP enrichment + cache ---

type liveModel struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created *int64 `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// enrich holds the stable per-model metadata OMP ships (context, limits,
// pricing, vision) because the public /models rows carry id/object/created
// only. Refreshed from `omp models --json` when available; otherwise the
// previous cache (or the hand-maintained fallback below) stands.
type enrich struct {
	Name       string
	Ctx        int64
	Out        int64
	InPrice    float64
	OutPrice   float64
	CacheRead  float64
	CacheWrite float64
	Vision     bool
}

// fetchCatalog returns display models. It always fetches the live id list,
// preserves first_seen per id, and layers OMP enrichment + fallback pricing.
func fetchCatalog(ctx context.Context, dbPath, _ string) ([]db.Model, error) {
	live, err := fetchLiveIDs(ctx)
	if err != nil {
		return nil, err
	}
	prev := map[string]db.Model{}
	if conn, err := openDB(dbPath, true); err == nil {
		if rows, rerr := db.New(conn).ListModels(ctx); rerr == nil {
			for _, m := range rows {
				prev[m.ID] = m
			}
		}
		conn.Close()
	}
	en := mdEnrichment(ctx)
	now := time.Now().UTC().Format(fetchedTimeFormat)
	models := make([]db.Model, 0, len(live))
	for _, lm := range live {
		m := db.Model{ID: lm.ID, FetchedAt: &now}
		if lm.Created != nil {
			m.Created = lm.Created
		}
		if p, ok := prev[lm.ID]; ok {
			// first_seen is append-only history: never rewrite it.
			m.FirstSeen = p.FirstSeen
			// Carry forward last-known enrichment; OMP/fallback below may refresh.
			m.Name, m.ContextLength, m.MaxOutputTokens = p.Name, p.ContextLength, p.MaxOutputTokens
			m.PriceInput, m.PriceOutput, m.PriceCacheRead, m.PriceCacheCreate = p.PriceInput, p.PriceOutput, p.PriceCacheRead, p.PriceCacheCreate
			m.CapVision, m.CapChat = p.CapVision, p.CapChat
		}
		if m.FirstSeen == nil {
			m.FirstSeen = &now
		}
		if e, ok := en[lm.ID]; ok {
			m.Name = strPtr(e.Name)
			m.ContextLength = &e.Ctx
			m.MaxOutputTokens = &e.Out
			m.PriceInput, m.PriceOutput = &e.InPrice, &e.OutPrice
			m.PriceCacheRead, m.PriceCacheCreate = &e.CacheRead, &e.CacheWrite
			if e.Vision {
				one := int64(1)
				m.CapVision = &one
			}
			one := int64(1)
			m.CapChat = &one
		} else if fb, ok := fallbackPricing[lm.ID]; ok && m.PriceInput == nil {
			// Hand-maintained docs pricing for ids OMP does not (yet) list.
			m.Name = strPtr(fb.Name)
			m.ContextLength = &fb.Ctx
			m.MaxOutputTokens = &fb.Out
			m.PriceInput, m.PriceOutput = &fb.InPrice, &fb.OutPrice
			m.PriceCacheRead = &fb.CacheRead
		}
		models = append(models, m)
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	if err := writeModels(ctx, dbPath, models); err != nil {
		return nil, err
	}
	return models, nil
}

func fetchLiveIDs(ctx context.Context) ([]liveModel, error) {
	ctx, cancel := context.WithTimeout(ctx, httpTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsEndpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("opencode.ai returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.UseNumber()
	var payload struct {
		Data []liveModel `json:"data"`
	}
	if err := dec.Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode catalog: %w", err)
	}
	out := payload.Data[:0]
	for _, m := range payload.Data {
		if m.ID != "" {
			out = append(out, m)
		}
	}
	return out, nil
}

func writeModels(ctx context.Context, path string, models []db.Model) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create cache dir: %w", err)
	}
	conn, err := openDB(path, false)
	if err != nil {
		return fmt.Errorf("open cache DB: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, schemaDDL); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()
	q := db.New(tx)
	if err := q.DeleteAllModels(ctx); err != nil {
		return fmt.Errorf("clear cache: %w", err)
	}
	for _, m := range models {
		if err := q.InsertModel(ctx, db.InsertModelParams{
			ID: m.ID, Name: m.Name, ContextLength: m.ContextLength,
			MaxOutputTokens: m.MaxOutputTokens, Created: m.Created,
			CapChat: m.CapChat, CapVision: m.CapVision,
			PriceInput: m.PriceInput, PriceOutput: m.PriceOutput,
			PriceCacheRead: m.PriceCacheRead, PriceCacheCreate: m.PriceCacheCreate,
			FetchedAt: m.FetchedAt, FirstSeen: m.FirstSeen,
		}); err != nil {
			return fmt.Errorf("cache model %q: %w", m.ID, err)
		}
	}
	return tx.Commit()
}

func readCache(ctx context.Context, path string) ([]db.Model, error) {
	conn, err := openDB(path, true)
	if err != nil {
		return nil, fmt.Errorf("open cache DB: %w", err)
	}
	defer conn.Close()
	models, err := db.New(conn).ListModels(ctx)
	if err != nil {
		return nil, fmt.Errorf("query cache: %w", err)
	}
	return models, nil
}

func printJSON(w io.Writer, models []db.Model, fetchedAt string) error {
	payload := struct {
		FetchedAt string     `json:"fetched_at"`
		Total     int        `json:"total"`
		Models    []db.Model `json:"models"`
	}{
		FetchedAt: fetchedAt,
		Total:     len(models),
		Models:    models,
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(payload)
}

func writeJSONSnapshot(path string, models []db.Model, fetchedAt string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create snapshot dir: %w", err)
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create snapshot file: %w", err)
	}
	defer f.Close()
	return printJSON(f, models, fetchedAt)
}
