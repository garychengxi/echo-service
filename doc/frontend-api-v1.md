# Echo Service 前端 API 对接文档 v1

文档日期：2026-11-05。适用：当前 `echo-service` 单实例 Demo 实现。
本文根据实际 Go 路由、DTO、控制器和 Echo CLI 扩展源码编写，可交给前端直接对接。
`api-plan-v1.md` 包含尚未实现的规划，不能作为已上线接口清单。

## 目录

- [1. 接入约定](#1-接入约定)
- [2. 公共数据结构](#2-公共数据结构)
- [3. 登录与个人信息](#3-登录与个人信息)
- [4. 管理后台](#4-管理后台)
- [5. 容器与 Agent](#5-容器与-agent)
- [6. Project](#6-project)
- [7. Session](#7-session)
- [8. Turn 消息执行与审批](#8-turn-消息执行与审批)
- [9. WebSocket 事件](#9-websocket-事件)
- [10. 前端调用示例与联调流程](#10-前端调用示例与联调流程)
- [11. 错误码与重试](#11-错误码与重试)
- [12. 当前限制](#12-当前限制)

## 1. 接入约定

### 1.1 地址与鉴权

开发环境默认 HTTP Origin：`http://127.0.0.1:9090`。
业务 Base URL：`http://127.0.0.1:9090/api/v1`。
WebSocket：`ws://127.0.0.1:9090/api/v1/events?ticket=<ticket>`。
HTTPS 部署对应使用 `wss://`。下文接口路径均相对于 `/api/v1`。

公开接口仅 `POST /auth/login`、`POST /auth/refresh` 和使用单次票据的 `GET /events`。
其余接口均需有效 access token。所有 `/admin/*` 另需 `platformRole=admin`。
管理员身份不会自动授予其他用户 Project/Session 的读取权。

```http
Authorization: Bearer <accessToken>
Content-Type: application/json
Request-ID: <client-generated-request-id>
```

| Header | 必填 | 说明 |
| --- | --- | --- |
| Authorization | 受保护接口必填 | access token，不能用 refresh token 替代 |
| Content-Type | JSON body 时必填 | application/json |
| Request-ID | 建议 | 请求关联标识；出错时便于定位。部分创建响应未传此头时 requestId 为空 |
| Idempotency-Key | 指定创建接口必填 | Project 创建、Session 创建、fork；1..128 bytes。一次逻辑操作保持不变 |

JSON body 最大 1 MiB；不接受未知 body 字段和多段 JSON。不要传入 userId、remoteSessionId 或上游地址
来决定当前执行身份；平台根据 bearer、资源 ID 和容器分配关系确定目标。

当前服务没有配置通用跨域 CORS。浏览器开发建议用同源反向代理，将 `/api` 和 WebSocket 转发到后端，
并启用 WebSocket upgrade。事件 WS 检查 Origin 的 scheme/host 与请求一致；独立端口直连、
TLS 在代理终止后的 Origin 处理需由部署侧核对，不能假设任意前端域名可连接。
Electron renderer 也应通过项目既有安全 bridge/同源接入，不向 renderer 暴露上游容器地址。

### 1.2 统一 HTTP 响应

除 HTTP 204 和 WS upgrade 外，业务接口返回以下结构。HTTP 状态是成功与失败的主要判断依据。

成功示例（HTTP 200）：

```json
{
  "data": {"id": "resource-id"},
  "error": {"code": "", "requestId": "req-001", "statusCode": 0}
}
```

失败示例（HTTP 401）：

```json
{
  "data": null,
  "error": {
    "code": "InvalidToken",
    "requestId": "req-001",
    "message": "Token is invalid, expired or revoked",
    "statusCode": 401
  }
}
```

- 成功时 `error` 对象仍存在，`code=""`、`statusCode=0`；`message` 通常省略。
- HTTP 201 表示创建；202 表示执行/取消已受理，并不代表完成。
- HTTP 204 无响应体，禁止调用 `response.json()`。
- 后文各接口“返回”描述的是 envelope 内的 `data`。
- 未注册路径可能返回框架 404 文本；客户端须兼容非 JSON 错误体。
- 接口标注“无 body”时直接省略；不要给 DELETE/GET 拼接 JSON body。

### 1.3 分页、时间与 ID

分页请求：`?limit=50&offset=0`。limit 默认 50，合法 1..200；offset 默认 0，按记录偏移。
当前非法/超限值会回落默认，而不是返回 422。
分页响应：`{items:[],limit:50,offset:0}`；没有 `total`、`hasMore` 或 cursor。
可在本页数量小于 limit 时结束；等于 limit 时继续请求，可能得到一个空页。

ID 均视为不透明字符串，不解析格式、不复用远端 ID。时间以各字段定义为准：
Session/Turn/设备登录/审计为 Unix 毫秒；Project.createdAt、事件 timestamp、审批 expiresAt、
connectivity.retryAt 为 ISO 8601/RFC3339 字符串。不要对所有时间字段统一假设同一类型。

### 1.4 空数据和上游扩展

平台分页 Project/Session/用户/容器返回 `items:[]`。审计和容器分配列表当前可能返回 `items:null`，
前端统一按 `items ?? []` 处理。Agent 目录采用上游扩展的响应结构，**并非全部都是 items 数组**，
snake_case 字段保持原样。未知可选字段应忽略，缺失的敏感字段不要当成协议异常。

## 2. 公共数据结构

### 2.1 TypeScript 类型

```ts
export interface ApiEnvelope<T> {
  data: T;
  error: { code: string; requestId: string; message?: string; statusCode: number };
}
export interface Page<T> { items: T[] | null; limit: number; offset: number }
export interface User {
  id: string;
  username: string;
  displayName: string;
  platformRole: "admin" | "user";
  status: "active" | "disabled";
}
export interface Tokens {
  accessToken: string;
  refreshToken: string;
  tokenType: "Bearer";
  expiresIn: number; // 秒，默认 access 有效期约 1800 秒，以服务配置/返回为准
}
export interface Container {
  id: string;
  name: string;
  status: "enabled" | "disabled";
  workspaceRoots: string[]; // 容器中的绝对路径
  endpoint?: string; // 仅管理员 DTO 返回
}
export interface Assignment {
  id: string;
  userId: string;
  containerId: string;
  role: "owner";
  status: "active" | "revoked";
}
export interface Project {
  id: string;
  containerId: string;
  name: string;
  roots: { path: string }[];
  createdAt: string; // ISO 8601
}
export interface Session {
  id: string;
  containerId: string;
  projectId: string | null;
  title: string;
  cwd: string;
  modelId: string;
  createdAt: number; // Unix 毫秒
}
export type TurnState =
  | "running" | "awaiting_approval" | "cancelling"
  | "completed" | "failed" | "cancelled" | "unknown";
export interface Turn {
  id: string;
  sessionId: string;
  clientRequestId: string;
  state: TurnState;
  stopReason?: string;
  createdAt: number;
  finishedAt?: number; // 未完成时省略，不是 null
}
export interface AuthSession {
  id: string;
  deviceName: string;
  createdAt: number;
  expiresAt: number;
  current: boolean;
}
export interface Connectivity {
  state: "connected" | "connecting" | "disconnected" | "backoff";
  retryAt?: string;
}
```

`Session.modelId` 是平台当前摘要，目录选择成功后更新；并不是完整的模型目录。
`Turn.unknown` 表示服务无法确认远端结果，不能展示为“已失败且可以安全重发”。

### 2.2 权限字符串

普通用户：`profile:read`、`profile:write`、`assigned-container:use`。
管理员额外包含：`user:manage`、`container:manage`、`assignment:manage`、`audit:read`。
前端可用于菜单显示；资源级鉴权仍由服务端执行。

## 3. 登录与个人信息

### 3.1 POST /auth/login

权限：公开。成功 HTTP 200。请求：

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| username | string | 是 | 账号，ASCII 字母/数字/点/下划线/短横线；创建时 3..64 bytes；登录忽略大小写和首尾空格 |
| password | string | 是 | 创建密码时要求 12..256 bytes |
| deviceName | string | 否 | 设备标识，服务端最多保存 128 bytes |

```json
{"username":"alice","password":"<password>","deviceName":"Echo Desktop"}
```

返回 `{tokens:Tokens,user:User}`。不存在账号、密码错误、禁用账号统一返回
401 `InvalidCredentials`。登录有按来源与账号限流；429 应延迟后重试。

### 3.2 POST /auth/refresh

权限：公开，不需要 access token。请求 `{"refreshToken":"<refreshToken>"}`。
成功 HTTP 200，data 直接是 `Tokens`，**没有外层 tokens 字段**。

access 默认约 30 分钟，refresh 最长约 7 天；轮换不会延长当前登录会话的绝对有效期。
每次成功须同时替换 access/refresh token，旧 access 立即失效；重复使用旧 refresh 会撤销整个登录会话。
前端必须用共享 Promise 做刷新 single-flight，禁止多个 401 同时各刷一次。
成功刷新会关闭该用户前端 WS 和容器连接，因此需重新申请票据、连接、核对 turn 状态并恢复会话。
不要在执行消息过程中无条件定时刷新并自动 load；存在 active turn 时 load 会 409。

失败 401 `InvalidToken`：清理凭据回到登录。不要继续重试同一个 refresh。

### 3.3 GET /me

权限：登录。无 body。成功 200：

```json
{
  "user": {
    "id":"user-id","username":"alice","displayName":"Alice",
    "platformRole":"user","status":"active"
  },
  "permissions":["profile:read","profile:write","assigned-container:use"]
}
```

### 3.4 PATCH /me

请求 `{"displayName":"Alice"}`，displayName 最多 128 bytes，允许空字符串。
成功 200，返回与 GET /me 相同。只允许 displayName，不能提交 role/status/password。

### 3.5 PUT /me/password

请求 `{"oldPassword":"<old>","newPassword":"<new>"}`，newPassword 12..256 bytes。
成功 204，撤销自己的全部设备登录并关闭连接，前端清理凭据重新登录。
旧密码错误返回 401 `InvalidCredentials`，不应把它当作自动 refresh 的触发器。

### 3.6 POST /auth/logout；POST /auth/logout-all

无 body，两个接口都需要当前有效 bearer。成功 204。
前者撤销当前设备，后者撤销所有设备；都会保守关闭该用户所有传输连接。
已失效 token 再请求返回 401，前端仍可完成本地退出。

### 3.7 GET /me/auth-sessions

query：通用 limit/offset。成功 200，返回 `Page<AuthSession>`，只列仍有效设备登录。
不返回 token。记录的 expiresAt 为登录绝对有效期，并非当前 access token 到期时间。

### 3.8 DELETE /me/auth-sessions/{authSessionId}

无 body。只能撤销自己的设备登录，成功 204；不存在或不属于自己也返回 204，不泄露其他登录。
保守关闭该用户全部连接；若撤销的是 current，清理凭据；其他有效设备可以重新连接。

### 3.9 GET /me/containers

无 body/query。成功 200，返回 `{items:Container[]}`，只包含 active 分配且 enabled 的容器。
无分页，不返回 endpoint。空列表应提示联系管理员分配容器。

## 4. 管理后台

以下全部要求 admin。单用户允许多个容器，**每容器最多一个 active owner**。
登记容器不代表创建/启动容器；必须先由部署侧启动容器内的 `echo server`。

### 4.1 GET /admin/users

query：通用分页。成功 200：`Page<User>`。排序 created_at/id 升序；无搜索过滤参数。

### 4.2 POST /admin/users

请求：

```json
{"username":"alice","password":"<12..256-byte-password>","displayName":"Alice","platformRole":"user"}
```

username/password 必填；displayName 可选，最多 128 bytes；platformRole 可选，默认 user，
枚举 user/admin。成功 201：`User`。409 `UserConflict` 可能表示账号冲突。

### 4.3 GET /admin/users/{userId}

无 body。成功 200：`User`。不存在返回 404 `ResourceNotFound`。

### 4.4 PATCH /admin/users/{userId}

允许 `{displayName?:string,status?:"active"|"disabled",platformRole?:"admin"|"user"}`，
至少一个字段。成功 200：更新后的 `User`。不可修改 username/password。
角色或状态修改撤销该用户登录；更新也会关闭其连接。
禁用或降级最后一个 active admin 返回 409 `Conflict`。

### 4.5 GET /admin/containers

query：通用分页。成功 200：`Page<Container>`，含 endpoint。

### 4.6 POST /admin/containers

```json
{"name":"alice-agent","endpoint":"ws://10.0.0.10:4600","workspaceRoots":["/workspace"]}
```

| 字段 | 约束 |
| --- | --- |
| name | 必填，1..128 bytes |
| endpoint | 必填；ws/wss、private/loopback 字面 IP、显式端口；不支持 DNS、凭据、query、任意 path |
| workspaceRoots | 必填，1..16 个容器绝对路径；路径必须规范化，无空字符和多余 /../ |

成功 201：管理员 `Container`。不主动检查目录存在和上游可用性，使用 probe 单独检查。
409 `ContainerConflict` 可能表示重复 endpoint。

### 4.7 GET /admin/containers/{containerId}

成功 200：管理员 `Container`；404 资源不存在。

### 4.8 PATCH /admin/containers/{containerId}

请求 `{name?:string,status?:"enabled"|"disabled"}`，至少一个字段。
成功 200：更新后 `Container`。name 1..128 bytes。
当前不支持变更 endpoint/workspaceRoots；更新关闭该容器相关连接。

### 4.9 GET /admin/containers/{containerId}/users

成功 200：`{items:Assignment[]|null}`，包含 active/revoked 历史，非分页。
没有分配记录时可能为 null。

### 4.10 PUT /admin/containers/{containerId}/users/{userId}

请求 `{"role":"owner"}`。成功 200：`Assignment`；相同 active 分配重复调用返回原关系。
用户必须 active、容器必须 enabled。已有其他 owner 或存在已撤销历史返回 409 `Conflict`。
首版没有“恢复旧关系/换用户分配”流程，UI 不提供撤销后的重新分配按钮。

### 4.11 DELETE /admin/containers/{containerId}/users/{userId}

无 body。成功 204，撤销关系并关闭相关连接。此响应不证明远端正在运行的工具已停止。
首版没有强制清理工作区/迁移/重置接口。

### 4.12 POST /admin/containers/{containerId}/probe

无 body。成功 200：`{"ready":true}`。临时拨号并 initialize，随后关闭探测连接。
失败 503 `ContainerUnavailable`。它是一次观测，不代表下一次请求一定成功。

### 4.13 GET /admin/audit-events

query：通用分页，created_at 倒序。成功 200，data 如下，**当前字段名首字母大写**：

```json
{
  "items": [{
    "ID":"audit-id","ActorUserID":"admin-id","Action":"container.create",
    "ResourceID":"container-id","Outcome":"success","CreatedAt":1793865600000
  }],
  "limit":50,"offset":0
}
```

空 items 可能 null。当前不支持规划中的用户、动作、时间高级过滤，也不保证记录全部行为。

## 5. 容器与 Agent

除管理员接口外，本节均需登录且已分配当前 enabled 容器。

### 5.1 GET /containers/{containerId}/status

无 body。成功 200：

```json
{
  "container": {
    "id":"container-id","name":"alice-agent","status":"enabled",
    "workspaceRoots":["/workspace"]
  },
  "connectivity":{"state":"backoff","retryAt":"2026-11-05T08:00:00Z"}
}
```

state 为 connected/connecting/disconnected/backoff；retryAt 只在 backoff 返回。
这是服务持有的连接状态快照；GET 本身不会建连或探测。connected 不代表模型凭据有效。
backoff 到期后下一次显式业务请求才尝试拨号，没有后台自动重发消息。

### 5.2 GET /containers/{containerId}/models

成功 200：`{models:Model[]}`。上游字段保留 snake_case，例如：

```json
{"models":[{"model_title":"demo-model","model_id":"provider-model-id","model_name":"Demo","source":"system"}]}
```

示例仅展示选择器有关字段；扩展字段可能不同，允许 model_id/model_name 为 null。
当前 Echo ACP 的可选择 modelId 以模型目录的 **model_title** 为准，不直接拿供应商 model_id 发送。
返回中敏感字段会移除，不能依赖 has_api_key、base_url、token 相关字段存在。

### 5.3 GET /containers/{containerId}/agents

成功 200：`{builtin_agents:Agent[],custom_agents:Agent[]}`。当前默认结构：

```json
{
  "builtin_agents":[{
    "name":"Agent","is_default":true,"logo":null,"description":"默认 Agent",
    "content":"","tools":["terminal","file_editor","task_tracker","task_tool_set"],
    "dataset_set_ids":[],"mcp_servers":[]
  }],
  "custom_agents":[]
}
```

当前没有切换 Agent 或创建自定义 Agent 的 HTTP 接口。

### 5.4 GET /containers/{containerId}/skills

| Query | 类型 | 默认/说明 |
| --- | --- | --- |
| scope | string | 默认 user；user/project/enterprise/all |
| projectId | string | 可选；scope=project 时必填，必须属于当前用户和容器；cwd 从项目第一条 root 推导 |

成功 200：

```json
{
  "skills":[{
    "name":"example","content":"...","source":"/workspace/.echo/skills/example/SKILL.md",
    "description":"示例技能","scope":"project"
  }]
}
```

仅目录技能；插件技能不合并进这个列表。返回 source 是展示信息，不是本服务文件正文接口。

### 5.5 GET /containers/{containerId}/plugins

无 query/body。成功 200：`{status:"success",plugins:InstalledPlugin[]}`。
空示例 `{"status":"success","plugins":[]}`。
条目沿用上游 camelCase，常用字段：
id/name/description/marketplace/version（可 null）、enabled/ready/builtin/removable、
runtimeReason（可 null）、skills/mcp。额外兼容字段按可选项处理。
默认排除 echo-primary-runtime 内建插件；当前无安装、卸载、启停接口。

### 5.6 GET /containers/{containerId}/mcp-servers

成功 200：`{status:"success"|"error",mcpServers:Record<string,unknown>}`。
空示例 `{"status":"success","mcpServers":{}}`。
status=error 可能仍是 HTTP 200，表示目录中存在启用但探测异常的 MCP。
env/headers/凭据字段会移除；首版仅展示，不提供 MCP 配置编辑。

### 5.7 GET /containers/{containerId}/environment

固定请求容器 local 环境，无 query/body。成功 200 示例：

```json
{"shell":{"name":"sh","path":"/bin/sh"},"cwd":"file:///workspace/"}
```

cwd 是容器进程的工作目录 URI，不等于当前 Session.cwd。

### 5.8 GET /containers/{containerId}/agent-settings

成功 200：`{"mode":"llm-approve"}` 或 `{"mode":"always-ask"}`。
上游没有配置时映射为 llm-approve。只返回默认权限 mode，不返回 OAuth/模型密钥配置。

### 5.9 PATCH /containers/{containerId}/agent-settings

请求 `{"mode":"always-ask"}` 或 `{"mode":"llm-approve"}`。
成功 200：`{mode:string}`。其他值 422。
这是默认设置；网关新建/恢复 Session 时仍强制 always-ask。若要切换当前 Session，调用 Session mode 接口。

### 5.10 GET /containers/{containerId}/files

query `path` 为相对第一条 container.workspaceRoots 的目录路径，默认 `.`。
示例 `?path=src/components`。不支持 projectId/rootIndex 选择；绝对路径或 ../ 逃逸返回 422。

成功 200：

```json
{
  "path":"/workspace/src",
  "entries":[
    {"fileName":"main.go","isDirectory":false,"isFile":true},
    {"fileName":"components","isDirectory":true,"isFile":false}
  ]
}
```

仅当前目录直接子项，不递归、无文件正文/上传/下载/删除/diff 接口。

## 6. Project

Project ID 来自平台返回，不能使用容器内 remote project ID。

### 6.1 GET /containers/{containerId}/projects

query：通用分页。成功 200：`{items:Project[],source:"platformIndex",limit,offset}`。
只列平台建立过归属索引的项目，不自动导入容器已有未知项目。

### 6.2 POST /containers/{containerId}/projects

必填 header：Idempotency-Key。请求：

```json
{"name":"demo","roots":[{"path":"/workspace/demo"}]}
```

name 非空，最多 128 bytes；roots 为 1..16 个对象，path 必须是规范化容器绝对路径，
位于登记 workspaceRoots 内，且能通过容器端目录校验。
成功 201：`Project`。相同 key、相同 name/roots 已创建则 200 返回原 Project；
内容不同返回 409 `IdempotencyConflict`。
409 `ProjectIndexConflict` 表示远端可能已创建，只能用相同 key 和原 body 核对/重试。

### 6.3 GET /projects/{projectId}

无 body。成功 200：`{project:Project,runtime:Record<string,unknown>}`。
runtime 当前为脱敏的上游 `_project/read` 结果（通常含 project），不是平台 Project DTO；
内部远端 ID 不可用于 HTTP 路径。运行时额外字段不作为平台固定 schema。

### 6.4 PATCH /projects/{projectId}

请求 `{"name":"new-name"}`，非空，最多 128 bytes。成功 200：`Project`。
当前不允许编辑 roots/metadata 或排序。

### 6.5 DELETE /projects/{projectId}

无 body。成功 204。项目中存在 active turn 返回 409 `TurnAlreadyRunning`。
不删除文件或 Session 历史；平台已有 Session.projectId 变为 null。

## 7. Session

所有 Session 归属由平台索引决定。列表和 GET 不会自动 load，也不会主动创建上游连接。

### 7.1 GET /containers/{containerId}/sessions

query：通用分页，可选 `projectId` 过滤。
成功 200：`Page<Session>`。过滤结果为空返回空 items，不自动调用容器 session/list。

### 7.2 POST /containers/{containerId}/sessions

必填 header：Idempotency-Key。请求 `{"projectId":"project-id","cwd":"/workspace/demo"}`。
projectId/cwd 都可省略，可发送 `{}`：
有 projectId 时 cwd 默认项目第一条 root；否则默认容器第一条 workspace root。
cwd 必须在对应 roots 中，projectId 必须属于当前用户和同容器。

成功 201：`Session`；相同幂等 key 完成后重试返回 200 原 Session。
成功后已在当前上游连接中加载，可直接 prompt。默认 title="New session"、mode=always-ask。
已有未完成提交记录返回 409 `OperationOutcomeUnknown`，不得换 key 自动重新创建。
500 `SessionIndexFailed` 也可能意味着远端已创建，不安全自动重试。

### 7.3 GET /sessions/{sessionId}

成功 200：`Session`，只返回平台摘要，无消息历史和权限 mode 列表。
资源不存在、非本人所有或失去容器权限通常为 404，不应以此推断是权限还是不存在。

### 7.4 POST /sessions/{sessionId}/load

无 body。先建立事件 WS，再调用 load。
成功 200：`{"sessionId":"session-id","loaded":true}`；回放同时通过 WS 推送。
有 active turn 返回 409 `TurnAlreadyRunning`。连接丢失后必须 load 才能再次 prompt/改设置。
load 完成把 mode 强制设为 always-ask，客户端同步更新选择器。

回放事件可能早于 HTTP 返回，必须按 sessionId 路由。
`session.replay.started` 到 `session.replay.completed` 期间替换/重建历史投影，避免重复追加。
失败时可能只有 started 没有 completed，必须用 HTTP 错误结束 loading。

### 7.5 PATCH /sessions/{sessionId}

请求 `{"title":"项目分析"}`。非空且最多 **80 UTF-8 bytes**，不是 80 个中文字符。
成功 200：`Session`。active turn 存在时 409。

### 7.6 DELETE /sessions/{sessionId}

无 body。成功 204。active turn 存在时 409。
删除容器会话和平台摘要/turn 索引，不表示删除工作区文件。

### 7.7 POST /sessions/{sessionId}/fork

必填 header：Idempotency-Key。无 body。
成功 201：新的 `Session`；同 key 完成后 200 返回原结果。active turn 存在时 409。
首版只复制会话历史，不接受任意消息 ID、代码恢复或 cwd 参数。
新 Session title="Fork"，必须显式 load 才能 prompt。未知结果按 Session 创建相同规则处理。

### 7.8 PUT /sessions/{sessionId}/model

请求 `{"modelId":"demo-model"}`，modelId 必须对应上游可用模型（当前选择模型目录的 model_title）。
成功 200：`{"sessionId":"session-id","modelId":"demo-model"}`。
必须已 load 且无 active turn；未加载返回 409 `SessionNotLoaded`，忙返回 409 `TurnAlreadyRunning`。
上游拒绝模型可能被映射为 502 `RemoteError`，当前不会逐项透传上游错误详情。

### 7.9 PUT /sessions/{sessionId}/mode

请求 `{"modeId":"always-ask"}` 或 `{"modeId":"llm-approve"}`。
成功 200：`{sessionId,modeId}`；要求已 load 且无 active turn。
always-approve 或其他 mode 返回 403 `PermissionDenied`，不要显示为可选项。
mode 不包含在 Session GET 中，前端维护本次设置结果，并在 load 后重置为 always-ask。

## 8. Turn 消息执行与审批

### 8.1 POST /sessions/{sessionId}/turns

权限：本人已加载会话。请求：

```json
{
  "clientRequestId":"client-turn-001",
  "content":[{"type":"text","text":"分析这个项目的入口"}],
  "collaborationMode":"default"
}
```

| 字段 | 类型 | 必填 | 约束 |
| --- | --- | --- | --- |
| clientRequestId | string | 是 | 1..128 bytes；同一逻辑消息保持不变，作用域是当前 Session |
| content | array | 是 | 1..32 项；当前仅允许非空 text，总文本最多 256 KiB |
| content[].type | string | 是 | 固定 text |
| content[].text | string | 是 | 非空 |
| collaborationMode | string | 否 | 空/default/plan；plan 要求 Session 已选择模型 |

首次成功 202：`Turn`，初始 state=running。同 clientRequestId、完全相同 payload 返回 200 原 Turn，
不同内容 409 `IdempotencyConflict`。建议保存并复用完整原始请求，省略 default 与显式 default
也可能产生不同摘要。不要将 Idempotency-Key 当作这个接口的消息幂等字段。

同一 Session 只允许一个 active turn（running/awaiting_approval/cancelling）。
HTTP 返回不是模型完成；流式文本、工具、审批通过事件到达。事件可能先于 HTTP response，
前端按 turnId/clientRequestId 关联，避免重插消息。

### 8.2 GET /turns/{turnId}

成功 200：`Turn`。用于轮询和断线后核对；不包含消息正文或待审批列表。

| state | UI 建议 |
| --- | --- |
| running | 执行中，允许停止 |
| awaiting_approval | 等待审批，不能展示为完成 |
| cancelling | 正在停止，等待最终确认 |
| completed | 正常完成 |
| failed | 明确失败，展示重试操作但新执行必须由用户决定 |
| cancelled | 上游明确确认取消 |
| unknown | 结果不明；禁止静默自动重发，提示核对远端 |

当前无 queued 状态。服务重启会把遗留 active turn 标为 unknown。
stopReason 是上游字符串，允许未知值，不能只按 end_turn 判断状态。

### 8.3 POST /turns/{turnId}/cancel

无 body。成功 202：`{"turnId":"turn-id","state":"cancelling"}`。
只能将 running/awaiting_approval 变为 cancelling，重复或已终态返回 409 `TurnNotActive`。
只有原上游连接仍可用才能取消；失去原连接返回 409 `TurnOutcomeUnknown`。
502 时也不能假设工具已停止。最终状态通过事件或 GET turn 获取。

### 8.4 POST /turns/{turnId}/approvals/{approvalId}/response

请求 `{"optionId":"allow-once"}`；**optionId 必须原样取自 approval.requested.options**，
示例值不是固定常量。成功 204，再等待 approval.resolved 和后续 turn 事件。
拒绝也提交上游给出的 reject 选项，不能自行构造 ACP JSON-RPC 回复。

审批过期/已处理/连接失效返回 409 `ApprovalExpired`；
非法或过期选项返回 422 `InvalidApprovalOption`。
审批默认五分钟后服务端取消；当前超时没有单独 approval.expired 事件，UI 可按 expiresAt 关闭交互
并查询 Turn。终态到达时清理该 turn 的所有待审批 UI。

## 9. WebSocket 事件

### 9.1 POST /ws-tickets

权限：有效 bearer 且拥有容器。请求 `{"containerId":"container-id"}`。
成功 200：`{"ticket":"<single-use-ticket>","expiresIn":30}`。
每次连接必须取新票据，不保存到日志或持久化；票据不能用于 HTTP bearer。
过期、消费过、token 失效或关系被撤销时握手会失败。

### 9.2 GET /events?ticket={ticket}

成功：HTTP 101 Upgrade。握手失败 401 `InvalidTicket`。
协议是业务 JSON 事件，不是 ACP JSON-RPC；前端不向此 socket 发送 prompt 或审批。
默认浏览器 WebSocket 自动处理 Ping/Pong，不另发应用 heartbeat 消息。

首条消息是独立握手对象，**不使用业务事件信封**：

```json
{"type":"connection.ready","containerId":"container-id","requiresLoad":true}
```

### 9.3 事件信封和类型

```ts
export interface ServiceEvent {
  type: string;
  eventId: string;
  streamId: string;
  seq: number;
  containerId: string;
  sessionId?: string;
  turnId?: string;
  timestamp: string; // ISO 8601
  payload: unknown;  // 无内容事件可能为 null
}
```

seq 在当前服务实例全局递增；其他用户的事件可能造成正常间隙，不能据此判丢包。
streamId 改变表示服务实例事件流重建。当前不支持 afterSeq/replay buffer。

| type | payload | 前端处理 |
| --- | --- | --- |
| connection.ready | 特殊握手对象，见上 | 连接可用，恢复已选 Session |
| session.replay.started | null | 对目标 Session 开始历史重建 |
| session.replay.completed | null | 结束回放、解除回放 loading |
| session.update | 上游更新参数，去掉远端 sessionId；含 update/_meta 等 | 根据 update.sessionUpdate 更新文字/推理/工具/差异等 |
| approval.requested | approvalId/options/toolCall/expiresAt | 展示选项与工具参数，保持 turn 未完成 |
| approval.resolved | {approvalId} | 关闭对应审批 |
| turn.started | null | 正在执行 |
| turn.completed | {state,stopReason} | 正常终态 |
| turn.failed | {state,stopReason} | 失败终态 |
| turn.cancelled | {state,stopReason} | 已确认取消 |
| turn.unknown | {state,stopReason} | 结果不明，禁止自动重发 |
| stream.reset | {reason:"upstreamDisconnected",requiresLoad:true} | 上游丢失；核对 turn 后恢复 |

session.update 示例：

```json
{
  "type":"session.update","eventId":"event-id","streamId":"stream-id","seq":12,
  "containerId":"container-id","sessionId":"platform-session-id","turnId":"platform-turn-id",
  "timestamp":"2026-11-05T08:00:00Z",
  "payload":{
    "update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"正在分析"}},
    "_meta":{"request_id":"platform-turn-id"}
  }
}
```

payload 的扩展字段和更新类型以后可能增加。当前网关也会将可识别的 codeflow/x.ai 通知包装成
session.update，并不承诺每个 payload 都有 update；未知类型应忽略/保留而非使整条连接报错。
历史回放可能没有已关联 turnId，以外层 sessionId 定位；不能拿内层远端 ID 调 HTTP API。

审批示例：

```json
{
  "type":"approval.requested","eventId":"event-id","streamId":"stream-id","seq":13,
  "containerId":"container-id","sessionId":"session-id","turnId":"turn-id",
  "timestamp":"2026-11-05T08:00:00Z",
  "payload":{
    "approvalId":"approval-id",
    "options":[
      {"optionId":"allow-once","name":"允许一次","kind":"allow_once"},
      {"optionId":"reject-once","name":"拒绝","kind":"reject_once"}
    ],
    "toolCall":{"toolCallId":"tool-call-id","title":"读取文件","rawInput":{"path":"src/main.go"}},
    "expiresAt":"2026-11-05T08:05:00Z"
  }
}
```

### 9.4 断线与恢复规则

- 连接绑定一个用户/容器，每 scope 最多 4 个订阅；每订阅缓冲 16 条，payload 上限 1 MiB。
- 慢客户端或超限会断开，不会静默跳过审批。断开原因不保证有业务 close code。
- WS 客户端断开与容器上游断开是两件事；不要把两者都标成“消息已取消”。
- 客户端重连采用有上限的退避，每次申请新票据；恢复前先 GET 当前 Turn。
- 无 active turn 时建立 WS，再 load Session；清理旧投影后重放，避免重复。
- active turn 尚在执行时 load 返回 409。继续订阅、查询 Turn；终态后再 load。
- 当前无“重取待审批列表”接口；若审批事件在断线期间丢失，不能凭空重建审批 ID。
  展示恢复受限提示、等待超时/终态或由用户取消，不能自动放行。
- token 到期、refresh、退出、禁用、撤权都会使连接关闭。先处理鉴权，再重订阅。
- 收到 stream.reset 不代表服务端已停止执行，当前在途结果可能成为 unknown。

## 10. 前端调用示例与联调流程

### 10.1 fetch 封装示例

示例展示 envelope/204/非 JSON 错误处理；生产项目按现有状态管理和凭据存储接入。

```ts
type ApiErrorDetail = { code: string; message?: string; requestId?: string };
class ApiError extends Error {
  constructor(public status: number, public detail: ApiErrorDetail) {
    super(detail.message ?? detail.code);
  }
}
async function request<T>(
  baseUrl: string, path: string,
  options: { method?: string; token?: string; body?: unknown; idempotencyKey?: string } = {}
): Promise<T | undefined> {
  const headers = new Headers({ "Request-ID": crypto.randomUUID() });
  if (options.token) headers.set("Authorization", `Bearer ${options.token}`);
  if (options.body !== undefined) headers.set("Content-Type", "application/json");
  if (options.idempotencyKey) headers.set("Idempotency-Key", options.idempotencyKey);
  const response = await fetch(baseUrl + path, {
    method: options.method ?? "GET", headers,
    body: options.body === undefined ? undefined : JSON.stringify(options.body)
  });
  if (response.status === 204) return undefined;
  const text = await response.text();
  let envelope: ApiEnvelope<T> | undefined;
  try { envelope = JSON.parse(text) as ApiEnvelope<T>; } catch { /* 非 JSON 错误体 */ }
  if (!response.ok) {
    throw new ApiError(response.status, envelope?.error ?? {
      code: "UnexpectedResponse", message: response.statusText
    });
  }
  if (!envelope) throw new Error("Invalid API JSON response");
  return envelope.data;
}
```

只对明确的 `Unauthorized/InvalidToken` 执行共享 refresh 流程；不要对密码错误、审批错误或
任意 401 都自动 refresh。修改请求不能通用“失败后重试一次”，需按幂等规则处理。

### 10.2 核心调用顺序

1. 管理员通过 CLI 初始化账号，再使用 admin API 创建用户、登记容器、分配 owner。
2. 用户 login，安全保存 tokens，再 GET /me、GET /me/containers。
3. 选容器，读取模型/项目/Session 目录。
4. POST /ws-tickets，连接 events；等待 connection.ready。
5. 新建项目和 Session 分别生成独立 Idempotency-Key；或选择已有 Session 并 POST load。
6. 根据 load/new 结果设置当前 Session.mode=always-ask；需要时调用 model/mode 接口。
7. 生成并保存完整 prompt 请求和 clientRequestId，POST turns，处理 202/200。
8. 处理流式文本/工具、审批；停止通过 HTTP cancel；终态用事件和 GET Turn 核对。
9. 切换已有会话先 load；重连/refresh 后按第 9.4 节恢复。

### 10.3 cURL 示例

从 echo-service 可访问的开发机执行；示例不会提供默认管理员密码。

```sh
# 登录
curl -sS http://127.0.0.1:9090/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"alice","password":"<password>","deviceName":"frontend-dev"}'

# 使用上一步 data.tokens.accessToken
curl -sS http://127.0.0.1:9090/api/v1/me \
  -H 'Authorization: Bearer <accessToken>'

# 项目创建
curl -sS http://127.0.0.1:9090/api/v1/containers/<containerId>/projects \
  -H 'Authorization: Bearer <accessToken>' -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: frontend-project-001' \
  -d '{"name":"demo","roots":[{"path":"/workspace/demo"}]}'

# 会话创建
curl -sS http://127.0.0.1:9090/api/v1/containers/<containerId>/sessions \
  -H 'Authorization: Bearer <accessToken>' -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: frontend-session-001' -d '{"projectId":"<projectId>"}'

# 发消息前应先接好事件 WebSocket
curl -sS http://127.0.0.1:9090/api/v1/sessions/<sessionId>/turns \
  -H 'Authorization: Bearer <accessToken>' -H 'Content-Type: application/json' \
  -d '{"clientRequestId":"message-001","content":[{"type":"text","text":"你好"}]}'
```

### 10.4 最小联调验收

| 场景 | 预期 |
| --- | --- |
| 未登录访问 /me | 401 Unauthorized |
| 普通用户访问 admin/users | 403 PermissionDenied |
| 无容器分配 | /me/containers 空数组 |
| 访问其他用户 Session | 404 |
| 相同 key 创建 Session | 首次 201；已完成重复 200 原资源 |
| 相同 clientRequestId 不同消息 | 409 IdempotencyConflict |
| active turn 再发一条新消息 | 409 TurnAlreadyRunning |
| WS 重用票据 | 握手 401 |
| 等待审批 | awaiting_approval，不显示完成 |
| 点击停止 | cancelling，确认后 cancelled |
| refresh/撤权 | 旧连接关闭，旧 token/资源访问失败 |
| 204 响应 | 前端不解析 JSON |

## 11. 错误码与重试

错误 message 为诊断英文，界面中文提示建议按 code 映射；不向用户展示底层数据库或凭据。
下表列出当前已实现 code。未列错误仍应按 HTTP 状态和通用兜底展示。

| HTTP | code | 前端处理 |
| --- | --- | --- |
| 401 | Unauthorized / InvalidToken | 按鉴权状态 single-flight refresh 或重新登录 |
| 401 | InvalidCredentials | 登录/旧密码错误，不自动刷新 |
| 401 | InvalidTicket | 重新取票据；检查 token/容器权限 |
| 403 | PermissionDenied | 隐藏/禁用无权功能或受限 mode |
| 404 | ResourceNotFound | 资源无权访问或不存在，刷新列表 |
| 409 | Conflict | 关系/最后一个管理员等业务冲突 |
| 409 | UserConflict / ContainerConflict | 检查重复账号或 endpoint |
| 409 | IdempotencyConflict | 保留原请求，不用相同 key 提交不同内容 |
| 409 | ProjectIndexConflict | 原 key + 原 body 核对；不换 key |
| 409 | OperationOutcomeUnknown | 保留请求，人工核对，不自动重新创建 |
| 409 | TurnAlreadyRunning | 保持当前执行 UI，等待终态 |
| 409 | SessionNotLoaded | 无 active turn 时重新 load |
| 409 | TurnNotActive | GET turn 刷新状态 |
| 409 | TurnOutcomeUnknown | 原连接丢失，取消结果不明 |
| 409 | ApprovalExpired | 关闭失效审批，查询 Turn |
| 422 | ValidationFailed | 校验字段/大小/路径，修正后重新提交 |
| 422 | InvalidApprovalOption | 使用服务提供的合法 optionId，或审批已过期 |
| 429 | RateLimited | 延迟重试，禁止密集循环 |
| 500 | InternalError | 展示 requestId；修改操作结果需核对 |
| 500 | SessionIndexFailed | 可能已远端创建，不自动重发 |
| 501 | CapabilityUnsupported | 容器不支持该功能，禁用入口 |
| 502 | RemoteError / RemoteProtocolError | 容器错误；修改类操作不能假设失败即未执行 |
| 503 | ContainerUnavailable / AuthenticationUnavailable | 稍后重试安全查询，读取连接 backoff 状态 |
| 504 | RemoteTimeout | 修改类结果可能未知，不能盲目重发 |

## 12. 当前限制

- 本文仅覆盖当前实现，不包含规划中的插件安装/市场、文件编辑上传、项目排序/roots 修改、
  容器重置再分配、管理员临时密码重置、模型目录刷新和原始 ACP 透传。
- 当前无 HTTP 消息历史列表、待审批列表、operation 查询/自动对账接口；历史通过 load + WS 重放。
- 登录 token 通过 JSON 返回，无 refresh Cookie，不配置默认生产跨域规则。
- 当前只保证单实例连接归属；Redis 接入不自动提供多实例连接路由。
- /livez、/healthz、/readyz 为根路径健康检查，不带 /api/v1。
- 兼容的 /echo-service/api/v1/GetServiceInfo 仅服务信息，前端业务使用本文 /api/v1。
- /echo-service/gateway/v1/* 的 OpenAI 兼容转发仍返回 501 GatewayNotImplemented。


### Remote desktop metadata

Container DTOs include `executionType: "remote"` and optional `webUrl`, a public
HTTP(S) remote desktop URL. `POST /admin/containers` and
`PATCH /admin/containers/{id}` accept `webUrl`; an empty string clears it.
Only administrators configure it. URLs with credentials, query strings or
fragments are rejected. The internal Agent `endpoint` remains admin-only.
Session and Project DTOs also include `executionType: "remote"`: Service owns
remote container execution; this is independent of the CLI session category.
Clients show the computer button for remote execution with a configured
`webUrl`, and open the page in a workspace browser tab. No Service tokens are
sent to that page. Frame permission and trusted HTTPS are deployment concerns.
