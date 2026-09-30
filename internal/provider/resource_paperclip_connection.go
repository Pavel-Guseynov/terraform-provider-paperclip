package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"git.guseynov.org/guseynov/paperclip/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &PaperclipConnectionResource{}
	_ resource.ResourceWithConfigure   = &PaperclipConnectionResource{}
	_ resource.ResourceWithImportState = &PaperclipConnectionResource{}
	_ resource.ResourceWithModifyPlan  = &PaperclipConnectionResource{}
)

func NewPaperclipConnectionResource() resource.Resource {
	return &PaperclipConnectionResource{}
}

type PaperclipConnectionResource struct {
	client *client.Client
}

type PaperclipConnectionResourceModel struct {
	ID                   types.String `tfsdk:"id"`
	CompanyID            types.String `tfsdk:"company_id"`
	AppID                types.String `tfsdk:"app_id"`
	Name                 types.String `tfsdk:"name"`
	Transport            types.String `tfsdk:"transport"`
	AuthKind             types.String `tfsdk:"auth_kind"`
	Config               types.String `tfsdk:"config"`
	CredentialRefs       types.String `tfsdk:"credential_refs"`
	CredentialPolicy     types.String `tfsdk:"credential_policy"`
	GalleryKey           types.String `tfsdk:"gallery_key"`
	CredentialGrantReady types.Bool   `tfsdk:"credential_grant_ready"`
}

func (r *PaperclipConnectionResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_connection"
}

func (r *PaperclipConnectionResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a Paperclip-native tool connection.",
		Attributes: map[string]schema.Attribute{
			"credential_grant_ready": schema.BoolAttribute{Computed: true, Description: "Whether the native default execution grant has the declared protected credential references."},
			"id": schema.StringAttribute{
				Description: "Unique identifier for the connection.",
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
			"app_id": schema.StringAttribute{
				Description: "Existing parent tool application ID; required unless gallery_key names an included connector, whose application Paperclip creates.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplace(),
				},
			},
			"name": schema.StringAttribute{
				Description: "Connection name; Paperclip names an included gallery connector.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"gallery_key": schema.StringAttribute{
				Description: "Paperclip app gallery slug of an included connector, connected through its gallery definition and identified by config.sourceTemplateKey.",
				Optional:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"transport": schema.StringAttribute{
				Description: "Connection transport type (e.g. stdio, mcp_remote).",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("local_stdio"),
			},
			"auth_kind": schema.StringAttribute{
				Description: "Optional authentication type.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"config": schema.StringAttribute{
				Description: "JSON connection configuration (command, args, env, url).",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"credential_refs": schema.StringAttribute{
				Description: "Optional JSON array of Paperclip credential references.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"credential_policy": schema.StringAttribute{
				Description: "Paperclip credential policy (shared or per_user), fixed at creation; an OAuth connection defaults to per_user, owned by its creator.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplace(),
				},
			},
		},
	}
}

func (r *PaperclipConnectionResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if !req.Plan.Raw.IsNull() {
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("credential_grant_ready"), true)...)
	}
}

// knownCredentialPolicy prefers the policy Paperclip reports, keeps a known
// prior value when it reports none, and resolves an unknown one to null.
func knownCredentialPolicy(current types.String, remote string) types.String {
	if remote != "" {
		return types.StringValue(remote)
	}
	if current.IsUnknown() {
		return types.StringNull()
	}
	return current
}

func preserveEquivalentConnectionJSON(current types.String, remote interface{}) types.String {
	data, _ := json.Marshal(remote)
	var prior, actual interface{}
	if !current.IsUnknown() && !current.IsNull() && json.Unmarshal([]byte(current.ValueString()), &prior) == nil && json.Unmarshal(data, &actual) == nil && reflect.DeepEqual(prior, actual) {
		return current
	}
	return types.StringValue(string(data))
}

func (r *PaperclipConnectionResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *PaperclipConnectionResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan PaperclipConnectionResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var configMap map[string]interface{}
	var credentialRefs []map[string]interface{}
	if !plan.Config.IsNull() && !plan.Config.IsUnknown() && plan.Config.ValueString() != "" {
		if err := json.Unmarshal([]byte(plan.Config.ValueString()), &configMap); err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("config"), "Invalid connection configuration", err.Error())
			return
		}
	}
	if !plan.CredentialRefs.IsNull() && !plan.CredentialRefs.IsUnknown() && plan.CredentialRefs.ValueString() != "" {
		if err := json.Unmarshal([]byte(plan.CredentialRefs.ValueString()), &credentialRefs); err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("credential_refs"), "Invalid credential references", err.Error())
			return
		}
	}

	conn := client.PaperclipToolConnection{
		CompanyID:        plan.CompanyID.ValueString(),
		AppID:            plan.AppID.ValueString(),
		Name:             plan.Name.ValueString(),
		Transport:        plan.Transport.ValueString(),
		AuthKind:         plan.AuthKind.ValueString(),
		Config:           configMap,
		CredentialRefs:   credentialRefs,
		CredentialPolicy: plan.CredentialPolicy.ValueString(),
	}
	secretRefs, err := client.ConnectionCredentialSecretRefs(credentialRefs)
	if err != nil {
		resp.Diagnostics.AddError("Invalid credential references", err.Error())
		return
	}
	conn.CredentialSecretRefs = []map[string]interface{}{}

	var created *client.PaperclipToolConnection
	if galleryKey := plan.GalleryKey.ValueString(); galleryKey != "" {
		created, err = r.client.ConnectGalleryApp(ctx, plan.CompanyID.ValueString(), galleryKey)
	} else if plan.AppID.ValueString() == "" {
		resp.Diagnostics.AddAttributeError(path.Root("app_id"), "Missing Paperclip application", "A connection that is not an included gallery connector requires app_id.")
		return
	} else {
		created, err = r.client.CreateToolConnection(ctx, plan.CompanyID.ValueString(), conn)
	}
	if err != nil {
		resp.Diagnostics.AddError("Error creating Paperclip connection", err.Error())
		return
	}

	plan.ID = types.StringValue(created.ID)
	plan.CompanyID = types.StringValue(created.CompanyID)
	plan.Name = types.StringValue(created.Name)
	if created.AppID != "" {
		plan.AppID = types.StringValue(created.AppID)
	} else if plan.AppID.IsUnknown() {
		plan.AppID = types.StringNull()
	}
	// A gallery connector's configuration is Paperclip's; record what it built.
	if plan.Config.IsUnknown() {
		plan.Config = preserveEquivalentConnectionJSON(plan.Config, created.Config)
	}
	if created.Transport != "" {
		plan.Transport = types.StringValue(created.Transport)
	}
	if created.AuthKind != "" {
		plan.AuthKind = types.StringValue(created.AuthKind)
	}
	plan.CredentialPolicy = knownCredentialPolicy(plan.CredentialPolicy, created.CredentialPolicy)
	// A computed reference list must be known after apply: record what Paperclip
	// echoes, or the empty list a credential-less connection reads back later.
	if created.CredentialRefs != nil {
		plan.CredentialRefs = preserveEquivalentConnectionJSON(plan.CredentialRefs, created.CredentialRefs)
	} else if plan.CredentialRefs.IsUnknown() || plan.CredentialRefs.IsNull() {
		plan.CredentialRefs = types.StringValue("[]")
	}
	// Retain the remote identity even if the following grant transaction fails.
	plan.CredentialGrantReady = types.BoolValue(false)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if len(credentialRefs) > 0 {
		if err := r.client.EnsureConnectionCredentialGrant(ctx, created.ID, secretRefs); err != nil {
			resp.Diagnostics.AddError("Error reconciling connection credential grant", err.Error())
			return
		}
	}
	plan.CredentialGrantReady = types.BoolValue(true)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipConnectionResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state PaperclipConnectionResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	conn, err := r.client.GetToolConnection(ctx, state.ID.ValueString())
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading Paperclip connection", err.Error())
		return
	}
	// Deleting a connection archives it; an archived connection is retired and
	// reads back as absent so the declared connection is created again.
	if conn.Status == client.ToolConnectionArchivedStatus {
		resp.State.RemoveResource(ctx)
		return
	}

	state.ID = types.StringValue(conn.ID)
	if conn.CompanyID != "" {
		state.CompanyID = types.StringValue(conn.CompanyID)
	}
	state.Name = types.StringValue(conn.Name)
	if conn.AppID != "" {
		state.AppID = types.StringValue(conn.AppID)
	} else if state.AppID.IsNull() || state.AppID.IsUnknown() || state.AppID.ValueString() == "" {
		resp.Diagnostics.AddError(
			"Error reading Paperclip connection",
			fmt.Sprintf("Paperclip did not return an applicationId for connection %s and no prior state value exists; the required app_id attribute cannot be populated", conn.ID),
		)
		return
	}
	if conn.Transport != "" {
		state.Transport = types.StringValue(conn.Transport)
	}
	if conn.AuthKind != "" {
		state.AuthKind = types.StringValue(conn.AuthKind)
	}
	state.CredentialPolicy = knownCredentialPolicy(state.CredentialPolicy, conn.CredentialPolicy)
	// The gallery source identifies an included connector, so an adopted one
	// matches its declaration and a custom one plans its replacement.
	state.GalleryKey = types.StringNull()
	if source, ok := conn.Config["sourceTemplateKey"].(string); ok && source != "" {
		state.GalleryKey = types.StringValue(source)
	}
	if conn.Config != nil {
		state.Config = preserveEquivalentConnectionJSON(state.Config, conn.Config)
	}
	if conn.CredentialRefs != nil {
		state.CredentialRefs = preserveEquivalentConnectionJSON(state.CredentialRefs, conn.CredentialRefs)
	}
	state.CredentialGrantReady = types.BoolValue(true)
	if len(conn.CredentialRefs) > 0 {
		refs, err := client.ConnectionCredentialSecretRefs(conn.CredentialRefs)
		if err != nil {
			resp.Diagnostics.AddError("Invalid native credential references", err.Error())
			return
		}
		grant, err := r.client.GetDefaultConnectionGrant(ctx, conn.ID)
		if err != nil {
			resp.Diagnostics.AddError("Error reading connection credential grant", err.Error())
			return
		}
		state.CredentialGrantReady = types.BoolValue(client.ConnectionGrantMatches(grant, refs))
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *PaperclipConnectionResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan PaperclipConnectionResourceModel
	var state PaperclipConnectionResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var configMap map[string]interface{}
	var credentialRefs []map[string]interface{}
	if !plan.Config.IsNull() && !plan.Config.IsUnknown() && plan.Config.ValueString() != "" {
		if err := json.Unmarshal([]byte(plan.Config.ValueString()), &configMap); err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("config"), "Invalid connection configuration", err.Error())
			return
		}
	}
	if !plan.CredentialRefs.IsNull() && !plan.CredentialRefs.IsUnknown() && plan.CredentialRefs.ValueString() != "" {
		if err := json.Unmarshal([]byte(plan.CredentialRefs.ValueString()), &credentialRefs); err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("credential_refs"), "Invalid credential references", err.Error())
			return
		}
	}

	conn := client.PaperclipToolConnection{
		CompanyID:      state.CompanyID.ValueString(),
		AppID:          plan.AppID.ValueString(),
		Name:           plan.Name.ValueString(),
		Transport:      plan.Transport.ValueString(),
		AuthKind:       plan.AuthKind.ValueString(),
		Config:         configMap,
		CredentialRefs: credentialRefs,
	}
	secretRefs, err := client.ConnectionCredentialSecretRefs(credentialRefs)
	if err != nil {
		resp.Diagnostics.AddError("Invalid credential references", err.Error())
		return
	}
	conn.CredentialSecretRefs = []map[string]interface{}{}

	updated, err := r.client.UpdateToolConnection(ctx, state.ID.ValueString(), conn)
	if err != nil {
		resp.Diagnostics.AddError("Error updating Paperclip connection", err.Error())
		return
	}

	plan.ID = types.StringValue(updated.ID)
	plan.Name = types.StringValue(updated.Name)
	if updated.AppID != "" {
		plan.AppID = types.StringValue(updated.AppID)
	}
	if updated.Transport != "" {
		plan.Transport = types.StringValue(updated.Transport)
	}
	if updated.AuthKind != "" {
		plan.AuthKind = types.StringValue(updated.AuthKind)
	}
	plan.CredentialPolicy = knownCredentialPolicy(plan.CredentialPolicy, updated.CredentialPolicy)
	if len(credentialRefs) > 0 {
		if err := r.client.EnsureConnectionCredentialGrant(ctx, state.ID.ValueString(), secretRefs); err != nil {
			resp.Diagnostics.AddError("Error reconciling connection credential grant", err.Error())
			return
		}
	}
	plan.CredentialGrantReady = types.BoolValue(true)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipConnectionResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state PaperclipConnectionResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.client.DeleteToolConnection(ctx, state.ID.ValueString()); err != nil {
		if !errors.Is(err, client.ErrNotFound) {
			resp.Diagnostics.AddError("Error deleting Paperclip connection", err.Error())
			return
		}
	}
}

func (r *PaperclipConnectionResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id := req.ID
	if strings.Contains(id, "/") {
		parts := strings.SplitN(id, "/", 2)
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("company_id"), parts[0])...)
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
		return
	}
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
