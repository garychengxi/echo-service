# Agent Skill and MCP management

These additions use the existing authenticated `/api/v1` gateway and the container's existing ACP connection. An active owner assignment is required; administrator identity alone grants no access. Deploy this Service version before the corresponding Web client.

## User skills

| Method | Path (relative to /api/v1) | JSON body | Result data |
| --- | --- | --- | --- |
| GET | /containers/{id}/skills?scope=user | none | Existing `{skills:[{name,description,content,scope,source}]}` |
| POST | /containers/{id}/skills | `{name,description,instruction}` | 201 `{name}` |
| PUT | /containers/{id}/skills/{name} | `{description,instruction}` | 200 `{name}` |
| DELETE | /containers/{id}/skills/{name} | none | 204 |

Names are 1–64 Unicode letters/numbers, hyphens or underscores. Description is nonblank, at most 200 characters; instruction is nonblank, at most 80,000 characters. Writes are user-scoped. Names cannot change on edit. Duplicate creation returns 409. Missing/non-user skills return 404; ambiguous runtime identities return 409.
The Service resolves delete source paths from the Agent catalog; clients cannot supply paths, cwd or scope.
The existing project/enterprise read contracts remain unchanged. This is directory Skill CRUD, not plugin-skill editing.

Confirmed ACP mappings: `_add_skill` uses content/scope=user; `_edit_skill` uses instruction/old_scope=user/new_scope=user; `_delete_skill` uses the resolved source/scope=user.

## MCP

| Method | Path | JSON body | Result data |
| --- | --- | --- | --- |
| GET | /containers/{id}/mcp-servers | none | `{mcpServers:{key:entry}}` |
| POST | /containers/{id}/mcp-servers | `{config:"<JSON string containing one mcpServers entry>"}` | Updated public catalog |
| PUT | /containers/{id}/mcp-servers/{key}/enabled | `{enabled:boolean}` | Updated public catalog |
| POST | /containers/{id}/mcp-servers/{key}/test | none | Updated public catalog |
| DELETE | /containers/{id}/mcp-servers/{key} | none | 204 |

An entry contains key, source (user/sys), enabled, status (success/error/null), public tool names, and config/raw_config containing only a transport hint. `hasProtectedConfig:true` means configuration values are deliberately not exported: commands, arguments and URLs can contain credentials too. Raw Agent error text is never returned. Test HTTP 200 does not imply connected: inspect that server's status.

Save accepts stdio (nonblank command; optional string-array args), http or sse (HTTP(S) URL without URL userinfo). Optional env and headers are string maps. The outer request accepts no source field; saves always use source=user. System entries can be enabled/tested but not overwritten or deleted.

Updating replaces ordinary configuration fields. Omitted env, headers and token/secret/password/API-key fields are retained from the current raw runtime configuration, including omitted entries inside env/headers. Supplied credential fields replace their values. To remove a retained credential entirely, delete and recreate that MCP explicitly. The UI requires a complete command/URL when editing because these values are not exported. Do not paste catalog responses as replacement configurations.

Confirmed ACP methods: `_list_mcp`, `_edit_mcp`, `_delete_mcp`, `_set_mcp_enabled`, `_test_mcp`.

## Error and execution semantics

Requests retain the existing 1 MiB body bound, unknown-field rejection, access checks, per-user command serialization and access/revocation lock. Audit records contain operation, container and success/failure, not skill instructions or MCP config.
Unsupported Agent methods return 501 CapabilityUnsupported; malformed Agent catalogs return 502 RemoteProtocolError. Mutation failures/timeouts may have an unknown outcome: inspect current state before retrying; no exactly-once or rollback promise is made.
Credentials submitted in a save body travel only to the authorized Agent. Use HTTPS in deployment. Never log request bodies.

Configuration is managed by the same Agent runtime used by conversations, not the Service filesystem or browser storage. New/load sessions use that runtime's configuration; existing-session hot reload follows the Agent version, not a new Service guarantee.

Skill enable/disable, archive upload, marketplace installation, plugin OAuth and desktop runtime assets are outside this feature. Actual Agent compatibility must be smoke-tested with the deployed binary; gateway tests use an isolated fake ACP server and do not claim full desktop parity.
