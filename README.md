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
  - Authentication (`401`), authorization (`403`), server (`500+`), and unknown-route `404` responses fail closed as structured diagnostics with redacted sensitive fields and never remove resources from state.
  - Write-only secret values (`paperclip_secret.value`), invitation tokens (`paperclip_invitation`), and ephemeral bearer tokens (`ephemeral.paperclip_board_key.token`, `ephemeral.paperclip_agent_key.token`) are never persisted in state or plan diffs.
- **Validation:**
  - `go test -v -cover ./internal/...` (enforcing `100.0%` statement coverage and verifying that `terraform-registry-manifest.json`, `docs/`, and `examples/` match the live protocol v6 provider schema).
  - The parent producer's `nix build .#terraform-provider-paperclip` and `nix flake check`, including real `tofu init`, `tofu plan`, and `tofu apply` against a filesystem mirror of `registry.opentofu.org/guseynov/paperclip`.
