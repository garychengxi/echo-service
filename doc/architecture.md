# Echo Service 架构

## 参考基线

只读参考：`/Users/chengxi/Documents/workspace/cestc/codeflow-service/codeflow-service-be`。

保留 Go 1.24.3、精确的 `server-go`/`logger-go` 版本、`cmd/server`、`cmd/cli`、`service`、
`internal/app`、`internal/client`、`internal/table`、`sdk`、TOML 配置和 `host.Service`
生命周期。没有复制 CodeFlow 业务域、AuthCenter、MinIO、LiteLLM 或动态重载逻辑。

## 分层

```text
HTTP / WebSocket controller
  -> control application service
     -> auth + directory(cache decorator) + SQL repository
     -> agentconn.Manager
        -> one ACP WebSocket per user/assignment/container
           -> echo server in the assigned container
```

- SQLite 是用户、token、容器、分配关系和平台资源归属的事实源。
- `cache.Store` 是非权威元数据缓存端口；当前为并发安全、TTL、有上限的内存实现。
- 容器 Echo 是 Project、Session 内容、技能、插件、文件和模型能力的事实源。
- 平台只返回自己建立过归属索引的 Project/Session，不暴露容器内未知资源。

## 鉴权和缓存正确性

access/refresh 均为 256-bit 随机不透明 token，数据库只存 SHA-256。refresh 单次使用并旋转；
重放撤销整个 auth session。每次 HTTP 请求查 token 表；前端事件发送前也按具体 access token ID
重新校验。用户禁用、密码修改、角色修改、退出和关系撤销都会关闭相关订阅及上游连接。

用户与容器元数据可缓存，PasswordHash 在写缓存前清除。关系和 token 不缓存。更新先提交 SQLite，
再失效缓存；如果未来 Redis 失效失败，当前实例切换为 DB bypass，避免陈旧值影响显示。多实例 Redis
实现还应使用版本化 key 或可靠失效广播。

## ACP 连接管理

`agentconn.Manager` 以 `(userId, assignmentId, containerId)` 为 key：

- 同 key 并发拨号 single-flight；不同 key 的网络拨号不持有全局锁。
- 失败按 key 指数退避（1 秒至 30 秒），并发失败共享同一结果，避免故障容器触发重复拨号风暴。
- 每个连接一个 reader、一个 writer；业务 goroutine 只向有界写队列提交消息。
- JSON-RPC request ID 只路由 RPC；ACP session ID 和 turn `_meta.request_id` 分开建模。
- server request 只允许 `session/request_permission`；未知 request 返回 MethodNotFound。
- prompt、审批和其他有副作用操作不因断线自动重试。
- endpoint 只接受带端口的 private/loopback IP `ws/wss`，禁止 DNS、代理、userinfo、query、
  redirect 和 link-local metadata 地址。

前端事件 Hub 按 `(userId, containerId)` 隔离，限制 scope 数、每 scope 订阅数、每订阅队列和
单事件大小；慢客户端会断开。当前没有事件 replay buffer，连接建立时返回 `requiresLoad: true`，
客户端需通过 `session/load` 恢复。后续多实例要引入 connection-owner registry 和定向路由，而
不是让任意实例同时连接同一用户容器。

## 跨系统一致性

SQLite 事务不能包住远端 ACP。Project 创建使用客户端 Idempotency-Key 派生稳定平台 ID，并把同一
稳定 key 发送给 CLI。Session new/fork 没有远端幂等契约，因此先写 `operations` reservation；
超时或断线后返回 outcome unknown，同 key 不自动重发。prompt 使用
`(sessionId, clientRequestId)` 唯一约束和稳定 turn ID，重复请求只返回原 turn。

服务启动时把遗留的 running/awaiting_approval/cancelling turn 标记为 unknown，绝不伪造完成状态。

## 权限策略

当前 Echo 容器共享 AgentRuntime，所以数据库只允许一个 active owner。新建/恢复会话后，服务强制
把 mode 设为 `always-ask`；用户可显式改为 `llm-approve`，不开放 `always-approve`。Agent
settings API 只投影/修改默认 mode，不开放 provider base URL、OAuth、sync API key 或原始扩展 RPC。

## 明确未实现

- OpenAI 兼容 Gateway 转发、模型密钥、配额与计费。
- 容器编排、自动创建、跨用户重置/重新分配和工作区清理。
- Redis 实现、多实例连接归属、事件回放、熔断指标和分布式限流。
- 插件安装/卸载、文件读写/上传、任意 ACP passthrough。

这些边界不会被现有接口伪装成已实现能力。

