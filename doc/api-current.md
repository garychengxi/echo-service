# 当前实现的 API 合同

前端字段说明、完整请求/响应、错误码与调用示例见 [前端 API 对接文档](frontend-api-v1.md)。

本文件描述可运行的单实例 Demo。`api-plan-v1.md` 保留完整目标规划；如有差异，以本文和实现为准。
所有业务路径前缀 `/api/v1`。HTTP 使用 `Authorization: Bearer <accessToken>`，JSON body 限制 1 MiB，
字段严格白名单；列表当前采用 `limit`/`offset`，默认 50，limit 上限 200。

## 认证、资料和管理

| 方法 | 路径 | 请求/结果 |
| --- | --- | --- |
| POST | `/auth/login` | `{username,password,deviceName?}` -> `{tokens:{accessToken,refreshToken,tokenType,expiresIn},user}` |
| POST | `/auth/refresh` | `{refreshToken}` -> 新 tokens；轮换后关闭该用户连接，需重新订阅/load |
| POST | `/auth/logout`、`/auth/logout-all` | 撤销登录；204 |
| GET/PATCH | `/me` | 用户与 permissions；PATCH 仅 `{displayName}` |
| PUT | `/me/password` | `{oldPassword,newPassword}`；成功撤销所有登录，204 |
| GET | `/me/auth-sessions` | 自己仍有效的设备登录；不返回 token |
| DELETE | `/me/auth-sessions/{authSessionId}` | 撤销指定设备，204；保守关闭该用户所有传输连接 |
| GET | `/me/containers` | 已分配的 enabled 容器，不返回 endpoint |
| GET/POST | `/admin/users` | 分页；创建 `{username,password,displayName?,platformRole?}` |
| GET/PATCH | `/admin/users/{userId}` | 详情；允许 displayName/status/platformRole；保留最后一个 active admin |
| GET/POST | `/admin/containers` | 分页；登记 `{name,endpoint,workspaceRoots:[absolutePath]}` |
| GET/PATCH | `/admin/containers/{containerId}` | 详情；当前仅修改 name/status，不允许换 endpoint/roots |
| GET | `/admin/containers/{containerId}/users` | 分配关系 |
| PUT | `/admin/containers/{containerId}/users/{userId}` | `{role:"owner"}`，单 active owner |
| DELETE | `/admin/containers/{containerId}/users/{userId}` | 撤销并关闭订阅/上游连接；204 |
| POST | `/admin/containers/{containerId}/probe` | 临时连接并 initialize；不保留 probe 连接 |
| GET | `/admin/audit-events` | 分页审计记录；当前不支持规划中的高级过滤 |

当前不发浏览器 refresh Cookie；token 交付模式用于 native/桌面客户端，客户端须使用安全凭据存储。
重复 logout 仍需有效 bearer，已撤销 token 返回 401。

## 项目、会话与执行

| 方法 | 路径 | 请求/结果 |
| --- | --- | --- |
| GET | `/containers/{c}/projects` | 平台归属索引，返回 `source:platformIndex` |
| POST | `/containers/{c}/projects` | `{name,roots:[{path}]}`；必须 Idempotency-Key；201 |
| GET | `/projects/{p}` | 平台索引与受控远端读取结果 |
| PATCH | `/projects/{p}` | 当前仅 `{name}` |
| DELETE | `/projects/{p}` | 有 active turn 返回 409；不删除文件和会话历史 |
| GET | `/containers/{c}/sessions?projectId=...` | 自己的会话索引 |
| POST | `/containers/{c}/sessions` | `{projectId?,cwd?}`；必须 Idempotency-Key；201 |
| GET | `/sessions/{s}` | 平台摘要；不隐式 load |
| POST | `/sessions/{s}/load` | 订阅中回放；完成后 HTTP 200；有 active turn 返回 409 |
| PATCH | `/sessions/{s}` | `{title}`，1..80 UTF-8 bytes |
| DELETE | `/sessions/{s}` | 无 active turn 才能删除；204 |
| POST | `/sessions/{s}/fork` | 无 active turn；必须 Idempotency-Key；201；新会话需 load |
| PUT | `/sessions/{s}/model` | `{modelId}`；必须已 load 且无 active turn；上游校验模型 |
| PUT | `/sessions/{s}/mode` | `{modeId:"always-ask"或"llm-approve"}` |
| POST | `/sessions/{s}/turns` | 异步 prompt，见下文；202 |
| GET | `/turns/{t}` | turn 及状态 |
| POST | `/turns/{t}/cancel` | ACP notification；202/cancelling，等待确认，不提前标为 cancelled |
| POST | `/turns/{t}/approvals/{a}/response` | `{optionId}`，必须是上游公布选项；204 |

prompt 示例：

```json
{
  "clientRequestId": "unique-client-turn-001",
  "content": [{"type": "text", "text": "检查这个项目的入口"}],
  "collaborationMode": "default"
}
```

首版只支持 text，最多 32 part/256 KiB；可选 collaborationMode 为 default/plan。Plan 要求已选模型。
同 session/clientRequestId、相同 payload 返回原 turn；payload 不同返回 409。一个 session 最多一个
active turn。数据库保存稳定 turn ID 和内容摘要，不保存 prompt 正文；正文由容器会话持久化。

Session new/fork 在上游执行前落库 operation reservation；相同幂等 key 已完成则返回原资源，结果未知
则 409 `OperationOutcomeUnknown`，不得换 key 自动重试。当前没有自动对账/导入未知远端会话接口，需
管理员核对数据库和容器。服务启动后遗留 active turn 标为 unknown，不冒充完成。

所有资源按 user/container/assignment 校验；admin 身份不隐式允许读取别人的会话。首版不允许撤销后
重新分配同一容器，需未来显式清理/重置流程，防止旧工作区泄漏。

## Agent 与目录

| 方法 | 路径 | 当前行为 |
| --- | --- | --- |
| GET | `/containers/{c}/status` | 登记信息；连接状态为 connected/connecting/disconnected/backoff；backoff 可带 retryAt |
| GET | `/containers/{c}/models`、`agents` | 受控只读扩展 |
| GET | `/containers/{c}/skills?scope=user` | scope 可 user/project/enterprise/all；project 必须提供授权 projectId |
| GET | `/containers/{c}/plugins` | 已安装插件，当前不提供安装/卸载/启停 |
| GET | `/containers/{c}/mcp-servers` | 递归剔除 env/headers/token/password 等敏感字段 |
| GET | `/containers/{c}/environment` | 固定 local environment |
| GET/PATCH | `/containers/{c}/agent-settings` | 仅默认 mode；不开放 OAuth/sync/provider 设置 |
| GET | `/containers/{c}/files?path=src` | 第一条登记 root 内的相对路径，直接子项，禁止 `..` 逃逸；不是任意文件读写 |

目录的字面路径校验不能替代容器文件系统隔离，也不能保证阻止容器内部 symlink 指向 root 外。
因此要求每用户独立受限容器，不把 root 当作安全沙箱。

## 前端 WebSocket

1. 带 bearer POST `/ws-tickets`，body `{containerId}`，返回 `{ticket,expiresIn:30}`。
2. 连接 `/events?ticket=...`，票据单次消费；只绑定一个用户和容器。
3. 收到 `connection.ready`，`requiresLoad:true`；已有会话通过 load 重建本地投影。
4. 接收 `session.update`、`approval.requested/resolved`、`turn.started/completed/failed/cancelled/unknown`
   和 `stream.reset`。每条业务事件带 sessionId/turnId（适用时）、streamId、seq 和 timestamp。

事件 payload 为受控 ACP 更新，客户端按当前流维护投影；seq 是当前服务实例全局递增，其他 scope 会
造成正常间隙，不应把任意 seq 间隙当作丢包。首版没有 replay buffer/afterSeq 恢复。每 scope 最多四条
订阅，每订阅队列 16 条，单事件 1 MiB；超限或慢客户端断开，必须重新连接/load，不静默丢审批。
审批默认五分钟超时取消，只能在原上游连接回复原 RPC ID。断线不会自动迁移审批。

## 当前不提供

管理员临时密码强制重置、容器重置/再分配、项目 roots 编辑/排序、插件目录与安装、文件上传/正文编辑、
模型目录刷新、原始 ACP 透传、OpenAI 兼容转发、Redis 适配器、多实例路由和持续性能指标。
