package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"git.guseynov.org/guseynov/paperclip/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &PaperclipAppPermissionResource{}
	_ resource.ResourceWithConfigure   = &PaperclipAppPermissionResource{}
	_ resource.ResourceWithImportState = &PaperclipAppPermissionResource{}
	_ resource.ResourceWithModifyPlan  = &PaperclipAppPermissionResource{}
)

func NewPaperclipAppPermissionResource() resource.Resource { return &PaperclipAppPermissionResource{} }

type PaperclipAppPermissionResource struct{ client *client.Client }

type PaperclipAppPermissionResourceModel struct {
	ID                      types.String `tfsdk:"id"`
	CompanyID               types.String `tfsdk:"company_id"`
	ConnectionID            types.String `tfsdk:"connection_id"`
	ToolSelection           types.String `tfsdk:"tool_selection"`
	EnabledCatalogEntryIDs  types.List   `tfsdk:"enabled_catalog_entry_ids"`
	AskFirstCatalogEntryIDs types.List   `tfsdk:"ask_first_catalog_entry_ids"`
	ReviewedCatalogEntryIDs types.List   `tfsdk:"reviewed_catalog_entry_ids"`
	AgentIDs                types.List   `tfsdk:"agent_ids"`
	AccessType              types.String `tfsdk:"access_type"`
}

func (r *PaperclipAppPermissionResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_app_permission"
}

func (r *PaperclipAppPermissionResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Applies registry tool selection through Paperclip's native app finish transaction.",
		Attributes: map[string]schema.Attribute{
			"id":                          schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"company_id":                  schema.StringAttribute{Required: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"connection_id":               schema.StringAttribute{Required: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"tool_selection": schema.StringAttribute{
				Required:    true,
				Description: "JSON registry tool names, writes approval mode, and explicit per-tool approval overrides.",
				Validators: []validator.String{
					JSONAttributeValidator(),
				},
				PlanModifiers: []planmodifier.String{
					SemanticJSONPlanModifier(),
				},
			},
			"enabled_catalog_entry_ids":   schema.ListAttribute{ElementType: types.StringType, Computed: true},
			"ask_first_catalog_entry_ids": schema.ListAttribute{ElementType: types.StringType, Computed: true},
			"reviewed_catalog_entry_ids":  schema.ListAttribute{ElementType: types.StringType, Computed: true},
			"agent_ids":                   schema.ListAttribute{ElementType: types.StringType, Required: true},
			"access_type":                 schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("agents")},
		},
	}
}

func (r *PaperclipAppPermissionResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Resource Configure Type", fmt.Sprintf("Expected *client.Client, got: %T", req.ProviderData))
		return
	}
	r.client = c
}

func (r *PaperclipAppPermissionResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if r.client == nil || req.Plan.Raw.IsNull() {
		return
	}
	var plan PaperclipAppPermissionResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() || plan.ConnectionID.IsUnknown() || plan.ToolSelection.IsUnknown() {
		return
	}
	emptyPlan := func() {
		for _, attribute := range []string{"enabled_catalog_entry_ids", "ask_first_catalog_entry_ids", "reviewed_catalog_entry_ids"} {
			resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root(attribute), []string{})...)
		}
	}
	if !plan.AgentIDs.IsUnknown() && len(plan.AgentIDs.Elements()) == 0 {
		// A permission granting no agent converges to no App profile and no policy.
		emptyPlan()
		return
	}
	connection, err := r.client.GetToolConnection(ctx, plan.ConnectionID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error planning Paperclip app permissions", err.Error())
		return
	}
	if connection.AwaitingAuthorization() {
		// Before a user authorizes it, an OAuth connection has no catalog, and
		// the pending permission writes no native App state.
		emptyPlan()
		return
	}
	catalog, err := r.client.GetConnectionCatalog(ctx, plan.ConnectionID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error planning Paperclip app permissions", err.Error())
		return
	}
	if len(catalog) == 0 {
		return
	}
	finish, err := client.SelectAppPermissions(catalog, plan.ToolSelection.ValueString(), nil)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Paperclip tool selection", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("enabled_catalog_entry_ids"), finish.EnabledCatalogEntryIDs)...)
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("ask_first_catalog_entry_ids"), finish.AskFirstCatalogEntryIDs)...)
	// Reviewing quarantine is an apply event, not persistent permission state.
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("reviewed_catalog_entry_ids"), []string{})...)
}

func stringListsEqual(left, right []string) bool {
	a, b := append([]string{}, left...), append([]string{}, right...)
	sort.Strings(a)
	sort.Strings(b)
	return reflect.DeepEqual(a, b)
}

func reconstructToolSelection(catalog []map[string]interface{}, enabledIDs, askFirstIDs []string) string {
	idToName := make(map[string]string)
	idToReadOnly := make(map[string]bool)
	for _, entry := range catalog {
		if id, ok := entry["id"].(string); ok {
			if name, ok := entry["toolName"].(string); ok {
				idToName[id] = name
				readOnly := entry["isReadOnly"] == true && entry["isWrite"] != true && entry["isDestructive"] != true
				idToReadOnly[id] = readOnly
			}
		}
	}
	enabledNames := make([]string, 0, len(enabledIDs))
	askFirstSet := make(map[string]bool, len(askFirstIDs))
	for _, id := range askFirstIDs {
		askFirstSet[id] = true
	}
	overrides := make(map[string]string)
	for _, id := range enabledIDs {
		name, ok := idToName[id]
		if !ok {
			continue
		}
		enabledNames = append(enabledNames, name)
		defaultAskFirst := !idToReadOnly[id]
		actualAskFirst := askFirstSet[id]
		if defaultAskFirst && !actualAskFirst {
			overrides[name] = "auto"
		} else if !defaultAskFirst && actualAskFirst {
			overrides[name] = "writes"
		}
	}
	sort.Strings(enabledNames)
	sel := client.PaperclipToolSelection{
		ApprovalMode: "writes",
		EnabledTools: enabledNames,
	}
	if len(overrides) > 0 {
		sel.ToolApprovalModes = overrides
	}
	data, _ := json.Marshal(sel)
	return string(data)
}

// revoke removes all App access to the connection: emptying its native profile
// revokes every binding and tool, and only then are the approval policies
// gating the revoked tools deleted, so no granted tool is ever left ungated.
func (r *PaperclipAppPermissionResource) revoke(ctx context.Context, companyID, connectionID string) error {
	if err := r.client.RevokeAppPermissionProfile(ctx, companyID, connectionID); err != nil {
		return err
	}
	connection, err := r.client.GetToolConnection(ctx, connectionID)
	if err != nil {
		return err
	}
	if connection.AwaitingAuthorization() {
		// Unbinding the profile revoked all access. The catalog is unreadable
		// until authorization, so any ask-first policy on its tools stays; an
		// approval policy only restricts.
		return nil
	}
	catalog, err := r.client.GetConnectionCatalog(ctx, connectionID)
	if err != nil {
		return err
	}
	if err = r.client.ReconcileAskFirstPolicies(ctx, companyID, "", catalog, nil, nil); err != nil {
		return err
	}
	actual, err := r.client.GetAppPermissionState(ctx, companyID, connectionID)
	if err != nil {
		return err
	}
	if actual.AccessType != "agents" || len(actual.AgentIDs) != 0 || len(actual.EnabledIDs) != 0 || len(actual.StrayPolicyIDs) != 0 {
		return fmt.Errorf("native App access or an approval policy remains after revocation")
	}
	return nil
}

func (r *PaperclipAppPermissionResource) apply(ctx context.Context, plan *PaperclipAppPermissionResourceModel, diagnostics *diag.Diagnostics) {
	if plan.AccessType.ValueString() != "agents" {
		diagnostics.AddError("Invalid Paperclip access scope", "App permissions require explicit agent IDs")
		return
	}
	agentIDs := []string{}
	diagnostics.Append(plan.AgentIDs.ElementsAs(ctx, &agentIDs, false)...)
	if diagnostics.HasError() {
		return
	}
	if len(agentIDs) == 0 {
		if err := r.revoke(ctx, plan.CompanyID.ValueString(), plan.ConnectionID.ValueString()); err != nil {
			diagnostics.AddError("Error revoking Paperclip app permissions", err.Error())
			return
		}
		plan.ID = types.StringValue(plan.ConnectionID.ValueString())
		for _, list := range []*types.List{&plan.EnabledCatalogEntryIDs, &plan.AskFirstCatalogEntryIDs, &plan.ReviewedCatalogEntryIDs} {
			*list, _ = types.ListValueFrom(ctx, types.StringType, []string{})
		}
		return
	}
	connection, err := r.client.GetToolConnection(ctx, plan.ConnectionID.ValueString())
	if err != nil {
		diagnostics.AddError("Error reading Paperclip connection", err.Error())
		return
	}
	if connection.AwaitingAuthorization() {
		// Record the pending permission without native App state. Once a user
		// authorizes the connection, Read finds it unfinished and it is created
		// again through finish.
		plan.ID = types.StringValue(plan.ConnectionID.ValueString())
		for _, list := range []*types.List{&plan.EnabledCatalogEntryIDs, &plan.AskFirstCatalogEntryIDs, &plan.ReviewedCatalogEntryIDs} {
			*list, _ = types.ListValueFrom(ctx, types.StringType, []string{})
		}
		return
	}
	catalog, err := r.client.GetConnectionCatalog(ctx, plan.ConnectionID.ValueString())
	if err == nil && len(catalog) == 0 {
		err = r.client.RefreshConnectionCatalog(ctx, plan.ConnectionID.ValueString())
		if err == nil {
			catalog, err = r.client.GetConnectionCatalog(ctx, plan.ConnectionID.ValueString())
		}
	}
	if err != nil {
		diagnostics.AddError("Error discovering Paperclip catalog", err.Error())
		return
	}
	finish, err := client.SelectAppPermissions(catalog, plan.ToolSelection.ValueString(), agentIDs)
	if err != nil {
		diagnostics.AddError("Invalid Paperclip tool selection", err.Error())
		return
	}
	for _, pair := range []struct {
		planned types.List
		actual  []string
	}{
		{plan.EnabledCatalogEntryIDs, finish.EnabledCatalogEntryIDs},
		{plan.AskFirstCatalogEntryIDs, finish.AskFirstCatalogEntryIDs},
	} {
		if !pair.planned.IsUnknown() && !pair.planned.IsNull() {
			var expected []string
			diagnostics.Append(pair.planned.ElementsAs(ctx, &expected, false)...)
			if diagnostics.HasError() {
				return
			}
			if !stringListsEqual(expected, pair.actual) {
				diagnostics.AddError("Paperclip catalog changed", "Re-plan the tool selection before applying changed catalog permissions")
				return
			}
		}
	}
	// Finish writes company-wide ask-first policies and disables its stale ones,
	// so it receives none and the agent-scoped policy is reconciled after it.
	askFirstIDs := finish.AskFirstCatalogEntryIDs
	finish.AskFirstCatalogEntryIDs = []string{}
	if _, err = r.client.FinishAppPermissions(ctx, plan.CompanyID.ValueString(), plan.ConnectionID.ValueString(), finish); err != nil {
		diagnostics.AddError("Error applying Paperclip app permissions", err.Error())
		return
	}
	if err = r.client.ReconcileAskFirstPolicies(ctx, plan.CompanyID.ValueString(), connection.Name, catalog, askFirstIDs, agentIDs); err != nil {
		diagnostics.AddError("Error applying Paperclip ask-first policies", err.Error())
		return
	}
	actual, err := r.client.GetAppPermissionState(ctx, plan.CompanyID.ValueString(), plan.ConnectionID.ValueString())
	if err != nil {
		diagnostics.AddError("Error verifying Paperclip app permissions", err.Error())
		return
	}
	if actual.AccessType != "agents" || len(actual.StrayPolicyIDs) != 0 || !stringListsEqual(actual.EnabledIDs, finish.EnabledCatalogEntryIDs) || !stringListsEqual(actual.AskFirstIDs, askFirstIDs) || !stringListsEqual(actual.AgentIDs, agentIDs) {
		diagnostics.AddError("Paperclip permission verification failed", "Native tool selection, approval policy or agent scope differs after finish")
		return
	}
	plan.ID = types.StringValue(plan.ConnectionID.ValueString())
	plan.EnabledCatalogEntryIDs, _ = types.ListValueFrom(ctx, types.StringType, actual.EnabledIDs)
	plan.AskFirstCatalogEntryIDs, _ = types.ListValueFrom(ctx, types.StringType, actual.AskFirstIDs)
	plan.ReviewedCatalogEntryIDs, _ = types.ListValueFrom(ctx, types.StringType, []string{})
}

func (r *PaperclipAppPermissionResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan PaperclipAppPermissionResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.apply(ctx, &plan, &resp.Diagnostics)
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	}
}

func (r *PaperclipAppPermissionResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan PaperclipAppPermissionResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.apply(ctx, &plan, &resp.Diagnostics)
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	}
}

func (r *PaperclipAppPermissionResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state PaperclipAppPermissionResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	connection, err := r.client.GetToolConnection(ctx, state.ConnectionID.ValueString())
	if errors.Is(err, client.ErrNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading Paperclip app permissions", err.Error())
		return
	}
	if connection.AwaitingAuthorization() {
		// A pending permission holds no readable native state; it keeps its record
		// until the connection is authorized.
		resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
		return
	}
	var priorAgents []string
	if !state.AgentIDs.IsNull() && !state.AgentIDs.IsUnknown() {
		resp.Diagnostics.Append(state.AgentIDs.ElementsAs(ctx, &priorAgents, false)...)
	}
	actual, err := r.client.GetAppPermissionState(ctx, state.CompanyID.ValueString(), state.ConnectionID.ValueString())
	// Without a native profile, only a permission granting no agent still exists.
	if errors.Is(err, client.ErrNotFound) || err == nil && !actual.Finished && len(priorAgents) != 0 {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading Paperclip app permissions", err.Error())
		return
	}
	state.EnabledCatalogEntryIDs, _ = types.ListValueFrom(ctx, types.StringType, actual.EnabledIDs)
	state.AskFirstCatalogEntryIDs, _ = types.ListValueFrom(ctx, types.StringType, actual.AskFirstIDs)
	if len(actual.StrayPolicyIDs) != 0 {
		// A stray approval policy is drift no ID list represents; null plans the repair.
		state.AskFirstCatalogEntryIDs = types.ListNull(types.StringType)
	}
	state.ReviewedCatalogEntryIDs, _ = types.ListValueFrom(ctx, types.StringType, []string{})
	state.AccessType = types.StringValue(actual.AccessType)
	if !stringListsEqual(priorAgents, actual.AgentIDs) || state.AgentIDs.IsNull() {
		state.AgentIDs, _ = types.ListValueFrom(ctx, types.StringType, actual.AgentIDs)
	}
	state.ID = types.StringValue(state.ConnectionID.ValueString())

	catalog, catErr := r.client.GetConnectionCatalog(ctx, state.ConnectionID.ValueString())
	if catErr == nil && len(catalog) > 0 {
		if state.ToolSelection.IsNull() || state.ToolSelection.IsUnknown() || state.ToolSelection.ValueString() == "" {
			if reconstructed := reconstructToolSelection(catalog, actual.EnabledIDs, actual.AskFirstIDs); reconstructed != "" {
				state.ToolSelection = types.StringValue(reconstructed)
			}
		}
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *PaperclipAppPermissionResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state PaperclipAppPermissionResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// A connection that no longer exists grants nothing.
	if err := r.revoke(ctx, state.CompanyID.ValueString(), state.ConnectionID.ValueString()); err != nil && !errors.Is(err, client.ErrNotFound) {
		resp.Diagnostics.AddError("Error removing Paperclip app permissions", err.Error())
	}
}

func (r *PaperclipAppPermissionResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.SplitN(req.ID, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		resp.Diagnostics.AddError("Invalid permission import ID", "Use company-id/connection-id")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("company_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("connection_id"), parts[1])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
}
