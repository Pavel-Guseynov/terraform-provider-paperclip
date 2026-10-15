package provider

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Pavel-Guseynov/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type PaperclipConnectionGrantsResource struct{ client *client.Client }
type connectionGrantsModel struct {
	ID            types.String `tfsdk:"id"`
	ConnectionID  types.String `tfsdk:"connection_id"`
	CompanyID     types.String `tfsdk:"company_id"`
	OwnerID       types.String `tfsdk:"owner_id"`
	SecretRefs    types.Map    `tfsdk:"credential_secret_refs"`
	MemberUserIDs types.Set    `tfsdk:"member_user_ids"`
	Delegations   types.Map    `tfsdk:"delegations"`
	GrantID       types.String `tfsdk:"grant_id"`
	Ready         types.Bool   `tfsdk:"ready"`
}

func NewPaperclipConnectionGrantsResource() resource.Resource {
	return &PaperclipConnectionGrantsResource{}
}
func (r *PaperclipConnectionGrantsResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_connection_grants"
}
func (r *PaperclipConnectionGrantsResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{Description: "Single aggregate owner of a connection's default organization installation grant, its member audience, and declared personal-grant delegations. Rotation revokes superseded organization grants. Personal credentials require native consent; this resource never mints or resolves credentials. Separate states must coordinate ownership externally because the native API has no owner/CAS field.", Attributes: map[string]schema.Attribute{
		"id":                     schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
		"connection_id":          schema.StringAttribute{Required: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
		"company_id":             schema.StringAttribute{Required: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
		"owner_id":               schema.StringAttribute{Computed: true, Description: "Provider ownership identity for this aggregate.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
		"credential_secret_refs": toolSecretRefsAttribute(),
		"member_user_ids":        schema.SetAttribute{Optional: true, ElementType: types.StringType, Description: "Default organization grant audience. An empty set means all company members in the native API."},
		"delegations":            schema.MapAttribute{Optional: true, ElementType: types.SetType{ElemType: types.StringType}, Description: "Personal grant IDs mapped to agent-ID sets. Only the named personal grant owner can create delegations. Removing a previously managed delegation revokes it."},
		"grant_id":               schema.StringAttribute{Computed: true, Description: "Current native default organization grant ID, changed when credentials rotate."},
		"ready":                  schema.BoolAttribute{Computed: true, Description: "Whether the default grant is active and superseded organization grants are revoked."},
	}}
}
func (r *PaperclipConnectionGrantsResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	var ok bool
	r.client, ok = req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Resource Configure Type", fmt.Sprintf("Expected *client.Client, got: %T", req.ProviderData))
	}
}

func (r *PaperclipConnectionGrantsResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() || r.client == nil {
		return
	}
	var plan connectionGrantsModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(validateToolRefs(ctx, r.client, plan.CompanyID.ValueString(), plan.SecretRefs)...)
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("ready"), true)...)
	if plan.ConnectionID.IsUnknown() || plan.CompanyID.IsUnknown() {
		return
	}
	if err := claimAggregate(r.client, "grants", plan.ConnectionID.ValueString(), &plan.OwnerID); err != nil {
		resp.Diagnostics.AddError("Connection grant ownership conflict", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("owner_id"), plan.OwnerID)...)
	if _, _, _, err := r.preflight(ctx, &plan); err != nil {
		resp.Diagnostics.AddError("Invalid connection grant reference", err.Error())
	}
}

func (r *PaperclipConnectionGrantsResource) reconcile(ctx context.Context, plan *connectionGrantsModel, previous connectionGrantsModel) error {
	if err := claimAggregate(r.client, "grants", plan.ConnectionID.ValueString(), &plan.OwnerID); err != nil {
		return err
	}
	conn, grants, members, err := r.preflight(ctx, plan)
	if err != nil {
		return err
	}
	plan.ID = types.StringValue(conn.ID)
	plan.Ready = types.BoolValue(false)
	if plan.GrantID.IsUnknown() {
		plan.GrantID = types.StringNull()
	}
	wanted := delegationMap(ctx, plan.Delegations)
	prior := delegationMap(ctx, previous.Delegations)
	if err := r.client.EnsureConnectionCredentialGrant(ctx, conn.ID, toolRefsToAPI(ctx, plan.SecretRefs)); err != nil {
		return err
	}
	current, err := r.client.GetDefaultConnectionGrant(ctx, conn.ID)
	if err != nil {
		return err
	}
	plan.GrantID = types.StringValue(current.ID)
	sort.Strings(members)
	if err := r.client.ReplaceConnectionGrantMembers(ctx, conn.ID, current.ID, members); err != nil {
		return err
	}
	for _, grant := range grants {
		for _, delegation := range grant.Delegations {
			if containsString(prior[grant.ID], delegation.AgentID) && !containsString(wanted[grant.ID], delegation.AgentID) {
				if err := r.client.RevokeConnectionGrantDelegation(ctx, conn.ID, grant.ID, delegation.ID); err != nil {
					return err
				}
			}
		}
		for _, agentID := range wanted[grant.ID] {
			found := false
			for _, delegation := range grant.Delegations {
				if delegation.AgentID == agentID {
					found = true
				}
			}
			if !found {
				if err := r.client.CreateConnectionGrantDelegation(ctx, conn.ID, grant.ID, agentID); err != nil {
					return err
				}
			}
		}
	}
	plan.ID = types.StringValue(conn.ID)
	plan.Ready = types.BoolValue(true)
	return nil
}

func (r *PaperclipConnectionGrantsResource) preflight(ctx context.Context, plan *connectionGrantsModel) (*client.PaperclipToolConnection, []client.ConnectionGrantAudience, []string, error) {
	conn, err := r.client.GetToolConnection(ctx, plan.ConnectionID.ValueString())
	if err != nil {
		return nil, nil, nil, err
	}
	if conn.CompanyID != plan.CompanyID.ValueString() {
		return nil, nil, nil, fmt.Errorf("connection does not belong to the declared company; no mutation was sent")
	}
	if diags := validateToolRefs(ctx, r.client, plan.CompanyID.ValueString(), plan.SecretRefs); diags.HasError() {
		return nil, nil, nil, fmt.Errorf("invalid protected grant references: %s", diags.Errors()[0].Detail())
	}
	grants, err := r.client.ListConnectionGrantAudiences(ctx, conn.ID)
	if err != nil {
		return nil, nil, nil, err
	}
	members := []string{}
	if !plan.MemberUserIDs.IsNull() {
		plan.MemberUserIDs.ElementsAs(ctx, &members, false)
	}
	for _, userID := range members {
		member, err := r.client.GetCompanyMember(ctx, conn.CompanyID, userID)
		if err != nil {
			return nil, nil, nil, err
		}
		if member.Status != "active" || member.PrincipalType != "user" {
			return nil, nil, nil, fmt.Errorf("grant audience requires active company users; no mutation was sent")
		}
	}
	wanted := delegationMap(ctx, plan.Delegations)
	// Validate all known references before installing or revoking a grant.
	for grantID, agents := range wanted {
		found := false
		for _, grant := range grants {
			if grant.ID == grantID && grant.Kind == "user" && grant.Status == "active" {
				found = true
				for _, agentID := range agents {
					existing := false
					for _, delegation := range grant.Delegations {
						if delegation.AgentID == agentID {
							existing = true
						}
					}
					if !existing && (grant.CallerUserID == "" || grant.CallerUserID != grant.SubjectUserID) {
						return nil, nil, nil, fmt.Errorf("only the named personal grant owner can create delegations; authenticate as that user. No mutation was sent")
					}
				}
			}
		}
		if !found {
			return nil, nil, nil, fmt.Errorf("delegation requires an existing active personal grant %s; complete native consent first. No mutation was sent", grantID)
		}
		for _, agentID := range agents {
			agent, err := r.client.GetAgent(ctx, agentID)
			if err != nil {
				return nil, nil, nil, err
			}
			if agent.CompanyID != conn.CompanyID {
				return nil, nil, nil, fmt.Errorf("delegated agent does not belong to the connection company; no mutation was sent")
			}
		}
	}
	return conn, grants, members, nil
}

func delegationMap(ctx context.Context, value types.Map) map[string][]string {
	result := map[string][]string{}
	if !value.IsNull() && !value.IsUnknown() {
		value.ElementsAs(ctx, &result, false)
	}
	return result
}
func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func (r *PaperclipConnectionGrantsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan connectionGrantsModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.reconcile(ctx, &plan, connectionGrantsModel{}); err != nil {
		if plan.ID.ValueString() != "" && !plan.Ready.IsNull() && !plan.Ready.ValueBool() {
			resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
		}
		resp.Diagnostics.AddError("Error reconciling connection grants", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}
func (r *PaperclipConnectionGrantsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state connectionGrantsModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if plan.OwnerID.IsNull() || plan.OwnerID.IsUnknown() {
		plan.OwnerID = state.OwnerID
	}
	if err := r.reconcile(ctx, &plan, state); err != nil {
		if plan.ID.ValueString() != "" && !plan.Ready.IsNull() && !plan.Ready.ValueBool() {
			resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
		}
		resp.Diagnostics.AddError("Error reconciling connection grants", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}
func (r *PaperclipConnectionGrantsResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state connectionGrantsModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	grants, err := r.client.ListConnectionGrantAudiences(ctx, state.ConnectionID.ValueString())
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
		} else {
			resp.Diagnostics.AddError("Error reading connection grants", err.Error())
		}
		return
	}
	var current *client.ConnectionGrantAudience
	for i := range grants {
		grant := &grants[i]
		if grant.Kind == "organization" && grant.IsDefault && grant.Status == "active" {
			if current != nil {
				resp.Diagnostics.AddError("Ambiguous default grant", "Connection has multiple active default organization grants")
				return
			}
			current = grant
		}
	}
	if current == nil {
		resp.State.RemoveResource(ctx)
		return
	}
	importing := state.GrantID.IsNull() || state.GrantID.IsUnknown()
	state.GrantID = types.StringValue(current.ID)
	state.Ready = types.BoolValue(true)
	for _, grant := range grants {
		if grant.Kind == "organization" && grant.Status == "active" && !grant.IsDefault {
			state.Ready = types.BoolValue(false)
		}
	}
	connectionState := PaperclipConnectionResourceModel{CredentialSecretRefs: state.SecretRefs}
	refreshConnectionFields(ctx, &connectionState, &client.PaperclipToolConnection{CredentialSecretRefs: current.CredentialSecretRefs})
	state.SecretRefs = connectionState.CredentialSecretRefs
	members := []string{}
	for _, member := range current.Members {
		members = append(members, member.SubjectID)
	}
	if len(members) > 0 || !state.MemberUserIDs.IsNull() {
		state.MemberUserIDs, _ = types.SetValueFrom(ctx, types.StringType, members)
	}
	declared := delegationMap(ctx, state.Delegations)
	if importing && state.Delegations.IsNull() {
		for _, grant := range grants {
			if grant.Kind == "user" && grant.Status == "active" && len(grant.Delegations) > 0 {
				declared[grant.ID] = []string{}
			}
		}
	}
	for grantID := range declared {
		declared[grantID] = []string{}
		for _, grant := range grants {
			if grant.ID == grantID {
				for _, delegation := range grant.Delegations {
					declared[grantID] = append(declared[grantID], delegation.AgentID)
				}
			}
		}
	}
	if len(declared) > 0 || !state.Delegations.IsNull() {
		state.Delegations, _ = types.MapValueFrom(ctx, types.SetType{ElemType: types.StringType}, declared)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
func (r *PaperclipConnectionGrantsResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state connectionGrantsModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	grants, err := r.client.ListConnectionGrantAudiences(ctx, state.ConnectionID.ValueString())
	if err != nil {
		if !errors.Is(err, client.ErrNotFound) {
			resp.Diagnostics.AddError("Error reading connection grants", err.Error())
		}
		return
	}
	managed := delegationMap(ctx, state.Delegations)
	for _, grant := range grants {
		for _, delegation := range grant.Delegations {
			if containsString(managed[grant.ID], delegation.AgentID) {
				if err := r.client.RevokeConnectionGrantDelegation(ctx, state.ConnectionID.ValueString(), grant.ID, delegation.ID); err != nil {
					resp.Diagnostics.AddError("Error revoking grant delegation", err.Error())
					return
				}
			}
		}
	}
	if err := r.client.RevokeConnectionCredentialGrants(ctx, state.ConnectionID.ValueString()); err != nil {
		resp.Diagnostics.AddError("Error revoking connection grant", err.Error())
	}
}
func (r *PaperclipConnectionGrantsResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(req.ID, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		resp.Diagnostics.AddError("Invalid grant import ID", "Expected company_id/connection_id")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("company_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("connection_id"), parts[1])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("owner_id"), rand.Text())...)
}
