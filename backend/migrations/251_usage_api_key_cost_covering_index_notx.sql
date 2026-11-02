-- The API Key page only needs cost sums, not full usage log records.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_logs_api_key_cost_cover
ON usage_logs (api_key_id, created_at) INCLUDE (actual_cost);
