package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Pavel-Guseynov/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &PaperclipWorkspaceResource{}
	_ resource.ResourceWithConfigure   = &PaperclipWorkspaceResource{}
	_ resource.ResourceWithImportState = &PaperclipWorkspaceResource{}
)

func NewPaperclipWorkspaceResource() resource.Resource {
	return &PaperclipWorkspaceResource{}
}

type PaperclipWorkspaceResource struct {
	client *client.Client
}

type PaperclipWorkspaceResourceModel struct {
	ID         types.String `tfsdk:"id"`
	ProjectID  types.String `tfsdk:"project_id"`
	Name       types.String `tfsdk:"name"`
	RepoURL    types.String `tfsdk:"repo_url"`
	Branch     types.String `tfsdk:"branch"`
	IsPrimary  types.Bool   `tfsdk:"is_primary"`
	SourceType types.String `tfsdk:"source_type"`
}

// workspaceBranch reads the declared branch back from Paperclip's repoRef and
// defaultRef. A missing or disagreeing pair reads back empty, so the next plan
// restores both refs to the declared branch instead of hiding the drift.
func workspaceBranch(ws *client.PaperclipWorkspace) types.String {
	if ws.RepoRef != ws.DefaultRef {
		return types.StringValue("")
	}
	return types.StringValue(ws.RepoRef)
}

func workspaceRequest(projectID string, model PaperclipWorkspaceResourceModel) client.PaperclipWorkspace {
	branch := model.Branch.ValueString()
	return client.PaperclipWorkspace{
		ProjectID:  projectID,
		Name:       model.Name.ValueString(),
		RepoURL:    model.RepoURL.ValueString(),
		RepoRef:    branch,
		DefaultRef: branch,
		IsPrimary:  model.IsPrimary.ValueBool(),
		SourceType: model.SourceType.ValueString(),
	}
}

func applyWorkspaceState(model *PaperclipWorkspaceResourceModel, ws *client.PaperclipWorkspace) {
	model.ID = types.StringValue(ws.ID)
	if ws.ProjectID != "" {
		model.ProjectID = types.StringValue(ws.ProjectID)
	}
	model.Name = types.StringValue(ws.Name)
	model.RepoURL = types.StringValue(ws.RepoURL)
	model.Branch = workspaceBranch(ws)
	model.IsPrimary = types.BoolValue(ws.IsPrimary)
	if ws.SourceType != "" {
		model.SourceType = types.StringValue(ws.SourceType)
	}
}

func (r *PaperclipWorkspaceResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_workspace"
}

func (r *PaperclipWorkspaceResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a Paperclip workspace for a project.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Unique identifier for the workspace.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"project_id": schema.StringAttribute{
				Description: "The Paperclip project ID.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"name": schema.StringAttribute{
				Description: "Workspace identifier name.",
				Required:    true,
			},
			"repo_url": schema.StringAttribute{
				Description: "Git repository URL for checkout.",
				Required:    true,
			},
			"branch": schema.StringAttribute{
				Description: "Declared repository branch (e.g. develop or main), applied to Paperclip's repoRef and defaultRef; a live workspace whose refs are missing or disagree reads back empty and plans an update.",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("develop"),
			},
			"is_primary": schema.BoolAttribute{
				Description: "Whether this workspace is the project's primary workspace.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
			},
			"source_type": schema.StringAttribute{
				Description: "Workspace source type (e.g. git_repo).",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("git_repo"),
			},
		},
	}
}

func (r *PaperclipWorkspaceResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *PaperclipWorkspaceResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan PaperclipWorkspaceResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	projectID := plan.ProjectID.ValueString()
	created, err := r.client.CreateWorkspace(ctx, projectID, workspaceRequest(projectID, plan))
	if err != nil {
		resp.Diagnostics.AddError("Error creating Paperclip workspace", err.Error())
		return
	}

	applyWorkspaceState(&plan, created)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipWorkspaceResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state PaperclipWorkspaceResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	ws, err := r.client.GetWorkspaceInProject(
		ctx, state.ProjectID.ValueString(), state.ID.ValueString(),
	)
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading Paperclip workspace", err.Error())
		return
	}

	applyWorkspaceState(&state, ws)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *PaperclipWorkspaceResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan PaperclipWorkspaceResourceModel
	var state PaperclipWorkspaceResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	projectID := state.ProjectID.ValueString()
	updated, err := r.client.UpdateWorkspace(
		ctx,
		projectID,
		state.ID.ValueString(),
		workspaceRequest(projectID, plan),
	)
	if err != nil {
		resp.Diagnostics.AddError("Error updating Paperclip workspace", err.Error())
		return
	}

	applyWorkspaceState(&plan, updated)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipWorkspaceResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state PaperclipWorkspaceResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.client.DeleteWorkspace(
		ctx,
		state.ProjectID.ValueString(),
		state.ID.ValueString(),
	); err != nil {
		if !errors.Is(err, client.ErrNotFound) {
			resp.Diagnostics.AddError("Error deleting Paperclip workspace", err.Error())
			return
		}
	}
}

func (r *PaperclipWorkspaceResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id := req.ID
	if strings.Contains(id, "/") {
		parts := strings.SplitN(id, "/", 2)
		if parts[0] != "" && parts[1] != "" {
			resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("project_id"), parts[0])...)
			resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
			return
		}
	}
	resp.Diagnostics.AddError(
		"Invalid Workspace Import ID",
		fmt.Sprintf("Expected <project_id>/<workspace_id>, got %q", id),
	)
}
