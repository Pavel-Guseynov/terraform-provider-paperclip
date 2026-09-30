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
		Description: "Creates one Paperclip agent key only during an approved OpenTofu apply.",
		Attributes: map[string]schema.Attribute{
			"agent_id": schema.StringAttribute{
				Description: "Paperclip agent that owns the generated key.",
				Required:    true,
			},
			"name": schema.StringAttribute{
				Description: "Name recorded for the generated Paperclip agent key.",
				Required:    true,
			},
			"activation": schema.StringAttribute{
				Description: "Apply-only activation value; pass timestamp() so planning cannot mint a key.",
				Required:    true,
			},
			"key_id": schema.StringAttribute{
				Description: "Generated Paperclip key identifier.",
				Computed:    true,
			},
			"token": schema.StringAttribute{
				Description: "Generated bearer supplied only to an ephemeral or write-only consumer.",
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
	key, err := r.client.CreateAgentKey(ctx, data.AgentID.ValueString(), data.Name.ValueString())
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
	resp.Diagnostics.Append(resp.Result.Set(ctx, &data)...)
}
