package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Pavel-Guseynov/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// toolAppTypeValidator enforces the client's authoritative allowed-value set
// at plan time, before any mutation reaches Paperclip.
type toolAppTypeValidator struct{}

func (toolAppTypeValidator) Description(context.Context) string {
	return fmt.Sprintf("value must be one of: %s", strings.Join(client.ToolAppTypes, ", "))
}

func (v toolAppTypeValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (toolAppTypeValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if err := client.ValidateToolAppType(req.ConfigValue.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid Application Type", err.Error())
	}
}

var (
	_ resource.Resource                = &PaperclipAppResource{}
	_ resource.ResourceWithConfigure   = &PaperclipAppResource{}
	_ resource.ResourceWithImportState = &PaperclipAppResource{}
)

func NewPaperclipAppResource() resource.Resource {
	return &PaperclipAppResource{}
}

type PaperclipAppResource struct {
	client *client.Client
}

type PaperclipAppResourceModel struct {
	ID             types.String `tfsdk:"id"`
	CompanyID      types.String `tfsdk:"company_id"`
	Name           types.String `tfsdk:"name"`
	Type           types.String `tfsdk:"type"`
	ApplicationKey types.String `tfsdk:"application_key"`
	Description    types.String `tfsdk:"description"`
	Metadata       types.String `tfsdk:"metadata"`
}

func normalizePaperclipAppState(state *PaperclipAppResourceModel, app *client.PaperclipToolApp) {
	state.ID = types.StringValue(app.ID)
	if app.CompanyID != "" {
		state.CompanyID = types.StringValue(app.CompanyID)
	}
	state.Name = types.StringValue(app.Name)
	state.ApplicationKey = stringOrNull(app.ApplicationKey)
	state.Description = stringOrNull(app.Description)
	state.Metadata = refreshSemanticJSON(state.Metadata, app.Metadata)
}

func (r *PaperclipAppResource) createAppState(ctx context.Context, plan PaperclipAppResourceModel) (PaperclipAppResourceModel, error) {
	if plan.Type.IsNull() || plan.Type.IsUnknown() {
		return plan, errors.New("application type is required")
	}
	appType := plan.Type.ValueString()
	if err := client.ValidateToolAppType(appType); err != nil {
		return plan, err
	}
	var metaMap map[string]interface{}
	if !plan.Metadata.IsNull() && !plan.Metadata.IsUnknown() && plan.Metadata.ValueString() != "" {
		if err := json.Unmarshal([]byte(plan.Metadata.ValueString()), &metaMap); err != nil {
			return plan, fmt.Errorf("invalid metadata JSON: %w", err)
		}
	}
	created, err := r.client.CreateToolApp(ctx, plan.CompanyID.ValueString(), client.PaperclipToolApp{
		CompanyID: plan.CompanyID.ValueString(), Name: plan.Name.ValueString(), Type: appType, ApplicationKey: plan.ApplicationKey.ValueString(),
		Description: plan.Description.ValueString(), Metadata: metaMap,
	})
	if err != nil {
		return plan, err
	}
	normalizePaperclipAppState(&plan, created)
	return plan, nil
}

func (r *PaperclipAppResource) readAppState(ctx context.Context, state PaperclipAppResourceModel) (PaperclipAppResourceModel, error) {
	app, err := r.client.GetToolApp(ctx, state.CompanyID.ValueString(), state.ID.ValueString())
	if err != nil {
		return state, err
	}
	// An archived application is Paperclip's retired state; it reads back as
	// absent so a retired resource never plans a second retirement.
	if app.Status == client.ToolAppArchivedStatus {
		return state, client.ErrNotFound
	}
	if app.Type != "" {
		state.Type = types.StringValue(app.Type)
	} else if state.Type.IsNull() || state.Type.IsUnknown() || state.Type.ValueString() == "" {
		return state, fmt.Errorf("Paperclip did not return a type for application %s and no prior state value exists; the required type attribute cannot be populated", app.ID)
	}
	normalizePaperclipAppState(&state, app)
	return state, nil
}

func (r *PaperclipAppResource) updateAppState(ctx context.Context, plan, state PaperclipAppResourceModel) (PaperclipAppResourceModel, error) {
	if plan.Type.IsNull() || plan.Type.IsUnknown() {
		return plan, errors.New("application type is required")
	}
	appType := plan.Type.ValueString()
	if err := client.ValidateToolAppType(appType); err != nil {
		return plan, err
	}
	var metaMap map[string]interface{}
	if !plan.Metadata.IsNull() && !plan.Metadata.IsUnknown() && plan.Metadata.ValueString() != "" {
		if err := json.Unmarshal([]byte(plan.Metadata.ValueString()), &metaMap); err != nil {
			return plan, fmt.Errorf("invalid metadata JSON: %w", err)
		}
	}
	updated, err := r.client.UpdateToolApp(ctx, state.ID.ValueString(), client.PaperclipToolApp{
		CompanyID: state.CompanyID.ValueString(), Name: plan.Name.ValueString(), Type: appType, ApplicationKey: plan.ApplicationKey.ValueString(),
		Description: plan.Description.ValueString(), Metadata: metaMap,
	})
	if err != nil {
		return plan, err
	}
	normalizePaperclipAppState(&plan, updated)
	return plan, nil
}

func (r *PaperclipAppResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_app"
}

func (r *PaperclipAppResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a Paperclip-native MCP application registration. Refresh and import select the exact application ID from the declared company's application list. Import using <company_id>/<app_id>. Removing the resource archives the application, the retirement Paperclip accepts while connection rows keep referencing it; an archived application reads back as absent.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Unique identifier for the application.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"company_id": schema.StringAttribute{
				Description: "The Paperclip company ID.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"name": schema.StringAttribute{
				Description: "Application name (e.g. acme.mcp.openobserve).",
				Required:    true,
			},
			"type": schema.StringAttribute{
				Description: fmt.Sprintf("Application type, one of: %s. Changing this forces replacement, because the type defines which connection and template shapes hang off the application.", strings.Join(client.ToolAppTypes, ", ")),
				Required:    true,
				Validators: []validator.String{
					toolAppTypeValidator{},
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"application_key": schema.StringAttribute{
				Description: "Optional stable Paperclip application key.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"description": schema.StringAttribute{
				Description: "Optional application description.",
				Optional:    true,
			},
			"metadata": schema.StringAttribute{
				Description: "Optional JSON metadata object for the application.",
				Optional:    true,
				Validators: []validator.String{
					JSONAttributeValidator(),
				},
				PlanModifiers: []planmodifier.String{
					SemanticJSONPlanModifier(),
				},
			},
		},
	}
}

func (r *PaperclipAppResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *PaperclipAppResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan PaperclipAppResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	plan, err := r.createAppState(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Error creating Paperclip application", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipAppResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state PaperclipAppResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	state, err := r.readAppState(ctx, state)
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading Paperclip application", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *PaperclipAppResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan PaperclipAppResourceModel
	var state PaperclipAppResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	plan, err := r.updateAppState(ctx, plan, state)
	if err != nil {
		resp.Diagnostics.AddError("Error updating Paperclip application", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipAppResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state PaperclipAppResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.client.ArchiveToolApp(ctx, state.ID.ValueString()); err != nil {
		if !errors.Is(err, client.ErrNotFound) {
			resp.Diagnostics.AddError("Error archiving Paperclip application", err.Error())
			return
		}
	}
}

func (r *PaperclipAppResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(req.ID, "/")
	if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("company_id"), parts[0])...)
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
		return
	}
	resp.Diagnostics.AddError(
		"Invalid Application Import ID",
		fmt.Sprintf("Expected <company_id>/<app_id>, got %q", req.ID),
	)
}
