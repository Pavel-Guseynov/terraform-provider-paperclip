package provider

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Pavel-Guseynov/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type PaperclipRoutineWebhookSecretEphemeralResource struct{ client *client.Client }
type PaperclipRoutineWebhookSecretModel struct {
	RoutineID       types.String `tfsdk:"routine_id"`
	Name            types.String `tfsdk:"name"`
	Generation      types.Int64  `tfsdk:"generation"`
	Applying        types.Bool   `tfsdk:"applying"`
	SigningMode     types.String `tfsdk:"signing_mode"`
	ReplayWindowSec types.Int64  `tfsdk:"replay_window_sec"`
	TriggerID       types.String `tfsdk:"trigger_id"`
	Secret          types.String `tfsdk:"secret"`
}

func NewPaperclipRoutineWebhookSecretEphemeralResource() ephemeral.EphemeralResource {
	return &PaperclipRoutineWebhookSecretEphemeralResource{}
}
func (r *PaperclipRoutineWebhookSecretEphemeralResource) Metadata(_ context.Context, req ephemeral.MetadataRequest, resp *ephemeral.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_routine_webhook_secret"
}
func (r *PaperclipRoutineWebhookSecretEphemeralResource) Schema(_ context.Context, _ ephemeral.SchemaRequest, resp *ephemeral.SchemaResponse) {
	resp.Schema = schema.Schema{Description: "Produces generation 1 of a pending native webhook only during apply. The trigger resource first records its non-secret identity; bind the write-only consumer to that ID, then acknowledge through paperclip_routine_webhook_delivery. Unconfirmed triggers are deleted on Close and graceful exit. Rotation is rejected.", Attributes: map[string]schema.Attribute{
		"routine_id":        schema.StringAttribute{Required: true, Description: "Owning routine identity."},
		"name":              schema.StringAttribute{Required: true, Description: "Stable delivery identity, matching webhook_delivery_name on the managed trigger in the same provider configuration."},
		"generation":        schema.Int64Attribute{Required: true, Description: "Generation 1 only. Native rotation cannot preserve the previous credential until acknowledgement."},
		"applying":          schema.BoolAttribute{Required: true, Description: "Use tofu.applying; false defers all minting during plan."},
		"signing_mode":      schema.StringAttribute{Required: true, Description: "bearer, hmac_sha256, github_hmac, or none; match managed trigger configuration."},
		"replay_window_sec": schema.Int64Attribute{Required: true, Description: "Native replay window, 30–86400 seconds."},
		"trigger_id":        schema.StringAttribute{Computed: true, Description: "Ephemeral producing trigger identity. Use the managed trigger resource's persistent id for persistent consumer attributes."},
		"secret":            schema.StringAttribute{Computed: true, Sensitive: true, Description: "One-time secret for a write-only consumer; null when already delivered. Never persisted."},
	}}
}
func (r *PaperclipRoutineWebhookSecretEphemeralResource) Configure(_ context.Context, req ephemeral.ConfigureRequest, resp *ephemeral.ConfigureResponse) {
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
func (r *PaperclipRoutineWebhookSecretEphemeralResource) Open(ctx context.Context, req ephemeral.OpenRequest, resp *ephemeral.OpenResponse) {
	var m PaperclipRoutineWebhookSecretModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if m.Generation.ValueInt64() != 1 {
		resp.Diagnostics.AddError("Unsupported webhook secret lifecycle", client.RoutineWebhookSecretLimitation)
		return
	}
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Paperclip Client", "Expected a configured Paperclip provider client.")
		return
	}
	if m.Name.ValueString() == "" || m.RoutineID.ValueString() == "" {
		resp.Diagnostics.AddError("Missing webhook delivery identity", "Set a nonempty name and routine_id.")
		return
	}
	if r.client.RoutineWebhookDelivered(m.Name.ValueString(), m.RoutineID.ValueString(), 1) {
		id, _, _ := r.client.RoutineWebhookForName(m.Name.ValueString(), m.RoutineID.ValueString())
		m.TriggerID = types.StringValue(id)
		m.Secret = types.StringNull()
		resp.Diagnostics.Append(resp.Result.Set(ctx, &m)...)
		return
	}
	if !m.Applying.ValueBool() {
		if req.ClientCapabilities.DeferralAllowed {
			resp.Deferred = &ephemeral.Deferred{Reason: ephemeral.DeferredReasonUnknown}
			return
		}
		m.TriggerID = types.StringNull()
		m.Secret = types.StringNull()
		resp.Diagnostics.Append(resp.Result.Set(ctx, &m)...)
		return
	}
	model := PaperclipRoutineTriggerModel{Kind: types.StringValue("webhook"), SigningMode: m.SigningMode, ReplayWindowSec: m.ReplayWindowSec}
	resp.Diagnostics.Append(validateRoutineTrigger(model)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !containsRoutineSigningMode(m.SigningMode.ValueString()) {
		resp.Diagnostics.AddError("Invalid webhook signing mode", "Use bearer, hmac_sha256, github_hmac or none.")
		return
	}
	id, secret, ok := r.client.RoutineWebhookForName(m.Name.ValueString(), m.RoutineID.ValueString())
	if ok && secret == "" {
		resp.Diagnostics.AddError("Pending webhook secret unavailable", "The native one-time value cannot be recovered. Resolve unconfirmed native setup before retrying; no new trigger or secret was minted.")
		return
	}
	if !ok {
		if _, err := r.client.GetRoutine(ctx, m.RoutineID.ValueString()); err != nil {
			resp.Diagnostics.AddError("Invalid routine reference", err.Error())
			return
		}
		trigger, value, err := r.client.MintRoutineWebhook(ctx, m.RoutineID.ValueString(), m.Name.ValueString(), m.SigningMode.ValueString(), m.ReplayWindowSec.ValueInt64())
		if err != nil {
			resp.Diagnostics.AddError("Error creating pending webhook", err.Error())
			return
		}
		id, secret = trigger.ID, value
	}
	m.TriggerID = types.StringValue(id)
	m.Secret = types.StringValue(secret)
	if resp.Private == nil {
		resp.Diagnostics.AddError("Missing ephemeral private state", "Cannot retain the trigger identity for compensating deletion.")
	} else {
		setPrivateStateKey(ctx, resp.Private, "trigger_id", id, &resp.Diagnostics)
	}
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.Result.Set(ctx, &m)...)
	}
	if resp.Diagnostics.HasError() {
		r.release(ctx, id, &resp.Diagnostics)
	}
}
func containsRoutineSigningMode(value string) bool {
	return value == "bearer" || value == "hmac_sha256" || value == "github_hmac" || value == "none"
}
func (r *PaperclipRoutineWebhookSecretEphemeralResource) release(ctx context.Context, id string, d *diag.Diagnostics) {
	if !r.client.IsKeyDeliveryAcknowledged(id) {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if err := r.client.DeleteRoutineTrigger(cleanupCtx, id); err != nil && !errors.Is(err, client.ErrNotFound) {
			d.AddError("Error deleting unconfirmed webhook", err.Error())
			return
		}
	}
	r.client.ClearMintedToken(id)
}
func (r *PaperclipRoutineWebhookSecretEphemeralResource) Close(ctx context.Context, req ephemeral.CloseRequest, resp *ephemeral.CloseResponse) {
	if r.client == nil || req.Private == nil {
		return
	}
	var id string
	getPrivateStateKey(ctx, req.Private, "trigger_id", &id, &resp.Diagnostics)
	if resp.Diagnostics.HasError() || id == "" {
		return
	}
	unconfirmed := !r.client.IsKeyDeliveryAcknowledged(id)
	r.release(ctx, id, &resp.Diagnostics)
	if unconfirmed && !resp.Diagnostics.HasError() {
		resp.Diagnostics.AddError("Webhook delivery unconfirmed", "Deleted pending setup because no successful write-only consumer delivery was acknowledged by paperclip_routine_webhook_delivery.")
	}
}
