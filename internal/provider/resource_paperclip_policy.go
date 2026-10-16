package provider

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"

	"github.com/Pavel-Guseynov/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

type PaperclipPolicyResource struct{ client *client.Client }
type PaperclipPolicyResourceModel struct {
	ID          types.String `tfsdk:"id"`
	CompanyID   types.String `tfsdk:"company_id"`
	OwnerID     types.String `tfsdk:"owner_id"`
	Name        types.String `tfsdk:"name"`
	PolicyType  types.String `tfsdk:"policy_type"`
	Description types.String `tfsdk:"description"`
	Priority    types.Int64  `tfsdk:"priority"`
	Enabled     types.Bool   `tfsdk:"enabled"`
	Selectors   types.String `tfsdk:"selectors"`
	Conditions  types.String `tfsdk:"conditions"`
	Config      types.String `tfsdk:"config"`
}

func NewPaperclipPolicyResource() resource.Resource { return &PaperclipPolicyResource{} }
func (r *PaperclipPolicyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_policy"
}
func (r *PaperclipPolicyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	a := toolIdentityAttributes()
	a["name"] = schema.StringAttribute{Required: true}
	a["policy_type"] = schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("allow"), Validators: []validator.String{nativeEnumValidator{values: []string{"allow", "block", "require_approval", "rate_limit"}}}}
	a["description"] = schema.StringAttribute{Optional: true, Description: "Native nullable description; omission clears it."}
	a["priority"] = schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(100), Description: "0-10000; lower values evaluate first. Only this policy is patched; global reorder is never invoked."}
	a["enabled"] = schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true)}
	a["selectors"] = toolJSONAttribute("Native loose selector object; omission clears to {}. Producer schemas and annotations are never changed.")
	selectorsAttribute := a["selectors"].(schema.StringAttribute)
	selectorsAttribute.Computed = true
	selectorsAttribute.Default = stringdefault.StaticString("{}")
	a["selectors"] = selectorsAttribute
	a["conditions"] = toolJSONAttribute("Native strict condition object: arguments/args, actor, context, risk, credentialScope, trustBoundary and timeWindow. Omission clears to null.")
	a["config"] = toolJSONAttribute("Native config, only for rate_limit: positive limit and windowSeconds, optionally keyBy (or a nested rateLimit object). Omission clears to null.")
	resp.Schema = schema.Schema{Version: 1, Description: "Owns one declaratively creatable native policy. trust_rule requires native action-request promotion and is excluded. app_permission/finish-owned policies cannot be imported or mutated. Rules and metadata were unsupported native fields and are removed in schema version 1; upgrade retains the same remote ID and refreshes native fields.", Attributes: a}
}
func (r *PaperclipPolicyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (m PaperclipPolicyResourceModel) policy() (client.PaperclipToolPolicy, error) {
	p := client.PaperclipToolPolicy{CompanyID: m.CompanyID.ValueString(), Name: m.Name.ValueString(), PolicyType: m.PolicyType.ValueString(), Priority: int(m.Priority.ValueInt64()), Enabled: new(m.Enabled.ValueBool()), Description: nativeStringPointer(m.Description)}
	var err error
	if p.Selectors, err = decodeApplicationMetadata(m.Selectors); err != nil {
		return p, err
	}
	if p.Conditions, err = decodeApplicationMetadata(m.Conditions); err != nil {
		return p, err
	}
	if p.Config, err = decodeApplicationMetadata(m.Config); err != nil {
		return p, err
	}
	if p.Config["source"] == "app_gallery_finish" {
		return p, fmt.Errorf("config.source=app_gallery_finish belongs to app_permission/finish; do not declare it in a tool policy")
	}
	if !m.PolicyType.IsUnknown() && p.PolicyType != "allow" && p.PolicyType != "block" && p.PolicyType != "require_approval" && p.PolicyType != "rate_limit" {
		return p, fmt.Errorf("policy_type must be allow, block, require_approval or rate_limit; trust_rule is action-request-only")
	}
	if !m.Priority.IsUnknown() && (p.Priority < 0 || p.Priority > 10000) {
		return p, fmt.Errorf("priority must be an integer from 0 to 10000")
	}
	if !m.Name.IsUnknown() && (strings.TrimSpace(p.Name) == "" || strings.TrimSpace(p.Name) != p.Name || len([]rune(p.Name)) > 160) {
		return p, fmt.Errorf("policy name requires 1-160 trimmed characters")
	}
	if len([]rune(m.Description.ValueString())) > 4000 {
		return p, fmt.Errorf("policy description exceeds 4000 characters")
	}
	if p.Conditions != nil {
		if err := validateToolConditions(p.Conditions); err != nil {
			return p, err
		}
	}
	if !m.PolicyType.IsUnknown() && !m.Config.IsUnknown() {
		if p.PolicyType != "rate_limit" && len(p.Config) > 0 {
			return p, fmt.Errorf("config is supported only for rate_limit policies")
		}
		if p.PolicyType == "rate_limit" {
			raw := p.Config
			if nested, ok := raw["rateLimit"].(map[string]interface{}); ok {
				raw = nested
			}
			limit, lok := raw["limit"].(float64)
			window, wok := raw["windowSeconds"].(float64)
			if !lok || !wok || limit <= 0 || window <= 0 {
				return p, fmt.Errorf("rate_limit config requires positive numeric limit and windowSeconds")
			}
		}
	}
	return p, nil
}
func policyOwnership(p *client.PaperclipToolPolicy) error {
	if (p.PolicyType != "allow" && p.PolicyType != "block" && p.PolicyType != "require_approval" && p.PolicyType != "rate_limit") || p.Config["source"] == "app_gallery_finish" {
		return fmt.Errorf("policy %s is owned by native action-request/app_permission lifecycle; retain that owner; no mutation was sent", p.ID)
	}
	return nil
}
func (r *PaperclipPolicyResource) validate(ctx context.Context, m PaperclipPolicyResourceModel) error {
	p, err := m.policy()
	if err != nil {
		return err
	}
	if r.client == nil || m.CompanyID.IsUnknown() {
		return nil
	}
	return validateToolReferenceObjects(ctx, r.client, m.CompanyID.ValueString(), p.Selectors, p.Conditions)
}
func (r *PaperclipPolicyResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}
	var m PaperclipPolicyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.validate(ctx, m); err != nil {
		resp.Diagnostics.AddError("Invalid tool policy", err.Error())
		return
	}
	if r.client != nil && !m.OwnerID.IsNull() && !m.OwnerID.IsUnknown() && !m.CompanyID.IsUnknown() && !m.Name.IsUnknown() {
		if err := claimAggregate(r.client, "tool_policy", m.CompanyID.ValueString()+"/"+m.Name.ValueString(), &m.OwnerID); err != nil {
			resp.Diagnostics.AddError("Policy ownership conflict", err.Error())
			return
		}
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("owner_id"), m.OwnerID)...)
	}
}
func (m *PaperclipPolicyResourceModel) refresh(p *client.PaperclipToolPolicy) {
	m.ID = types.StringValue(p.ID)
	m.CompanyID = types.StringValue(p.CompanyID)
	m.Name = types.StringValue(p.Name)
	m.PolicyType = types.StringValue(p.PolicyType)
	m.Description = types.StringNull()
	if p.Description != nil {
		m.Description = types.StringValue(*p.Description)
	}
	m.Priority = types.Int64Value(int64(p.Priority))
	m.Enabled = types.BoolValue(p.Enabled == nil || *p.Enabled)
	if m.Selectors.IsNull() && p.Selectors != nil {
		m.Selectors = types.StringUnknown()
	}
	m.Selectors = refreshSemanticJSON(m.Selectors, p.Selectors)
	m.Conditions = refreshSemanticJSON(m.Conditions, p.Conditions)
	if m.Config.IsNull() && p.Config != nil {
		m.Config = types.StringUnknown()
	}
	m.Config = refreshSemanticJSON(m.Config, p.Config)
}
func (r *PaperclipPolicyResource) save(ctx context.Context, m *PaperclipPolicyResourceModel, update bool) error {
	if err := r.validate(ctx, *m); err != nil {
		return err
	}
	if err := claimAggregate(r.client, "tool_policy", m.CompanyID.ValueString()+"/"+m.Name.ValueString(), &m.OwnerID); err != nil {
		return err
	}
	var p *client.PaperclipToolPolicy
	var err error
	payload, _ := m.policy()
	if update {
		existing, e := r.client.GetToolPolicy(ctx, m.CompanyID.ValueString(), m.ID.ValueString())
		if e != nil {
			return e
		}
		if e = policyOwnership(existing); e != nil {
			return e
		}
		p, err = r.client.UpdateToolPolicy(ctx, m.CompanyID.ValueString(), m.ID.ValueString(), payload)
	} else {
		p, err = r.client.CreateToolPolicy(ctx, m.CompanyID.ValueString(), payload)
	}
	if err != nil {
		return err
	}
	if p.ID == "" || p.CompanyID != m.CompanyID.ValueString() || (update && p.ID != m.ID.ValueString()) {
		return fmt.Errorf("policy write returned an invalid identity")
	}
	m.refresh(p)
	return nil
}
func (r *PaperclipPolicyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m PaperclipPolicyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.save(ctx, &m, false); err != nil {
		resp.Diagnostics.AddError("Error creating Paperclip policy", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (r *PaperclipPolicyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m PaperclipPolicyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.save(ctx, &m, true); err != nil {
		resp.Diagnostics.AddError("Error updating Paperclip policy", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (r *PaperclipPolicyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m PaperclipPolicyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p, err := r.client.GetToolPolicy(ctx, m.CompanyID.ValueString(), m.ID.ValueString())
	if errors.Is(err, client.ErrNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err == nil {
		err = policyOwnership(p)
	}
	if err == nil {
		err = claimAggregate(r.client, "tool_policy", m.CompanyID.ValueString()+"/"+p.Name, &m.OwnerID)
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading Paperclip policy", err.Error())
		return
	}
	m.refresh(p)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (r *PaperclipPolicyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m PaperclipPolicyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p, err := r.client.GetToolPolicy(ctx, m.CompanyID.ValueString(), m.ID.ValueString())
	if errors.Is(err, client.ErrNotFound) {
		return
	}
	if err == nil {
		err = policyOwnership(p)
	}
	if err == nil {
		err = r.client.DeleteToolPolicy(ctx, m.CompanyID.ValueString(), m.ID.ValueString())
	}
	if err != nil && !errors.Is(err, client.ErrNotFound) {
		resp.Diagnostics.AddError("Error deleting Paperclip policy", err.Error())
	}
}
func (r *PaperclipPolicyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(req.ID, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		resp.Diagnostics.AddError("Invalid Policy Import ID", "Expected company_id/policy_id")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("company_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
}
func (r *PaperclipPolicyResource) UpgradeState(ctx context.Context) map[int64]resource.StateUpgrader {
	var current resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &current)
	prior := schema.Schema{Attributes: map[string]schema.Attribute{}}
	for _, key := range []string{"id", "company_id", "name", "policy_type", "rules", "metadata"} {
		prior.Attributes[key] = schema.StringAttribute{Optional: true}
	}
	return map[int64]resource.StateUpgrader{0: {PriorSchema: &prior, StateUpgrader: func(ctx context.Context, req resource.UpgradeStateRequest, resp *resource.UpgradeStateResponse) {
		values := map[string]tftypes.Value{}
		if err := req.State.Raw.As(&values); err != nil {
			resp.Diagnostics.AddError("Invalid policy state", err.Error())
			return
		}
		delete(values, "rules")
		delete(values, "metadata")
		for key, value := range map[string]attr.Value{"owner_id": types.StringValue(rand.Text()), "description": types.StringNull(), "priority": types.Int64Null(), "enabled": types.BoolNull(), "selectors": types.StringNull(), "conditions": types.StringNull(), "config": types.StringNull()} {
			values[key], _ = value.ToTerraformValue(ctx)
		}
		resp.State.Raw = tftypes.NewValue(current.Schema.Type().TerraformType(ctx), values)
	}}}
}
