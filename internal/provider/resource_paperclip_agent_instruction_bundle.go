package provider

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"

	"git.guseynov.org/guseynov/paperclip/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	tfpath "github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                   = &PaperclipAgentInstructionBundleResource{}
	_ resource.ResourceWithConfigure      = &PaperclipAgentInstructionBundleResource{}
	_ resource.ResourceWithImportState    = &PaperclipAgentInstructionBundleResource{}
	_ resource.ResourceWithModifyPlan     = &PaperclipAgentInstructionBundleResource{}
	_ resource.ResourceWithValidateConfig = &PaperclipAgentInstructionBundleResource{}
)

func NewPaperclipAgentInstructionBundleResource() resource.Resource {
	return &PaperclipAgentInstructionBundleResource{}
}

type PaperclipAgentInstructionBundleResource struct {
	client *client.Client
}

type PaperclipAgentInstructionBundleResourceModel struct {
	ID                        types.String `tfsdk:"id"`
	AgentID                   types.String `tfsdk:"agent_id"`
	Mode                      types.String `tfsdk:"mode"`
	RootPath                  types.String `tfsdk:"root_path"`
	EntryFile                 types.String `tfsdk:"entry_file"`
	ClearLegacyPromptTemplate types.Bool   `tfsdk:"clear_legacy_prompt_template"`
	Files                     types.Map    `tfsdk:"files"`
	FileInventory             types.List   `tfsdk:"file_inventory"`
}

type InstructionBundleFileInventoryModel struct {
	Path        types.String `tfsdk:"path"`
	Size        types.Int64  `tfsdk:"size"`
	Language    types.String `tfsdk:"language"`
	Markdown    types.Bool   `tfsdk:"markdown"`
	IsEntryFile types.Bool   `tfsdk:"is_entry_file"`
	Editable    types.Bool   `tfsdk:"editable"`
	Deprecated  types.Bool   `tfsdk:"deprecated"`
	Virtual     types.Bool   `tfsdk:"virtual"`
}

var instructionBundleFileInventoryAttrTypes = map[string]attr.Type{
	"path":          types.StringType,
	"size":          types.Int64Type,
	"language":      types.StringType,
	"markdown":      types.BoolType,
	"is_entry_file": types.BoolType,
	"editable":      types.BoolType,
	"deprecated":    types.BoolType,
	"virtual":       types.BoolType,
}

func (r *PaperclipAgentInstructionBundleResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_agent_instruction_bundle"
}

func (r *PaperclipAgentInstructionBundleResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Version:     1,
		Description: "Manages an agent's instruction bundle as complete desired state (bundle mode, entry file, multi-file desired state, and legacy prompt template clearing).",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Unique identifier for the instruction bundle (agent ID).",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"agent_id": schema.StringAttribute{
				Description: "The Paperclip agent ID.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"mode": schema.StringAttribute{
				Description: "Bundle mode ('managed' or 'external'). External mode requires instance-admin authorization and an absolute root_path.",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("managed"),
				Validators: []validator.String{
					stringOneOfValidator{allowed: []string{"managed", "external"}},
				},
			},
			"root_path": schema.StringAttribute{
				Description: "Filesystem root path of the instruction bundle. Required and must be an absolute path when mode is 'external'; computed by the server when 'managed'.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"entry_file": schema.StringAttribute{
				Description: "Relative entry file path within the bundle (defaults to 'AGENTS.md').",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("AGENTS.md"),
			},
			"clear_legacy_prompt_template": schema.BoolAttribute{
				Description: "Whether to explicitly clear legacy promptTemplate and bootstrapPromptTemplate adapter configurations.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
			},
			"files": schema.MapAttribute{
				Description: "Complete desired set of native editable, non-virtual files (relative path -> file content). Undeclared editable files are removed on apply; virtual and producer-owned files remain intact.",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
			},
			"file_inventory": schema.ListNestedAttribute{
				Description: "Read-only inventory of all files present in the bundle (including virtual, deprecated, or producer-owned files) as reported by the server.",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"path": schema.StringAttribute{
							Description: "Relative file path.",
							Computed:    true,
						},
						"size": schema.Int64Attribute{
							Description: "File size in bytes.",
							Computed:    true,
						},
						"language": schema.StringAttribute{
							Description: "Inferred language.",
							Computed:    true,
						},
						"markdown": schema.BoolAttribute{
							Description: "Whether the file is markdown.",
							Computed:    true,
						},
						"is_entry_file": schema.BoolAttribute{
							Description: "Whether this file is the entry file.",
							Computed:    true,
						},
						"editable": schema.BoolAttribute{
							Description: "Whether this file is editable.",
							Computed:    true,
						},
						"deprecated": schema.BoolAttribute{
							Description: "Whether this file is deprecated.",
							Computed:    true,
						},
						"virtual": schema.BoolAttribute{
							Description: "Whether this file is virtual.",
							Computed:    true,
						},
					},
				},
			},
		},
	}
}

func (r *PaperclipAgentInstructionBundleResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *client.Client, got: %T", req.ProviderData),
		)
		return
	}
	r.client = c
}

func (r *PaperclipAgentInstructionBundleResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var mode, rootPath types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, tfpath.Root("mode"), &mode)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, tfpath.Root("root_path"), &rootPath)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !mode.IsNull() && !mode.IsUnknown() && mode.ValueString() == "external" {
		if rootPath.IsNull() {
			resp.Diagnostics.AddAttributeError(
				tfpath.Root("root_path"),
				"Missing root_path for External Mode",
				"External instructions bundles require an absolute rootPath.",
			)
		} else if !rootPath.IsUnknown() {
			rp := rootPath.ValueString()
			if strings.TrimSpace(rp) == "" || !strings.HasPrefix(rp, "/") {
				resp.Diagnostics.AddAttributeError(
					tfpath.Root("root_path"),
					"Invalid root_path for External Mode",
					"External instructions bundles require an absolute rootPath.",
				)
			}
		}
	}
}

func (r *PaperclipAgentInstructionBundleResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}
	resp.Plan = req.Plan

	var plan PaperclipAgentInstructionBundleResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// 1. External mode check
	if !plan.Mode.IsNull() && !plan.Mode.IsUnknown() && plan.Mode.ValueString() == "external" {
		if plan.RootPath.IsNull() {
			resp.Diagnostics.AddAttributeError(
				tfpath.Root("root_path"),
				"Missing root_path for External Mode",
				"External instructions bundles require an absolute rootPath.",
			)
		} else if !plan.RootPath.IsUnknown() {
			rp := plan.RootPath.ValueString()
			if strings.TrimSpace(rp) == "" || !strings.HasPrefix(rp, "/") {
				resp.Diagnostics.AddAttributeError(
					tfpath.Root("root_path"),
					"Invalid root_path for External Mode",
					"External instructions bundles require an absolute rootPath.",
				)
			}
		}
	}
	if resp.Diagnostics.HasError() {
		return
	}

	entryFile := "AGENTS.md"
	if !plan.EntryFile.IsNull() && !plan.EntryFile.IsUnknown() && plan.EntryFile.ValueString() != "" {
		entryFile = plan.EntryFile.ValueString()
	}

	normEntry := path.Clean(strings.ReplaceAll(entryFile, "\\", "/"))
	if normEntry == "." || normEntry == ".." || strings.HasPrefix(normEntry, "../") || strings.HasPrefix(normEntry, "/") {
		resp.Diagnostics.AddAttributeError(
			tfpath.Root("entry_file"),
			"Invalid entry_file Path",
			"Instructions file path must stay within the bundle root.",
		)
		return
	}

	filesOmittedInConfig := false
	if !req.Config.Raw.IsNull() && req.Config.Schema != nil {
		var configFiles types.Map
		_ = req.Config.GetAttribute(ctx, tfpath.Root("files"), &configFiles)
		if configFiles.IsNull() {
			filesOmittedInConfig = true
		}
	}

	if filesOmittedInConfig {
		if !req.State.Raw.IsNull() {
			var stateFiles types.Map
			_ = req.State.GetAttribute(ctx, tfpath.Root("files"), &stateFiles)
			plan.Files = stateFiles
			resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
		}
		return
	}

	// 2. Validate declared files
	if !plan.Files.IsNull() && !plan.Files.IsUnknown() {
		filesElements := plan.Files.Elements()

		if _, hasEntry := filesElements[normEntry]; !hasEntry {
			resp.Diagnostics.AddAttributeError(
				tfpath.Root("files"),
				"Missing Entry File in Files Map",
				fmt.Sprintf("Entry file %q must be declared in files map; removing the bundle entry file is not supported.", normEntry),
			)
		}

		for filePath := range filesElements {
			cleanP := path.Clean(strings.ReplaceAll(filePath, "\\", "/"))
			if cleanP == "." || cleanP == ".." || strings.HasPrefix(cleanP, "../") || strings.HasPrefix(cleanP, "/") {
				resp.Diagnostics.AddAttributeError(
					tfpath.Root("files"),
					"Invalid File Path",
					fmt.Sprintf("Instructions file path %q must stay within the bundle root.", filePath),
				)
			}
			if cleanP == "promptTemplate.legacy.md" {
				resp.Diagnostics.AddAttributeError(
					tfpath.Root("files"),
					"Protected Virtual File Cannot Be Managed",
					"Cannot manage virtual legacy prompt template pseudo-file 'promptTemplate.legacy.md' in files.",
				)
			}
		}
	}
}

func (r *PaperclipAgentInstructionBundleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Client", "Expected configured Paperclip client.")
		return
	}

	var plan PaperclipAgentInstructionBundleResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	agentID := plan.AgentID.ValueString()
	mode := "managed"
	if !plan.Mode.IsNull() && !plan.Mode.IsUnknown() && plan.Mode.ValueString() != "" {
		mode = plan.Mode.ValueString()
	}
	entryFile := "AGENTS.md"
	if !plan.EntryFile.IsNull() && !plan.EntryFile.IsUnknown() && plan.EntryFile.ValueString() != "" {
		entryFile = plan.EntryFile.ValueString()
	}
	clearLegacy := plan.ClearLegacyPromptTemplate.ValueBool()

	var rootPathPtr *string
	if mode == "external" {
		rp := plan.RootPath.ValueString()
		rootPathPtr = &rp
	}

	updateReq := client.UpdateAgentInstructionsBundleRequest{
		Mode:                      mode,
		RootPath:                  rootPathPtr,
		EntryFile:                 entryFile,
		ClearLegacyPromptTemplate: clearLegacy,
	}

	_, err := r.client.UpdateAgentInstructionsBundle(ctx, agentID, updateReq)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error updating Paperclip agent instructions bundle",
			fmt.Sprintf("Failed to update instructions bundle for agent %s: %s", agentID, err.Error()),
		)
		return
	}

	filesConfigured := !plan.Files.IsNull() && !plan.Files.IsUnknown()
	if !req.Config.Raw.IsNull() && req.Config.Schema != nil {
		var configFiles types.Map
		_ = req.Config.GetAttribute(ctx, tfpath.Root("files"), &configFiles)
		filesConfigured = !configFiles.IsNull() && !configFiles.IsUnknown()
	}

	var remoteFiles map[string]string
	if filesConfigured {
		var desiredFiles map[string]string
		_ = plan.Files.ElementsAs(ctx, &desiredFiles, false)

		for fPath, content := range desiredFiles {
			if err := r.client.UpdateAgentInstructionsFileWithClear(ctx, agentID, fPath, content, clearLegacy); err != nil {
				resp.Diagnostics.AddError(
					"Error writing Paperclip agent instructions file",
					fmt.Sprintf("Failed to write file %q for agent %s: %s", fPath, agentID, err.Error()),
				)
				return
			}
		}

		refreshedBundle, err := r.client.GetAgentInstructionsBundle(ctx, agentID)
		if err != nil {
			resp.Diagnostics.AddError("Error fetching updated Paperclip instructions bundle", err.Error())
			return
		}

		for _, f := range refreshedBundle.Files {
			if f.Editable && !f.Virtual {
				if _, declared := desiredFiles[f.Path]; !declared {
					if f.Path == entryFile {
						resp.Diagnostics.AddError(
							"Cannot Remove Entry File",
							fmt.Sprintf("Cannot delete entry file %q from instructions bundle", f.Path),
						)
						return
					}
					if err := r.client.DeleteAgentInstructionsFile(ctx, agentID, f.Path); err != nil {
						resp.Diagnostics.AddError(
							"Error removing undeclared instructions file",
							fmt.Sprintf("Failed to remove undeclared file %q for agent %s: %s", f.Path, agentID, err.Error()),
						)
						return
					}
				}
			}
		}
		remoteFiles = desiredFiles
	} else {
		bundle, err := r.client.GetAgentInstructionsBundle(ctx, agentID)
		if err != nil {
			resp.Diagnostics.AddError("Error fetching instructions bundle", err.Error())
			return
		}
		remoteFiles = make(map[string]string)
		for _, f := range bundle.Files {
			if f.Editable && !f.Virtual {
				content, ferr := r.client.GetAgentInstructionsFile(ctx, agentID, f.Path)
				if ferr != nil {
					if errors.Is(ferr, client.ErrNotFound) {
						continue
					}
					resp.Diagnostics.AddError("Error reading instructions file content", ferr.Error())
					return
				}
				remoteFiles[f.Path] = content
			}
		}
	}

	finalBundle, err := r.client.GetAgentInstructionsBundle(ctx, agentID)
	if err != nil {
		resp.Diagnostics.AddError("Error fetching final Paperclip instructions bundle", err.Error())
		return
	}

	r.populateState(ctx, &plan, finalBundle, remoteFiles)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipAgentInstructionBundleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Client", "Expected configured Paperclip client.")
		return
	}

	var state PaperclipAgentInstructionBundleResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	agentID := state.AgentID.ValueString()
	if agentID == "" {
		agentID = state.ID.ValueString()
	}

	bundle, err := r.client.GetAgentInstructionsBundle(ctx, agentID)
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading Paperclip agent instructions bundle", err.Error())
		return
	}

	remoteFiles := make(map[string]string)
	for _, f := range bundle.Files {
		if f.Editable && !f.Virtual {
			content, ferr := r.client.GetAgentInstructionsFile(ctx, agentID, f.Path)
			if ferr != nil {
				if errors.Is(ferr, client.ErrNotFound) {
					continue
				}
				resp.Diagnostics.AddError("Error reading instructions file content", ferr.Error())
				return
			}
			remoteFiles[f.Path] = content
		}
	}

	r.populateState(ctx, &state, bundle, remoteFiles)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *PaperclipAgentInstructionBundleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Client", "Expected configured Paperclip client.")
		return
	}

	var plan PaperclipAgentInstructionBundleResourceModel
	var state PaperclipAgentInstructionBundleResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	agentID := state.AgentID.ValueString()
	mode := "managed"
	if !plan.Mode.IsNull() && !plan.Mode.IsUnknown() && plan.Mode.ValueString() != "" {
		mode = plan.Mode.ValueString()
	}
	entryFile := "AGENTS.md"
	if !plan.EntryFile.IsNull() && !plan.EntryFile.IsUnknown() && plan.EntryFile.ValueString() != "" {
		entryFile = plan.EntryFile.ValueString()
	}
	clearLegacy := plan.ClearLegacyPromptTemplate.ValueBool()

	var rootPathPtr *string
	if mode == "external" {
		rp := plan.RootPath.ValueString()
		rootPathPtr = &rp
	}

	updateReq := client.UpdateAgentInstructionsBundleRequest{
		Mode:                      mode,
		RootPath:                  rootPathPtr,
		EntryFile:                 entryFile,
		ClearLegacyPromptTemplate: clearLegacy,
	}

	_, err := r.client.UpdateAgentInstructionsBundle(ctx, agentID, updateReq)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error updating Paperclip agent instructions bundle",
			fmt.Sprintf("Failed to update instructions bundle for agent %s: %s", agentID, err.Error()),
		)
		return
	}

	filesConfigured := !plan.Files.IsNull() && !plan.Files.IsUnknown()
	if !req.Config.Raw.IsNull() && req.Config.Schema != nil {
		var configFiles types.Map
		_ = req.Config.GetAttribute(ctx, tfpath.Root("files"), &configFiles)
		filesConfigured = !configFiles.IsNull() && !configFiles.IsUnknown()
	}

	var remoteFiles map[string]string
	if filesConfigured {
		var desiredFiles map[string]string
		_ = plan.Files.ElementsAs(ctx, &desiredFiles, false)

		for fPath, content := range desiredFiles {
			if err := r.client.UpdateAgentInstructionsFileWithClear(ctx, agentID, fPath, content, clearLegacy); err != nil {
				resp.Diagnostics.AddError(
					"Error writing Paperclip agent instructions file",
					fmt.Sprintf("Failed to write file %q for agent %s: %s", fPath, agentID, err.Error()),
				)
				return
			}
		}

		refreshedBundle, err := r.client.GetAgentInstructionsBundle(ctx, agentID)
		if err != nil {
			resp.Diagnostics.AddError("Error fetching updated Paperclip instructions bundle", err.Error())
			return
		}

		for _, f := range refreshedBundle.Files {
			if f.Editable && !f.Virtual {
				if _, declared := desiredFiles[f.Path]; !declared {
					if f.Path == entryFile {
						resp.Diagnostics.AddError(
							"Cannot Remove Entry File",
							fmt.Sprintf("Cannot delete entry file %q from instructions bundle", f.Path),
						)
						return
					}
					if err := r.client.DeleteAgentInstructionsFile(ctx, agentID, f.Path); err != nil {
						resp.Diagnostics.AddError(
							"Error removing undeclared instructions file",
							fmt.Sprintf("Failed to remove undeclared file %q for agent %s: %s", f.Path, agentID, err.Error()),
						)
						return
					}
				}
			}
		}
		remoteFiles = desiredFiles
	} else {
		// When files is omitted in config, do NOT mutate or delete files.
		bundle, err := r.client.GetAgentInstructionsBundle(ctx, agentID)
		if err != nil {
			resp.Diagnostics.AddError("Error fetching instructions bundle", err.Error())
			return
		}
		remoteFiles = make(map[string]string)
		for _, f := range bundle.Files {
			if f.Editable && !f.Virtual {
				content, ferr := r.client.GetAgentInstructionsFile(ctx, agentID, f.Path)
				if ferr != nil {
					if errors.Is(ferr, client.ErrNotFound) {
						continue
					}
					resp.Diagnostics.AddError("Error reading instructions file content", ferr.Error())
					return
				}
				remoteFiles[f.Path] = content
			}
		}
	}

	finalBundle, err := r.client.GetAgentInstructionsBundle(ctx, agentID)
	if err != nil {
		resp.Diagnostics.AddError("Error fetching final Paperclip instructions bundle", err.Error())
		return
	}

	r.populateState(ctx, &plan, finalBundle, remoteFiles)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipAgentInstructionBundleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Client", "Expected configured Paperclip client.")
		return
	}

	var state PaperclipAgentInstructionBundleResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	agentID := state.AgentID.ValueString()
	bundle, err := r.client.GetAgentInstructionsBundle(ctx, agentID)
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			return
		}
		resp.Diagnostics.AddError("Error reading instructions bundle during delete", err.Error())
		return
	}

	for _, f := range bundle.Files {
		if f.Editable && !f.Virtual && f.Path != bundle.EntryFile {
			if err := r.client.DeleteAgentInstructionsFile(ctx, agentID, f.Path); err != nil {
				resp.Diagnostics.AddError(
					"Error deleting instructions bundle file",
					fmt.Sprintf("Failed to delete file %q for agent %s during destroy: %s", f.Path, agentID, err.Error()),
				)
				return
			}
		}
	}

	if err := r.client.UpdateAgentInstructionsFile(ctx, agentID, bundle.EntryFile, ""); err != nil {
		resp.Diagnostics.AddError(
			"Error clearing entry file",
			fmt.Sprintf("Failed to clear entry file %q for agent %s during destroy: %s", bundle.EntryFile, agentID, err.Error()),
		)
		return
	}

	if bundle.Mode == "external" {
		if _, err := r.client.UpdateAgentInstructionsBundle(ctx, agentID, client.UpdateAgentInstructionsBundleRequest{
			Mode:      "managed",
			EntryFile: "AGENTS.md",
		}); err != nil {
			resp.Diagnostics.AddError(
				"Error resetting bundle mode",
				fmt.Sprintf("Failed to reset bundle mode to managed for agent %s during destroy: %s", agentID, err.Error()),
			)
			return
		}
	}
}

func (r *PaperclipAgentInstructionBundleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Client", "Expected configured Paperclip client.")
		return
	}

	id := req.ID
	agentID := id
	if strings.Contains(id, "/") {
		parts := strings.SplitN(id, "/", 2)
		if parts[1] != "" {
			agentID = parts[1]
		}
	}
	if agentID == "" {
		resp.Diagnostics.AddError(
			"Invalid Import ID",
			fmt.Sprintf("Expected <company_id>/<agent_id> or <agent_id>, got %q", id),
		)
		return
	}

	bundle, err := r.client.GetAgentInstructionsBundle(ctx, agentID)
	if err != nil {
		resp.Diagnostics.AddError("Error importing Paperclip agent instructions bundle", err.Error())
		return
	}

	remoteFiles := make(map[string]string)
	for _, f := range bundle.Files {
		if f.Editable && !f.Virtual {
			content, ferr := r.client.GetAgentInstructionsFile(ctx, agentID, f.Path)
			if ferr == nil {
				remoteFiles[f.Path] = content
			}
		}
	}

	var state PaperclipAgentInstructionBundleResourceModel
	state.ClearLegacyPromptTemplate = types.BoolValue(false)
	r.populateState(ctx, &state, bundle, remoteFiles)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *PaperclipAgentInstructionBundleResource) populateState(
	ctx context.Context,
	m *PaperclipAgentInstructionBundleResourceModel,
	bundle *client.AgentInstructionsBundle,
	filesMap map[string]string,
) {
	agentID := bundle.AgentID
	if agentID == "" && !m.AgentID.IsNull() && m.AgentID.ValueString() != "" {
		agentID = m.AgentID.ValueString()
	}
	m.ID = types.StringValue(agentID)
	m.AgentID = types.StringValue(agentID)
	if bundle.Mode != "" {
		m.Mode = types.StringValue(bundle.Mode)
	} else {
		m.Mode = types.StringValue("managed")
	}
	m.RootPath = stringOrNull(bundle.RootPath)
	if bundle.EntryFile != "" {
		m.EntryFile = types.StringValue(bundle.EntryFile)
	} else {
		m.EntryFile = types.StringValue("AGENTS.md")
	}
	if m.ClearLegacyPromptTemplate.IsNull() || m.ClearLegacyPromptTemplate.IsUnknown() {
		m.ClearLegacyPromptTemplate = types.BoolValue(false)
	}

	if len(filesMap) > 0 {
		mFiles, _ := types.MapValueFrom(ctx, types.StringType, filesMap)
		m.Files = mFiles
	} else {
		m.Files = types.MapNull(types.StringType)
	}

	if len(bundle.Files) > 0 {
		inv := make([]InstructionBundleFileInventoryModel, len(bundle.Files))
		for i, f := range bundle.Files {
			inv[i] = InstructionBundleFileInventoryModel{
				Path:        types.StringValue(f.Path),
				Size:        types.Int64Value(f.Size),
				Language:    types.StringValue(f.Language),
				Markdown:    types.BoolValue(f.Markdown),
				IsEntryFile: types.BoolValue(f.IsEntryFile),
				Editable:    types.BoolValue(f.Editable),
				Deprecated:  types.BoolValue(f.Deprecated),
				Virtual:     types.BoolValue(f.Virtual),
			}
		}
		invList, _ := types.ListValueFrom(ctx, types.ObjectType{AttrTypes: instructionBundleFileInventoryAttrTypes}, inv)
		m.FileInventory = invList
	} else {
		m.FileInventory = types.ListNull(types.ObjectType{AttrTypes: instructionBundleFileInventoryAttrTypes})
	}
}
