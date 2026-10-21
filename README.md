# opencode-go-models

Standalone catalog + usage inspector for the [OpenCode Go](https://opencode.ai/docs/go/)
subscription (`https://opencode.ai/zen/go/v1`).

## Why this exists

The public `/v1/models` endpoint returns id/object/created only — no pricing,
limits, context, vision, or catalog history. This tool layers those on:

- live id list from `GET /zen/go/v1/models` (no key needed);
- context/limits/pricing/vision from [models.dev](https://models.dev) (the
  `opencode-go` provider rows, with the sibling `opencode` rows covering ids
  missing there, plus one hand patch for the straggler; a failed fetch just means fewer columns, never an error);
- `first_seen` per id, append-only in the local cache, so new arrivals are visible
  (upstream `created` is identical on every row — a placeholder, not an add date);
- rolling/weekly/monthly usage from `GET /inference/go/v1/usage` (key needed).

Cache lives at `~/Notes/opencode-go-models.db` (override with `--db`).

## Authentication

- Catalog browsing: none. Unauthenticated fetch works.
- Usage (`--usage`): `--key` or `$OPENCODE_GO_API_KEY`.
  `/connect` login already holds a key — pass it through the env var.

## Examples

```console
opencode-go-models --refresh            # fetch live catalog, show paid models
opencode-go-models --latest             # one winner per family (gpt, grok, qwen3…)
opencode-go-models --all                # include free/preview rows
opencode-go-models --free               # only free/preview rows
opencode-go-models --1m --sort ctx      # 1M+ context, biggest first
opencode-go-models --family qwen        # prefix filter
opencode-go-models --usage              # subscription windows, not the catalog
opencode-go-models --json --save snap.json
opencode-go-models --cached             # never fetch; render cache as-is
opencode-go-models --sql "select id, price_input, price_output from models order by price_output"
```

Sort keys: `name|ctx|output|input-price|output-price|seen`.

## first_seen semantics

Set on first observation, never rewritten by later refreshes. After a few
refreshes `--sort seen` shows genuinely new arrivals. A fresh `--db` path
resets history — expected, not a bug.
