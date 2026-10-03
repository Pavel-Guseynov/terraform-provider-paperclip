package provider

import (
	"context"
	"fmt"

	"git.guseynov.org/guseynov/paperclip/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ ephemeral.EphemeralResource              = &PaperclipAgentKeyEphemeralResource{}
	_ ephemeral.EphemeralResourceWithConfigure = &PaperclipAgentKeyEphemeralResource{}
)

type PaperclipAgentKeyEphemeralResource struct {
	client *client.Client
}

type PaperclipAgentKeyEphemeralResourceModel struct {
	AgentID    types.String `tfsdk:"agent_id"`
	Name       types.String `tfsdk:"name"`
	Activation types.String `tfsdk:"activation"`
	Generation types.Int64  `tfsdk:"generation"`
	KeyID      types.String `tfsdk:"key_id"`
	Token      types.String `tfsdk:"token"`
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
			"agent_id": schema.StringAttribute{
				Description: "Paperclip agent identifier. Required when minting directly without a managed key_id.",
				Optional:    true,
			},
			"name": schema.StringAttribute{
				Description: "Name recorded for the agent key.",
				Optional:    true,
			},
			"activation": schema.StringAttribute{
				Description: "Deprecated: use declared generation and managed key_id.",
				Optional:    true,
			},
			"generation": schema.Int64Attribute{
				Description: "Declared generation trigger.",
				Optional:    true,
			},
			"key_id": schema.StringAttribute{
				Description: "Managed Paperclip agent key ID.",
				Optional:    true,
				Computed:    true,
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
	if req.ClientCapabilities.DeferralAllowed {
		resp.Deferred = &ephemeral.Deferred{
			Reason: ephemeral.DeferredReasonEphemeralResourceConfigUnknown,
		}
		return
	}

	var data PaperclipAgentKeyEphemeralResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Paperclip Client", "Expected a configured Paperclip provider client.")
		return
	}

	if !data.KeyID.IsNull() && !data.KeyID.IsUnknown() && data.KeyID.ValueString() != "" {
		keyID := data.KeyID.ValueString()
		token, ok := r.client.GetMintedToken(keyID)
		if !ok {
			data.Token = types.StringValue("token-already-delivered")
			resp.Diagnostics.Append(resp.Result.Set(ctx, &data)...)
			return
		}
		data.Token = types.StringValue(token)
		r.client.MarkTokenDelivered(keyID)
		resp.Diagnostics.Append(resp.Result.Set(ctx, &data)...)
		return
	}

	if data.AgentID.IsNull() || data.AgentID.ValueString() == "" {
		resp.Diagnostics.AddError("Missing agent_id", "agent_id is required when key_id is not specified")
		return
	}
	name := "agent-key"
	if !data.Name.IsNull() && data.Name.ValueString() != "" {
		name = data.Name.ValueString()
	}

	key, err := r.client.CreateAgentKey(ctx, data.AgentID.ValueString(), name, nil)
	if err != nil {
		resp.Diagnostics.AddError("Error creating Paperclip agent key", err.Error())
		return
	}
	if key.ID == "" || key.Token == "" {
		resp.Diagnostics.AddError("Invalid Paperclip agent key", "Paperclip did not return both a key identifier and bearer token.")
		return
	}

	data.KeyID = types.StringValue(key.ID)
	data.Token = types.StringValue(key.Token)
	r.client.RegisterMintedToken(key.ID, key.Token)
	r.client.MarkTokenDelivered(key.ID)
	resp.Diagnostics.Append(resp.Result.Set(ctx, &data)...)
}


