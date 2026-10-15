package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Pavel-Guseynov/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type defaultEffectValidator struct{}

func (v defaultEffectValidator) Description(ctx context.Context) string {
	return "value must be 'allow' or 'deny'"
}

func (v defaultEffectValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (defaultEffectValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	val := req.ConfigValue.ValueString()
	if val != "allow" && val != "deny" {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid Default Effect", "default_effect must be 'allow' or 'deny'")
	}
}

var (
	_ resource.Resource                = &PaperclipSkillPolicyResource{}
	_ resource.ResourceWithConfigure   = &PaperclipSkillPolicyResource{}
	_ resource.ResourceWithImportState = &PaperclipSkillPolicyResource{}
)

func NewPaperclipSkillPolicyResource() resource.Resource {
	return &PaperclipSkillPolicyResource{}
}

type PaperclipSkillPolicyResource struct {
	client *client.Client
}

type PaperclipSkillPolicyResourceModel struct {
	ID            types.String `tfsdk:"id"`
	CompanyID     types.String `tfsdk:"company_id"`
	DefaultEffect types.String `tfsdk:"default_effect"`
	Rules         types.String `tfsdk:"rules"`
	Revision      types.Int64  `tfsdk:"revision"`
}

func (r *PaperclipSkillPolicyResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_skill_policy"
}

func (r *PaperclipSkillPolicyResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages the company skill policy with revision check and conflict detection.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Unique identifier for the skill policy (matches company_id).",
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
			"default_effect": schema.StringAttribute{
				Description: "Default effect for the policy: 'allow' or 'deny'.",
				Required:    true,
				Validators: []validator.String{
					defaultEffectValidator{},
				},
			},
			"rules": schema.StringAttribute{
				Description: "JSON array of policy rules.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					SemanticJSONPlanModifier(),
				},
			},
			"revision": schema.Int64Attribute{
				Description: "Revision sequence counter for optimistic concurrency control.",
				Computed:    true,
			},
		},
	}
}

func (r *PaperclipSkillPolicyResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func parsePolicyRules(rulesVal types.String) ([]map[string]interface{}, error) {
	if rulesVal.IsNull() || rulesVal.IsUnknown() || rulesVal.ValueString() == "" {
		return []map[string]interface{}{}, nil
	}
	var rules []map[string]interface{}
	if err := json.Unmarshal([]byte(rulesVal.ValueString()), &rules); err != nil {
		return nil, fmt.Errorf("rules must be a valid JSON array of objects: %w", err)
	}
	if rules == nil {
		rules = []map[string]interface{}{}
	}
	return rules, nil
}

func (r *PaperclipSkillPolicyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan PaperclipSkillPolicyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	companyID := plan.CompanyID.ValueString()
	current, err := r.client.GetSkillPolicy(ctx, companyID)
	if err != nil {
		resp.Diagnostics.AddError("Error reading Paperclip company skill policy", err.Error())
		return
	}

	rules, err := parsePolicyRules(plan.Rules)
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("rules"), "Invalid Rules JSON", err.Error())
		return
	}

	expectedRevision := current.Revision
	updated, err := r.client.ReplaceSkillPolicy(ctx, companyID, expectedRevision, plan.DefaultEffect.ValueString(), rules)
	if err != nil {
		resp.Diagnostics.AddError("Error applying Paperclip skill policy", err.Error())
		return
	}

	plan.ID = types.StringValue(companyID)
	plan.DefaultEffect = types.StringValue(updated.DefaultEffect)
	plan.Revision = types.Int64Value(updated.Revision)
	plan.Rules = refreshSemanticJSON(plan.Rules, updated.Rules)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipSkillPolicyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state PaperclipSkillPolicyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	companyID := state.CompanyID.ValueString()
	policy, err := r.client.GetSkillPolicy(ctx, companyID)
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading Paperclip skill policy", err.Error())
		return
	}

	state.ID = types.StringValue(companyID)
	state.DefaultEffect = types.StringValue(policy.DefaultEffect)
	state.Revision = types.Int64Value(policy.Revision)
	state.Rules = refreshSemanticJSON(state.Rules, policy.Rules)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *PaperclipSkillPolicyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan PaperclipSkillPolicyResourceModel
	var state PaperclipSkillPolicyResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	companyID := state.CompanyID.ValueString()
	expectedRevision := state.Revision.ValueInt64()

	rules, err := parsePolicyRules(plan.Rules)
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("rules"), "Invalid Rules JSON", err.Error())
		return
	}

	updated, err := r.client.ReplaceSkillPolicy(ctx, companyID, expectedRevision, plan.DefaultEffect.ValueString(), rules)
	if err != nil {
		resp.Diagnostics.AddError("Error updating Paperclip skill policy", err.Error())
		return
	}

	plan.ID = types.StringValue(companyID)
	plan.DefaultEffect = types.StringValue(updated.DefaultEffect)
	plan.Revision = types.Int64Value(updated.Revision)
	plan.Rules = refreshSemanticJSON(plan.Rules, updated.Rules)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipSkillPolicyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state PaperclipSkillPolicyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err := r.client.ResetSkillPolicy(ctx, state.CompanyID.ValueString())
	if err != nil && !errors.Is(err, client.ErrNotFound) {
		resp.Diagnostics.AddError("Error resetting Paperclip skill policy", err.Error())
		return
	}
}

func (r *PaperclipSkillPolicyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	companyID := req.ID
	if companyID == "" {
		resp.Diagnostics.AddError("Invalid Skill Policy Import ID", "Expected <company_id>")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), companyID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("company_id"), companyID)...)
}
