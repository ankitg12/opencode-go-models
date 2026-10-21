-- name: ListModels :many
SELECT * FROM models ORDER BY id;

-- name: DeleteAllModels :exec
DELETE FROM models;

-- name: InsertModel :exec
INSERT INTO models (
    id, name, object, created,
    context_length, max_output_tokens,
    cap_chat, cap_vision,
    price_input, price_output, price_cache_read, price_cache_create,
    fetched_at, first_seen
) VALUES (
    ?, ?, ?, ?,
    ?, ?,
    ?, ?,
    ?, ?, ?, ?,
    ?, ?
);
