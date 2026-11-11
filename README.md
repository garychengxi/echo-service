# Echo Service

Echo 的控制台后端与用户容器 Agent 网关。工程目录、启动方式和核心依赖严格参考
`codeflow-service-be`，Go 版本固定为 **1.24.3**；业务边界针对 Echo 做了收敛。

## 当前能力

- 本地账号、Argon2id 密码、不透明 access/refresh token、refresh 旋转与重放撤销。
- admin/user RBAC，用户、容器和单 active owner 分配关系。
- SQLite 显式版本迁移；用户和容器元数据使用可替换的 `cache.Store` 内存实现。
- 每个授权用户/容器一条复用的 ACP WebSocket 连接，单 reader/single writer、并发 RPC 路由、有界队列、超时、失败退避、取消和撤权关闭。
- Project 创建、查看、改名、删除；Session 创建、加载、重命名、fork、删除、模型/权限模式设置。
- 异步 prompt、稳定 `request_id`、turn 状态、审批转发、取消和前端事件 WebSocket。
- 模型、Agent、技能、插件、MCP、环境、受限 Agent 设置及目录列表。

前端联调优先阅读 [前端 API 对接文档](doc/frontend-api-v1.md)。简要合同见 [当前接口](doc/api-current.md)，完整目标和数据约束见 [接口与数据规划](doc/api-plan-v1.md)，架构边界见
[架构说明](doc/architecture.md)。Gateway 的 OpenAI 兼容转发仍明确返回 501，不与 Agent ACP
连接混为一层。

## 技术基线

- Go `1.24.3`
- `server-go v1.1.2-0.20260605025852-1344b2386b62`
- `logger-go v0.0.2`、Gin `v1.11.0`、Viper `v1.21.0`
- GORM `v1.31.1`、纯 Go SQLite、Gorilla WebSocket

私有模块需要对 `code.cestc.cn` 的合法访问权限。构建脚本不会修改全局 Git、Go、代理或凭据配置。

## 启动

```sh
go version
make check
make build
./output/bin/echoservice check-config
./output/bin/echoservice bootstrap-admin --username admin
./output/bin/echoservice-server --config conf/server/config.toml
```

默认监听 `127.0.0.1:9090`，SQLite 为 `data/echo-service.db`。首次管理员只能通过交互式
CLI 创建，没有默认密码。容器通过 admin API 登记为 `ws://private-ip:port`；客户端不能提交
任意上游地址。

常用环境变量：`ECHO_HTTPSERVER_LISTEN`、`ECHO_DB_PATH`、`ECHO_DB_BUSYTIMEOUT`、
`ECHO_CACHE_TTL`、`ECHO_AUTH_ACCESSTOKENTTL`、`ECHO_AUTH_REFRESHTOKENTTL`、
`ECHO_AGENTCONNECTION_DIALTIMEOUT`、`ECHO_AGENTCONNECTION_REQUESTTIMEOUT`。

## 主要 API

业务 API 前缀为 `/api/v1`：

- `/auth/login`、`/auth/refresh`、`/auth/logout`、`/me`
- `/admin/users`、`/admin/containers`、容器用户分配、审计和 probe
- `/containers/{id}/projects`、`/containers/{id}/sessions`
- `/sessions/{id}/load|fork|turns`、模型/模式设置、审批和取消
- `/containers/{id}/models|agents|skills|plugins|mcp-servers|files|agent-settings`
- `/ws-tickets` + `/events?ticket=...`，使用 30 秒单次票据，不把 access token 放入 URL

`/livez` 与 `/healthz` 只反映进程存活，`/readyz` 会实际检查 SQLite。

## 缓存与 Redis 迁移

`internal/cache.Store` 只缓存无密码 hash 的用户和容器元数据，授权、token 和用户容器关系
始终查 SQLite。缓存失效异常会让当前进程绕过缓存回源，不能影响权限正确性。以后新增 Redis
实现并注入 `directory.Service` 即可；多实例还需要连接归属/路由层，不能把活跃 ACP 连接或审批
对象直接塞进 Redis。

## 验证

```sh
make check
make test-race
make build
make smoke
ECHO_TEST_BINARY=/absolute/path/to/echo make test-echo
make cross-build
```

真实 Echo 集成测试使用独立临时 `HOME`/`ECHO_HOME`，不读取或改写用户配置。

## 部署边界

`echo server` 当前没有平台用户鉴权且共用一个 `AgentRuntime`。Demo 要求 Agent 端口只在私有
网络向 Echo Service 开放，并限制一个容器只有一个 active 用户。生产环境应增加 mTLS/受控反向
代理、进程级用户隔离、指标与多实例连接路由。文章中 ring buffer/sharded broadcast 的方向可用于
未来大规模广播，但本实现先采用有界 channel、慢消费者断开和按用户/容器隔离，保持行为可验证。

# HTTPS event gateways

When TLS terminates at a reverse proxy, set `ECHO_EVENTSTREAM_ALLOWEDORIGINS`
to the exact browser origins, separated by commas (for example
`https://echo.example.com:5443`). The default retains direct same-origin checks.
Forward the original `Host` including its port using Nginx `$http_host`.
Forwarded scheme headers alone do not bypass Origin checks.

