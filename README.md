# terraform-provider-paperclip

## Purpose

`terraform-provider-paperclip` is the canonical OpenTofu (`tofu`) and Terraform provider for declarative Paperclip desired-state reconciliation. Built on the Terraform Plugin Framework (Protocol v6.0), it manages Paperclip companies, projects, project leads, workspaces, local execution environments, agents, multi-file agent instruction bundles, skills, skill assignments, skill execution policies, secrets, secret provider vaults, user secret definitions, tool applications, connections, connection installs, application permissions, execution policies, stdio templates, goals, budget policies, invitations, human memberships, member permission grants, instance administrators, multi-company access, and generation-bound ephemeral API keys. The provider lives as a Git subrepo at `packages/providers/terraform-provider-paperclip` in the `paperclip` packaging repository and exports as a standalone standard OpenTofu provider repository to `git.guseynov.org/guseynov/terraform-provider-paperclip` and `git@github.com:Pavel-Guseynov/terraform-provider-paperclip.git` (`registry.opentofu.org/guseynov/paperclip`).

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

### Local Forge Mirror (`git.guseynov.org/guseynov/paperclip`)

```hcl
terraform {
  required_providers {
    paperclip = {
      source  = "git.guseynov.org/guseynov/paperclip"
      version = "0.1.0"
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

When consuming the immutable Nix-built binary (`nix build .#terraform-provider-paperclip`) offline without network registry fetches, configure an OpenTofu filesystem mirror:

```hcl
provider_installation {
  filesystem_mirror {
    path    = "/path/to/mirror"
    include = ["registry.opentofu.org/guseynov/*", "git.guseynov.org/guseynov/*"]
  }
  direct {
    exclude = ["registry.opentofu.org/guseynov/*", "git.guseynov.org/guseynov/*"]
  }
}
```

## Integration contract

- **Authority:** `packages/providers/terraform-provider-paperclip` is the authoritative Go source and OpenTofu Registry repository root; `package-provider.nix` in the parent `paperclip` flake owns the declarative Nix build and unit-test gate (`100.0%` statement coverage).
- **Inputs:** Go source (`main.go`, `internal/client`, `internal/provider`, `cmd/terraform-provider-testconsumer`), locked Go module manifests (`go.mod`, `go.sum`), OpenTofu Registry manifest (`terraform-registry-manifest.json`), registry documentation (`docs/`), and HCL examples (`examples/`).
- **Outputs:**
  - Standalone executable `bin/terraform-provider-paperclip` speaking Terraform Plugin Protocol v6.0 (`terraform-registry-manifest.json`).
  - Registry documentation in [docs/index.md](docs/index.md), [docs/resources/](docs/resources/), [docs/data-sources/](docs/data-sources/), and [docs/ephemeral-resources/](docs/ephemeral-resources/).
  - Runnable HCL examples in [examples/](examples/).
- **Dependencies:** Go 1.26+, `github.com/hashicorp/terraform-plugin-framework` v1.19.0, and a target Paperclip HTTP API instance.
- **Consumers:** OpenTofu (`tofu`) and Terraform configurations across Harness and external fleet automation, via `registry.opentofu.org/guseynov/paperclip` (`guseynov/paperclip`) or `git.guseynov.org/guseynov/paperclip`.
- **Lifecycle & Subrepo Export:**
  - Maintained in-tree inside `paperclip` at `packages/providers/terraform-provider-paperclip` so `nix flake check` and `derivation-inputs` verify every change hermetically without network submodule fetches.
  - Exported or synchronized as a standalone Git repository root via `git subtree split --prefix=packages/providers/terraform-provider-paperclip -b terraform-provider-paperclip`, publishing the split branch to `git.guseynov.org/guseynov/terraform-provider-paperclip` and `git@github.com:Pavel-Guseynov/terraform-provider-paperclip.git`.
  - On `SIGINT` or `SIGTERM`, the provider process runs deferred exit cleanup (`CleanupAllUnconfirmedKeys`) to revoke any unacknowledged ephemeral keys minted during an interrupted apply.
- **Failures:**
  - Plugin lifecycle and configuration writes require instance administrator access. The hidden `instance.plugins` setting rejects management; cloud-managed deployments reject npm installation. `upgrade_pending` means newly added capabilities await approval.
  - `paperclip_plugin` imports by plugin ID and soft-uninstalls by default; `purge_on_destroy = true` removes plugin data and company configurations. `paperclip_plugin_config` imports by `company_id/plugin_id`, reads `GET /api/plugins/:id/config?companyId=:companyId`, and writes `POST /api/plugins/:id/config` with `{companyId, configJson}`. Server schema field errors are surfaced. Destroy relinquishes config management without changing remote configuration; the API has no config deletion or per-company enable route.
  - Plugin config references share the environment-binding validation rules, normalize snake_case HCL and camelCase API keys semantically, verify secret IDs in the target company, and never resolve secret values.
  - Authentication (`401`), authorization (`403`), server (`500+`), and unknown-route `404` responses fail closed as structured diagnostics with redacted sensitive fields and never remove resources from state.
  - Write-only secret values (`paperclip_secret.value`), invitation tokens (`paperclip_invitation`), and ephemeral bearer tokens (`ephemeral.paperclip_board_key.token`, `ephemeral.paperclip_agent_key.token`) are never persisted in state or plan diffs.
- **Validation:**
  - `go test -v -cover ./internal/...` (enforcing `100.0%` statement coverage and verifying that `terraform-registry-manifest.json`, `docs/`, and `examples/` match the live protocol v6 provider schema).
  - `nix build .#terraform-provider-paperclip` and `nix flake check` (running `provider-opentofu-test` with real `tofu init`, `tofu plan`, and `tofu apply` across both `registry.opentofu.org/guseynov/paperclip` and `git.guseynov.org/guseynov/paperclip` mirrors).
