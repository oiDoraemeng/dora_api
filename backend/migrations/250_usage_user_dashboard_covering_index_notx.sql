-- Cover the user dashboard summary, platform breakdown and default trend/model
-- queries without fetching wide usage_logs heap rows for every request.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_logs_user_dashboard_cover
ON usage_logs (user_id, created_at)
INCLUDE (input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens,
         total_cost, actual_cost, duration_ms, group_id, account_id,
         model, requested_model, upstream_model, account_stats_cost, account_rate_multiplier);
