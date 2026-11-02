# sub2api 本地定制与上游同步记录

## 2026-11-02 更新基线

| 项目 | 值 |
| --- | --- |
| 更新前版本 | `0.2.18` |
| 更新前提交 | `a626729be` (`fix: return requested model in gateway responses`) |
| 上游仓库 | `upstream` -> `https://github.com/Wei-Shaw/sub2api.git` |
| 本次上游版本 | `0.2.19` |
| 本次上游提交 | `4a4a83ee5` |
| 更新前备份分支 | `codex/pre-upstream-update-20261102` |

本次将 `upstream/main` 合并到本地 `main`。冲突集中在前端侧边栏和路由，合并结果同时保留了本地图片中心 `/images` 与上游 MCP 页面 `/mcp`。

## 本地定制功能清单

| 功能 | 主要内容 | 关键位置 | 更新后状态 |
| --- | --- | --- | --- |
| 请求模型名回写 | 无论本地映射、渠道映射或上游再次映射，响应中的 `model` 始终回写为客户端请求模型 | `backend/internal/pkg/modelresponse/`、`backend/internal/pkg/requestmodel/`、各网关 handler/service | 已保留并通过回归测试 |
| 多协议模型名覆盖 | 覆盖 OpenAI、Claude/Anthropic、Gemini 同类别模型映射，以及 JSON、SSE、WebSocket、Gemini model detail 等响应 | `backend/internal/handler/`、`backend/internal/service/` | 已保留并通过回归测试 |
| 显式模型校验 | 文本和图片请求未携带有效模型名时直接返回错误，不再依赖默认模型；视频路由暂不处理 | 各网关请求解析和 handler | 已保留并通过回归测试 |
| AI 图片生成中心 | 用户图片页面、会话与历史记录、批量任务、取消任务、质量/尺寸/模式选择 | `frontend/src/views/user/ImageGenerationView.vue`、图片相关 API/store/service | 已保留并通过前后端测试 |
| ChatGPT Web 生图 | 支持 `generation_backend`、`generation_method`、`image_backend` 别名，支持 OAuth/SetupToken 调度、Web 请求链与原生工具不可用时回退 | `backend/internal/service/openai_images*.go` | 合并时补回能力判断和回退链路，测试通过 |
| EasyPay 主备容灾 | 多支付提供方按优先级候选、失败切换、审计与提供方快照更新，允许弹窗/二维码跨模式容灾 | `backend/internal/service/payment_order.go`、`payment_failover.go` | 合并时补回完整执行链，测试通过 |
| Token 分类计费倍率 | 输入、输出、缓存 Token 独立倍率，支持全局配置和按用户覆盖，使用量日志记录缩放后的 Token | `backend/internal/config/config.go`、`billing_service.go`、`token_billing_overrides.go` | 合并时补回配置和计费调用链，测试通过 |
| 定向邮件发送 | 管理端按用户/收件人筛选并投递邮件 | `frontend/src/components/admin/settings/TargetedEmailSender.vue`、后端定向邮件服务 | 已保留并通过测试 |
| 余额提醒 | 用户余额阈值、提醒开关、额外收件邮箱与管理配置 | 用户设置、余额通知 service、前端 Dashboard | 已保留并通过测试 |
| 价格定制 | OpenAI 长上下文计费默认值、GPT-5.5 等模型价格修正 | 计费服务与模型价格资源 | 已保留并通过计费测试 |
| 品牌与多语言 | Dora2API 品牌；法语、日语、俄语、越南语、繁体中文 | 前端布局和 `frontend/src/i18n/locales/` | 已保留，前端构建通过 |
| 本地部署定制 | Docker Compose、端口、环境变量加载和本地部署文档 | `deploy/`、README | 已保留 |

## 请求模型名保证

当前规则以客户端请求中的模型名为唯一对外模型名：

1. 请求模型映射到渠道模型时，响应返回请求模型。
2. 本地未配置映射但上游自行映射或返回其他模型时，响应仍返回请求模型。
3. JSON、SSE 和 WebSocket 响应均执行相同规则；不仅替换完全相同或日期后缀模型，任何上游 `model` 值都回写为请求模型。
4. OpenAI、Claude/Anthropic、Gemini 路由按各自响应结构处理，Gemini 模型详情也返回请求侧名称。
5. 请求没有有效模型名时直接报错。视频接口按约定暂不纳入本规则。

相关本地提交：`a626729be`、`59bcf3816`、`2aca8acdc`、`1b11a3f75`。

## 本次合并发现并修复的断链

更新前代码中部分测试和前端入口仍存在，但后端执行入口已在此前同步中被覆盖。本次检查没有只确认文件存在，而是恢复并执行了端到端调用链：

1. 恢复 Token 分类倍率的默认值、校验、用户覆盖读取、费用计算和 usage log 持久化。
2. 恢复 ChatGPT Web 生图后端字段解析、账户能力筛选、显式路由及 OAuth 原生工具不可用时的 Web 回退。
3. 恢复 EasyPay 多候选提供方选择与失败切换，同时保留弹窗/二维码跨模式容灾。
4. 修正旧模型名测试，使其符合“上游返回任何模型名都回写请求模型”的规则。

## 验证记录

- 后端：`go build ./...` 通过。
- 后端服务层：`go test ./internal/service -count=1` 全量通过。
- 模型名：`internal/pkg/modelresponse`、`internal/pkg/requestmodel`、handler、service 和路由定向回归通过。
- 计费：分类 Token 倍率、按用户覆盖、usage log 缩放、长上下文和价格定向测试通过。
- 图片：请求解析、ChatGPT Web、OAuth 回退、批量/取消任务相关测试通过。
- 支付：EasyPay 候选选择、主备切换、弹窗模式相关测试通过。
- 邮件与余额提醒：后端管理接口和服务定向测试通过。
- 前端：侧边栏、图片中心、设置页、MCP 路由、i18n 定向测试通过；`pnpm build` 通过。

以后同步上游时，应以本文件逐项验证行为，不能只根据合并是否无冲突判断定制功能仍然有效。
