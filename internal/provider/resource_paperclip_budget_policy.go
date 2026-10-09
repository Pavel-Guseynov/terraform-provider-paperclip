package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"git.guseynov.org/guseynov/paperclip/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &PaperclipBudgetPolicyResource{}
	_ resource.ResourceWithConfigure   = &PaperclipBudgetPolicyResource{}
	_ resource.ResourceWithImportState = &PaperclipBudgetPolicyResource{}
	_ resource.ResourceWithModifyPlan  = &PaperclipBudgetPolicyResource{}
)

func NewPaperclipBudgetPolicyResource() resource.Resource {
	return &PaperclipBudgetPolicyResource{}
}

type PaperclipBudgetPolicyResource struct {
	client *client.Client
}

type PaperclipBudgetPolicyResourceModel struct {
	ID              types.String `tfsdk:"id"`
	CompanyID       types.String `tfsdk:"company_id"`
	ScopeType       types.String `tfsdk:"scope_type"`
	ScopeID         types.String `tfsdk:"scope_id"`
	Metric          types.String `tfsdk:"metric"`
	WindowKind      types.String `tfsdk:"window_kind"`
	Window          types.String `tfsdk:"window"`
	Amount          types.Int64  `tfsdk:"amount"`
	WarnPercent     types.Int64  `tfsdk:"warn_percent"`
	HardStop        types.Bool   `tfsdk:"hard_stop"`
	HardStopEnabled types.Bool   `tfsdk:"hard_stop_enabled"`
	Notify          types.Bool   `tfsdk:"notify"`
	NotifyEnabled   types.Bool   `tfsdk:"notify_enabled"`
	Active          types.Bool   `tfsdk:"active"`
	IsActive        types.Bool   `tfsdk:"is_active"`
}

func (r *PaperclipBudgetPolicyResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_budget_policy"
}

func (r *PaperclipBudgetPolicyResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a Paperclip budget policy across company, agent, or project scopes.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Unique identifier for the budget policy.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"company_id": schema.StringAttribute{
				Description: "The Paperclip company ID.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"scope_type": schema.StringAttribute{
				Description: "Scope type for the policy: company, agent, or project.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					stringOneOfValidator{allowed: []string{"company", "agent", "project"}},
				},
			},
			"scope_id": schema.StringAttribute{
				Description: "Target identifier of the company, agent, or project.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"metric": schema.StringAttribute{
				Description: "Budget tracking metric (defaults to billed_cents).",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("billed_cents"),
				Validators: []validator.String{
					stringOneOfValidator{allowed: []string{"billed_cents"}},
				},
			},
			"window_kind": schema.StringAttribute{
				Description: "Window kind for the policy: calendar_month_utc or lifetime (defaults to calendar_month_utc).",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("calendar_month_utc"),
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					stringOneOfValidator{allowed: []string{"calendar_month_utc", "lifetime"}},
				},
			},
			"window": schema.StringAttribute{
				Description: "Optional alias for window_kind.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					stringOneOfValidator{allowed: []string{"calendar_month_utc", "lifetime"}},
				},
			},
			"amount": schema.Int64Attribute{
				Description: "Budget limit amount in cents. Must be non-negative.",
				Required:    true,
			},
			"warn_percent": schema.Int64Attribute{
				Description: "Warning notification threshold percentage (1 to 99, defaults to 80).",
				Optional:    true,
				Computed:    true,
			},
			"hard_stop": schema.BoolAttribute{
				Description: "Whether hard stop enforcement is enabled (defaults to true).",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
			},
			"hard_stop_enabled": schema.BoolAttribute{
				Description: "Optional alias for hard_stop.",
				Optional:    true,
				Computed:    true,
			},
			"notify": schema.BoolAttribute{
				Description: "Whether notification is enabled (defaults to true).",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
			},
			"notify_enabled": schema.BoolAttribute{
				Description: "Optional alias for notify.",
				Optional:    true,
				Computed:    true,
			},
			"active": schema.BoolAttribute{
				Description: "Whether the budget policy is active (defaults to true).",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
			},
			"is_active": schema.BoolAttribute{
				Description: "Optional alias for active.",
				Optional:    true,
				Computed:    true,
			},
		},
	}
}

func (r *PaperclipBudgetPolicyResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func syncBudgetPolicyAliases(m *PaperclipBudgetPolicyResourceModel) {
	// warn_percent default: 80
	if m.WarnPercent.IsNull() || m.WarnPercent.IsUnknown() {
		m.WarnPercent = types.Int64Value(80)
	}

	// window_kind <-> window
	if !m.Window.IsNull() && !m.Window.IsUnknown() && m.Window.ValueString() != "" {
		m.WindowKind = m.Window
	} else if !m.WindowKind.IsNull() && !m.WindowKind.IsUnknown() && m.WindowKind.ValueString() != "" {
		m.Window = m.WindowKind
	} else {
		m.WindowKind = types.StringValue("calendar_month_utc")
		m.Window = types.StringValue("calendar_month_utc")
	}

	// hard_stop <-> hard_stop_enabled
	if !m.HardStopEnabled.IsNull() && !m.HardStopEnabled.IsUnknown() {
		m.HardStop = m.HardStopEnabled
	} else if !m.HardStop.IsNull() && !m.HardStop.IsUnknown() {
		m.HardStopEnabled = m.HardStop
	} else {
		m.HardStop = types.BoolValue(true)
		m.HardStopEnabled = types.BoolValue(true)
	}

	// notify <-> notify_enabled
	if !m.NotifyEnabled.IsNull() && !m.NotifyEnabled.IsUnknown() {
		m.Notify = m.NotifyEnabled
	} else if !m.Notify.IsNull() && !m.Notify.IsUnknown() {
		m.NotifyEnabled = m.Notify
	} else {
		m.Notify = types.BoolValue(true)
		m.NotifyEnabled = types.BoolValue(true)
	}

	// active <-> is_active
	if !m.IsActive.IsNull() && !m.IsActive.IsUnknown() {
		m.Active = m.IsActive
	} else if !m.Active.IsNull() && !m.Active.IsUnknown() {
		m.IsActive = m.Active
	} else {
		m.Active = types.BoolValue(true)
		m.IsActive = types.BoolValue(true)
	}
}

func (r *PaperclipBudgetPolicyResource) validateBudgetScope(ctx context.Context, companyID, scopeType, scopeID string) error {
	if r.client == nil {
		return nil
	}
	switch scopeType {
	case "company":
		if scopeID != companyID {
			return fmt.Errorf("scope_id %s must match company_id %s for company-scoped budget policy", scopeID, companyID)
		}
		if _, err := r.client.GetCompany(ctx, companyID); err != nil {
			return fmt.Errorf("failed to validate company scope %s: %w", companyID, err)
		}
	case "agent":
		agent, err := r.client.GetAgent(ctx, scopeID)
		if err != nil {
			return fmt.Errorf("failed to validate agent scope %s: %w", scopeID, err)
		}
		if agent.CompanyID != companyID {
			return fmt.Errorf("agent scope %s belongs to company %s, expected %s", scopeID, agent.CompanyID, companyID)
		}
	case "project":
		project, err := r.client.GetProject(ctx, scopeID)
		if err != nil {
			return fmt.Errorf("failed to validate project scope %s: %w", scopeID, err)
		}
		if project.CompanyID != companyID {
			return fmt.Errorf("project scope %s belongs to company %s, expected %s", scopeID, project.CompanyID, companyID)
		}
	}
	return nil
}

func (r *PaperclipBudgetPolicyResource) checkDuplicatePolicyIdentity(ctx context.Context, companyID, scopeType, scopeID, windowKind, currentPolicyID string) error {
	if r.client == nil {
		return nil
	}
	policies, err := r.client.ListBudgetPolicies(ctx, companyID)
	if err != nil {
		return nil
	}
	for _, p := range policies {
		if p.ScopeType == scopeType && p.ScopeID == scopeID && p.WindowKind == windowKind && p.IsActive {
			if currentPolicyID == "" || p.PolicyID != currentPolicyID {
				return fmt.Errorf("a budget policy for company %s, scope %s (%s), and window %s already exists (policy ID %s); a second owner of the same policy identity is rejected", companyID, scopeType, scopeID, windowKind, p.PolicyID)
			}
		}
	}
	return nil
}

func (r *PaperclipBudgetPolicyResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}

	var plan PaperclipBudgetPolicyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	syncBudgetPolicyAliases(&plan)

	// Validate amount and warn_percent
	if !plan.Amount.IsNull() && !plan.Amount.IsUnknown() {
		if plan.Amount.ValueInt64() < 0 {
			resp.Diagnostics.AddAttributeError(path.Root("amount"), "Invalid Amount", "amount cannot be negative")
			return
		}
	}
	if !plan.WarnPercent.IsNull() && !plan.WarnPercent.IsUnknown() {
		wp := plan.WarnPercent.ValueInt64()
		if wp < 1 || wp > 99 {
			resp.Diagnostics.AddAttributeError(path.Root("warn_percent"), "Invalid Warn Percent", "warn_percent must be between 1 and 99")
			return
		}
	}

	// Check duplicate identity if fields are known
	if !plan.CompanyID.IsUnknown() && !plan.ScopeType.IsUnknown() && !plan.ScopeID.IsUnknown() && !plan.WindowKind.IsUnknown() {
		cid := plan.CompanyID.ValueString()
		st := plan.ScopeType.ValueString()
		sid := plan.ScopeID.ValueString()
		wk := plan.WindowKind.ValueString()
		curID := ""
		if !plan.ID.IsNull() && !plan.ID.IsUnknown() {
			curID = plan.ID.ValueString()
		}
		if err := r.checkDuplicatePolicyIdentity(ctx, cid, st, sid, wk, curID); err != nil {
			resp.Diagnostics.AddError("Duplicate Budget Policy Identity", err.Error())
			return
		}
	}

	resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
}

func (r *PaperclipBudgetPolicyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan PaperclipBudgetPolicyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	syncBudgetPolicyAliases(&plan)

	cid := plan.CompanyID.ValueString()
	st := plan.ScopeType.ValueString()
	sid := plan.ScopeID.ValueString()
	wk := plan.WindowKind.ValueString()
	metric := plan.Metric.ValueString()
	if metric == "" {
		metric = "billed_cents"
	}

	// Validate scope read-only before first mutation
	if err := r.validateBudgetScope(ctx, cid, st, sid); err != nil {
		resp.Diagnostics.AddError("Invalid Budget Policy Scope", err.Error())
		return
	}

	// Validate no conflicting active policy before first mutation
	if err := r.checkDuplicatePolicyIdentity(ctx, cid, st, sid, wk, ""); err != nil {
		resp.Diagnostics.AddError("Duplicate Budget Policy Identity", err.Error())
		return
	}

	// Every budget write sends full desired state
	policyReq := client.PaperclipBudgetPolicy{
		ScopeType:       st,
		ScopeID:         sid,
		Metric:          metric,
		WindowKind:      wk,
		Amount:          plan.Amount.ValueInt64(),
		WarnPercent:     plan.WarnPercent.ValueInt64(),
		HardStopEnabled: plan.HardStop.ValueBool(),
		NotifyEnabled:   plan.Notify.ValueBool(),
		IsActive:        plan.Active.ValueBool(),
	}

	summary, err := r.client.UpsertBudgetPolicy(ctx, cid, policyReq)
	if err != nil {
		resp.Diagnostics.AddError("Error creating Paperclip budget policy", err.Error())
		return
	}

	plan.ID = types.StringValue(summary.PolicyID)
	plan.CompanyID = types.StringValue(summary.CompanyID)
	plan.ScopeType = types.StringValue(summary.ScopeType)
	plan.ScopeID = types.StringValue(summary.ScopeID)
	plan.Metric = types.StringValue(summary.Metric)
	plan.WindowKind = types.StringValue(summary.WindowKind)
	plan.Window = types.StringValue(summary.WindowKind)
	plan.Amount = types.Int64Value(summary.Amount)
	plan.WarnPercent = types.Int64Value(summary.WarnPercent)
	plan.HardStop = types.BoolValue(summary.HardStopEnabled)
	plan.HardStopEnabled = types.BoolValue(summary.HardStopEnabled)
	plan.Notify = types.BoolValue(summary.NotifyEnabled)
	plan.NotifyEnabled = types.BoolValue(summary.NotifyEnabled)
	plan.Active = types.BoolValue(summary.IsActive)
	plan.IsActive = types.BoolValue(summary.IsActive)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipBudgetPolicyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state PaperclipBudgetPolicyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	cid := state.CompanyID.ValueString()
	pid := state.ID.ValueString()

	summary, err := r.client.GetBudgetPolicy(ctx, cid, pid)
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading Paperclip budget policy", err.Error())
		return
	}

	// Deactivation works as retirement: inactive policies are removed from state
	if !summary.IsActive {
		resp.State.RemoveResource(ctx)
		return
	}

	state.ID = types.StringValue(summary.PolicyID)
	state.CompanyID = types.StringValue(summary.CompanyID)
	state.ScopeType = types.StringValue(summary.ScopeType)
	state.ScopeID = types.StringValue(summary.ScopeID)
	state.Metric = types.StringValue(summary.Metric)
	state.WindowKind = types.StringValue(summary.WindowKind)
	state.Window = types.StringValue(summary.WindowKind)
	state.Amount = types.Int64Value(summary.Amount)
	state.WarnPercent = types.Int64Value(summary.WarnPercent)
	state.HardStop = types.BoolValue(summary.HardStopEnabled)
	state.HardStopEnabled = types.BoolValue(summary.HardStopEnabled)
	state.Notify = types.BoolValue(summary.NotifyEnabled)
	state.NotifyEnabled = types.BoolValue(summary.NotifyEnabled)
	state.Active = types.BoolValue(summary.IsActive)
	state.IsActive = types.BoolValue(summary.IsActive)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *PaperclipBudgetPolicyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan PaperclipBudgetPolicyResourceModel
	var state PaperclipBudgetPolicyResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	syncBudgetPolicyAliases(&plan)

	cid := plan.CompanyID.ValueString()
	st := plan.ScopeType.ValueString()
	sid := plan.ScopeID.ValueString()
	wk := plan.WindowKind.ValueString()
	metric := plan.Metric.ValueString()
	if metric == "" {
		metric = "billed_cents"
	}

	// Validate scope
	if err := r.validateBudgetScope(ctx, cid, st, sid); err != nil {
		resp.Diagnostics.AddError("Invalid Budget Policy Scope", err.Error())
		return
	}

	// Every budget write sends full desired state so omitted fields are not reset
	policyReq := client.PaperclipBudgetPolicy{
		ScopeType:       st,
		ScopeID:         sid,
		Metric:          metric,
		WindowKind:      wk,
		Amount:          plan.Amount.ValueInt64(),
		WarnPercent:     plan.WarnPercent.ValueInt64(),
		HardStopEnabled: plan.HardStop.ValueBool(),
		NotifyEnabled:   plan.Notify.ValueBool(),
		IsActive:        plan.Active.ValueBool(),
	}

	summary, err := r.client.UpsertBudgetPolicy(ctx, cid, policyReq)
	if err != nil {
		resp.Diagnostics.AddError("Error updating Paperclip budget policy", err.Error())
		return
	}

	plan.ID = types.StringValue(summary.PolicyID)
	plan.CompanyID = types.StringValue(summary.CompanyID)
	plan.ScopeType = types.StringValue(summary.ScopeType)
	plan.ScopeID = types.StringValue(summary.ScopeID)
	plan.Metric = types.StringValue(summary.Metric)
	plan.WindowKind = types.StringValue(summary.WindowKind)
	plan.Window = types.StringValue(summary.WindowKind)
	plan.Amount = types.Int64Value(summary.Amount)
	plan.WarnPercent = types.Int64Value(summary.WarnPercent)
	plan.HardStop = types.BoolValue(summary.HardStopEnabled)
	plan.HardStopEnabled = types.BoolValue(summary.HardStopEnabled)
	plan.Notify = types.BoolValue(summary.NotifyEnabled)
	plan.NotifyEnabled = types.BoolValue(summary.NotifyEnabled)
	plan.Active = types.BoolValue(summary.IsActive)
	plan.IsActive = types.BoolValue(summary.IsActive)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipBudgetPolicyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state PaperclipBudgetPolicyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// A budget policy retires by deactivation, because policies cannot be deleted
	_, err := r.client.UpsertBudgetPolicy(ctx, state.CompanyID.ValueString(), client.PaperclipBudgetPolicy{
		ScopeType:       state.ScopeType.ValueString(),
		ScopeID:         state.ScopeID.ValueString(),
		Metric:          state.Metric.ValueString(),
		WindowKind:      state.WindowKind.ValueString(),
		Amount:          0,
		WarnPercent:     state.WarnPercent.ValueInt64(),
		HardStopEnabled: state.HardStop.ValueBool(),
		NotifyEnabled:   state.Notify.ValueBool(),
		IsActive:        false,
	})
	if err != nil {
		resp.Diagnostics.AddError("Error deactivating Paperclip budget policy", err.Error())
	}
}

func (r *PaperclipBudgetPolicyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id := req.ID
	if strings.Contains(id, "/") {
		parts := strings.Split(id, "/")
		if len(parts) == 4 {
			// company_id/scope_type/scope_id/window_kind
			cid, st, sid, wk := parts[0], parts[1], parts[2], parts[3]
			if r.client != nil {
				pol, err := r.client.FindBudgetPolicy(ctx, cid, st, sid, wk)
				if err != nil {
					resp.Diagnostics.AddError("Budget Policy Not Found", err.Error())
					return
				}
				resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("company_id"), cid)...)
				resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), pol.PolicyID)...)
				return
			}
		} else if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
			resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("company_id"), parts[0])...)
			resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
			return
		}
	} else if id != "" {
		if r.client != nil {
			pol, err := r.client.FindBudgetPolicyByID(ctx, id)
			if err == nil && pol != nil {
				resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("company_id"), pol.CompanyID)...)
				resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), pol.PolicyID)...)
				return
			}
		}
		resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
		return
	}
	resp.Diagnostics.AddError(
		"Invalid Budget Policy Import ID",
		fmt.Sprintf("Expected <company_id>/<policy_id> or <company_id>/<scope_type>/<scope_id>/<window_kind> or <policy_id>, got %q", id),
	)
}
