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
| `endpoint` | String | Optional | `PAPERCLIP_API_URL` -> `PAPERCLIP_ENDPOINT` -> `PAPERCLIP_URL` -> `"http://127.0.0.1:3100"` | HTTP base URL of the Paperclip board API. |
| `token` | String (Sensitive) | Optional | `PAPERCLIP_API_KEY` -> `PAPERCLIP_BOARD_TOKEN` -> `PAPERCLIP_TOKEN` | Bearer token or API key for authenticating with Paperclip. |
| `paperclip_endpoint` | String | Optional | Alias for `endpoint` | Backward-compatible alias for `endpoint`. |
| `paperclip_token` | String (Sensitive) | Optional | Alias for `token` | Backward-compatible alias for `token`. |

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

### Projects and workspaces

Projects manage `status` (default `backlog`), `goal_ids`, `target_date`, `color`,
`icon`, shared typed `env` bindings, `archived_at`, and the strict nested
`execution_workspace_policy` block. `paperclip_project_lead` alone owns the lead.
Null or empty `env` clears through the native empty object. Projects hard-delete
by default; `archive_on_destroy = true` archives instead. State versions 0 and 1
upgrade to version 2 without replacing identity. Replace the previous
`execution_workspace_policy = jsonencode(...)` configuration with typed block
syntax without `=`, including nested strategy and authorization blocks, using
snake_case keys; native free-form record fields remain JSON
objects. Unrepresentable old policy keys cause an explicit migration diagnostic.
Native block decoding rejects unknown arguments instead of silently discarding
them during object type conversion.

`target_date` must be `YYYY-MM-DD` or null. Set `archived_at` after creation:
the existing create route does not convert timestamp strings to database dates,
so the provider rejects archival during creation before mutation. UTC RFC3339
archive timestamps use native millisecond precision and compare by instant,
preserving equivalent `.000Z` readback. Environment/workspace policy references
are verified through native read-only APIs; invalid, archived, unsupported and
probe-only environment selections and absent project workspaces are rejected.
Shared env bindings preserve empty plain strings, expose native reference
defaults, and send pinned version strings as native JSON integers.

Workspaces manage all four native `source_type` values (`local_path`, `git_repo`,
`remote_managed`, `non_git_path`) and all native configuration fields. Defaults
are `source_type = "git_repo"`, `is_primary = true`, and
`visibility = "default"`. Provide `cwd` or `repo_url`; `remote_managed` instead
requires `remote_workspace_ref` or `repo_url`. Version 0 state upgrades to
version 1 by mapping the old `branch` to both `repo_ref` and `default_ref`,
preserving identity. Update configuration to the independent ref attributes.
Imports use `company_id/project_id` (or project ID) and
`project_id/workspace_id`; workspace refresh always selects the exact ID from
the native project-scoped list.

The provider exposes the API's effective `default_ref`: native null storage
inherits `repo_ref`. Configuring a null `default_ref` with a non-null `repo_ref`
cannot express an independent clear and fails before mutation. Set the effective
inherited value explicitly, set a distinct ref, or clear both refs. The provider
also rejects demoting the only workspace, leading/trailing or empty workspace
strings, the native repo-only cwd sentinel, and empty/all-null runtime settings
that the API normalizes away. Runtime JSON accepts only `workspaceRuntime`,
`desiredState`, and `serviceStates`; desired/service states are `running`,
`stopped`, or `manual`. Null clears runtime configuration; null members and
absent members compare equivalently. Use `runtime_config` for the metadata
`runtimeConfig` key, which is excluded from the separately managed `metadata`
object to give it one owner. These errors retain existing identity and do not
issue an unsupported mutation. Known invalid goal/secret references fail at
plan; apply-time references are validated read-only before writes.

The existing API stores policy `environment_id` without using it to select run
environments. It stores `setup_command` without executing it and executes
`cleanup_command`. Typed authorization policy excludes trust keys because the
native assignment evaluator rejects them. Provider checks reproduce these
native limitations; server changes, source-pin advancement, live deployment,
and consumer/backend selection are outside provider delivery.

## Integration contract

- **Authority:** This repository contains the authoritative Go source and OpenTofu Registry documentation. The parent Paperclip packaging repository owns the declarative Nix build and verification gate (`100.0%` statement coverage).
- **Inputs:** Go source (`main.go`, `internal/client`, `internal/provider`, `cmd/terraform-provider-testconsumer`), locked Go module manifests (`go.mod`, `go.sum`), OpenTofu Registry manifest (`terraform-registry-manifest.json`), registry documentation (`docs/`), and HCL examples (`examples/`).
- **Outputs:**
  - Standalone executable `bin/terraform-provider-paperclip` speaking Terraform Plugin Protocol v6.0 (`terraform-registry-manifest.json`).
  - Registry documentation in [docs/index.md](docs/index.md), [docs/resources/](docs/resources/), [docs/data-sources/](docs/data-sources/), and [docs/ephemeral-resources/](docs/ephemeral-resources/).
  - Runnable HCL examples in [examples/](examples/).
- **Dependencies:** Go 1.26+, `github.com/hashicorp/terraform-plugin-framework` v1.19.0, and a target Paperclip HTTP API instance.
- **Consumers:** OpenTofu (`tofu`) and Terraform configurations managing a Paperclip instance, via `registry.opentofu.org/guseynov/paperclip` (`guseynov/paperclip`).
- **Application refresh:** `paperclip_app` reads `GET /api/companies/<company_id>/tools/applications` and selects its exact application ID within that company. Import using `<company_id>/<app_id>`. It never adopts an application by name or searches other companies. A valid list without that ID removes the resource from state; incomplete identities, API errors and malformed or contradictory lists preserve state and fail the refresh.
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
