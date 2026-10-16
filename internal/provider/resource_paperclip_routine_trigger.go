package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Pavel-Guseynov/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type PaperclipRoutineTriggerResource struct{ client *client.Client }
type PaperclipRoutineTriggerModel struct {
	ID                  types.String `tfsdk:"id"`
	RoutineID           types.String `tfsdk:"routine_id"`
	Kind                types.String `tfsdk:"kind"`
	Label               types.String `tfsdk:"label"`
	Enabled             types.Bool   `tfsdk:"enabled"`
	CronExpression      types.String `tfsdk:"cron_expression"`
	Timezone            types.String `tfsdk:"timezone"`
	SigningMode         types.String `tfsdk:"signing_mode"`
	ReplayWindowSec     types.Int64  `tfsdk:"replay_window_sec"`
	WebhookDeliveryName types.String `tfsdk:"webhook_delivery_name"`
	WebhookGeneration   types.Int64  `tfsdk:"webhook_generation"`
}

func NewPaperclipRoutineTriggerResource() resource.Resource {
	return &PaperclipRoutineTriggerResource{}
}
func (r *PaperclipRoutineTriggerResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_routine_trigger"
}
func (r *PaperclipRoutineTriggerResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{Description: "Manages native routine triggers. New webhooks require generation-1 ephemeral delivery to a write-only consumer before setup acknowledgement. Rotation is rejected without mutation. Imported webhooks support metadata and native deletion. Kind and owning routine cannot change in place.", Attributes: map[string]schema.Attribute{
		"id":                    schema.StringAttribute{Computed: true, Description: "Native trigger identity.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
		"routine_id":            schema.StringAttribute{Required: true, Description: "Owning routine identity; immutable."},
		"kind":                  schema.StringAttribute{Required: true, Description: "schedule, webhook, or api; immutable.", Validators: []validator.String{stringOneOfValidator{allowed: []string{"schedule", "webhook", "api"}}}},
		"label":                 schema.StringAttribute{Optional: true, Description: "Nullable label, at most 120 characters; omitted configuration clears it."},
		"enabled":               schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true), Description: "Whether the trigger is enabled; default true."},
		"cron_expression":       schema.StringAttribute{Optional: true, Description: "Required only for schedule triggers. Native cron syntax; cannot be cleared on schedules."},
		"timezone":              schema.StringAttribute{Optional: true, Description: "Required only for schedule triggers. Native IANA timezone; use UTC explicitly. Cannot be cleared on schedules."},
		"signing_mode":          schema.StringAttribute{Optional: true, Description: "Webhook signing mode: bearer, hmac_sha256, github_hmac, or none; nullable. Match the initial ephemeral producer settings.", Validators: []validator.String{stringOneOfValidator{allowed: []string{"bearer", "hmac_sha256", "github_hmac", "none"}}}},
		"replay_window_sec":     schema.Int64Attribute{Optional: true, Description: "Webhook replay window, 30–86400 seconds; nullable. Match the initial ephemeral producer settings."},
		"webhook_delivery_name": schema.StringAttribute{Optional: true, Description: "Stable generation-1 delivery identity matching the ephemeral producer. Required for webhook creation; omit for metadata-only imports. Immutable."},
		"webhook_generation":    schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(1), Description: "Credential generation; default 1. Rotation is unsupported and larger generations are rejected before mutation."},
	}}
}
func (r *PaperclipRoutineTriggerResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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
func validateRoutineTrigger(m PaperclipRoutineTriggerModel) diag.Diagnostics {
	var d diag.Diagnostics
	if !m.Label.IsUnknown() && (len([]rune(m.Label.ValueString())) > 120 || strings.TrimSpace(m.Label.ValueString()) != m.Label.ValueString()) {
		d.AddAttributeError(path.Root("label"), "Invalid trigger label", "Use at most 120 characters without surrounding whitespace.")
	}
	if m.Kind.IsUnknown() {
		return d
	}
	if m.Kind.ValueString() == "webhook" && !m.WebhookGeneration.IsNull() && !m.WebhookGeneration.IsUnknown() && m.WebhookGeneration.ValueInt64() != 1 {
		d.AddError("Unsupported webhook secret lifecycle", client.RoutineWebhookSecretLimitation)
	}
	if m.Kind.ValueString() == "schedule" {
		for key, value := range map[string]types.String{"cron_expression": m.CronExpression, "timezone": m.Timezone} {
			if !value.IsUnknown() && strings.TrimSpace(value.ValueString()) == "" {
				d.AddAttributeError(path.Root(key), "Schedule field required", "Schedule triggers require cron_expression and timezone; they cannot be cleared.")
			}
		}
	} else if !m.CronExpression.IsNull() || !m.Timezone.IsNull() {
		d.AddError("Invalid trigger fields", "Only schedule triggers accept cron_expression or timezone.")
	}
	if m.Kind.ValueString() != "webhook" && (!m.SigningMode.IsNull() || !m.ReplayWindowSec.IsNull()) {
		d.AddError("Invalid trigger fields", "Only webhook triggers accept signing_mode or replay_window_sec.")
	}
	if !m.ReplayWindowSec.IsNull() && !m.ReplayWindowSec.IsUnknown() && (m.ReplayWindowSec.ValueInt64() < 30 || m.ReplayWindowSec.ValueInt64() > 86400) {
		d.AddAttributeError(path.Root("replay_window_sec"), "Invalid replay window", "Use 30–86400 seconds.")
	}
	return d
}
func (r *PaperclipRoutineTriggerResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m PaperclipRoutineTriggerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(validateRoutineTrigger(m)...)
	}
}
func triggerTransition(m PaperclipRoutineTriggerModel, previous PaperclipRoutineTriggerModel) error {
	if m.RoutineID.ValueString() != previous.RoutineID.ValueString() || m.Kind.ValueString() != previous.Kind.ValueString() {
		return fmt.Errorf("native trigger kind and routine ownership are immutable; keep routine_id and kind unchanged")
	}
	if m.WebhookDeliveryName != previous.WebhookDeliveryName {
		return fmt.Errorf("webhook delivery identity is immutable; keep webhook_delivery_name unchanged")
	}
	return nil
}
func (r *PaperclipRoutineTriggerResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}
	var m PaperclipRoutineTriggerModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(validateRoutineTrigger(m)...)
	if req.State.Raw.IsNull() {
		if m.Kind.ValueString() == "webhook" && m.WebhookDeliveryName.IsNull() {
			resp.Diagnostics.AddError("Missing initial webhook delivery", "Use the ephemeral webhook producer with webhook_delivery_name and depend on its write-only consumer before acknowledging setup.")
		}
	} else {
		var previous PaperclipRoutineTriggerModel
		resp.Diagnostics.Append(req.State.Get(ctx, &previous)...)
		if !m.RoutineID.IsUnknown() && !m.Kind.IsUnknown() {
			if err := triggerTransition(m, previous); err != nil {
				resp.Diagnostics.AddError("Unsupported trigger transition", err.Error())
			}
		}
	}
	if r.client != nil && !m.RoutineID.IsUnknown() {
		if _, err := r.client.GetRoutine(ctx, m.RoutineID.ValueString()); err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("routine_id"), "Invalid routine reference", err.Error())
		}
	}
}
func triggerInput(m PaperclipRoutineTriggerModel) map[string]interface{} {
	input := map[string]interface{}{"label": m.Label.ValueStringPointer(), "enabled": m.Enabled.ValueBool()}
	if m.Kind.ValueString() == "schedule" {
		input["cronExpression"] = m.CronExpression.ValueString()
		input["timezone"] = m.Timezone.ValueString()
	}
	if m.Kind.ValueString() == "webhook" {
		input["signingMode"] = m.SigningMode.ValueStringPointer()
		input["replayWindowSec"] = m.ReplayWindowSec.ValueInt64Pointer()
	}
	return input
}
func refreshRoutineTrigger(m *PaperclipRoutineTriggerModel, remote *client.PaperclipRoutineTrigger) {
	m.ID = types.StringValue(remote.ID)
	m.RoutineID = types.StringValue(remote.RoutineID)
	m.Kind = types.StringValue(remote.Kind)
	m.Label = types.StringPointerValue(remote.Label)
	m.Enabled = types.BoolValue(remote.Enabled)
	m.CronExpression = types.StringPointerValue(remote.CronExpression)
	m.Timezone = types.StringPointerValue(remote.Timezone)
	m.SigningMode = types.StringPointerValue(remote.SigningMode)
	m.ReplayWindowSec = types.Int64PointerValue(remote.ReplayWindowSec)
	if m.WebhookGeneration.IsNull() || m.WebhookGeneration.IsUnknown() {
		m.WebhookGeneration = types.Int64Value(1)
	}
}
func (r *PaperclipRoutineTriggerResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m PaperclipRoutineTriggerModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(validateRoutineTrigger(m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if _, err := r.client.GetRoutine(ctx, m.RoutineID.ValueString()); err != nil {
		resp.Diagnostics.AddError("Invalid routine reference", err.Error())
		return
	}
	input := triggerInput(m)
	var remote *client.PaperclipRoutineTrigger
	var err error
	if m.Kind.ValueString() == "webhook" {
		id, secret, ok := r.client.RoutineWebhookForName(m.WebhookDeliveryName.ValueString(), m.RoutineID.ValueString())
		if !ok || secret == "" {
			resp.Diagnostics.AddError("Missing initial webhook delivery", "Use the generation-1 ephemeral producer and depend on its successful write-only consumer before creating the managed trigger.")
			return
		}
		pending, readErr := r.client.GetRoutineTrigger(ctx, m.RoutineID.ValueString(), id)
		if readErr != nil {
			resp.Diagnostics.AddError("Invalid pending webhook", readErr.Error())
			return
		}
		if !pending.SetupPending {
			resp.Diagnostics.AddError("Invalid pending webhook", "Refusing to acknowledge a trigger that is not in native setupPending state.")
			return
		}
		if pending.SigningMode == nil || pending.ReplayWindowSec == nil || *pending.SigningMode != m.SigningMode.ValueString() || *pending.ReplayWindowSec != m.ReplayWindowSec.ValueInt64() {
			resp.Diagnostics.AddError("Pending webhook settings differ", "Match the ephemeral producer signing_mode and replay_window_sec before acknowledging its delivery.")
			return
		}
		input["setupPending"] = false
		remote, err = r.client.UpdateRoutineTrigger(ctx, id, input)
	} else {
		input["kind"] = m.Kind.ValueString()
		remote, err = r.client.CreateRoutineTrigger(ctx, m.RoutineID.ValueString(), input)
	}
	if err != nil {
		resp.Diagnostics.AddError("Error creating routine trigger", err.Error())
		return
	}
	refreshRoutineTrigger(&m, remote)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
	if !resp.Diagnostics.HasError() && m.Kind.ValueString() == "webhook" {
		r.client.ConfirmRoutineWebhook(m.WebhookDeliveryName.ValueString(), m.RoutineID.ValueString(), m.ID.ValueString(), 1)
	}
}
func (r *PaperclipRoutineTriggerResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m PaperclipRoutineTriggerModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	remote, err := r.client.GetRoutineTrigger(ctx, m.RoutineID.ValueString(), m.ID.ValueString())
	if errors.Is(err, client.ErrNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading routine trigger", err.Error())
		return
	}
	refreshRoutineTrigger(&m, remote)
	if m.WebhookDeliveryName.ValueString() != "" && remote.Kind == "webhook" && !remote.SetupPending {
		r.client.RegisterRoutineWebhook(remote.ID, "", m.WebhookDeliveryName.ValueString(), remote.RoutineID)
		r.client.ConfirmRoutineWebhook(m.WebhookDeliveryName.ValueString(), remote.RoutineID, remote.ID, m.WebhookGeneration.ValueInt64())
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (r *PaperclipRoutineTriggerResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m, previous PaperclipRoutineTriggerModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &previous)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(validateRoutineTrigger(m)...)
	if err := triggerTransition(m, previous); err != nil {
		resp.Diagnostics.AddError("Unsupported trigger transition", err.Error())
	}
	if resp.Diagnostics.HasError() {
		return
	}
	if _, err := r.client.GetRoutineTrigger(ctx, previous.RoutineID.ValueString(), previous.ID.ValueString()); err != nil {
		resp.Diagnostics.AddError("Invalid routine trigger reference", err.Error())
		return
	}
	remote, err := r.client.UpdateRoutineTrigger(ctx, previous.ID.ValueString(), triggerInput(m))
	if err != nil {
		resp.Diagnostics.AddError("Error updating routine trigger", err.Error())
		return
	}
	refreshRoutineTrigger(&m, remote)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (r *PaperclipRoutineTriggerResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m PaperclipRoutineTriggerModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteRoutineTrigger(ctx, m.ID.ValueString()); err != nil && !errors.Is(err, client.ErrNotFound) {
		resp.Diagnostics.AddError("Error deleting routine trigger", err.Error())
	}
}
func (r *PaperclipRoutineTriggerResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(req.ID, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		resp.Diagnostics.AddError("Invalid routine trigger import ID", "Use routine_id/trigger_id; native reads resolve exact trigger identity from its owning routine.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("routine_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
}
