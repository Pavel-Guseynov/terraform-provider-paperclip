package provider

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Pavel-Guseynov/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &PaperclipBoardKeyResource{}
	_ resource.ResourceWithConfigure   = &PaperclipBoardKeyResource{}
	_ resource.ResourceWithImportState = &PaperclipBoardKeyResource{}
	_ resource.ResourceWithModifyPlan  = &PaperclipBoardKeyResource{}
)

func NewPaperclipBoardKeyResource() resource.Resource {
	return &PaperclipBoardKeyResource{}
}

type PaperclipBoardKeyResource struct {
	client *client.Client
}

type PaperclipBoardKeyResourceModel struct {
	ID                 types.String `tfsdk:"id"`
	Name               types.String `tfsdk:"name"`
	Generation         types.Int64  `tfsdk:"generation"`
	ExpiresAt          types.String `tfsdk:"expires_at"`
	RequestedCompanyID types.String `tfsdk:"requested_company_id"`
	KeyID              types.String `tfsdk:"key_id"`
	DeliveryState      types.String `tfsdk:"delivery_state"`
}

func (r *PaperclipBoardKeyResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_board_key"
}

func (r *PaperclipBoardKeyResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages the durable, non-secret lifecycle record of a Paperclip board API key.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "OpenTofu internal resource ID matching the Paperclip key ID.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Description: "Label identifying the board key.",
				Required:    true,
			},
			"generation": schema.Int64Attribute{
				Description: "Declared generation. Changing generation mints a new key, delivers it, and revokes the superseded key.",
				Required:    true,
			},
			"expires_at": schema.StringAttribute{
				Description: "Optional RFC3339 expiration timestamp.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"requested_company_id": schema.StringAttribute{
				Description: "Optional company ID to scope board key activity records.",
				Optional:    true,
			},
			"key_id": schema.StringAttribute{
				Description: "Paperclip board key identifier.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"delivery_state": schema.StringAttribute{
				Description: "Lifecycle delivery state (e.g. delivered).",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *PaperclipBoardKeyResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *client.Client, got: %T", req.ProviderData),
		)
		return
	}
	r.client = c
}

func (r *PaperclipBoardKeyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan PaperclipBoardKeyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	keyID := ""
	if !plan.KeyID.IsNull() && !plan.KeyID.IsUnknown() && plan.KeyID.ValueString() != "" {
		keyID = plan.KeyID.ValueString()
	} else if kID, _, ok := r.client.GetMintedKeyForName(plan.Name.ValueString(), true, ""); ok && kID != "" {
		keyID = kID
	}

	if keyID == "" {
		resp.Diagnostics.AddError(
			"Missing Ephemeral Mint",
			"No ephemeral board key was minted for this generation. Ephemeral minting via ephemeral.paperclip_board_key delivered to a consumer is required before creating the managed board key record.",
		)
		return
	}

	r.client.AcknowledgeKeyDelivery(keyID)
	r.client.RecordDeliveredGeneration(plan.Name.ValueString(), true, "", plan.Generation.ValueInt64(), keyID)

	plan.ID = types.StringValue(keyID)
	plan.KeyID = types.StringValue(keyID)
	plan.DeliveryState = types.StringValue("delivered")
	if plan.ExpiresAt.IsUnknown() {
		plan.ExpiresAt = types.StringNull()
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipBoardKeyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state PaperclipBoardKeyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	keyID := state.KeyID.ValueString()
	if keyID == "" {
		keyID = state.ID.ValueString()
	}
	if keyID == "" {
		resp.State.RemoveResource(ctx)
		return
	}

	key, err := r.client.GetBoardKey(ctx, keyID)
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading Paperclip board key", err.Error())
		return
	}

	state.ID = types.StringValue(key.ID)
	state.KeyID = types.StringValue(key.ID)
	state.Name = types.StringValue(key.Name)
	if key.ExpiresAt != nil {
		state.ExpiresAt = types.StringValue(key.ExpiresAt.Format(time.RFC3339))
	} else {
		state.ExpiresAt = types.StringNull()
	}

	// Preserve delivery_state from durable state without deriving it from in-process memory.
	if state.DeliveryState.IsNull() || state.DeliveryState.IsUnknown() || state.DeliveryState.ValueString() == "" {
		state.DeliveryState = types.StringValue("delivered")
	}

	if r.client != nil && key.ID != "" {
		r.client.RecordDeliveredGeneration(key.Name, true, "", state.Generation.ValueInt64(), key.ID)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *PaperclipBoardKeyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan PaperclipBoardKeyResourceModel
	var state PaperclipBoardKeyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	oldKeyID := state.KeyID.ValueString()
	if oldKeyID == "" {
		oldKeyID = state.ID.ValueString()
	}

	shouldRotate := !plan.Generation.Equal(state.Generation) || plan.KeyID.IsUnknown() || state.KeyID.IsNull()

	if shouldRotate {
		newKeyID := ""
		if !plan.KeyID.IsNull() && !plan.KeyID.IsUnknown() && plan.KeyID.ValueString() != "" && plan.KeyID.ValueString() != oldKeyID {
			newKeyID = plan.KeyID.ValueString()
		} else if kID, _, ok := r.client.GetMintedKeyForName(plan.Name.ValueString(), true, ""); ok && kID != "" && kID != oldKeyID {
			newKeyID = kID
		}

		if newKeyID == "" {
			resp.Diagnostics.AddError(
				"Missing Ephemeral Mint",
				"No ephemeral board key was minted for generation rotation. Ephemeral minting via ephemeral.paperclip_board_key delivered to a consumer is required before updating the managed board key record.",
			)
			return
		}

		if oldKeyID != "" && oldKeyID != newKeyID {
			if err := r.client.RevokeBoardKey(ctx, oldKeyID); err != nil {
				resp.Diagnostics.AddError("Error revoking superseded board key", err.Error())
				return
			}
		}

		r.client.AcknowledgeKeyDelivery(newKeyID)
		r.client.RecordDeliveredGeneration(plan.Name.ValueString(), true, "", plan.Generation.ValueInt64(), newKeyID)

		if err := r.client.ReconcileUnconfirmedBoardKeys(ctx, plan.Name.ValueString(), newKeyID); err != nil {
			resp.Diagnostics.AddError("Error reconciling unconfirmed board keys", err.Error())
			return
		}

		plan.ID = types.StringValue(newKeyID)
		plan.KeyID = types.StringValue(newKeyID)
		plan.DeliveryState = types.StringValue("delivered")
		if plan.ExpiresAt.IsUnknown() {
			plan.ExpiresAt = types.StringNull()
		}

		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
		return
	}

	plan.ID = state.ID
	plan.KeyID = state.KeyID
	plan.DeliveryState = state.DeliveryState
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipBoardKeyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state PaperclipBoardKeyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	keyID := state.KeyID.ValueString()
	if keyID == "" {
		keyID = state.ID.ValueString()
	}
	if keyID != "" {
		if err := r.client.RevokeBoardKey(ctx, keyID); err != nil {
			resp.Diagnostics.AddError("Error revoking Paperclip board key", err.Error())
			return
		}
	}
}

func (r *PaperclipBoardKeyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func (r *PaperclipBoardKeyResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() || req.State.Raw.IsNull() {
		return
	}
	var plan PaperclipBoardKeyResourceModel
	var state PaperclipBoardKeyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !plan.Generation.Equal(state.Generation) || state.KeyID.IsNull() {
		plan.ID = types.StringUnknown()
		plan.KeyID = types.StringUnknown()
		plan.DeliveryState = types.StringUnknown()
		resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
	}
}

