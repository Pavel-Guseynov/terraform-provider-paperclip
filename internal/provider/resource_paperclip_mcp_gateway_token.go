package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Pavel-Guseynov/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type PaperclipMCPGatewayTokenResource struct{ client *client.Client }
type gatewayTokenModel struct {
	ID             types.String `tfsdk:"id"`
	OwnerID        types.String `tfsdk:"owner_id"`
	CompanyID      types.String `tfsdk:"company_id"`
	GatewayID      types.String `tfsdk:"gateway_id"`
	Name           types.String `tfsdk:"name"`
	Generation     types.Int64  `tfsdk:"generation"`
	ClientLabel    types.String `tfsdk:"client_label"`
	AllowedActions types.String `tfsdk:"allowed_actions"`
	ExpiresAt      types.String `tfsdk:"expires_at"`
}

func NewPaperclipMCPGatewayTokenResource() resource.Resource {
	return &PaperclipMCPGatewayTokenResource{}
}
func (r *PaperclipMCPGatewayTokenResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_mcp_gateway_token"
}
func (r *PaperclipMCPGatewayTokenResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	a := map[string]schema.Attribute{}
	for _, k := range []string{"company_id", "gateway_id", "name", "client_label", "expires_at"} {
		a[k] = schema.StringAttribute{Required: true}
	}
	a["id"] = schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}}
	a["owner_id"] = schema.StringAttribute{Computed: true, Description: "Single lifecycle owner within this configured provider process; independent states must coordinate externally.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}}
	a["generation"] = schema.Int64Attribute{Required: true, Description: "Positive generation. Increase to mint and deliver a replacement before revoking the predecessor. All token attribute changes require a new generation."}
	a["allowed_actions"] = schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString(`["tools/list","tools/call"]`), Description: "JSON array of native MCP token actions.", PlanModifiers: []planmodifier.String{SemanticJSONPlanModifier()}}
	resp.Schema = schema.Schema{Description: "Non-secret gateway_client token lifecycle record. Requires ephemeral.paperclip_mcp_gateway_token and depends_on the external write-only consumer. Never stores a token value. Not importable: generation and delivery cannot be inferred from the native API.", Attributes: a}
}
func (r *PaperclipMCPGatewayTokenResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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
func (m gatewayTokenModel) payload() (map[string]interface{}, error) {
	for k, v := range map[string]types.String{"name": m.Name, "client_label": m.ClientLabel} {
		if !v.IsUnknown() && !gatewayText(v.ValueString(), 160, true) {
			return nil, fmt.Errorf("%s requires 1-160 trimmed characters", k)
		}
	}
	if !m.Generation.IsUnknown() && m.Generation.ValueInt64() < 1 {
		return nil, fmt.Errorf("generation must be positive")
	}
	if !m.ExpiresAt.IsUnknown() {
		if _, err := time.Parse(time.RFC3339, m.ExpiresAt.ValueString()); err != nil {
			return nil, fmt.Errorf("expires_at requires a finite RFC3339 timestamp")
		}
	}
	var actions []string
	if !m.AllowedActions.IsUnknown() {
		if err := json.Unmarshal([]byte(m.AllowedActions.ValueString()), &actions); err != nil || len(actions) == 0 || len(actions) > 6 {
			return nil, fmt.Errorf("allowed_actions requires a JSON array of 1-6 native actions")
		}
		seen := map[string]bool{}
		for _, action := range actions {
			if !gatewayTokenActions[action] || seen[action] {
				return nil, fmt.Errorf("allowed_actions contains an unsupported or duplicate action")
			}
			seen[action] = true
		}
	}
	return map[string]interface{}{"name": m.Name.ValueString(), "clientLabel": m.ClientLabel.ValueString(), "allowedActions": actions, "expiresAt": m.ExpiresAt.ValueString()}, nil
}

var gatewayTokenActions = map[string]bool{"tools/list": true, "tools/call": true, "resources/list": true, "resources/read": true, "prompts/list": true, "prompts/get": true}

func validateGatewayToken(ctx context.Context, c *client.Client, m gatewayTokenModel) error {
	if _, err := m.payload(); err != nil {
		return err
	}
	if c == nil || m.CompanyID.IsUnknown() || m.GatewayID.IsUnknown() {
		return nil
	}
	_, err := c.GetGateway(ctx, m.CompanyID.ValueString(), m.GatewayID.ValueString())
	return err
}
func gatewayTokenTransition(m, prior gatewayTokenModel) error {
	for _, f := range []struct {
		name      string
		next, old types.String
	}{{"company_id", m.CompanyID, prior.CompanyID}, {"gateway_id", m.GatewayID, prior.GatewayID}, {"name", m.Name, prior.Name}} {
		if !f.next.IsUnknown() && !f.next.Equal(f.old) {
			return fmt.Errorf("%s is the token lifecycle identity; explicitly retire it before declaring another identity; no mutation was sent", f.name)
		}
	}
	if !m.Generation.IsUnknown() && m.Generation.ValueInt64() < prior.Generation.ValueInt64() {
		return fmt.Errorf("generation must increase for rotation")
	}
	if m.Generation.Equal(prior.Generation) {
		for _, f := range []struct{ next, old types.String }{{m.ClientLabel, prior.ClientLabel}, {m.ExpiresAt, prior.ExpiresAt}, {m.AllowedActions, prior.AllowedActions}} {
			if !f.next.IsUnknown() && !f.next.Equal(f.old) && !semanticJSONEqual(f.next.ValueString(), f.old.ValueString()) {
				return fmt.Errorf("token attribute changes require an increased generation; no mutation was sent")
			}
		}
	}
	return nil
}
func (r *PaperclipMCPGatewayTokenResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}
	var m, prior gatewayTokenModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := validateGatewayToken(ctx, r.client, m); err != nil {
		resp.Diagnostics.AddError("Invalid gateway token", err.Error())
		return
	}
	if !req.State.Raw.IsNull() {
		resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
		if resp.Diagnostics.HasError() {
			return
		}
		if err := gatewayTokenTransition(m, prior); err != nil {
			resp.Diagnostics.AddError("Unsupported gateway token transition", err.Error())
			return
		}
		if !m.Generation.Equal(prior.Generation) {
			resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("id"), types.StringUnknown())...)
		}
	}
}
func (r *PaperclipMCPGatewayTokenResource) delivered(ctx context.Context, m *gatewayTokenModel, oldID string) error {
	if err := validateGatewayToken(ctx, r.client, *m); err != nil {
		return err
	}
	if err := claimAggregate(r.client, "mcp_gateway_token", m.CompanyID.ValueString()+"/"+m.GatewayID.ValueString()+"/"+m.Name.ValueString(), &m.OwnerID); err != nil {
		return err
	}
	id, token, _ := r.client.GatewayTokenGeneration(m.CompanyID.ValueString(), m.GatewayID.ValueString(), m.Name.ValueString(), m.Generation.ValueInt64())
	if id == "" || token == "" || id == oldID {
		return fmt.Errorf("missing ephemeral gateway token mint for this generation; deliver ephemeral.paperclip_mcp_gateway_token.token to a write-only consumer and depend on that consumer before recording delivery")
	}
	payload, _ := m.payload()
	if r.client.GatewayTokenConfiguration(id) != marshalJSON(payload) {
		return fmt.Errorf("ephemeral producer and durable token record must have identical attributes; no predecessor was revoked")
	}
	if oldID != "" {
		if err := r.client.RevokeGatewayToken(ctx, m.CompanyID.ValueString(), oldID); err != nil {
			return err
		}
	}
	r.client.RecordGatewayTokenDelivery(m.CompanyID.ValueString(), m.GatewayID.ValueString(), m.Name.ValueString(), m.Generation.ValueInt64(), id)
	m.ID = types.StringValue(id)
	return nil
}
func (r *PaperclipMCPGatewayTokenResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m gatewayTokenModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.delivered(ctx, &m, ""); err != nil {
		resp.Diagnostics.AddError("Error recording gateway token delivery", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (r *PaperclipMCPGatewayTokenResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m, prior gatewayTokenModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	if resp.Diagnostics.HasError() {
		return
	}
	m.OwnerID = prior.OwnerID
	if err := gatewayTokenTransition(m, prior); err != nil {
		resp.Diagnostics.AddError("Unsupported gateway token transition", err.Error())
		return
	}
	if !m.Generation.Equal(prior.Generation) {
		if err := r.delivered(ctx, &m, prior.ID.ValueString()); err != nil {
			resp.Diagnostics.AddError("Error rotating gateway token", err.Error())
			return
		}
	} else {
		m.ID = prior.ID
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (r *PaperclipMCPGatewayTokenResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m gatewayTokenModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	g, err := r.client.GetGateway(ctx, m.CompanyID.ValueString(), m.GatewayID.ValueString())
	// An archived gateway cannot be inspected; retaining the token record allows
	// an explicit revoke without pretending its live token inventory was read.
	if errors.Is(err, client.ErrNotFound) {
		resp.Diagnostics.AddError("Gateway token inventory unavailable", "The gateway is absent or archived. Its tokens cannot be refreshed through the native inventory; restore visibility or explicitly revoke the recorded token before removing state.")
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading gateway token", err.Error())
		return
	}
	for _, token := range g.Tokens {
		if token.ID != m.ID.ValueString() {
			continue
		}
		if token.RevokedAt != nil {
			resp.State.RemoveResource(ctx)
			return
		}
		m.Name, m.ClientLabel = types.StringValue(token.Name), types.StringValue(token.ClientLabel)
		remoteExpiry, err := time.Parse(time.RFC3339, token.ExpiresAt)
		if err != nil {
			resp.Diagnostics.AddError("Invalid native token expiry", "Native inventory did not return a finite RFC3339 expiry; retain state and inspect the recorded token.")
			return
		}
		priorExpiry, _ := time.Parse(time.RFC3339, m.ExpiresAt.ValueString())
		if !remoteExpiry.Equal(priorExpiry) {
			m.ExpiresAt = types.StringValue(remoteExpiry.Format(time.RFC3339Nano))
		}
		if err := claimAggregate(r.client, "mcp_gateway_token", m.CompanyID.ValueString()+"/"+m.GatewayID.ValueString()+"/"+m.Name.ValueString(), &m.OwnerID); err != nil {
			resp.Diagnostics.AddError("Gateway token ownership conflict", err.Error())
			return
		}
		m.AllowedActions = refreshSemanticJSON(m.AllowedActions, token.AllowedActions)
		r.client.RecordGatewayTokenDelivery(m.CompanyID.ValueString(), m.GatewayID.ValueString(), m.Name.ValueString(), m.Generation.ValueInt64(), token.ID)
		resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
		return
	}
	resp.State.RemoveResource(ctx)
}
func (r *PaperclipMCPGatewayTokenResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m gatewayTokenModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.RevokeGatewayToken(ctx, m.CompanyID.ValueString(), m.ID.ValueString()); err != nil {
		resp.Diagnostics.AddError("Error revoking gateway token", err.Error())
	}
}
