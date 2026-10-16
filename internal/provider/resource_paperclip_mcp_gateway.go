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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type PaperclipMCPGatewayResource struct{ client *client.Client }
type gatewayModel struct {
	ID                  types.String `tfsdk:"id"`
	CompanyID           types.String `tfsdk:"company_id"`
	OwnerID             types.String `tfsdk:"owner_id"`
	Name                types.String `tfsdk:"name"`
	Slug                types.String `tfsdk:"slug"`
	Description         types.String `tfsdk:"description"`
	Status              types.String `tfsdk:"status"`
	ProfileID           types.String `tfsdk:"profile_id"`
	DefaultProfileMode  types.String `tfsdk:"default_profile_mode"`
	ContextScopeType    types.String `tfsdk:"context_scope_type"`
	ContextScopeID      types.String `tfsdk:"context_scope_id"`
	AgentID             types.String `tfsdk:"agent_id"`
	ProjectID           types.String `tfsdk:"project_id"`
	IssueID             types.String `tfsdk:"issue_id"`
	ApprovalIssueID     types.String `tfsdk:"approval_issue_id"`
	AuthConfig          types.String `tfsdk:"auth_config"`
	HeaderPolicy        types.String `tfsdk:"header_policy"`
	MetadataPolicy      types.String `tfsdk:"metadata_policy"`
	OnDemandToolsConfig types.String `tfsdk:"on_demand_tools_config"`
	Metadata            types.String `tfsdk:"metadata"`
}

func NewPaperclipMCPGatewayResource() resource.Resource { return &PaperclipMCPGatewayResource{} }
func (r *PaperclipMCPGatewayResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_mcp_gateway"
}
func (r *PaperclipMCPGatewayResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	a := toolIdentityAttributes()
	a["company_id"] = schema.StringAttribute{Required: true, Description: "Immutable company identity. Retire the gateway explicitly before declaring a gateway in another company."}
	for _, k := range []string{"name", "slug", "profile_id"} {
		a[k] = schema.StringAttribute{Required: true}
	}
	for _, k := range []string{"description", "context_scope_id", "agent_id", "project_id", "issue_id", "approval_issue_id"} {
		a[k] = schema.StringAttribute{Optional: true}
	}
	a["status"] = schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("active"), Description: "Create as active; update to draft, active or disabled. Destroy archives. Archived gateways are absent from the native inventory.", Validators: []validator.String{nativeEnumValidator{values: []string{"draft", "active", "disabled"}, archiveViaDestroy: true}}}
	a["default_profile_mode"] = schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("gateway_only"), Validators: []validator.String{nativeEnumValidator{values: []string{"gateway_only", "inherit_context_then_gateway", "gateway_then_context"}}}}
	a["context_scope_type"] = schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("none"), Validators: []validator.String{nativeEnumValidator{values: []string{"none", "company", "project", "routine", "issue", "agent"}}}}
	for attr, kind := range map[string]string{"auth_config": "authConfig", "header_policy": "headerPolicy", "metadata_policy": "metadataPolicy", "on_demand_tools_config": "onDemandToolsConfig", "metadata": "metadata"} {
		def := gatewayPolicyDefaults[kind]
		if kind == "metadata" {
			def = "{}"
		}
		v := toolJSONAttribute("Native JSON object. Omission resets to native defaults. Include only non-secret policy values; policy JSON is retained in state.")
		v.Computed, v.Default = true, stringdefault.StaticString(def)
		if kind != "metadata" {
			v.PlanModifiers = []planmodifier.String{semanticJSONPlanModifier{normalize: func(value interface{}) interface{} { return normalizeGatewayPolicy(kind, value) }}}
		}
		a[attr] = v
	}
	resp.Schema = schema.Schema{Description: "Manages a named MCP gateway through the existing tools:admin board API. Import as company_id/gateway_id. Destroy archives without setting archivedAt or deleting bindings/tokens. Profile updates retain old native bindings; coordinate their ownership separately.", Attributes: a}
}
func (r *PaperclipMCPGatewayResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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
func (m gatewayModel) payload() (map[string]interface{}, error) {
	p := map[string]interface{}{"name": m.Name.ValueString(), "displaySlug": m.Slug.ValueString(), "profileId": m.ProfileID.ValueString(), "defaultProfileMode": m.DefaultProfileMode.ValueString(), "contextScopeType": m.ContextScopeType.ValueString()}
	for _, f := range []struct {
		k        string
		v        types.String
		limit    int
		required bool
	}{{"name", m.Name, 160, true}, {"slug", m.Slug, 120, true}, {"description", m.Description, 4000, false}, {"context_scope_id", m.ContextScopeID, 200, true}} {
		if !f.v.IsUnknown() && !f.v.IsNull() && !gatewayText(f.v.ValueString(), f.limit, f.required) {
			return nil, fmt.Errorf("%s exceeds native text limits or is not trimmed", f.k)
		}
	}
	if !m.Slug.IsUnknown() && !gatewaySafeKey.MatchString(m.Slug.ValueString()) {
		return nil, fmt.Errorf("slug must be a native safe key")
	}
	if err := rejectDeclaredArchive(m.Status); err != nil {
		return nil, err
	}
	for k, v := range map[string]types.String{"description": m.Description, "contextScopeId": m.ContextScopeID, "agentId": m.AgentID, "projectId": m.ProjectID, "issueId": m.IssueID, "approvalIssueId": m.ApprovalIssueID} {
		p[k] = nativeStringPointer(v)
	}
	for k, v := range map[string]types.String{"authConfig": m.AuthConfig, "headerPolicy": m.HeaderPolicy, "metadataPolicy": m.MetadataPolicy, "onDemandToolsConfig": m.OnDemandToolsConfig, "metadata": m.Metadata} {
		var obj map[string]interface{}
		var err error
		if k == "metadata" {
			obj, err = decodeApplicationMetadata(v)
		} else {
			obj, err = gatewayPolicy(k, v)
		}
		if err != nil {
			return nil, err
		}
		p[k] = obj
	}
	return p, nil
}
func (r *PaperclipMCPGatewayResource) validate(ctx context.Context, m gatewayModel, creating bool) error {
	if _, err := m.payload(); err != nil {
		return err
	}
	if creating && !m.Status.IsUnknown() && m.Status.ValueString() != "active" {
		return fmt.Errorf("native gateway creation always uses active status; create as active, then update status; no mutation was sent")
	}
	if m.ContextScopeType.ValueString() == "none" && !m.ContextScopeID.IsUnknown() && !m.ContextScopeID.IsNull() {
		return fmt.Errorf("context_scope_id must be omitted for none scope")
	}
	if r.client == nil || m.CompanyID.IsUnknown() {
		return nil
	}
	if !m.ProfileID.IsUnknown() {
		p, err := r.client.GetToolProfile(ctx, m.CompanyID.ValueString(), m.ProfileID.ValueString())
		if err != nil {
			return err
		}
		if err := p.AssertDeclarativeOwnership(); err != nil {
			return err
		}
	}
	refs := []struct {
		kind string
		id   types.String
	}{{"agent", m.AgentID}, {"project", m.ProjectID}, {"issue", m.ApprovalIssueID}}
	if !m.IssueID.IsNull() && !m.IssueID.IsUnknown() {
		if err := r.client.AssertGatewayIssue(ctx, m.CompanyID.ValueString(), m.IssueID.ValueString(), m.ProjectID.ValueString()); err != nil {
			return err
		}
	}
	if !m.ContextScopeType.IsUnknown() && m.ContextScopeType.ValueString() != "none" {
		if m.ContextScopeID.IsNull() {
			return fmt.Errorf("context_scope_id is required for a scoped gateway")
		}
		refs = append(refs, struct {
			kind string
			id   types.String
		}{m.ContextScopeType.ValueString(), m.ContextScopeID})
	}
	for _, ref := range refs {
		if !ref.id.IsNull() && !ref.id.IsUnknown() {
			if err := r.client.AssertToolTarget(ctx, m.CompanyID.ValueString(), ref.kind, ref.id.ValueString()); err != nil {
				return err
			}
		}
	}
	return nil
}
func (r *PaperclipMCPGatewayResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}
	var m gatewayModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !req.State.Raw.IsNull() {
		var prior gatewayModel
		resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
		if resp.Diagnostics.HasError() {
			return
		}
		if err := gatewayCompanyTransition(m, prior); err != nil {
			resp.Diagnostics.AddError("Unsupported gateway transition", err.Error())
			return
		}
	}
	if err := r.validate(ctx, m, req.State.Raw.IsNull()); err != nil {
		resp.Diagnostics.AddError("Invalid MCP gateway", err.Error())
	}
}

func gatewayCompanyTransition(next, prior gatewayModel) error {
	if !next.CompanyID.IsUnknown() && !next.CompanyID.Equal(prior.CompanyID) {
		return fmt.Errorf("company_id cannot change; explicitly retire the existing gateway before declaring another company; no mutation or replacement was performed")
	}
	return nil
}
func (m *gatewayModel) refresh(g *client.Gateway) {
	m.ID, m.CompanyID, m.Name, m.Slug, m.Status, m.ProfileID = types.StringValue(g.ID), types.StringValue(g.CompanyID), types.StringValue(g.Name), types.StringValue(g.Slug), types.StringValue(g.Status), types.StringValue(g.ProfileID)
	m.DefaultProfileMode, m.ContextScopeType = types.StringValue(g.DefaultProfileMode), types.StringValue(g.ContextScopeType)
	for _, f := range []struct {
		out *types.String
		v   *string
	}{{&m.Description, g.Description}, {&m.ContextScopeID, g.ContextScopeID}, {&m.AgentID, g.AgentID}, {&m.ProjectID, g.ProjectID}, {&m.IssueID, g.IssueID}, {&m.ApprovalIssueID, g.ApprovalIssueID}} {
		*f.out = types.StringNull()
		if f.v != nil {
			*f.out = types.StringValue(*f.v)
		}
	}
	for _, f := range []struct {
		kind string
		out  *types.String
		v    map[string]interface{}
	}{{"authConfig", &m.AuthConfig, g.AuthConfig}, {"headerPolicy", &m.HeaderPolicy, g.HeaderPolicy}, {"metadataPolicy", &m.MetadataPolicy, g.MetadataPolicy}, {"onDemandToolsConfig", &m.OnDemandToolsConfig, g.OnDemandToolsConfig}, {"metadata", &m.Metadata, g.Metadata}} {
		if f.kind == "metadata" {
			if f.out.IsNull() { *f.out = types.StringUnknown() }
			*f.out = refreshSemanticJSON(*f.out, f.v)
		} else {
			*f.out = refreshSemanticJSONNormalized(*f.out, f.v, func(value interface{}) interface{} { return normalizeGatewayPolicy(f.kind, value) })
		}
	}
}
func (r *PaperclipMCPGatewayResource) save(ctx context.Context, m *gatewayModel, creating bool) error {
	if err := r.validate(ctx, *m, creating); err != nil {
		return err
	}
	if err := claimAggregate(r.client, "mcp_gateway", m.CompanyID.ValueString()+"/"+m.Slug.ValueString(), &m.OwnerID); err != nil {
		return err
	}
	id := ""
	if !creating {
		id = m.ID.ValueString()
		if _, err := r.client.GetGateway(ctx, m.CompanyID.ValueString(), id); err != nil {
			return err
		}
	}
	p, _ := m.payload()
	if !creating {
		p["status"] = m.Status.ValueString()
	}
	g, err := r.client.WriteGateway(ctx, m.CompanyID.ValueString(), id, p)
	if err != nil {
		return err
	}
	m.refresh(g)
	return nil
}
func (r *PaperclipMCPGatewayResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m gatewayModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.save(ctx, &m, true); err != nil {
		resp.Diagnostics.AddError("Error creating MCP gateway", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (r *PaperclipMCPGatewayResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m, prior gatewayModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := gatewayCompanyTransition(m, prior); err != nil {
		resp.Diagnostics.AddError("Unsupported gateway transition", err.Error())
		return
	}
	if err := r.save(ctx, &m, false); err != nil {
		resp.Diagnostics.AddError("Error updating MCP gateway", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (r *PaperclipMCPGatewayResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m gatewayModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	g, err := r.client.GetGateway(ctx, m.CompanyID.ValueString(), m.ID.ValueString())
	if errors.Is(err, client.ErrNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err == nil {
		err = claimAggregate(r.client, "mcp_gateway", m.CompanyID.ValueString()+"/"+g.Slug, &m.OwnerID)
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading MCP gateway", err.Error())
		return
	}
	m.refresh(g)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (r *PaperclipMCPGatewayResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m gatewayModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	_, err := r.client.GetGateway(ctx, m.CompanyID.ValueString(), m.ID.ValueString())
	if errors.Is(err, client.ErrNotFound) {
		return
	}
	if err == nil {
		_, err = r.client.WriteGateway(ctx, m.CompanyID.ValueString(), m.ID.ValueString(), map[string]interface{}{"status": "archived"})
	}
	if err != nil {
		resp.Diagnostics.AddError("Error archiving MCP gateway", err.Error())
	}
}
func (r *PaperclipMCPGatewayResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(req.ID, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		resp.Diagnostics.AddError("Invalid gateway import ID", "Expected company_id/gateway_id")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("company_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
}
