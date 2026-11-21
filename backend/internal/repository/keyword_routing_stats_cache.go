package repository

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

type keywordRoutingStatsCache struct {
	reader *redis.Client
	writer *redis.Client
}

func NewKeywordRoutingStatsCache(client *redis.Client) service.KeywordRoutingStatsStore {
	// These views share the application's pool. Bound socket I/O separately:
	// the shared client's settings may ignore context deadlines during reads.
	return &keywordRoutingStatsCache{
		reader: client.WithTimeout(500 * time.Millisecond),
		writer: client.WithTimeout(100 * time.Millisecond),
	}
}

// A single daily hash keeps all dimensions atomic and uses one cluster slot.
// Refuse late writes after midnight rather than resurrecting yesterday's key.
const recordKeywordRoutingStatsLua = `
local now = redis.call('TIME')
if tonumber(now[1]) >= tonumber(ARGV[1]) then return 0 end
redis.call('HINCRBY', KEYS[1], 'checks', 1)
redis.call('HINCRBY', KEYS[1], 'g:' .. ARGV[2] .. ':checks', 1)
if tonumber(ARGV[3]) > 0 then
 redis.call('HINCRBY', KEYS[1], 'hits', 1)
 redis.call('HINCRBY', KEYS[1], 'g:' .. ARGV[2] .. ':hits', 1)
end
for i = 4, #ARGV, 2 do
 redis.call('HSET', KEYS[1], 'label:' .. ARGV[i], ARGV[i+1])
 redis.call('HINCRBY', KEYS[1], 'k:' .. ARGV[i], 1)
 redis.call('HINCRBY', KEYS[1], 'gk:' .. ARGV[2] .. ':' .. ARGV[i], 1)
end
redis.call('EXPIREAT', KEYS[1], ARGV[1])
return 1
`

var recordKeywordRoutingStats = redis.NewScript(recordKeywordRoutingStatsLua)

// A lost reply may mean that Redis has already incremented the counters. Never
// retry this non-idempotent command automatically on a transport error.
type keywordRoutingStatsCommand struct{ *redis.Cmd }

func (*keywordRoutingStatsCommand) NoRetry() bool { return true }

func keywordRoutingStatsKey(day time.Time) string {
	return "keyword-routing:today:" + day.In(timezone.Location()).Format("2006-01-02")
}
func (s *keywordRoutingStatsCache) Record(ctx context.Context, at time.Time, groupID int64, words []string) error {
	day := timezone.StartOfDay(at)
	args := []any{day.AddDate(0, 0, 1).Unix(), groupID, len(words)}
	for _, word := range words {
		id := fmt.Sprintf("%x", sha256.Sum256([]byte(strings.ToLower(word))))
		args = append(args, id, word)
	}
	commandArgs := append([]any{"evalsha", recordKeywordRoutingStats.Hash(), 1, keywordRoutingStatsKey(day)}, args...)
	command := &keywordRoutingStatsCommand{redis.NewCmd(ctx, commandArgs...)}
	err := s.writer.Process(ctx, command)
	if redis.HasErrorPrefix(err, "NOSCRIPT") {
		// NOSCRIPT confirms that nothing ran, so loading the script is safe.
		commandArgs[0], commandArgs[1] = "eval", recordKeywordRoutingStatsLua
		command = &keywordRoutingStatsCommand{redis.NewCmd(ctx, commandArgs...)}
		err = s.writer.Process(ctx, command)
	}
	return err
}
func (s *keywordRoutingStatsCache) Today(ctx context.Context, groupID *int64) (*service.KeywordRoutingTodayStats, error) {
	now := timezone.Now()
	values, err := s.reader.HGetAll(ctx, keywordRoutingStatsKey(now)).Result()
	if err != nil {
		return nil, err
	}
	stats := &service.KeywordRoutingTodayStats{Date: now.Format("2006-01-02"), Timezone: timezone.Name(), Groups: []service.KeywordRoutingGroupStats{}, Keywords: []service.KeywordRoutingKeywordStats{}}
	integer := func(key string) int64 { value, _ := strconv.ParseInt(values[key], 10, 64); return value }
	prefix := ""
	if groupID != nil {
		prefix = fmt.Sprintf("g:%d:", *groupID)
	}
	stats.Checks = integer(prefix + "checks")
	stats.Hits = integer(prefix + "hits")
	for field, value := range values {
		if strings.HasPrefix(field, "g:") && strings.HasSuffix(field, ":checks") {
			id, e := strconv.ParseInt(strings.Split(field, ":")[1], 10, 64)
			if e != nil || (groupID != nil && *groupID != id) {
				continue
			}
			stats.Groups = append(stats.Groups, service.KeywordRoutingGroupStats{GroupID: id, KeywordRoutingCounter: service.KeywordRoutingCounter{Checks: integer(field), Hits: integer(fmt.Sprintf("g:%d:hits", id))}})
		}
		if strings.HasPrefix(field, "label:") {
			id := strings.TrimPrefix(field, "label:")
			counter := "k:" + id
			if groupID != nil {
				counter = fmt.Sprintf("gk:%d:%s", *groupID, id)
			}
			if hits := integer(counter); hits > 0 {
				stats.Keywords = append(stats.Keywords, service.KeywordRoutingKeywordStats{Keyword: value, Hits: hits})
			}
		}
	}
	sort.Slice(stats.Groups, func(i, j int) bool { return stats.Groups[i].GroupID < stats.Groups[j].GroupID })
	sort.Slice(stats.Keywords, func(i, j int) bool {
		if stats.Keywords[i].Hits != stats.Keywords[j].Hits {
			return stats.Keywords[i].Hits > stats.Keywords[j].Hits
		}
		return stats.Keywords[i].Keyword < stats.Keywords[j].Keyword
	})
	return stats, nil
}
