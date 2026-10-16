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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type PaperclipRoutineWebhookDeliveryResource struct{ client *client.Client }
type PaperclipRoutineWebhookDeliveryModel struct {
	ID         types.String `tfsdk:"id"`
	RoutineID  types.String `tfsdk:"routine_id"`
	TriggerID  types.String `tfsdk:"trigger_id"`
	Generation types.Int64  `tfsdk:"generation"`
}

func NewPaperclipRoutineWebhookDeliveryResource() resource.Resource {
	return &PaperclipRoutineWebhookDeliveryResource{}
}
func (r *PaperclipRoutineWebhookDeliveryResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_routine_webhook_delivery"
}
func (r *PaperclipRoutineWebhookDeliveryResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{Description: "Acknowledges initial write-only delivery by clearing the native trigger's setupPending flag. Depend explicitly on the successful consumer. This has no independent native object; the trigger resource owns revocation. Destroy relinquishes this irreversible acknowledgement. Import as routine_id/trigger_id for an already acknowledged webhook.", Attributes: map[string]schema.Attribute{
		"id":         schema.StringAttribute{Computed: true, Description: "Native producing trigger identity.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
		"routine_id": schema.StringAttribute{Required: true, Description: "Owning routine; immutable."},
		"trigger_id": schema.StringAttribute{Required: true, Description: "Persistent producing trigger ID from paperclip_routine_trigger; immutable."},
		"generation": schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(1), Description: "Generation 1 only. Replacements through native rotate-secret are unsupported."},
	}}
}
func (r *PaperclipRoutineWebhookDeliveryResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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
func validateWebhookDelivery(m PaperclipRoutineWebhookDeliveryModel) diag.Diagnostics {
	var d diag.Diagnostics
	if !m.Generation.IsNull() && !m.Generation.IsUnknown() && m.Generation.ValueInt64() != 1 {
		d.AddError("Unsupported webhook secret lifecycle", client.RoutineWebhookSecretLimitation)
	}
	return d
}
func (r *PaperclipRoutineWebhookDeliveryResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m PaperclipRoutineWebhookDeliveryModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(validateWebhookDelivery(m)...)
	}
}
func (r *PaperclipRoutineWebhookDeliveryResource) observe(ctx context.Context, m PaperclipRoutineWebhookDeliveryModel) (*client.PaperclipRoutineTrigger, error) {
	remote, err := r.client.GetRoutineTrigger(ctx, m.RoutineID.ValueString(), m.TriggerID.ValueString())
	if err != nil {
		return nil, err
	}
	if remote.Kind != "webhook" {
		return nil, fmt.Errorf("only webhook triggers have delivery acknowledgements")
	}
	return remote, nil
}
func (r *PaperclipRoutineWebhookDeliveryResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}
	var m PaperclipRoutineWebhookDeliveryModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(validateWebhookDelivery(m)...)
	if !req.State.Raw.IsNull() {
		var previous PaperclipRoutineWebhookDeliveryModel
		resp.Diagnostics.Append(req.State.Get(ctx, &previous)...)
		if (!m.RoutineID.IsUnknown() && m.RoutineID != previous.RoutineID) || (!m.TriggerID.IsUnknown() && m.TriggerID != previous.TriggerID) {
			resp.Diagnostics.AddError("Immutable webhook delivery identity", "Retain routine_id and trigger_id; delivery cannot replace its producing trigger.")
		}
	}
	if r.client != nil && !m.RoutineID.IsUnknown() && !m.TriggerID.IsUnknown() {
		if _, err := r.observe(ctx, m); err != nil {
			resp.Diagnostics.AddError("Invalid webhook delivery reference", err.Error())
		}
	}
}
func (r *PaperclipRoutineWebhookDeliveryResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m PaperclipRoutineWebhookDeliveryModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(validateWebhookDelivery(m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	remote, err := r.observe(ctx, m)
	if err != nil {
		resp.Diagnostics.AddError("Invalid webhook delivery reference", err.Error())
		return
	}
	name, routineID, registered := r.client.RoutineWebhookIdentity(m.TriggerID.ValueString())
	secret, available := r.client.GetMintedToken(m.TriggerID.ValueString())
	if !registered || routineID != m.RoutineID.ValueString() || !available || secret == "" {
		resp.Diagnostics.AddError("Initial webhook secret unavailable", "The pending webhook must come from the same apply's ephemeral producer. Its one-time value cannot be recovered; remove abandoned pending setup before recreating it.")
		return
	}
	if !remote.SetupPending {
		resp.Diagnostics.AddError("Webhook already acknowledged", "Import an existing acknowledgement instead of claiming a new delivery.")
		return
	}
	updated, err := r.client.UpdateRoutineTrigger(ctx, m.TriggerID.ValueString(), map[string]interface{}{"setupPending": false})
	if err != nil {
		resp.Diagnostics.AddError("Error acknowledging webhook delivery", err.Error())
		return
	}
	if updated.SetupPending {
		resp.Diagnostics.AddError("Webhook acknowledgement did not converge", "The native trigger is still pending; delivery is not confirmed.")
		return
	}
	m.ID = m.TriggerID
	m.Generation = types.Int64Value(1)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
	if !resp.Diagnostics.HasError() {
		r.client.ConfirmRoutineWebhook(name, routineID, m.ID.ValueString(), 1)
	}
}
func (r *PaperclipRoutineWebhookDeliveryResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m PaperclipRoutineWebhookDeliveryModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	remote, err := r.observe(ctx, m)
	if errors.Is(err, client.ErrNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading webhook delivery", err.Error())
		return
	}
	if remote.SetupPending {
		resp.Diagnostics.AddError("Webhook delivery incomplete", "The native trigger remains in pending setup; acknowledgement is not established.")
		return
	}
	m.ID = m.TriggerID
	m.Generation = types.Int64Value(1)
	r.client.AcknowledgeKeyDelivery(m.ID.ValueString())
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (r *PaperclipRoutineWebhookDeliveryResource) Update(_ context.Context, _ resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Immutable webhook delivery", "Initial delivery acknowledgement cannot be updated or rotated. Retain its producing identity and generation 1.")
}
func (r *PaperclipRoutineWebhookDeliveryResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
}
func (r *PaperclipRoutineWebhookDeliveryResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(req.ID, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		resp.Diagnostics.AddError("Invalid webhook delivery import ID", "Use routine_id/trigger_id for an acknowledged webhook.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("routine_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("trigger_id"), parts[1])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
}
