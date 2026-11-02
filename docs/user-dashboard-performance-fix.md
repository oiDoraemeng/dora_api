# 用户仪表盘与 API Key 页面加载优化

本次修复针对 `usage_logs` 大表聚合慢，以及前端等待这些聚合导致整页持续加载的问题。生产环境仍需发布本次代码并完成迁移后才能生效。

- 用户统计、趋势、模型和批量 Key 费用采用进程内 30 秒缓存，同一查询合并执行。缓存按用户、日期、完整筛选条件或 Key 集合隔离，上限 256 条；错误不缓存，返回值使用独立快照。单个客户端取消不会取消其他等待者共享的查询，后台查询最多执行 60 秒。余额刷新、鉴权与实际扣费不经过此缓存。
- 用户仪表盘将累计、今日和近 5 分钟指标合为一次扫描，Key 数量合为一次查询，共从 6 次 SQL 减为 3 次。保留零费用请求、历史已删除 Key 用量及有效平台归属的原有统计口径。
- 迁移 250、251 分别为用户统计和 Key 费用添加覆盖索引，使用 `CREATE INDEX CONCURRENTLY`。默认趋势与请求模型统计所需列也包含在用户索引中。索引可减少宽表回表读取；近期未被 vacuum 标记为可见的数据仍可能回表。
- Key 列表先显示，费用独立加载；尚未取到费用时显示 `—`。换页后旧请求不会覆盖新结果。
- 仪表盘统计、趋势、模型、最近用量与快捷入口独立显示；趋势与模型互不阻塞，过期日期请求被忽略。最近用量只取 5 条，通过 `skip_total=true` 使用现有 lookahead 分页，不再执行全量 COUNT；正常明细列表的精确总数行为不变。

验证：前端页面回归 30 项、类型检查、修改文件 ESLint、i18n 完整性及生产构建通过；后端缓存、筛选隔离、预览分页、用量处理与删除 Key 计费相关测试通过。另在独立 PostgreSQL 18 数据库验证真实 SQL 统计结果和两份并发迁移，在 100004 条测试记录上确认两个索引均命中 Index Only Scan 且 Heap Fetches 为 0。这是执行计划验证，不能视为生产 1460 万条记录上的耗时承诺。

本机 CGO 未启用且未发现 C 编译器，未运行 Go race detector；20 个并发等待者及首个请求取消的功能回归已通过。顺便修正了升级后两个测试中失配的构造函数/方法参数，以便相关测试能编译。

验证范围为本次功能的定向回归，并非全仓库测试全绿。`go test -tags=unit ./internal/handler` 仍被既有的 `grok_media_slots_test.go`、`openai_gateway_credential_failover_loop_test.go`、`openai_images_failover_test.go`、`openai_responses_failover_cancel_test.go` 中旧版 `NewOpenAIGatewayService` 参数阻塞；不带 `unit` 标签的本次用量 Handler 回归可正常运行。

发布时需要同时包含前端产物、后端及迁移 250/251。生产库当前数据和索引较大，建索引前应核实剩余磁盘及构建过程的空间需求，并安排低峰执行；可先在现有服务运行时建立两个索引，再发布应用。发布后检查索引 `indisvalid`、统计信息和实际接口耗时，比较冷查询与 30 秒内重复查询；大用户首次累计聚合仍依赖数据库读取历史索引。统计结果生成后缓存 30 秒，实际扣费与余额不受这项展示缓存影响。

独立数据库回归命令（只用于测试库，会建立并清理私有测试 schema）：

```powershell
cd D:\github\sub2api\backend
$env:DASHBOARD_TEST_POSTGRES_DSN = 'host=127.0.0.1 port=55439 user=dashboard_test dbname=postgres sslmode=disable'
go test -tags=dashboard_postgres ./internal/repository -run '^TestUserDashboardPostgres$' -count=1 -v
```
