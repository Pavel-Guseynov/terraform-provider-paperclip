# terraform-provider-paperclip

## Purpose

`terraform-provider-paperclip` is the OpenTofu (`tofu`) and Terraform provider for declarative Paperclip configuration. Built on the Terraform Plugin Framework (Protocol v6.0), it manages Paperclip companies, projects, project leads, workspaces, local execution environments, agents, multi-file agent instruction bundles, skills, skill assignments, skill execution policies, secrets, secret provider vaults, user secret definitions, tool applications, connections, connection installs, application permissions, execution policies, stdio templates, goals, budget policies, invitations, human memberships, member permission grants, instance administrators, multi-company access, and generation-bound ephemeral API keys. Its public source repository is [Pavel-Guseynov/terraform-provider-paperclip](https://github.com/Pavel-Guseynov/terraform-provider-paperclip), with provider address `registry.opentofu.org/guseynov/paperclip`.

## Configuration

### OpenTofu Registry (`registry.opentofu.org/guseynov/paperclip`)

```hcl
terraform {
  required_providers {
    paperclip = {
      source  = "guseynov/paperclip"
      version = "~> 0.1"
    }
  }
}

provider "paperclip" {
  endpoint = "http://127.0.0.1:3100"
  token    = var.paperclip_token
}
```

### Provider Schema Attributes

| Attribute | Type | Required | Default / Environment Fallback | Description |
|---|---|---|---|---|
| `endpoint` | String | Optional | `PAPERCLIP_API_URL`, then `"http://127.0.0.1:3100"` | Absolute HTTP(S) base URL of the Paperclip board API, without URL credentials, query or fragment. |
| `token` | String (Sensitive) | Optional | `PAPERCLIP_API_KEY`, then unauthenticated native access | Bearer token for authenticating with Paperclip. |

Explicit values take precedence over environment variables. Only omitted or null
attributes consult the environment. An explicitly empty token disables bearer
authentication; an explicitly empty endpoint is invalid. Unknown values produce
attribute errors rather than selecting another endpoint or identity. URL errors
never reproduce endpoint credentials or token values.

The provider has no compatibility configuration/environment aliases. Update old
provider blocks to `endpoint` and `token` and use only `PAPERCLIP_API_URL` and
`PAPERCLIP_API_KEY` for environment configuration. Terraform's native provider
`alias` meta-argument still selects multiple independent provider configurations.

### Filesystem Mirror Configuration (`.tofurc`)

When installing a prebuilt provider offline, configure an OpenTofu filesystem mirror containing the binary at `registry.opentofu.org/guseynov/paperclip/<version>/<platform>/terraform-provider-paperclip_v<version>`:

```hcl
provider_installation {
  filesystem_mirror {
    path    = "/path/to/mirror"
    include = ["registry.opentofu.org/guseynov/paperclip"]
  }
  direct {
    exclude = ["registry.opentofu.org/guseynov/paperclip"]
  }
}
```

## Integration contract

### Named MCP gateways and gateway client tokens

`paperclip_mcp_gateway` owns a named gateway through Paperclip's existing
board API with `tools:admin`. Required inputs are `company_id`, `name`, `slug`
and `profile_id`. Import uses `company_id/gateway_id` and preserves that ID.
Company identity cannot change in place; retire explicitly before declaring
a gateway for another company. The native issue/project relationship is
validated before writes as well as each referenced object's company.
`default_profile_mode` defaults to `gateway_only`; the other native modes are
`inherit_context_then_gateway` and `gateway_then_context`. `context_scope_type`
defaults to `none`; company, project, routine, issue and agent scopes require
`context_scope_id`. References are checked read-only at plan when known and
again before mutation. Optional `agent_id`, `project_id`, `issue_id` and
`approval_issue_id` supply the separate native context fields. Scope metadata
does not replace these native runtime identity fields. Description and optional
IDs clear to null when omitted; metadata resets to `{}`.

The JSON policies `auth_config`, `header_policy`, `metadata_policy` and
`on_demand_tools_config` accept native camelCase fields and expand omitted
fields to native defaults. Removing a policy resets it to those defaults.
Auth version is 1, bearer is enabled with prefix `pcgw`, default TTL 7776000
seconds, finite-expiry and long-lived-override flags true; OAuth is fixed to
disabled with `reservedFor = "v1_5"`. Default header policy disables caller
passthrough and generated metadata with empty header lists and static headers;
response forwarding enables MCP-required and safe-cache headers. Metadata
forwarding defaults false except correlation ID. On-demand tools default off
with fixed names `search_tools` and `run_tool`. Native policy settings are
persisted as provided by the API; their downstream runtime enforcement belongs
to Paperclip. JSON values enter state: use only public policy data, never
credentials in static header values or metadata. No secret resolution is
implemented for header policy `valueRef`.

In native JSON, header options are `callerPassthrough.enabled/allowedHeaders`,
`generatedMetadata.enabled/allowedHeaders`, `staticHeaders` entries with required
`name` and optional nullable `valueRef`/`value`, and
`responseHeaders.forwardMcpRequiredHeaders/forwardSafeCacheHeaders`. Header lists
and static headers allow 50 entries, except generated metadata allows 20.
Header names allow 1–120 HTTP token characters; public values allow 4000
characters and trimmed references 240. Metadata flags are `forwardCompanyId`,
`forwardGatewayId`, `forwardProjectId`, `forwardIssueId`, `forwardAgentId`,
`forwardRunId` and `forwardCorrelationId`. Bearer `defaultTtlSeconds` accepts
null or an integer from 1 to 31536000. OAuth's optional
`protectedResourceMetadataPath` is nullable (240 trimmed characters);
`dynamicClientRegistration` and `authorizationCodePkce` support only false.

Creation always uses active status. Create active, then update to draft,
active or disabled. Destroy archives the gateway using PATCH; declared
`status = "archived"` fails before mutation because the native inventory omits
archives and has no exact read route. Archive does not set `archivedAt`, revoke
tokens or remove bindings. Profile changes insert a new native gateway binding
and retain the old binding. Coordinate binding ownership explicitly; never
declare a second owner for a gateway-created binding. One configured provider
process rejects duplicate gateway and token lifecycle owners; independent
states must coordinate.

`ephemeral.paperclip_mcp_gateway_token` produces only `gateway_client` tokens.
Required inputs are `company_id`, `gateway_id`, `name`, `client_label`, positive
`generation`, finite RFC3339 `expires_at`, JSON `allowed_actions`, and
`applying = tofu.applying`. Native actions are tools/list, tools/call,
resources/list, resources/read, prompts/list and prompts/get. The durable
`paperclip_mcp_gateway_token` record has the same inputs except `applying` and
defaults `allowed_actions` to tools/list and tools/call. It stores only the token
ID, ownership ID and non-secret configuration; import is unsupported because the native API
cannot establish generation or delivery. Name, company and gateway form its
immutable identity; retire it explicitly to change them. Attribute changes
require an increased generation.

Use one shared set of non-secret inputs for both declarations. Pass the
ephemeral `.token` only into an external consumer's write-only value, bind its
write trigger to the producing `company_id`/`gateway_id`/`name` identity and
generation, and set the durable record's `depends_on` to that
consumer. This dependency is the caller's delivery acknowledgement contract;
the provider cannot inspect an external consumer's write. The external consumer
owns secret distribution and any native owner approval; this provider never
connects to a secret manager. Plans defer minting. An unchanged generation
learned from durable state mints and writes nothing. Apply mints once, delivers,
then records delivery and revokes the predecessor. Failed consumer delivery,
private-state failures and graceful exits revoke unconfirmed mints while
preserving the predecessor. Revocation errors are explicit and retain cleanup
metadata; a hard crash can leave a token live until its required expiry or
explicit native revocation. Token bytes never enter plans, state or diagnostics.
If a mint response is lost or lacks a token ID, the native API cannot prove
generation or delivery: inspect its token inventory and revoke the ambiguous
mint before retrying. The provider never adopts or revokes a token by name.
Destroy token records before archiving their gateway: archived token inventory
cannot be refreshed, and the provider retains the record with a diagnostic
rather than claiming absence. Native authorization or inventory errors retain
state. The parent pure-Nix unit and real OpenTofu gates prove these contracts
against test-owned fixtures, including failed delivery and reference validation.

For the external consumer's non-secret write trigger, use
`jsonencode({ company_id = local.company_id, gateway_id = paperclip_mcp_gateway.automation.id, name = local.token_name, generation = local.generation })`.
Use the same locals in both token declarations and retain the consumer's native
approval. This binds delivery to a new producer identity even if its initial
generation equals the retired producer's generation.

`paperclip_tool_profile` is the sole aggregate writer of one profile's complete
`entries` JSON array (maximum 250). Entries use native camelCase fields:
`selectorType` (`application`, `connection`, `catalog_entry`, `tool_name` or
`risk_level`), its corresponding selector, optional `effect` (`include` by
default or `exclude`) and `conditions`. Updates replace the whole set; entry
IDs and order do not define ownership. Omission means an empty set. There is
no per-entry resource. Status defaults to active, default action to deny,
and metadata to {}. An existing description or metadata cannot be cleared
to null through the native API: retain it, use an empty description or {}
metadata. Unsupported transitions fail before mutation without replacing
the profile. Import as `company_id/profile_id`.

Deleting a company default requires `force_destroy = true` (default false)
or an explicit `reassign_to_profile_id`. Reassignment moves native bindings
to a different active profile in the same company, outside finish ownership.
Profile deletion cascades its bindings; express dependencies through profile
references and coordinate retirement with any bindings owned elsewhere.

`paperclip_tool_profile_binding` owns one company, agent, project, routine,
issue or gateway binding. Import as `company_id/profile_id/binding_id`;
the native company profile inventory supplies both entries and bindings.
Priority defaults to 100 (range 0–10000), metadata to {}. Bind is insert-only: changes to
identity, priority or metadata fail before mutation. Explicitly retire the
binding before declaring another one. Duplicates surface as conflicts rather
than adoption. Company bindings target their own company; all other targets
must belong to it. Known references are checked read-only during planning;
references unknown until apply are checked again before mutation.

`paperclip_policy` schema version 1 owns `allow`, `block`,
`require_approval` and `rate_limit` policies. It exposes nullable
`description`, `priority` (0–10000, default 100, lowest first), `enabled`
(default true), loose native `selectors`, strict `conditions`, and
`config` JSON objects. Conditions accept the native `arguments`/`args`,
`actor`, `context`, `risk`, `credentialScope`, `trustBoundary` and
`timeWindow` groups; malformed or unsupported conditions fail at plan.
Use trimmed native strings in conditions so server normalization cannot
change configured JSON after apply.
Rate limits require positive numeric `limit` and `windowSeconds`, directly
in config or under `rateLimit`, with optional native `keyBy`. Nonempty
config is supported only for rate limits. Omitted description, conditions
and config clear to null; omitted selectors clear to {}. Priority updates
patch only the owned ID; the provider never calls global reorder. Import as
`company_id/policy_id`. Upgrade from version 0 retains that native ID,
drops unsupported `rules` and `metadata` state, and refreshes the native
fields. Remove those unsupported attributes from configuration and declare
native selectors/conditions explicitly; the old fields never expressed
native policy behavior.

Profiles in the `app:` namespace or with native
`source=app_gallery_finish` metadata, their bindings, and finish-owned
policies cannot be imported, reassigned or mutated by these resources.
Action-request-only `trust_rule` policies remain outside declarative creation
and ownership. Each profile/policy has one provider ownership identity;
duplicate owners in one configured provider process fail. The native API
has no persistent declarative owner or conditional write: independent
provider configurations and state files must coordinate exclusive ownership
externally. Only native catalog refresh and new-tool review control the
catalog; these resources never write producer tool schemas or annotations,
including `readOnlyHint`. Board access and a profile inventory containing
entries and bindings are required; unavailable reads fail without dropping
state or claiming convergence. Consumer deployment remains separate work.

- **Authority:** This repository contains the authoritative Go source and OpenTofu Registry documentation. The parent Paperclip packaging repository owns the declarative Nix build and verification gate (`100.0%` statement coverage).
- **Inputs:** Go source (`main.go`, `internal/client`, `internal/provider`, `cmd/terraform-provider-testconsumer`), locked Go module manifests (`go.mod`, `go.sum`), OpenTofu Registry manifest (`terraform-registry-manifest.json`), registry documentation (`docs/`), and HCL examples (`examples/`).
- **Outputs:**
  - Standalone executable `bin/terraform-provider-paperclip` speaking Terraform Plugin Protocol v6.0 (`terraform-registry-manifest.json`).
  - Registry documentation in [docs/index.md](docs/index.md), [docs/resources/](docs/resources/), [docs/data-sources/](docs/data-sources/), and [docs/ephemeral-resources/](docs/ephemeral-resources/).
  - Runnable HCL examples in [examples/](examples/).
- **Dependencies:** Go 1.26+, `github.com/hashicorp/terraform-plugin-framework` v1.19.0, and a target Paperclip HTTP API instance.
- **Consumers:** OpenTofu (`tofu`) and Terraform configurations managing a Paperclip instance, via `registry.opentofu.org/guseynov/paperclip` (`guseynov/paperclip`).
- **Application refresh:** `paperclip_app` reads `GET /api/companies/<company_id>/tools/applications` and selects its exact application ID within that company. Import using `<company_id>/<app_id>`. It never adopts an application by name or searches other companies. A valid list without that ID removes the resource from state; incomplete identities, API errors and malformed or contradictory lists preserve state and fail the refresh.
- **Native application limits:** All five native types, status and plugin/agent/user ownership are exposed. Type and application key cannot change after creation. The current API cannot clear non-null description/ownership to null; the provider rejects these requests before mutation without replacing identity. An empty description string and empty metadata object are supported. Omitted status retains its current value; native creation defaults to active. Destroy archives the application.
- **Connections:** Native purpose, kind, ownership, credential source/policy, status, enabled state and transport configuration refresh as drift. Purpose defaults to tool, kind to managed, ownership to customer, source to paperclip_vault, status to draft and enabled to false on custom native creation. Omitted values retain imported state. The update API cannot change transport, auth kind, purpose, kind, ownership or credential source. Unsupported changes fail explicitly; custom channel purpose and vercel_connect setup require native setup and import. Local stdio activation follows the server's deployment and trusted-runtime policy. Config/transport config reject sensitive keys recursively. Remote MCP configuration must retain its HTTP(S) endpoint; local stdio must retain an approved template ID. Native transports without these required fields support config clearing.
- **Protected references:** `credential_secret_refs` maps native config paths to `secret_id`, optional `version` (latest or a positive integer string), projection metadata, `required`, `label`, `key_scope` and `expires_at`. It reuses the environment-binding identity/version/projection rules and verifies company secret identity read-only before writing. It contains no credential value. The existing `credential_refs` projection also owns the default execution grant and revokes it when removed.
- **Grant aggregate:** `paperclip_connection_grants` owns one connection's default organization installation grant, its `member_user_ids` audience and a `delegations` map from existing personal grant IDs to agent-ID sets. Empty audience means every company member. Rotation revokes superseded organization grants; `ready` exposes incomplete revocation as drift. Personal grants require native consent and only their named user owner can create delegations. Import is `company_id/connection_id`; destroy revokes the managed grant and delegations. Do not combine this aggregate with implicit grant ownership through a connection's `credential_refs`.
- **Whole-set ownership:** One `paperclip_connection_install` owns the full install set per connection. Grant/install duplicates fail within one configured provider process. The native API has no persistent owner/CAS field; separate provider configurations and state files must coordinate ownership externally. Application, connection and install v0 state upgrades preserve remote IDs. Interactive consent, backend setup and deployment activation are separate operations.
- **Execution environments:** `paperclip_environment` manages native instance-scoped Local, SSH, sandbox and plugin config, typed `env` bindings, metadata and active/archived status. `company_id` is secret-resolution context only. Import as `company_id/environment_id`. Local must be imported, never created or replaced. Same-driver config is natively shallow-merged: removed top-level keys fail before mutation, including Local; nested objects can be replaced. SSH uses protected `privateKeySecretRef` references, never plaintext private keys. Sandbox requires an installed provider; `fake` cannot be saved. Plugin-specific schemas and defaults belong to the installed plugin and server. Supply normalized plugin config, and use references rather than credentials because sensitive config still enters state. These fixtures prove API behavior, not production Kubernetes deployment.
- **Local ownership migration:** Retain the old `paperclip_local_environment` consumer binding until its owner is ready. Replace its configuration with a `removed` block naming that resource and `lifecycle { destroy = false }`, and import `paperclip_environment.local` at the same native ID, with matching native fields and full env bindings (omit PATH for service inheritance). The plan must only forget/import, never replace, destroy or mutate Local; require a zero-change plan afterward. Keep exactly one Local state/configuration owner. The consumed old schema remains available until this cutover is safe; consumer source and activation are separate work.
- **Native retirement:** DELETE failures preserve the native `managed_local`, `instance_default`, `pending_sandbox_cleanup`, and `reusable_sandbox_lease` reasons. There is no automatic destructive lease cleanup consent. SSH DELETE also removes its referenced private-key secret under the current native API; coordinate that secret's ownership before retirement. Omitted description/metadata clear to null; omitted env bindings clear to an empty native map.
- **Board-key bootstrap:** An unauthenticated local challenge identifies its command as `terraform-provider-paperclip` and uses the configured key name as its client name.
- **Lifecycle & Subrepo Export:**
  - Maintained as a subtree of the Paperclip packaging repository, whose `nix flake check` and `derivation-inputs` checks verify changes hermetically.
  - Exported as the standalone public repository `github.com/Pavel-Guseynov/terraform-provider-paperclip`, which is also its Go module identity.
  - On `SIGINT` or `SIGTERM`, the provider process runs deferred exit cleanup (`CleanupAllUnconfirmedKeys`) to revoke any unacknowledged ephemeral keys minted during an interrupted apply.
- **Failures:**
  - Plugin lifecycle and configuration writes require instance administrator access. The hidden `instance.plugins` setting rejects management; cloud-managed deployments reject npm installation. `upgrade_pending` means newly added capabilities await approval.
  - `paperclip_plugin` imports by plugin ID and soft-uninstalls by default; `purge_on_destroy = true` removes plugin data and company configurations. `paperclip_plugin_config` imports by `company_id/plugin_id`, reads `GET /api/plugins/:id/config?companyId=:companyId`, and writes `POST /api/plugins/:id/config` with `{companyId, configJson}`. Server schema field errors are surfaced. Destroy relinquishes config management without changing remote configuration; the API has no config deletion or per-company enable route.
  - Plugin config references share the environment-binding validation rules, normalize snake_case HCL and camelCase API keys semantically, verify secret IDs in the target company, and never resolve secret values.
  - Authentication (`401`), authorization (`403`), server (`500+`), and unknown-route `404` responses fail closed as structured diagnostics with redacted sensitive fields and never remove resources from state.
  - Write-only secret values (`paperclip_secret.value`), invitation tokens (`paperclip_invitation`), and ephemeral bearer tokens (`ephemeral.paperclip_board_key.token`, `ephemeral.paperclip_agent_key.token`) are never persisted in state or plan diffs.
- **Validation:**
  - `go test -v -cover ./internal/...` (enforcing `100.0%` statement coverage and verifying that `terraform-registry-manifest.json`, `docs/`, and `examples/` match the live protocol v6 provider schema).
  - The parent producer's `nix build .#terraform-provider-paperclip` and `nix flake check`, including real `tofu init`, `tofu plan`, and `tofu apply` against a filesystem mirror of `registry.opentofu.org/guseynov/paperclip`.
