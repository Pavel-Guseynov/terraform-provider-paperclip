package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

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
	client   *client.Client
	keyID    string
	isLookup bool
}

type PaperclipAgentKeyEphemeralResourceModel struct {
	AgentID    types.String                      `tfsdk:"agent_id"`
	Name       types.String                      `tfsdk:"name"`
	Generation types.Int64                       `tfsdk:"generation"`
	Scope      *AgentKeyScopeModel               `tfsdk:"scope"`
	KeyID      types.String                      `tfsdk:"key_id"`
	Token      types.String                      `tfsdk:"token"`
	Applying   types.Bool                        `tfsdk:"applying"`
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
				Description: "Paperclip agent identifier owning the key.",
				Optional:    true,
			},
			"name": schema.StringAttribute{
				Description: "Agent key name.",
				Optional:    true,
			},
			"generation": schema.Int64Attribute{
				Description: "Monotonic generation number.",
				Optional:    true,
			},
			"scope": schema.SingleNestedAttribute{
				Description: "Native agent key scope: standard, task_bridge, or skill_test.",
				Optional:    true,
				Attributes: map[string]schema.Attribute{
					"kind": schema.StringAttribute{
						Description: "Scope kind: standard, task_bridge, or skill_test.",
						Optional:    true,
					},
					"project_id": schema.StringAttribute{
						Description: "Primary project boundary for task_bridge scope.",
						Optional:    true,
					},
					"project_ids": schema.ListAttribute{
						Description: "Additional project boundaries for task_bridge scope.",
						Optional:    true,
						ElementType: types.StringType,
					},
					"parent_issue_id": schema.StringAttribute{
						Description: "Primary parent issue boundary for task_bridge scope.",
						Optional:    true,
					},
					"parent_issue_ids": schema.ListAttribute{
						Description: "Additional parent issue boundaries for task_bridge scope.",
						Optional:    true,
						ElementType: types.StringType,
					},
					"allowed_assignee_agent_ids": schema.ListAttribute{
						Description: "Allowed assignee agent boundaries for task_bridge scope.",
						Optional:    true,
						ElementType: types.StringType,
					},
					"issue_id": schema.StringAttribute{
						Description: "Issue boundary for skill_test scope.",
						Optional:    true,
					},
				},
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
			"applying": schema.BoolAttribute{
				Description: "Indicates whether the apply phase is actively running (e.g. tofu.applying). When false, credential minting is deferred.",
				Optional:    true,
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

	agentID := data.AgentID.ValueString()
	name := data.Name.ValueString()
	gen := int64(1)
	if !data.Generation.IsNull() && !data.Generation.IsUnknown() {
		gen = data.Generation.ValueInt64()
	}

	// 0. Plan-phase deferral when applying is false (e.g. via tofu.applying)
	if !data.Applying.IsNull() && !data.Applying.IsUnknown() && !data.Applying.ValueBool() {
		data.KeyID = types.StringValue(fmt.Sprintf("(plan-agent-key-id-%d)", gen))
		data.Token = types.StringValue(fmt.Sprintf("(plan-agent-token-%d)", gen))
		resp.Diagnostics.Append(resp.Result.Set(ctx, &data)...)
		return
	}

	// 1. If key_id is provided directly without name, handle key_id lookup
	if name == "" && !data.KeyID.IsNull() && !data.KeyID.IsUnknown() && data.KeyID.ValueString() != "" {
		keyID := data.KeyID.ValueString()
		token, ok := r.client.GetMintedToken(keyID)
		if !ok {
			if r.client.IsTokenDelivered(keyID) {
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
		r.isLookup = true
		if resp.Private != nil {
			keyBytes, _ := json.Marshal(keyID)
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, "key_id", keyBytes)...)
			lookupBytes, _ := json.Marshal(true)
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, "is_lookup", lookupBytes)...)
			if aID, ok := r.client.GetAgentIDForKey(keyID); ok && aID != "" {
				agentBytes, _ := json.Marshal(aID)
				resp.Diagnostics.Append(resp.Private.SetKey(ctx, "agent_id", agentBytes)...)
			}
		}
		resp.Diagnostics.Append(resp.Result.Set(ctx, &data)...)
		return
	}

	if name == "" || agentID == "" {
		if req.ClientCapabilities.DeferralAllowed {
			resp.Deferred = &ephemeral.Deferred{
				Reason: ephemeral.DeferredReasonUnknown,
			}
			return
		}
		resp.Diagnostics.AddError("Missing name or agent_id", "A valid name and agent_id (or key_id) are required for ephemeral agent key.")
		return
	}

	// 2. Check if this generation is already delivered
	if r.client.IsGenerationDelivered(name, false, agentID, gen) {
		data.Token = types.StringNull()
		if keyID, _, ok := r.client.GetMintedKeyForName(name, false, agentID); ok && keyID != "" {
			data.KeyID = types.StringValue(keyID)
		}
		resp.Diagnostics.Append(resp.Result.Set(ctx, &data)...)
		return
	}

	// 3. Defer during plan so plan never mints
	if req.ClientCapabilities.DeferralAllowed {
		resp.Deferred = &ephemeral.Deferred{
			Reason: ephemeral.DeferredReasonUnknown,
		}
		return
	}

	// 4. Mint key at apply time
	scope, err := scopeModelToClient(ctx, data.Scope)
	if err != nil {
		resp.Diagnostics.AddError("Invalid agent key scope", err.Error())
		return
	}

	newKey, err := r.client.CreateAgentKey(ctx, agentID, name, scope)
	if err != nil {
		resp.Diagnostics.AddError("Error minting Paperclip agent key", err.Error())
		return
	}

	r.client.RegisterMintedKey(newKey.ID, newKey.Token, name, false, agentID)
	r.keyID = newKey.ID

	data.KeyID = types.StringValue(newKey.ID)
	data.Token = types.StringValue(newKey.Token)

	if resp.Private != nil {
		keyBytes, _ := json.Marshal(newKey.ID)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, "key_id", keyBytes)...)
		agentBytes, _ := json.Marshal(agentID)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, "agent_id", agentBytes)...)
	}

	resp.Diagnostics.Append(resp.Result.Set(ctx, &data)...)
}

func (r *PaperclipAgentKeyEphemeralResource) Close(ctx context.Context, req ephemeral.CloseRequest, resp *ephemeral.CloseResponse) {
	if r.client == nil {
		return
	}
	var keyID string
	var agentID string
	var isLookup bool
	if req.Private != nil {
		keyIDBytes, diags := req.Private.GetKey(ctx, "key_id")
		resp.Diagnostics.Append(diags...)
		if len(keyIDBytes) > 0 {
			_ = json.Unmarshal(keyIDBytes, &keyID)
		}

		lookupBytes, diagsLookup := req.Private.GetKey(ctx, "is_lookup")
		resp.Diagnostics.Append(diagsLookup...)
		if len(lookupBytes) > 0 {
			_ = json.Unmarshal(lookupBytes, &isLookup)
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
	if !isLookup {
		isLookup = r.isLookup
	}
	if keyID == "" {
		return
	}
	if agentID == "" {
		if id, ok := r.client.GetAgentIDForKey(keyID); ok {
			agentID = id
		}
	}

	if isLookup {
		r.client.MarkTokenDelivered(keyID)
		r.client.ClearMintedToken(keyID)
		return
	}

	if !r.client.IsKeyDeliveryAcknowledged(keyID) {
		if agentID != "" {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := r.client.RevokeAgentKey(cleanupCtx, agentID, keyID); err != nil {
				resp.Diagnostics.AddError("Error revoking unconfirmed agent key", err.Error())
			}
		}
		return
	}

	r.client.ClearMintedToken(keyID)
}
