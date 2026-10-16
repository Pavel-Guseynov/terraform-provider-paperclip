package provider

import (
	"context"
	"fmt"

	"github.com/Pavel-Guseynov/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type PaperclipMCPGatewayTokenEphemeralResource struct{ client *client.Client }
type gatewayTokenEphemeralModel struct {
	CompanyID      types.String `tfsdk:"company_id"`
	GatewayID      types.String `tfsdk:"gateway_id"`
	Name           types.String `tfsdk:"name"`
	Generation     types.Int64  `tfsdk:"generation"`
	ClientLabel    types.String `tfsdk:"client_label"`
	AllowedActions types.String `tfsdk:"allowed_actions"`
	ExpiresAt      types.String `tfsdk:"expires_at"`
	Applying       types.Bool   `tfsdk:"applying"`
	TokenID        types.String `tfsdk:"token_id"`
	Token          types.String `tfsdk:"token"`
}

func (m gatewayTokenEphemeralModel) record() gatewayTokenModel {
	return gatewayTokenModel{CompanyID: m.CompanyID, GatewayID: m.GatewayID, Name: m.Name, Generation: m.Generation, ClientLabel: m.ClientLabel, AllowedActions: m.AllowedActions, ExpiresAt: m.ExpiresAt}
}
func NewPaperclipMCPGatewayTokenEphemeralResource() ephemeral.EphemeralResource {
	return &PaperclipMCPGatewayTokenEphemeralResource{}
}
func (r *PaperclipMCPGatewayTokenEphemeralResource) Metadata(_ context.Context, req ephemeral.MetadataRequest, resp *ephemeral.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_mcp_gateway_token"
}
func (r *PaperclipMCPGatewayTokenEphemeralResource) Schema(_ context.Context, _ ephemeral.SchemaRequest, resp *ephemeral.SchemaResponse) {
	a := map[string]schema.Attribute{}
	for _, k := range []string{"company_id", "gateway_id", "name", "client_label", "expires_at", "allowed_actions"} {
		a[k] = schema.StringAttribute{Required: true}
	}
	a["generation"] = schema.Int64Attribute{Required: true}
	a["applying"] = schema.BoolAttribute{Required: true, Description: "Set to tofu.applying. False defers minting; never mint during plan."}
	a["token_id"] = schema.StringAttribute{Computed: true}
	a["token"] = schema.StringAttribute{Computed: true, Sensitive: true, Description: "Apply-only token for an external write-only consumer. Never store in an output or persistent resource attribute."}
	resp.Schema = schema.Schema{Description: "Apply-only gateway_client token producer. Every token requires expires_at. Pair with a write-only consumer and paperclip_mcp_gateway_token lifecycle record sharing exactly the same generation and attributes. Close revokes unconfirmed tokens.", Attributes: a}
}
func (r *PaperclipMCPGatewayTokenEphemeralResource) Configure(_ context.Context, req ephemeral.ConfigureRequest, resp *ephemeral.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Ephemeral Resource Configure Type", fmt.Sprintf("Expected *client.Client, got: %T", req.ProviderData))
		return
	}
	r.client = c
}
func (r *PaperclipMCPGatewayTokenEphemeralResource) Open(ctx context.Context, req ephemeral.OpenRequest, resp *ephemeral.OpenResponse) {
	var m gatewayTokenEphemeralModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Paperclip Client", "Configure the provider before producing a gateway token.")
		return
	}
	if err := validateGatewayToken(ctx, r.client, m.record()); err != nil {
		resp.Diagnostics.AddError("Invalid gateway token", err.Error())
		return
	}
	if req.ClientCapabilities.DeferralAllowed || m.Applying.IsUnknown() || !m.Applying.ValueBool() {
		if req.ClientCapabilities.DeferralAllowed {
			resp.Deferred = &ephemeral.Deferred{Reason: ephemeral.DeferredReasonUnknown}
			return
		}
		m.Token, m.TokenID = types.StringNull(), types.StringNull()
		resp.Diagnostics.Append(resp.Result.Set(ctx, &m)...)
		return
	}
	id, token, delivered := r.client.GatewayTokenGeneration(m.CompanyID.ValueString(), m.GatewayID.ValueString(), m.Name.ValueString(), m.Generation.ValueInt64())
	if delivered {
		m.Token, m.TokenID = types.StringNull(), types.StringNull()
		resp.Diagnostics.Append(resp.Result.Set(ctx, &m)...)
		return
	}
	if resp.Private == nil {
		resp.Diagnostics.AddError("Missing ephemeral private state", "Cannot mint without cleanup metadata; no mutation was sent.")
		return
	}
	if id == "" {
		payload, _ := m.record().payload()
		created, err := r.client.CreateGatewayToken(ctx, m.CompanyID.ValueString(), m.GatewayID.ValueString(), payload)
		if err != nil {
			resp.Diagnostics.AddError("Error minting gateway token", err.Error())
			return
		}
		id, token = created.ID, created.Token
		// The client adds routing fields to its payload; compare only the public
		// producer inputs when the durable record acknowledges delivery.
		configuration, _ := m.record().payload()
		r.client.RegisterGatewayToken(m.CompanyID.ValueString(), m.GatewayID.ValueString(), m.Name.ValueString(), m.Generation.ValueInt64(), id, token, marshalJSON(configuration))
	}
	m.TokenID, m.Token = types.StringValue(id), types.StringValue(token)
	setPrivateStateKey(ctx, resp.Private, "token_id", id, &resp.Diagnostics)
	setPrivateStateKey(ctx, resp.Private, "company_id", m.CompanyID.ValueString(), &resp.Diagnostics)
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.Result.Set(ctx, &m)...)
	}
	if resp.Diagnostics.HasError() {
		if err := r.client.RevokeGatewayToken(context.WithoutCancel(ctx), m.CompanyID.ValueString(), id); err != nil {
			resp.Diagnostics.AddError("Error revoking unconfirmed gateway token", err.Error())
		} else {
			r.client.ClearMintedToken(id)
		}
	}
}
func (r *PaperclipMCPGatewayTokenEphemeralResource) Close(ctx context.Context, req ephemeral.CloseRequest, resp *ephemeral.CloseResponse) {
	if r.client == nil || req.Private == nil {
		return
	}
	var id, companyID string
	getPrivateStateKey(ctx, req.Private, "token_id", &id, &resp.Diagnostics)
	getPrivateStateKey(ctx, req.Private, "company_id", &companyID, &resp.Diagnostics)
	if resp.Diagnostics.HasError() || id == "" {
		return
	}
	if !r.client.IsKeyDeliveryAcknowledged(id) {
		if err := r.client.RevokeGatewayToken(context.WithoutCancel(ctx), companyID, id); err != nil {
			resp.Diagnostics.AddError("Error revoking unconfirmed gateway token", err.Error())
			return
		}
	}
	r.client.ClearMintedToken(id)
}
