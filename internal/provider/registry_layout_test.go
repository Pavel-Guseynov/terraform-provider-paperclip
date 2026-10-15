package provider

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

var generateProviderDocs = flag.Bool("generate-docs", false, "regenerate OpenTofu Registry docs and examples from live provider schema")

type registryManifest struct {
	Version  int `json:"version"`
	Metadata struct {
		ProtocolVersions []string `json:"protocol_versions"`
	} `json:"metadata"`
}

var resourceExamples = map[string]string{
	"paperclip_company": `resource "paperclip_company" "acme" {
  name                                  = "Acme"
  description                           = "Autonomous engineering organization"
  status                                = "active"
  require_board_approval_for_new_agents = false
  feedback_data_sharing_enabled         = false
  archive_on_destroy                    = true
}
`,
	"paperclip_project": `resource "paperclip_project" "core" {
  company_id                 = paperclip_company.acme.id
  name                       = "core-platform"
  description                = "Core platform engineering project"
  execution_workspace_policy = jsonencode({ mode = "git_worktree" })
}
`,
	"paperclip_project_lead": `resource "paperclip_project_lead" "core_lead" {
  project_id = paperclip_project.core.id
  agent_id   = paperclip_agent.lead.id
}
`,
	"paperclip_workspace": `resource "paperclip_workspace" "main" {
  project_id  = paperclip_project.core.id
  name        = "primary"
  repo_url    = "https://git.example.com/acme/core-platform.git"
  branch      = "develop"
  is_primary  = true
  source_type = "git_repo"
}
`,
	"paperclip_local_environment": `resource "paperclip_local_environment" "host" {
  company_id           = paperclip_company.acme.id
  inherit_service_path = true
}
`,
	"paperclip_skill": `resource "paperclip_skill" "task_lifecycle" {
  company_id       = paperclip_company.acme.id
  name             = "paperclip-task-lifecycle"
  slug             = "paperclip-task-lifecycle"
  description      = "Deliver a Paperclip code task in its selected worktree"
  content          = "# Paperclip Task Lifecycle\n"
  version_snapshot = "v1.0.0"
}
`,
	"paperclip_secret": `resource "paperclip_secret" "api_token" {
  company_id    = paperclip_company.acme.id
  name          = "forge-token"
  key           = "forge_token"
  description   = "Forge API token"
  provider_type = "local_encrypted"
  value         = var.forge_token
  rotation      = "2026-10"
}
`,
	"paperclip_secret_provider_config": `resource "paperclip_secret_provider_config" "vault" {
  company_id    = paperclip_company.acme.id
  provider_type = "local_encrypted"
  display_name  = "Local Encrypted Store"
  is_default    = true
}
`,
	"paperclip_user_secret_definition": `resource "paperclip_user_secret_definition" "personal_token" {
  company_id     = paperclip_company.acme.id
  key            = "personal_access_token"
  name           = "Personal Access Token"
  description    = "User-supplied personal access token"
  usage_guidance = "Provide a read/write token from your account settings."
}
`,
	"paperclip_agent": `resource "paperclip_agent" "engineer" {
  company_id             = paperclip_company.acme.id
  name                   = "Platform Engineer"
  role                   = "engineer"
  title                  = "Senior Platform Engineer"
  adapter_type           = "antigravity_local"
  default_environment_id = paperclip_local_environment.host.id
  can_assign_tasks       = true
  can_create_agents      = false
  can_create_skills      = true
  trust_preset           = "standard"
}
`,
	"paperclip_agent_instruction_bundle": `resource "paperclip_agent_instruction_bundle" "engineer_bundle" {
  agent_id   = paperclip_agent.engineer.id
  mode       = "managed"
  entry_file = "AGENTS.md"
  files = {
    "AGENTS.md" = "# Agent Instructions\n"
  }
}
`,
	"paperclip_app": `resource "paperclip_app" "gitea" {
  company_id  = paperclip_company.acme.id
  name        = "gitea"
  type        = "mcp_stdio"
  description = "Gitea MCP Server"
}
`,
	"paperclip_connection": `resource "paperclip_connection" "gitea_conn" {
  company_id = paperclip_company.acme.id
  app_id     = paperclip_app.gitea.id
  name       = "gitea-default"
  transport  = "stdio"
  auth_kind  = "api_key"
  config     = jsonencode({ command = "gitea-mcp" })
}
`,
	"paperclip_policy": `resource "paperclip_policy" "require_review" {
  company_id  = paperclip_company.acme.id
  name        = "require-write-approval"
  policy_type = "require_approval"
  rules       = jsonencode({ enabled = true })
}
`,
	"paperclip_stdio_template": `resource "paperclip_stdio_template" "gitea_stdio" {
  company_id  = paperclip_company.acme.id
  template_id = "acme.mcp.gitea"
  name        = "gitea"
  description = "Managed Gitea MCP stdio launcher"
  command     = "gitea-mcp"
  args        = ["serve"]
  env_keys    = ["GITEA_TOKEN"]
}
`,
	"paperclip_connection_install": `resource "paperclip_connection_install" "gitea_install" {
  connection_id = paperclip_connection.gitea_conn.id
  target_type   = "agent"
  agent_ids     = [paperclip_agent.engineer.id]
}
`,
	"paperclip_app_permission": `resource "paperclip_app_permission" "gitea_perms" {
  company_id    = paperclip_company.acme.id
  connection_id = paperclip_connection.gitea_conn.id
  access_type   = "agents"
  agent_ids     = [paperclip_agent.engineer.id]
  tool_selection = jsonencode({
    approval_mode = "writes"
    enabled_tools = ["search_repos", "issue_read"]
  })
}
`,
	"paperclip_skill_assignment": `resource "paperclip_skill_assignment" "engineer_skills" {
  agent_id = paperclip_agent.engineer.id
  skills = [
    {
      key        = paperclip_skill.task_lifecycle.key
      version_id = paperclip_skill.task_lifecycle.current_version_id
    }
  ]
}
`,
	"paperclip_skill_policy": `resource "paperclip_skill_policy" "company_policy" {
  company_id     = paperclip_company.acme.id
  default_effect = "allow"
  rules = jsonencode([
    {
      id       = "allow-skill-updates"
      priority = 100
      effect   = "allow"
      subject  = { type = "all_agents" }
      actions  = ["skills.edit", "skills.update"]
    }
  ])
}
`,
	"paperclip_goal": `resource "paperclip_goal" "q4_delivery" {
  company_id     = paperclip_company.acme.id
  title          = "Deliver OpenTofu Provider Export"
  description    = "Publish standard OpenTofu Registry provider repository"
  level          = "company"
  status         = "active"
  owner_agent_id = paperclip_agent.engineer.id
  retirement     = "fail"
}
`,
	"paperclip_budget_policy": `resource "paperclip_budget_policy" "monthly_cap" {
  company_id        = paperclip_company.acme.id
  scope_type        = "company"
  scope_id          = paperclip_company.acme.id
  metric            = "billed_cents"
  window_kind       = "calendar_month_utc"
  amount            = 500000
  warn_percent      = 80
  hard_stop_enabled = true
  notify_enabled    = true
}
`,
	"paperclip_board_key": `ephemeral "paperclip_board_key" "board_token" {
  name       = "automation-board-key"
  generation = 1
  applying   = tofu.applying
}

resource "paperclip_board_key" "board_key" {
  name       = "automation-board-key"
  generation = 1
}
`,
	"paperclip_agent_key": `ephemeral "paperclip_agent_key" "agent_token" {
  agent_id   = paperclip_agent.engineer.id
  name       = "engineer-key"
  generation = 1
  scope = {
    kind = "standard"
  }
  applying = tofu.applying
}

resource "paperclip_agent_key" "agent_key" {
  agent_id   = paperclip_agent.engineer.id
  name       = "engineer-key"
  generation = 1
  scope = {
    kind = "standard"
  }
}
`,
	"paperclip_invitation": `resource "paperclip_invitation" "operator_invite" {
  company_id         = paperclip_company.acme.id
  allowed_join_types = "both"
  human_role         = "operator"
  agent_message      = "Join the Acme engineering fleet"
}
`,
	"paperclip_invite": `resource "paperclip_invite" "operator_invite" {
  company_id         = paperclip_company.acme.id
  allowed_join_types = "both"
  human_role         = "operator"
  agent_message      = "Join the Acme engineering fleet"
}
`,
	"paperclip_membership": `resource "paperclip_membership" "operator" {
  company_id = paperclip_company.acme.id
  user_id    = data.paperclip_user.operator.id
  role       = "operator"
  status     = "active"
}
`,
	"paperclip_permission_grants": `resource "paperclip_permission_grants" "operator_grants" {
  company_id = paperclip_company.acme.id
  member_id  = paperclip_membership.operator.id
  grants = [
    {
      permission_key = "agents:create"
    }
  ]
}
`,
	"paperclip_member_permissions": `resource "paperclip_member_permissions" "operator_grants" {
  company_id = paperclip_company.acme.id
  member_id  = paperclip_membership.operator.id
  grants = [
    {
      permission_key = "agents:create"
    }
  ]
}
`,
	"paperclip_instance_admin": `resource "paperclip_instance_admin" "admin" {
  user_id           = data.paperclip_user.operator.id
  is_instance_admin = true
}
`,
	"paperclip_user_company_access": `resource "paperclip_user_company_access" "operator_companies" {
  user_id     = data.paperclip_user.operator.id
  company_ids = [paperclip_company.acme.id]
}
`,
	"paperclip_user_instance_access": `resource "paperclip_user_instance_access" "operator_access" {
  user_id           = data.paperclip_user.operator.id
  is_instance_admin = true
  company_ids       = [paperclip_company.acme.id]
}
`,
}

var dataSourceExamples = map[string]string{
	"paperclip_company": `data "paperclip_company" "acme" {
  name = "Acme"
}
`,
	"paperclip_project": `data "paperclip_project" "core" {
  company_id = data.paperclip_company.acme.id
  name       = "core-platform"
}
`,
	"paperclip_agent": `data "paperclip_agent" "engineer" {
  company_id = data.paperclip_company.acme.id
  name       = "Platform Engineer"
}
`,
	"paperclip_skill": `data "paperclip_skill" "task_lifecycle" {
  company_id = data.paperclip_company.acme.id
  name       = "paperclip-task-lifecycle"
}
`,
	"paperclip_user": `data "paperclip_user" "operator" {
  email = "operator@example.com"
}
`,
	"paperclip_users": `data "paperclip_users" "directory" {
  company_id = data.paperclip_company.acme.id
  query      = "operator"
}
`,
}

var ephemeralExamples = map[string]string{
	"paperclip_board_key": `ephemeral "paperclip_board_key" "board_token" {
  name       = "automation-board-key"
  generation = 1
  applying   = tofu.applying
}
`,
	"paperclip_agent_key": `ephemeral "paperclip_agent_key" "agent_token" {
  agent_id   = paperclip_agent.engineer.id
  name       = "engineer-key"
  generation = 1
  scope = {
    kind = "standard"
  }
  applying = tofu.applying
}
`,
}

const providerExampleTF = `terraform {
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
`

func formatTFType(t tftypes.Type) string {
	if t == nil {
		return "Dynamic"
	}
	switch {
	case t.Is(tftypes.String):
		return "String"
	case t.Is(tftypes.Number):
		return "Number"
	case t.Is(tftypes.Bool):
		return "Boolean"
	case t.Is(tftypes.List{}):
		elem := t.(tftypes.List).ElementType
		return fmt.Sprintf("List of %s", formatTFType(elem))
	case t.Is(tftypes.Set{}):
		elem := t.(tftypes.Set).ElementType
		return fmt.Sprintf("Set of %s", formatTFType(elem))
	case t.Is(tftypes.Map{}):
		elem := t.(tftypes.Map).ElementType
		return fmt.Sprintf("Map of %s", formatTFType(elem))
	case t.Is(tftypes.Object{}):
		return "Object"
	default:
		return "Object"
	}
}

func formatNestedMode(mode tfprotov6.SchemaObjectNestingMode) string {
	switch mode {
	case tfprotov6.SchemaObjectNestingModeSingle:
		return "Single Nested Object"
	case tfprotov6.SchemaObjectNestingModeList:
		return "List of Objects"
	case tfprotov6.SchemaObjectNestingModeSet:
		return "Set of Objects"
	case tfprotov6.SchemaObjectNestingModeMap:
		return "Map of Objects"
	default:
		return "Nested Object"
	}
}

func renderAttributesSection(attrs []*tfprotov6.SchemaAttribute) string {
	var required, optional, readOnly []*tfprotov6.SchemaAttribute
	var nested []*tfprotov6.SchemaAttribute

	sorted := append([]*tfprotov6.SchemaAttribute(nil), attrs...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Name < sorted[j].Name
	})

	for _, a := range sorted {
		if a.NestedType != nil && len(a.NestedType.Attributes) > 0 {
			nested = append(nested, a)
		}
		if a.Required {
			required = append(required, a)
		} else if a.Optional {
			optional = append(optional, a)
		} else {
			readOnly = append(readOnly, a)
		}
	}

	var b strings.Builder
	writeAttr := func(a *tfprotov6.SchemaAttribute) {
		typeStr := formatTFType(a.Type)
		if a.NestedType != nil {
			typeStr = formatNestedMode(a.NestedType.Nesting)
		}
		var flags []string
		flags = append(flags, typeStr)
		if a.Sensitive {
			flags = append(flags, "Sensitive")
		}
		if a.WriteOnly {
			flags = append(flags, "Write-Only")
		}
		if a.NestedType != nil && len(a.NestedType.Attributes) > 0 {
			flags = append(flags, fmt.Sprintf("see [below for nested schema](#nestedatt--%s)", strings.ReplaceAll(a.Name, "_", "-")))
		}
		desc := strings.TrimSpace(a.Description)
		if desc != "" {
			b.WriteString(fmt.Sprintf("- `%s` (%s) %s\n", a.Name, strings.Join(flags, ", "), desc))
		} else {
			b.WriteString(fmt.Sprintf("- `%s` (%s)\n", a.Name, strings.Join(flags, ", ")))
		}
	}

	if len(required) > 0 {
		b.WriteString("\n### Required\n\n")
		for _, a := range required {
			writeAttr(a)
		}
	}
	if len(optional) > 0 {
		b.WriteString("\n### Optional\n\n")
		for _, a := range optional {
			writeAttr(a)
		}
	}
	if len(readOnly) > 0 {
		b.WriteString("\n### Read-Only\n\n")
		for _, a := range readOnly {
			writeAttr(a)
		}
	}

	for _, n := range nested {
		anchor := fmt.Sprintf("nestedatt--%s", strings.ReplaceAll(n.Name, "_", "-"))
		b.WriteString(fmt.Sprintf("\n<a id=\"%s\"></a>\n", anchor))
		b.WriteString(fmt.Sprintf("### Nested Schema for `%s`\n", n.Name))
		b.WriteString(renderAttributesSection(n.NestedType.Attributes))
	}

	return b.String()
}

func renderProviderDoc(s *tfprotov6.Schema) string {
	desc := strings.TrimSpace(s.Block.Description)
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("page_title: \"paperclip Provider - terraform-provider-paperclip\"\n")
	b.WriteString("subcategory: \"\"\n")
	b.WriteString("description: |-\n")
	b.WriteString("  " + desc + "\n")
	b.WriteString("---\n\n")
	b.WriteString("# paperclip Provider\n\n")
	b.WriteString(desc + "\n\n")
	b.WriteString("## Example Usage\n\n")
	b.WriteString("```terraform\n")
	b.WriteString(strings.TrimSpace(providerExampleTF) + "\n")
	b.WriteString("```\n\n")
	b.WriteString("<!-- schema generated by tfplugindocs -->\n")
	b.WriteString("## Schema\n")
	b.WriteString(renderAttributesSection(s.Block.Attributes))
	return b.String()
}

func renderComponentDoc(kindTitle, typeName string, s *tfprotov6.Schema, exampleHCL string) string {
	desc := strings.TrimSpace(s.Block.Description)
	if desc == "" {
		desc = fmt.Sprintf("Manages %s (%s).", typeName, kindTitle)
	}
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString(fmt.Sprintf("page_title: \"%s %s - terraform-provider-paperclip\"\n", typeName, kindTitle))
	b.WriteString("subcategory: \"\"\n")
	b.WriteString("description: |-\n")
	for _, line := range strings.Split(desc, "\n") {
		b.WriteString("  " + line + "\n")
	}
	b.WriteString("---\n\n")
	b.WriteString(fmt.Sprintf("# %s (%s)\n\n", typeName, kindTitle))
	b.WriteString(desc + "\n\n")
	if strings.TrimSpace(exampleHCL) != "" {
		b.WriteString("## Example Usage\n\n")
		b.WriteString("```terraform\n")
		b.WriteString(strings.TrimSpace(exampleHCL) + "\n")
		b.WriteString("```\n\n")
	}
	b.WriteString("<!-- schema generated by tfplugindocs -->\n")
	b.WriteString("## Schema\n")
	b.WriteString(renderAttributesSection(s.Block.Attributes))
	return b.String()
}

func verifyOrWriteFile(t *testing.T, path, expected string, generate bool) {
	t.Helper()
	if generate {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(expected), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	actualBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing required standard OpenTofu repo file %s: %v (run with GENERATE_PROVIDER_DOCS=1 to regenerate)", path, err)
	}
	if string(actualBytes) != expected {
		t.Fatalf("file %s is out of sync with live provider schema (run with GENERATE_PROVIDER_DOCS=1 to regenerate)", path)
	}
}

func TestOpenTofuRegistryRepositoryLayoutAndDocs(t *testing.T) {
	ctx := context.Background()
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	generate := os.Getenv("GENERATE_PROVIDER_DOCS") == "1" || *generateProviderDocs

	// 1. Validate terraform-registry-manifest.json
	manifestPath := filepath.Join(repoRoot, "terraform-registry-manifest.json")
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("missing %s: %v", manifestPath, err)
	}
	var manifest registryManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatalf("invalid JSON in %s: %v", manifestPath, err)
	}
	if manifest.Version != 1 {
		t.Fatalf("expected manifest version 1, got %d", manifest.Version)
	}
	hasProto6 := false
	for _, pv := range manifest.Metadata.ProtocolVersions {
		if pv == "6.0" {
			hasProto6 = true
		}
	}
	if !hasProto6 {
		t.Fatalf("expected protocol_versions to include 6.0, got %v", manifest.Metadata.ProtocolVersions)
	}

	// 2. Validate required root files (README.md, LICENSE, .goreleaser.yml)
	for _, rootFile := range []string{"README.md", "LICENSE", ".goreleaser.yml"} {
		full := filepath.Join(repoRoot, rootFile)
		info, err := os.Stat(full)
		if err != nil || info.Size() == 0 {
			t.Fatalf("required OpenTofu repository file %s missing or empty: %v", full, err)
		}
	}

	// 3. Query live Protocol v6 schemas
	server := providerserver.NewProtocol6(New("0.1.0")())()
	schemas, err := server.GetProviderSchema(ctx, &tfprotov6.GetProviderSchemaRequest{})
	if err != nil || len(schemas.Diagnostics) != 0 {
		t.Fatalf("GetProviderSchema failed: %v, %v", err, schemas.Diagnostics)
	}

	// 4. Verify/generate provider example and docs/index.md
	verifyOrWriteFile(t, filepath.Join(repoRoot, "examples", "provider", "provider.tf"), providerExampleTF, generate)
	verifyOrWriteFile(t, filepath.Join(repoRoot, "docs", "index.md"), renderProviderDoc(schemas.Provider), generate)

	// 5. Verify/generate every Resource example and doc
	var resourceNames []string
	for name := range schemas.ResourceSchemas {
		resourceNames = append(resourceNames, name)
	}
	sort.Strings(resourceNames)
	for _, typeName := range resourceNames {
		shortName := strings.TrimPrefix(typeName, "paperclip_")
		ex, ok := resourceExamples[typeName]
		if !ok {
			t.Fatalf("missing HCL example definition in registry_layout_test.go for resource %s", typeName)
		}
		verifyOrWriteFile(t, filepath.Join(repoRoot, "examples", "resources", typeName, "resource.tf"), ex, generate)
		docContent := renderComponentDoc("Resource", typeName, schemas.ResourceSchemas[typeName], ex)
		verifyOrWriteFile(t, filepath.Join(repoRoot, "docs", "resources", shortName+".md"), docContent, generate)
	}

	// 6. Verify/generate every Data Source example and doc
	var dataSourceNames []string
	for name := range schemas.DataSourceSchemas {
		dataSourceNames = append(dataSourceNames, name)
	}
	sort.Strings(dataSourceNames)
	for _, typeName := range dataSourceNames {
		shortName := strings.TrimPrefix(typeName, "paperclip_")
		ex, ok := dataSourceExamples[typeName]
		if !ok {
			t.Fatalf("missing HCL example definition in registry_layout_test.go for data source %s", typeName)
		}
		verifyOrWriteFile(t, filepath.Join(repoRoot, "examples", "data-sources", typeName, "data-source.tf"), ex, generate)
		docContent := renderComponentDoc("Data Source", typeName, schemas.DataSourceSchemas[typeName], ex)
		verifyOrWriteFile(t, filepath.Join(repoRoot, "docs", "data-sources", shortName+".md"), docContent, generate)
	}

	// 7. Verify/generate every Ephemeral Resource example and doc
	var ephemeralNames []string
	for name := range schemas.EphemeralResourceSchemas {
		ephemeralNames = append(ephemeralNames, name)
	}
	sort.Strings(ephemeralNames)
	for _, typeName := range ephemeralNames {
		shortName := strings.TrimPrefix(typeName, "paperclip_")
		ex, ok := ephemeralExamples[typeName]
		if !ok {
			t.Fatalf("missing HCL example definition in registry_layout_test.go for ephemeral resource %s", typeName)
		}
		verifyOrWriteFile(t, filepath.Join(repoRoot, "examples", "ephemeral-resources", typeName, "ephemeral-resource.tf"), ex, generate)
		docContent := renderComponentDoc("Ephemeral Resource", typeName, schemas.EphemeralResourceSchemas[typeName], ex)
		verifyOrWriteFile(t, filepath.Join(repoRoot, "docs", "ephemeral-resources", shortName+".md"), docContent, generate)
	}
}

func TestPublicProviderModuleIdentity(t *testing.T) {
	module, err := os.ReadFile(filepath.Join("..", "..", "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.SplitN(string(module), "\n", 2)[0] != "module github.com/Pavel-Guseynov/terraform-provider-paperclip" {
		t.Fatal("provider module must use its public standalone repository identity")
	}
}

func TestPublicProviderDocumentationHasNoPrivateDeploymentDetails(t *testing.T) {
	err := filepath.WalkDir(filepath.Join("..", ".."), func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || (filepath.Ext(path) != ".md" && filepath.Ext(path) != ".tf") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, privateDetail := range []string{"guseynov.org", "Local Forge Mirror", "Harness", "harness.", "paperclip-fleet-health"} {
			if strings.Contains(string(content), privateDetail) {
				t.Errorf("%s contains private deployment detail %q", path, privateDetail)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
