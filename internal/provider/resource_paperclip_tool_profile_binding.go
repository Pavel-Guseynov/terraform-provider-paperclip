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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type PaperclipToolProfileBindingResource struct{ client *client.Client }
type toolBindingModel struct {
	ID         types.String `tfsdk:"id"`
	CompanyID  types.String `tfsdk:"company_id"`
	OwnerID    types.String `tfsdk:"owner_id"`
	ProfileID  types.String `tfsdk:"profile_id"`
	TargetType types.String `tfsdk:"target_type"`
	TargetID   types.String `tfsdk:"target_id"`
	Priority   types.Int64  `tfsdk:"priority"`
	Metadata   types.String `tfsdk:"metadata"`
}

func NewPaperclipToolProfileBindingResource() resource.Resource {
	return &PaperclipToolProfileBindingResource{}
}
func (r *PaperclipToolProfileBindingResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_tool_profile_binding"
}
func (r *PaperclipToolProfileBindingResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	a := toolIdentityAttributes()
	// The API is insert-only. Changes are rejected rather than replacing a binding.
	a["company_id"] = schema.StringAttribute{Required: true}
	a["profile_id"] = schema.StringAttribute{Required: true}
	a["target_type"] = schema.StringAttribute{Required: true, Validators: []validator.String{nativeEnumValidator{values: []string{"company", "agent", "project", "routine", "issue", "gateway"}}}}
	a["target_id"] = schema.StringAttribute{Required: true}
	a["priority"] = schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(100)}
	a["metadata"] = toolJSONAttribute("Native binding metadata object.")
	metadataAttribute := a["metadata"].(schema.StringAttribute)
	metadataAttribute.Computed = true
	metadataAttribute.Default = stringdefault.StaticString("{}")
	a["metadata"] = metadataAttribute
	resp.Schema = schema.Schema{Description: "Owns one scoped profile binding. Import as company_id/profile_id/binding_id using the native profile inventory. Native bind is insert-only: attribute changes fail without unbinding or replacing the existing identity. Explicitly retire the resource before creating a different binding. Duplicate bindings conflict; finish-route profiles are excluded.", Attributes: a}
}
func (r *PaperclipToolProfileBindingResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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
func (m toolBindingModel) payload() (client.ToolProfileBinding, error) {
	metadata, err := decodeApplicationMetadata(m.Metadata)
	if err != nil {
		return client.ToolProfileBinding{}, err
	}
	if !m.Priority.IsUnknown() && (m.Priority.ValueInt64() < 0 || m.Priority.ValueInt64() > 10000) {
		return client.ToolProfileBinding{}, fmt.Errorf("priority must be an integer from 0 to 10000")
	}
	if !m.TargetID.IsUnknown() && (len(strings.TrimSpace(m.TargetID.ValueString())) == 0 || len([]rune(m.TargetID.ValueString())) > 200 || strings.TrimSpace(m.TargetID.ValueString()) != m.TargetID.ValueString()) {
		return client.ToolProfileBinding{}, fmt.Errorf("target_id requires 1-200 trimmed characters")
	}
	return client.ToolProfileBinding{TargetType: m.TargetType.ValueString(), TargetID: m.TargetID.ValueString(), Priority: m.Priority.ValueInt64(), Metadata: metadata}, nil
}
func (r *PaperclipToolProfileBindingResource) validate(ctx context.Context, m toolBindingModel) (*client.ToolProfile, error) {
	if _, err := m.payload(); err != nil {
		return nil, err
	}
	if r.client == nil || m.CompanyID.IsUnknown() {
		return nil, nil
	}
	if !m.TargetID.IsUnknown() && !m.TargetType.IsUnknown() {
		if err := r.client.AssertToolTarget(ctx, m.CompanyID.ValueString(), m.TargetType.ValueString(), m.TargetID.ValueString()); err != nil {
			return nil, err
		}
	}
	if m.ProfileID.IsUnknown() {
		return nil, nil
	}
	p, err := r.client.GetToolProfile(ctx, m.CompanyID.ValueString(), m.ProfileID.ValueString())
	if err == nil {
		err = p.AssertDeclarativeOwnership()
	}
	return p, err
}
func bindingTransition(m, prior toolBindingModel) error {
	for _, field := range []struct {
		name      string
		next, old types.String
	}{{"company_id", m.CompanyID, prior.CompanyID}, {"profile_id", m.ProfileID, prior.ProfileID}, {"target_type", m.TargetType, prior.TargetType}, {"target_id", m.TargetID, prior.TargetID}, {"metadata", m.Metadata, prior.Metadata}} {
		if !field.next.IsUnknown() && !field.next.Equal(field.old) {
			if field.name == "metadata" && semanticJSONEqual(field.next.ValueString(), field.old.ValueString()) {
				continue
			}
			return fmt.Errorf("native bindings are insert-only: %s cannot change; explicitly retire this binding before declaring a different one; no mutation was sent", field.name)
		}
	}
	if !m.Priority.IsUnknown() && !m.Priority.Equal(prior.Priority) {
		return fmt.Errorf("native bindings are insert-only: priority cannot change; no mutation was sent")
	}
	return nil
}
func (r *PaperclipToolProfileBindingResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}
	var m toolBindingModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !req.State.Raw.IsNull() {
		var prior toolBindingModel
		resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
		if resp.Diagnostics.HasError() {
			return
		}
		if err := bindingTransition(m, prior); err != nil {
			resp.Diagnostics.AddError("Unsupported binding transition", err.Error())
			return
		}
	}
	if _, err := r.validate(ctx, m); err != nil {
		resp.Diagnostics.AddError("Invalid profile binding", err.Error())
		return
	}
	if r.client != nil && !m.OwnerID.IsNull() && !m.OwnerID.IsUnknown() && !m.CompanyID.IsUnknown() && !m.ProfileID.IsUnknown() && !m.TargetID.IsUnknown() && !m.TargetType.IsUnknown() {
		if err := claimAggregate(r.client, "tool_binding", bindingKey(m), &m.OwnerID); err != nil {
			resp.Diagnostics.AddError("Binding ownership conflict", err.Error())
			return
		}
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("owner_id"), m.OwnerID)...)
	}
}
func bindingKey(m toolBindingModel) string {
	return m.CompanyID.ValueString() + "/" + m.ProfileID.ValueString() + "/" + m.TargetType.ValueString() + "/" + m.TargetID.ValueString()
}
func (m *toolBindingModel) refresh(b client.ToolProfileBinding) {
	m.ID = types.StringValue(b.ID)
	m.TargetType = types.StringValue(b.TargetType)
	m.TargetID = types.StringValue(b.TargetID)
	m.Priority = types.Int64Value(b.Priority)
	if m.Metadata.IsNull() && b.Metadata != nil {
		m.Metadata = types.StringUnknown()
	}
	m.Metadata = refreshSemanticJSON(m.Metadata, b.Metadata)
}
func (r *PaperclipToolProfileBindingResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m toolBindingModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p, err := r.validate(ctx, m)
	if err != nil {
		resp.Diagnostics.AddError("Invalid profile binding", err.Error())
		return
	}
	for _, b := range p.Bindings {
		if b.TargetType == m.TargetType.ValueString() && b.TargetID == m.TargetID.ValueString() {
			resp.Diagnostics.AddError("Profile binding conflict", "The profile already has this target binding; import its existing ID instead of claiming or replacing it")
			return
		}
	}
	if err := claimAggregate(r.client, "tool_binding", bindingKey(m), &m.OwnerID); err != nil {
		resp.Diagnostics.AddError("Binding ownership conflict", err.Error())
		return
	}
	payload, _ := m.payload()
	b, err := r.client.BindToolProfile(ctx, m.CompanyID.ValueString(), m.ProfileID.ValueString(), payload)
	if err != nil {
		resp.Diagnostics.AddError("Error creating profile binding", err.Error())
		return
	}
	if b.ID == "" || b.CompanyID != m.CompanyID.ValueString() || b.ProfileID != m.ProfileID.ValueString() {
		resp.Diagnostics.AddError("Invalid binding response", "Native bind returned an incomplete or contradictory identity")
		return
	}
	m.refresh(*b)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (r *PaperclipToolProfileBindingResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m toolBindingModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p, err := r.client.GetToolProfile(ctx, m.CompanyID.ValueString(), m.ProfileID.ValueString())
	if errors.Is(err, client.ErrNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err == nil {
		err = p.AssertDeclarativeOwnership()
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading profile binding", err.Error())
		return
	}
	for _, b := range p.Bindings {
		if b.ID == m.ID.ValueString() {
			m.refresh(b)
			if err := claimAggregate(r.client, "tool_binding", bindingKey(m), &m.OwnerID); err != nil {
				resp.Diagnostics.AddError("Binding ownership conflict", err.Error())
				return
			}
			resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
			return
		}
	}
	resp.State.RemoveResource(ctx)
}
func (r *PaperclipToolProfileBindingResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m, prior toolBindingModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := bindingTransition(m, prior); err != nil {
		resp.Diagnostics.AddError("Unsupported binding transition", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (r *PaperclipToolProfileBindingResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m toolBindingModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p, err := r.client.GetToolProfile(ctx, m.CompanyID.ValueString(), m.ProfileID.ValueString())
	if errors.Is(err, client.ErrNotFound) {
		return
	}
	if err == nil {
		err = p.AssertDeclarativeOwnership()
	}
	if err != nil {
		resp.Diagnostics.AddError("Error deleting profile binding", err.Error())
		return
	}
	for _, b := range p.Bindings {
		if b.ID == m.ID.ValueString() {
			if b.TargetType != m.TargetType.ValueString() || b.TargetID != m.TargetID.ValueString() {
				resp.Diagnostics.AddError("Binding identity conflict", "Native binding ID changed its target; no mutation was sent")
				return
			}
			if err := r.client.UnbindToolProfile(ctx, m.CompanyID.ValueString(), m.ProfileID.ValueString(), b.TargetType, b.TargetID); err != nil {
				resp.Diagnostics.AddError("Error deleting profile binding", err.Error())
			}
			return
		}
	}
}
func (r *PaperclipToolProfileBindingResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(req.ID, "/")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		resp.Diagnostics.AddError("Invalid binding import ID", "Expected company_id/profile_id/binding_id")
		return
	}
	for i, key := range []string{"company_id", "profile_id", "id"} {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root(key), parts[i])...)
	}
}
