//go:build dashboard_postgres

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)

// Run against a disposable PostgreSQL database with DASHBOARD_TEST_POSTGRES_DSN.
// This does not use the Docker harness, and all fixtures live in a private schema.
func TestUserDashboardPostgres(t *testing.T) {
	dsn := os.Getenv("DASHBOARD_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set DASHBOARD_TEST_POSTGRES_DSN to test real aggregates and covering indexes")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	schema := fmt.Sprintf("dashboard_test_%d", time.Now().UnixNano())
	_, err = db.ExecContext(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	defer func() { _, _ = db.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE") }()
	_, err = db.ExecContext(ctx, "SET search_path TO "+schema)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
		CREATE TABLE api_keys (id bigint, user_id bigint, status text, deleted_at timestamptz);
		CREATE TABLE groups (id bigint, platform text);
		CREATE TABLE accounts (id bigint, platform text);
		CREATE TABLE usage_logs (
			user_id bigint, api_key_id bigint, group_id bigint, account_id bigint, created_at timestamptz,
			input_tokens integer NOT NULL DEFAULT 0, output_tokens integer NOT NULL DEFAULT 0,
			cache_creation_tokens integer NOT NULL DEFAULT 0, cache_read_tokens integer NOT NULL DEFAULT 0,
			total_cost numeric NOT NULL DEFAULT 0, actual_cost numeric NOT NULL DEFAULT 0, duration_ms integer,
			model varchar(100) NOT NULL DEFAULT 'test', requested_model varchar(100), upstream_model varchar(100),
			account_stats_cost numeric, account_rate_multiplier numeric
		);
		INSERT INTO api_keys VALUES (1, 1, 'active', NULL), (2, 1, 'inactive', NULL),
			(3, 1, 'active', now()), (4, 2, 'active', NULL);
		INSERT INTO groups VALUES (1, 'openai'), (2, 'composite');
		INSERT INTO accounts VALUES (1, 'anthropic');
	`)
	require.NoError(t, err)
	today := timezone.Today()
	insert := `INSERT INTO usage_logs (user_id, api_key_id, group_id, account_id, created_at,
		input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens, total_cost, actual_cost, duration_ms)
		VALUES ($1, $2, $3, $4, $5, 10, 20, 30, 40, $6, $7, $8)`
	// Historical usage of a deleted key still contributes to all-time totals.
	_, err = db.ExecContext(ctx, insert, 1, 3, 1, 1, today.AddDate(0, 0, -1), 2, 1, 100)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, insert, 1, 1, 2, 1, time.Now(), 4, 2, 300)
	require.NoError(t, err)
	// A failed placeholder belongs to total requests, but not the platform split.
	_, err = db.ExecContext(ctx, insert, 1, 1, 1, 1, time.Now(), 0, 0, nil)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, insert, 2, 4, 1, 1, time.Now(), 900, 900, 900)
	require.NoError(t, err)
	repo := &usageLogRepository{sql: db}
	stats, err := repo.GetUserDashboardStats(ctx, 1)
	require.NoError(t, err)
	require.EqualValues(t, 2, stats.TotalAPIKeys)
	require.EqualValues(t, 1, stats.ActiveAPIKeys)
	require.EqualValues(t, 3, stats.TotalRequests)
	require.EqualValues(t, 300, stats.TotalTokens)
	require.Equal(t, 6.0, stats.TotalCost)
	require.Equal(t, 3.0, stats.TotalActualCost)
	require.EqualValues(t, 2, stats.TodayRequests)
	require.EqualValues(t, 200, stats.TodayTokens)
	require.Equal(t, 2.0, stats.TodayActualCost)
	require.Equal(t, 200.0, stats.AverageDurationMs)
	require.EqualValues(t, 0, stats.Rpm)
	require.EqualValues(t, 40, stats.Tpm)
	require.Len(t, stats.ByPlatform, 2)
	require.Equal(t, "anthropic", stats.ByPlatform[0].Platform)
	require.EqualValues(t, 1, stats.ByPlatform[0].TotalRequests)
	require.Equal(t, "openai", stats.ByPlatform[1].Platform)
	empty, err := repo.GetUserDashboardStats(ctx, 999)
	require.NoError(t, err)
	require.EqualValues(t, 0, empty.TotalRequests)
	require.EqualValues(t, 0, empty.TodayTokens)
	keyStats, err := repo.GetBatchAPIKeyUsageStats(ctx, []int64{1, 3, 1, 999}, today.AddDate(0, 0, -30), time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.Equal(t, 2.0, keyStats[1].TodayActualCost)
	require.Equal(t, 1.0, keyStats[3].TotalActualCost)
	require.Zero(t, keyStats[999].TotalActualCost)

	// Apply the actual non-transactional migration files, including CONCURRENTLY.
	for _, file := range []string{"250_usage_user_dashboard_covering_index_notx.sql", "251_usage_api_key_cost_covering_index_notx.sql"} {
		migration, readErr := os.ReadFile(filepath.Join("..", "..", "migrations", file))
		require.NoError(t, readErr)
		_, err = db.ExecContext(ctx, string(migration))
		require.NoError(t, err)
	}
	_, err = db.ExecContext(ctx, `
		INSERT INTO usage_logs (user_id, api_key_id, group_id, account_id, created_at,
			input_tokens, output_tokens, total_cost, actual_cost)
		SELECT 10 + n % 100, 10 + n % 100, 1, 1, now() - (n % 86400) * interval '1 second',
			10, 20, 0.1, 0.05 FROM generate_series(1, 100000) n;
	`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, "VACUUM ANALYZE usage_logs")
	require.NoError(t, err)
	queries := map[string]string{
		"summary":  `SELECT count(*), sum(input_tokens), sum(output_tokens), sum(cache_creation_tokens), sum(cache_read_tokens), sum(total_cost), sum(actual_cost), avg(duration_ms) FROM usage_logs WHERE user_id = 10`,
		"key-cost": `SELECT api_key_id, sum(actual_cost) FROM usage_logs WHERE api_key_id = ANY(ARRAY[10,11]::bigint[]) AND created_at >= now() - interval '30 days' GROUP BY api_key_id`,
	}
	for name, query := range queries {
		rows, queryErr := db.QueryContext(ctx, "EXPLAIN (ANALYZE, BUFFERS) "+query)
		require.NoError(t, queryErr)
		defer rows.Close()
		var plan strings.Builder
		for rows.Next() {
			var line string
			require.NoError(t, rows.Scan(&line))
			plan.WriteString(line + "\n")
		}
		require.NoError(t, rows.Close())
		require.NoError(t, rows.Err())
		t.Log(name + "\n" + plan.String())
		require.Contains(t, plan.String(), "Index Only Scan")
		require.Contains(t, plan.String(), "Heap Fetches: 0")
	}
	// The default model query must also be covered (including account cost).
	_, err = repo.GetModelStatsWithUsageFiltersBySource(ctx, today.AddDate(0, 0, -30), time.Now().Add(time.Hour), usagestats.UsageLogFilters{UserID: 10}, usagestats.ModelSourceRequested)
	require.NoError(t, err)
}
