package provider

import (
	"context"
	"encoding/json"
	"fmt"

	"git.guseynov.org/guseynov/paperclip/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ ephemeral.EphemeralResource              = &PaperclipAgentKeyEphemeralResource{}
	_ ephemeral.EphemeralResourceWithConfigure = &PaperclipAgentKeyEphemeralResource{}
	_ ephemeral.EphemeralResourceWithClose     = &PaperclipAgentKeyEphemeralResource{}
)

type PaperclipAgentKeyEphemeralResource struct {
	client *client.Client
	keyID  string
}

type PaperclipAgentKeyEphemeralResourceModel struct {
	KeyID types.String `tfsdk:"key_id"`
	Token types.String `tfsdk:"token"`
}

func NewPaperclipAgentKeyEphemeralResource() ephemeral.EphemeralResource {
	return &PaperclipAgentKeyEphemeralResource{}
}

func (r *PaperclipAgentKeyEphemeralResource) Metadata(ctx context.Context, req ephemeral.MetadataRequest, resp *ephemeral.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_agent_key"
}

func (r *PaperclipAgentKeyEphemeralResource) Schema(ctx context.Context, req ephemeral.SchemaRequest, resp *ephemeral.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Produces a Paperclip agent key token only during apply for ephemeral or write-only delivery.",
		Attributes: map[string]schema.Attribute{
			"key_id": schema.StringAttribute{
				Description: "Managed Paperclip agent key ID.",
				Required:    true,
			},
			"token": schema.StringAttribute{
				Description: "Ephemeral bearer token delivered only to an ephemeral or write-only consumer.",
				Computed:    true,
				Sensitive:   true,
			},
		},
	}
}

func (r *PaperclipAgentKeyEphemeralResource) Configure(ctx context.Context, req ephemeral.ConfigureRequest, resp *ephemeral.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Ephemeral Resource Configure Type",
			fmt.Sprintf("Expected *client.Client, got: %T", req.ProviderData),
		)
		return
	}
	r.client = c
}

func (r *PaperclipAgentKeyEphemeralResource) Open(ctx context.Context, req ephemeral.OpenRequest, resp *ephemeral.OpenResponse) {
	var data PaperclipAgentKeyEphemeralResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Paperclip Client", "Expected a configured Paperclip provider client.")
		return
	}

	if data.KeyID.IsNull() || data.KeyID.IsUnknown() || data.KeyID.ValueString() == "" {
		if req.ClientCapabilities.DeferralAllowed {
			resp.Deferred = &ephemeral.Deferred{
				Reason: ephemeral.DeferredReasonEphemeralResourceConfigUnknown,
			}
			return
		}
		resp.Diagnostics.AddError("Missing key_id", "A valid managed key_id is required to deliver the ephemeral agent key token.")
		return
	}

	keyID := data.KeyID.ValueString()
	token, ok := r.client.GetMintedToken(keyID)
	if !ok {
		if r.client.IsTokenDelivered(keyID) {
			data.Token = types.StringNull()
			resp.Diagnostics.Append(resp.Result.Set(ctx, &data)...)
			return
		}
		if key, err := r.client.FindAgentKeyByID(ctx, keyID); err == nil && key != nil {
			r.client.MarkTokenDelivered(keyID)
			data.Token = types.StringNull()
			resp.Diagnostics.Append(resp.Result.Set(ctx, &data)...)
			return
		}
		if req.ClientCapabilities.DeferralAllowed {
			resp.Deferred = &ephemeral.Deferred{
				Reason: ephemeral.DeferredReasonEphemeralResourceConfigUnknown,
			}
			return
		}
		resp.Diagnostics.AddError(
			"Token Unavailable",
			fmt.Sprintf("The bearer token for agent key %q is unavailable. Tokens are only delivered during the initial apply or generation rotation in which they are minted.", keyID),
		)
		return
	}

	data.Token = types.StringValue(token)
	r.keyID = keyID
	if resp.Private != nil {
		keyBytes, _ := json.Marshal(keyID)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, "key_id", keyBytes)...)
		if agentID, ok := r.client.GetAgentIDForKey(keyID); ok && agentID != "" {
			agentBytes, _ := json.Marshal(agentID)
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, "agent_id", agentBytes)...)
		}
		if superID, ok := r.client.GetSupersededKey(keyID); ok && superID != "" {
			superBytes, _ := json.Marshal(superID)
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, "superseded_key_id", superBytes)...)
		}
	}

	resp.Diagnostics.Append(resp.Result.Set(ctx, &data)...)
}

func (r *PaperclipAgentKeyEphemeralResource) Close(ctx context.Context, req ephemeral.CloseRequest, resp *ephemeral.CloseResponse) {
	if r.client == nil {
		return
	}
	var keyID string
	var agentID string
	if req.Private != nil {
		keyIDBytes, diags := req.Private.GetKey(ctx, "key_id")
		resp.Diagnostics.Append(diags...)
		if len(keyIDBytes) > 0 {
			_ = json.Unmarshal(keyIDBytes, &keyID)
		}

		agentIDBytes, diags := req.Private.GetKey(ctx, "agent_id")
		resp.Diagnostics.Append(diags...)
		if len(agentIDBytes) > 0 {
			_ = json.Unmarshal(agentIDBytes, &agentID)
		}
	}
	if keyID == "" {
		keyID = r.keyID
	}
	if keyID == "" {
		return
	}
	if agentID == "" {
		if id, ok := r.client.GetAgentIDForKey(keyID); ok {
			agentID = id
		}
	}

	if r.client.IsDeliveryConfirmed(keyID) {
		if err := r.client.ConfirmDelivery(ctx, keyID); err != nil {
			resp.Diagnostics.AddError("Error confirming agent key delivery", err.Error())
		}
	} else {
		if err := r.client.ReconcileFailedAgentKeyDelivery(ctx, agentID, keyID); err != nil {
			resp.Diagnostics.AddError("Error reconciling failed agent key delivery", err.Error())
		}
	}
}
