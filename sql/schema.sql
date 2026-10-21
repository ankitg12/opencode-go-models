-- OpenCode Go model catalog. Live /zen/go/v1/models rows carry id/object/created
-- only; name/context/pricing/vision come from optional local OMP enrichment or
-- hand-maintained fallback pricing. first_seen is append-only catalog history:
-- set on first observation, never rewritten by later refreshes.
CREATE TABLE IF NOT EXISTS models (
    id               TEXT PRIMARY KEY,
    name             TEXT,
    object           TEXT,
    created          INTEGER,
    context_length   INTEGER,
    max_output_tokens INTEGER,
    cap_chat         INTEGER,
    cap_vision       INTEGER,
    price_input      REAL,
    price_output     REAL,
    price_cache_read REAL,
    price_cache_create REAL,
    fetched_at       TEXT,
    first_seen       TEXT
);
