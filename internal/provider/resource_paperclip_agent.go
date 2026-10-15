package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/Pavel-Guseynov/terraform-provider-paperclip/internal/client"
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
	_ resource.Resource                   = &PaperclipAgentResource{}
	_ resource.ResourceWithConfigure      = &PaperclipAgentResource{}
	_ resource.ResourceWithImportState    = &PaperclipAgentResource{}
	_ resource.ResourceWithModifyPlan     = &PaperclipAgentResource{}
	_ resource.ResourceWithUpgradeState   = &PaperclipAgentResource{}
	_ resource.ResourceWithValidateConfig = &PaperclipAgentResource{}
)

func NewPaperclipAgentResource() resource.Resource {
	return &PaperclipAgentResource{}
}

type PaperclipAgentResource struct {
	client *client.Client
}

type PaperclipAgentResourceModel struct {
	ID                   types.String               `tfsdk:"id"`
	CompanyID            types.String               `tfsdk:"company_id"`
	Name                 types.String               `tfsdk:"name"`
	Role                 types.String               `tfsdk:"role"`
	Title                types.String               `tfsdk:"title"`
	Icon                 types.String               `tfsdk:"icon"`
	ReportsTo            types.String               `tfsdk:"reports_to"`
	Capabilities         types.String               `tfsdk:"capabilities"`
	DefaultEnvironmentID types.String               `tfsdk:"default_environment_id"`
	AdapterType          types.String               `tfsdk:"adapter_type"`
	Status               types.String               `tfsdk:"status"`
	RuntimeConfig        types.String               `tfsdk:"runtime_config"`
	AdapterConfig        types.String               `tfsdk:"adapter_config"`
	Metadata             types.String               `tfsdk:"metadata"`
	CanAssignTasks       types.Bool                 `tfsdk:"can_assign_tasks"`
	CanCreateAgents      types.Bool                 `tfsdk:"can_create_agents"`
	CanCreateSkills      types.Bool                 `tfsdk:"can_create_skills"`
	TrustPreset          types.String               `tfsdk:"trust_preset"`
	AuthorizationPolicy  types.String               `tfsdk:"authorization_policy"`
	Paused               types.Bool                 `tfsdk:"paused"`
	Retirement           types.String               `tfsdk:"retirement"`
	Env                  map[string]EnvBindingModel `tfsdk:"env"`
}

func (r *PaperclipAgentResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_agent"
}

type stringOneOfValidator struct {
	allowed []string
}

func (v stringOneOfValidator) Description(ctx context.Context) string {
	return fmt.Sprintf("value must be one of: %s", strings.Join(v.allowed, ", "))
}

func (v stringOneOfValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v stringOneOfValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	val := req.ConfigValue.ValueString()
	for _, a := range v.allowed {
		if val == a {
			return
		}
	}
	resp.Diagnostics.AddAttributeError(req.Path, "Invalid Attribute Value", fmt.Sprintf("Value %q must be one of: %s", val, strings.Join(v.allowed, ", ")))
}

func (r *PaperclipAgentResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Version:     2,
		Description: "Manages a Paperclip agent (project or company scope).",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Unique identifier for the agent.",
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
			"name": schema.StringAttribute{
				Description: "Agent identifier name.",
				Required:    true,
			},
			"role": schema.StringAttribute{
				Description: "Agent role (e.g. engineer, committer, reviewer).",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"title": schema.StringAttribute{
				Description: "Optional human-readable title.",
				Optional:    true,
			},
			"icon": schema.StringAttribute{
				Description: "Optional icon identifier for the agent.",
				Optional:    true,
			},
			"reports_to": schema.StringAttribute{
				Description: "Optional manager agent ID within the same company.",
				Optional:    true,
			},
			"capabilities": schema.StringAttribute{
				Description: "Optional capabilities description or comma-separated list.",
				Optional:    true,
			},
			"default_environment_id": schema.StringAttribute{
				Description: "Optional execution environment ID for the agent.",
				Optional:    true,
			},
			"adapter_type": schema.StringAttribute{
				Description: "Execution adapter type (e.g. antigravity_local).",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"status": schema.StringAttribute{
				Description: "Agent lifecycle status reported by the board (board-owned runtime state).",
				Computed:    true,
			},
			"runtime_config": schema.StringAttribute{
				Description: "Optional JSON runtime object. Declared heartbeat settings are type-checked; native defaults and undeclared runtime fields are preserved. The native API cannot clear an existing aiConnection binding: retain it or declare a replacement.",
				Optional:    true,
				Validators: []validator.String{
					JSONAttributeValidator(),
				},
				PlanModifiers: []planmodifier.String{
					SemanticJSONPlanModifier(),
				},
			},
			"adapter_config": schema.StringAttribute{
				Description: "Optional JSON adapter object validated against published native field metadata at plan and before writes. UI omissions do not close the map; only an explicit additional-properties policy does. Instruction and paperclipSkillSync keys belong to separate resources and are rejected here. Requires board access to adapter schema metadata. Clear a nonempty declaration with jsonencode({}) before omitting it; null/omission cannot silently clear this optional/computed attribute. Remove adapter-agnostic keys with the original adapter_type before changing adapter_type.",
				Optional:    true,
				Computed:    true,
				Validators: []validator.String{
					JSONAttributeValidator(),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					SemanticJSONPlanModifier(),
				},
			},
			"metadata": schema.StringAttribute{
				Description: "Optional JSON metadata object for the agent.",
				Optional:    true,
				Validators: []validator.String{
					JSONAttributeValidator(),
				},
				PlanModifiers: []planmodifier.String{
					SemanticJSONPlanModifier(),
				},
			},
			"can_assign_tasks": schema.BoolAttribute{
				Description: "Whether the agent holds the tasks:assign permission required by protected project assignment policies. This permission controls assignment, not the creation of unassigned issues. The provider reconciles it after writes and reads it back from Paperclip.",
				Required:    true,
			},
			"can_create_agents": schema.BoolAttribute{
				Description: "Whether the agent may create other agents. Paperclip can also derive task-assignment permission from agent-creation permission. The provider explicitly manages this value and reads it back to detect drift.",
				Required:    true,
			},
			"can_create_skills": schema.BoolAttribute{
				Description: "Whether the agent may create skills. Defaults to true.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
			},
			"trust_preset": schema.StringAttribute{
				Description: "Optional trust policy preset ('standard' or 'low_trust_review').",
				Optional:    true,
				Validators: []validator.String{
					stringOneOfValidator{allowed: []string{"standard", "low_trust_review"}},
				},
			},
			"authorization_policy": schema.StringAttribute{
				Description: "Optional JSON authorization policy for the agent. Recognised top-level keys: agentVisibility, assignmentPolicy, protectedAgent, managedBy.",
				Optional:    true,
				Validators: []validator.String{
					JSONAttributeValidator(),
				},
				PlanModifiers: []planmodifier.String{
					SemanticJSONPlanModifier(),
				},
			},
			"paused": schema.BoolAttribute{
				Description: "Whether the agent is declaratively paused. Setting true pauses the agent; setting false resumes it.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
			},
			"retirement": schema.StringAttribute{
				Description: "Administrative retirement strategy on destroy ('delete' for hard deletion, 'terminate' for key revocation and termination). Defaults to 'delete'.",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("delete"),
				Validators: []validator.String{
					stringOneOfValidator{allowed: []string{"terminate", "delete"}},
				},
			},
			"env": EnvBindingMapAttribute("Optional environment bindings for the agent (merged into adapter_config.env)."),
		},
	}
}

func (r *PaperclipAgentResource) UpgradeState(ctx context.Context) map[int64]resource.StateUpgrader {
	return map[int64]resource.StateUpgrader{
		0: {
			PriorSchema: &schema.Schema{
				Attributes: map[string]schema.Attribute{
					"id":                schema.StringAttribute{Computed: true},
					"company_id":        schema.StringAttribute{Required: true},
					"name":              schema.StringAttribute{Required: true},
					"role":              schema.StringAttribute{Optional: true, Computed: true},
					"title":             schema.StringAttribute{Optional: true},
					"adapter_type":      schema.StringAttribute{Optional: true, Computed: true},
					"status":            schema.StringAttribute{Computed: true},
					"runtime_config":    schema.StringAttribute{Optional: true},
					"adapter_config":    schema.StringAttribute{Optional: true, Computed: true},
					"metadata":          schema.StringAttribute{Optional: true},
					"instructions":      schema.StringAttribute{Optional: true},
					"can_assign_tasks":  schema.BoolAttribute{Required: true},
					"can_create_agents": schema.BoolAttribute{Required: true},
				},
			},
			StateUpgrader: func(ctx context.Context, req resource.UpgradeStateRequest, resp *resource.UpgradeStateResponse) {
				var prior struct {
					ID              types.String `tfsdk:"id"`
					CompanyID       types.String `tfsdk:"company_id"`
					Name            types.String `tfsdk:"name"`
					Role            types.String `tfsdk:"role"`
					Title           types.String `tfsdk:"title"`
					AdapterType     types.String `tfsdk:"adapter_type"`
					Status          types.String `tfsdk:"status"`
					RuntimeConfig   types.String `tfsdk:"runtime_config"`
					AdapterConfig   types.String `tfsdk:"adapter_config"`
					Metadata        types.String `tfsdk:"metadata"`
					Instructions    types.String `tfsdk:"instructions"`
					CanAssignTasks  types.Bool   `tfsdk:"can_assign_tasks"`
					CanCreateAgents types.Bool   `tfsdk:"can_create_agents"`
				}
				resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
				if resp.Diagnostics.HasError() {
					return
				}
				upgraded := PaperclipAgentResourceModel{
					ID:                   prior.ID,
					CompanyID:            prior.CompanyID,
					Name:                 prior.Name,
					Role:                 prior.Role,
					Title:                prior.Title,
					Icon:                 types.StringNull(),
					ReportsTo:            types.StringNull(),
					Capabilities:         types.StringNull(),
					DefaultEnvironmentID: types.StringNull(),
					AdapterType:          prior.AdapterType,
					Status:               prior.Status,
					RuntimeConfig:        prior.RuntimeConfig,
					AdapterConfig:        prior.AdapterConfig,
					Metadata:             prior.Metadata,
					CanAssignTasks:       prior.CanAssignTasks,
					CanCreateAgents:      prior.CanCreateAgents,
					CanCreateSkills:      types.BoolValue(true),
					TrustPreset:          types.StringNull(),
					AuthorizationPolicy:  types.StringNull(),
					Paused:               types.BoolValue(prior.Status.ValueString() == "paused"),
					Retirement:           types.StringValue("delete"),
				}
				resp.Diagnostics.Append(resp.State.Set(ctx, &upgraded)...)
			},
		},
		1: {
			PriorSchema: &schema.Schema{
				Attributes: map[string]schema.Attribute{
					"id":                     schema.StringAttribute{Computed: true},
					"company_id":             schema.StringAttribute{Required: true},
					"name":                   schema.StringAttribute{Required: true},
					"role":                   schema.StringAttribute{Optional: true, Computed: true},
					"title":                  schema.StringAttribute{Optional: true},
					"icon":                   schema.StringAttribute{Optional: true},
					"reports_to":             schema.StringAttribute{Optional: true},
					"capabilities":           schema.StringAttribute{Optional: true},
					"default_environment_id": schema.StringAttribute{Optional: true},
					"adapter_type":           schema.StringAttribute{Optional: true, Computed: true},
					"status":                 schema.StringAttribute{Computed: true},
					"runtime_config":         schema.StringAttribute{Optional: true},
					"adapter_config":         schema.StringAttribute{Optional: true, Computed: true},
					"metadata":               schema.StringAttribute{Optional: true},
					"instructions":           schema.StringAttribute{Optional: true},
					"can_assign_tasks":       schema.BoolAttribute{Required: true},
					"can_create_agents":      schema.BoolAttribute{Required: true},
					"can_create_skills":      schema.BoolAttribute{Optional: true, Computed: true},
					"trust_preset":           schema.StringAttribute{Optional: true},
					"authorization_policy":   schema.StringAttribute{Optional: true},
					"paused":                 schema.BoolAttribute{Optional: true, Computed: true},
					"retirement":             schema.StringAttribute{Optional: true, Computed: true},
					"env":                    EnvBindingMapAttribute("Optional environment bindings for the agent (merged into adapter_config.env)."),
				},
			},
			StateUpgrader: func(ctx context.Context, req resource.UpgradeStateRequest, resp *resource.UpgradeStateResponse) {
				var prior struct {
					ID                   types.String               `tfsdk:"id"`
					CompanyID            types.String               `tfsdk:"company_id"`
					Name                 types.String               `tfsdk:"name"`
					Role                 types.String               `tfsdk:"role"`
					Title                types.String               `tfsdk:"title"`
					Icon                 types.String               `tfsdk:"icon"`
					ReportsTo            types.String               `tfsdk:"reports_to"`
					Capabilities         types.String               `tfsdk:"capabilities"`
					DefaultEnvironmentID types.String               `tfsdk:"default_environment_id"`
					AdapterType          types.String               `tfsdk:"adapter_type"`
					Status               types.String               `tfsdk:"status"`
					RuntimeConfig        types.String               `tfsdk:"runtime_config"`
					AdapterConfig        types.String               `tfsdk:"adapter_config"`
					Metadata             types.String               `tfsdk:"metadata"`
					Instructions         types.String               `tfsdk:"instructions"`
					CanAssignTasks       types.Bool                 `tfsdk:"can_assign_tasks"`
					CanCreateAgents      types.Bool                 `tfsdk:"can_create_agents"`
					CanCreateSkills      types.Bool                 `tfsdk:"can_create_skills"`
					TrustPreset          types.String               `tfsdk:"trust_preset"`
					AuthorizationPolicy  types.String               `tfsdk:"authorization_policy"`
					Paused               types.Bool                 `tfsdk:"paused"`
					Retirement           types.String               `tfsdk:"retirement"`
					Env                  map[string]EnvBindingModel `tfsdk:"env"`
				}
				resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
				if resp.Diagnostics.HasError() {
					return
				}
				upgraded := PaperclipAgentResourceModel{
					ID:                   prior.ID,
					CompanyID:            prior.CompanyID,
					Name:                 prior.Name,
					Role:                 prior.Role,
					Title:                prior.Title,
					Icon:                 prior.Icon,
					ReportsTo:            prior.ReportsTo,
					Capabilities:         prior.Capabilities,
					DefaultEnvironmentID: prior.DefaultEnvironmentID,
					AdapterType:          prior.AdapterType,
					Status:               prior.Status,
					RuntimeConfig:        prior.RuntimeConfig,
					AdapterConfig:        prior.AdapterConfig,
					Metadata:             prior.Metadata,
					CanAssignTasks:       prior.CanAssignTasks,
					CanCreateAgents:      prior.CanCreateAgents,
					CanCreateSkills:      prior.CanCreateSkills,
					TrustPreset:          prior.TrustPreset,
					AuthorizationPolicy:  prior.AuthorizationPolicy,
					Paused:               prior.Paused,
					Retirement:           prior.Retirement,
					Env:                  prior.Env,
				}
				resp.Diagnostics.Append(resp.State.Set(ctx, &upgraded)...)
			},
		},
	}
}

func validateAuthorizationPolicy(policyJSON string) (map[string]interface{}, error) {
	if policyJSON == "" || policyJSON == "null" {
		return nil, nil
	}
	var policy map[string]interface{}
	if err := json.Unmarshal([]byte(policyJSON), &policy); err != nil {
		return nil, err
	}
	knownKeys := map[string]bool{
		"agentVisibility":  true,
		"assignmentPolicy": true,
		"protectedAgent":   true,
		"managedBy":        true,
	}
	for k := range policy {
		if !knownKeys[k] {
			return nil, fmt.Errorf("authorization_policy contains unrecognised key %q; recognised keys are: agentVisibility, assignmentPolicy, protectedAgent, managedBy", k)
		}
	}
	return policy, nil
}

func (r *PaperclipAgentResource) validateReportsToCycle(ctx context.Context, agentID, companyID, reportsTo string) error {
	if reportsTo == "" {
		return nil
	}
	if agentID != "" && reportsTo == agentID {
		return fmt.Errorf("agent cannot report to itself")
	}
	seen := make(map[string]bool)
	if agentID != "" {
		seen[agentID] = true
	}
	cursor := reportsTo
	for cursor != "" {
		if seen[cursor] {
			if cursor == agentID {
				return fmt.Errorf("reporting relationship would create cycle: %s", cursor)
			}
			return fmt.Errorf("reporting relationship cycle detected: %s", cursor)
		}
		seen[cursor] = true
		mgr, err := r.client.GetAgent(ctx, cursor)
		if err != nil {
			if errors.Is(err, client.ErrNotFound) {
				return fmt.Errorf("manager agent %s not found", cursor)
			}
			return err
		}
		if mgr.CompanyID != "" && companyID != "" && mgr.CompanyID != companyID {
			return fmt.Errorf("manager must belong to same company (manager %s belongs to %s, agent belongs to %s)", cursor, mgr.CompanyID, companyID)
		}
		cursor = mgr.ReportsTo
	}
	return nil
}

func validateTrustPresetNotCleared(stateVal, planVal types.String) error {
	if !stateVal.IsNull() && !stateVal.IsUnknown() && stateVal.ValueString() != "" &&
		(planVal.IsNull() || planVal.ValueString() == "") {
		return fmt.Errorf("trust_preset cannot be cleared once configured: the Paperclip API does not support removing trust_preset. Set trust_preset to an explicit value (such as \"standard\") instead.")
	}
	return nil
}

func (r *PaperclipAgentResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config PaperclipAgentResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(validateAgentConfigValues(config)...)

	if config.Env != nil {
		resp.Diagnostics.Append(ValidateEnvBindingsMapPlan(path.Root("env"), config.Env, "agent")...)
	}

	if !config.AdapterConfig.IsNull() && !config.AdapterConfig.IsUnknown() && config.AdapterConfig.ValueString() != "" {
		var rawMap map[string]interface{}
		if err := json.Unmarshal([]byte(config.AdapterConfig.ValueString()), &rawMap); err == nil {
			if rawEnv, ok := rawMap["env"].(map[string]interface{}); ok {
				envJSON, _ := json.Marshal(rawEnv)
				resp.Diagnostics.Append(ValidateEnvBindingsJSONPlan(path.Root("adapter_config"), string(envJSON), "agent")...)
			}
		}
	}
}

func (r *PaperclipAgentResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}
	var plan PaperclipAgentResourceModel
	if diags := req.Plan.Get(ctx, &plan); diags.HasError() {
		resp.Diagnostics.Append(diags...)
		return
	}
	resp.Diagnostics.Append(validateAgentConfigValues(plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(r.validateAgentNativeConfig(ctx, plan)...)
	if !plan.CompanyID.IsUnknown() && !plan.CompanyID.IsNull() {
		resp.Diagnostics.Append(ValidateEnvBindingsApply(ctx, r.client, plan.CompanyID.ValueString(), plan.Env)...)
		if adapter, _ := agentConfigObject(plan.AdapterConfig); adapter != nil {
			if env, exists := adapter["env"]; exists {
				raw, _ := json.Marshal(env)
				resp.Diagnostics.Append(ValidateEnvBindingsJSONApply(ctx, r.client, plan.CompanyID.ValueString(), string(raw))...)
			}
		}
	}

	if req.State.Raw.IsNull() {
		if plan.CanCreateSkills.IsUnknown() || plan.CanCreateSkills.IsNull() {
			plan.CanCreateSkills = types.BoolValue(true)
		}
		if plan.Retirement.IsUnknown() || plan.Retirement.IsNull() {
			plan.Retirement = types.StringValue("delete")
		}
		if plan.Paused.IsUnknown() || plan.Paused.IsNull() {
			plan.Paused = types.BoolValue(false)
		}
		resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
	}

	if !req.State.Raw.IsNull() {
		var state PaperclipAgentResourceModel
		if !req.State.Get(ctx, &state).HasError() {
			// Attribute JSON modifiers can remove the only configured difference
			// after the framework has marked computed status unknown. Preserve the
			// refreshed status only when the entire remaining plan is unchanged.
			unchanged := plan
			unchanged.Status = state.Status
			if plan.Status.IsUnknown() && reflect.DeepEqual(unchanged, state) {
				resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("status"), state.Status)...)
			}
			if err := validateAgentRuntimeTransition(state.RuntimeConfig, plan.RuntimeConfig); err != nil {
				resp.Diagnostics.AddAttributeError(path.Root("runtime_config"), "Unsupported runtime transition", err.Error())
			}
			if err := validateAgentAdapterTransition(state, plan); err != nil {
				resp.Diagnostics.AddAttributeError(path.Root("adapter_config"), "Unsupported adapter transition", err.Error())
			}
			if !req.Config.Raw.IsNull() {
				var configured types.String
				if d := req.Config.GetAttribute(ctx, path.Root("adapter_config"), &configured); !d.HasError() && configured.IsNull() {
					if previous, _ := agentConfigObject(state.AdapterConfig); len(previous) > 0 {
						resp.Diagnostics.AddAttributeError(path.Root("adapter_config"), "Explicit adapter clearing required", "adapter_config is optional/computed and null retains the existing configuration; set adapter_config = jsonencode({}) to prune provider-owned keys explicitly before omitting it")
					}
				}
			}
			if err := validateTrustPresetNotCleared(state.TrustPreset, plan.TrustPreset); err != nil {
				resp.Diagnostics.AddAttributeError(
					path.Root("trust_preset"),
					"Invalid Attribute Modification",
					err.Error(),
				)
			}
		}
	}

	if !plan.AuthorizationPolicy.IsNull() && !plan.AuthorizationPolicy.IsUnknown() && plan.AuthorizationPolicy.ValueString() != "" {
		if _, err := validateAuthorizationPolicy(plan.AuthorizationPolicy.ValueString()); err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("authorization_policy"), "Invalid Authorization Policy", err.Error())
		}
	}
	if r.client != nil && !plan.ReportsTo.IsNull() && !plan.ReportsTo.IsUnknown() && plan.ReportsTo.ValueString() != "" &&
		!plan.CompanyID.IsNull() && !plan.CompanyID.IsUnknown() {
		agentID := ""
		if !req.State.Raw.IsNull() {
			var state PaperclipAgentResourceModel
			if !req.State.Get(ctx, &state).HasError() && !state.ID.IsNull() {
				agentID = state.ID.ValueString()
			}
		}
		if err := r.validateReportsToCycle(ctx, agentID, plan.CompanyID.ValueString(), plan.ReportsTo.ValueString()); err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("reports_to"), "Invalid reports_to", err.Error())
		}
	}
}

func authPolicyEqual(a, b map[string]interface{}) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

// reconcilePermissions brings the agent's declared permission flags to their
// declared values in one write and proves them from the board's own read.
func (r *PaperclipAgentResource) reconcilePermissions(ctx context.Context, agentID string, canAssignTasks, canCreateAgents, canCreateSkills bool, trustPreset string, authPolicy map[string]interface{}) error {
	agent, err := r.client.GetAgent(ctx, agentID)
	if err != nil {
		return err
	}
	if agent.Permissions == nil {
		// The permissions route replaces every flag, so the undeclared ones are
		// only ever written back from the board's own report.
		return fmt.Errorf("board reported no permissions for agent %s, so its declared flags cannot be changed without altering the undeclared ones", agentID)
	}
	if agent.Permissions.CanAssignTasks == canAssignTasks &&
		agent.Permissions.CanCreateAgents == canCreateAgents &&
		agent.Permissions.CanCreateSkills == canCreateSkills &&
		agent.Permissions.TrustPreset == trustPreset &&
		authPolicyEqual(agent.Permissions.AuthorizationPolicy, authPolicy) {
		return nil
	}
	permissions := *agent.Permissions
	permissions.CanAssignTasks = canAssignTasks
	permissions.CanCreateAgents = canCreateAgents
	permissions.CanCreateSkills = canCreateSkills
	permissions.TrustPreset = trustPreset
	if len(authPolicy) == 0 && len(agent.Permissions.AuthorizationPolicy) > 0 {
		permissions.AuthorizationPolicy = map[string]interface{}{}
	} else {
		permissions.AuthorizationPolicy = authPolicy
	}
	if err := r.client.UpdateAgentPermissions(ctx, agentID, permissions); err != nil {
		return err
	}
	agent, err = r.client.GetAgent(ctx, agentID)
	if err != nil {
		return err
	}
	if agent.Permissions == nil ||
		agent.Permissions.CanAssignTasks != canAssignTasks ||
		agent.Permissions.CanCreateAgents != canCreateAgents ||
		agent.Permissions.CanCreateSkills != canCreateSkills ||
		agent.Permissions.TrustPreset != trustPreset ||
		!authPolicyEqual(agent.Permissions.AuthorizationPolicy, authPolicy) {
		return fmt.Errorf("agent permissions did not converge: declared can_assign_tasks %t and can_create_agents %t, board reports %+v", canAssignTasks, canCreateAgents, agent.Permissions)
	}
	return nil
}

// permissionState reports the declared permission flags as the board reads
// them, or nulls when the board reports no permissions.
func permissionState(permissions *client.PaperclipAgentPermissions) (canAssignTasks, canCreateAgents, canCreateSkills types.Bool) {
	if permissions == nil {
		return types.BoolNull(), types.BoolNull(), types.BoolNull()
	}
	return types.BoolValue(permissions.CanAssignTasks), types.BoolValue(permissions.CanCreateAgents), types.BoolValue(permissions.CanCreateSkills)
}

func (r *PaperclipAgentResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *PaperclipAgentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan PaperclipAgentResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(validateAgentConfigValues(plan)...)
	nativePlan := plan
	if nativePlan.AdapterType.IsUnknown() || nativePlan.AdapterType.IsNull() {
		nativePlan.AdapterType = types.StringValue("antigravity_local")
	}
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(r.validateAgentNativeConfig(ctx, nativePlan)...)
	}
	if resp.Diagnostics.HasError() {
		return
	}

	var authPolicyMap map[string]interface{}
	if !plan.AuthorizationPolicy.IsNull() && !plan.AuthorizationPolicy.IsUnknown() && plan.AuthorizationPolicy.ValueString() != "" {
		var err error
		authPolicyMap, err = validateAuthorizationPolicy(plan.AuthorizationPolicy.ValueString())
		if err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("authorization_policy"), "Invalid Authorization Policy", err.Error())
			return
		}
	}

	reportsTo := ""
	if !plan.ReportsTo.IsNull() && !plan.ReportsTo.IsUnknown() && plan.ReportsTo.ValueString() != "" {
		reportsTo = plan.ReportsTo.ValueString()
		if err := r.validateReportsToCycle(ctx, "", plan.CompanyID.ValueString(), reportsTo); err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("reports_to"), "Invalid reports_to", err.Error())
			return
		}
	}

	runtimeMap, _ := agentConfigObject(plan.RuntimeConfig)
	adapterMap, _ := agentConfigObject(plan.AdapterConfig)
	var metaMap map[string]interface{}
	if plan.Env != nil {
		resp.Diagnostics.Append(ValidateEnvBindingsApply(ctx, r.client, plan.CompanyID.ValueString(), plan.Env)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}
	if !plan.AdapterConfig.IsNull() && !plan.AdapterConfig.IsUnknown() && plan.AdapterConfig.ValueString() != "" {
		if rawEnv, ok := adapterMap["env"].(map[string]interface{}); ok {
			envJSON, _ := json.Marshal(rawEnv)
			resp.Diagnostics.Append(ValidateEnvBindingsJSONApply(ctx, r.client, plan.CompanyID.ValueString(), string(envJSON))...)
			if resp.Diagnostics.HasError() {
				return
			}
		}
	}
	if plan.Env != nil && len(plan.Env) > 0 {
		if adapterMap == nil {
			adapterMap = make(map[string]interface{})
		}
		apiEnv := EnvBindingsMapToAPI(plan.Env)
		existingEnv, ok := adapterMap["env"].(map[string]interface{})
		if !ok || existingEnv == nil {
			adapterMap["env"] = apiEnv
		} else {
			for k, v := range apiEnv {
				existingEnv[k] = v
			}
			adapterMap["env"] = existingEnv
		}
	}
	if !plan.Metadata.IsNull() && !plan.Metadata.IsUnknown() && plan.Metadata.ValueString() != "" {
		if err := json.Unmarshal([]byte(plan.Metadata.ValueString()), &metaMap); err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("metadata"), "Invalid JSON", err.Error())
			return
		}
	}

	role := "engineer"
	if !plan.Role.IsNull() && !plan.Role.IsUnknown() && plan.Role.ValueString() != "" {
		role = plan.Role.ValueString()
	}
	adapterType := "antigravity_local"
	if !plan.AdapterType.IsNull() && !plan.AdapterType.IsUnknown() && plan.AdapterType.ValueString() != "" {
		adapterType = plan.AdapterType.ValueString()
	}

	canCreateSkills := true
	if !plan.CanCreateSkills.IsNull() && !plan.CanCreateSkills.IsUnknown() {
		canCreateSkills = plan.CanCreateSkills.ValueBool()
	}
	trustPreset := ""
	if !plan.TrustPreset.IsNull() && !plan.TrustPreset.IsUnknown() {
		trustPreset = plan.TrustPreset.ValueString()
	}

	initialPerms := &client.PaperclipAgentPermissions{
		CanAssignTasks:      plan.CanAssignTasks.ValueBool(),
		CanCreateAgents:     plan.CanCreateAgents.ValueBool(),
		CanCreateSkills:     canCreateSkills,
		TrustPreset:         trustPreset,
		AuthorizationPolicy: authPolicyMap,
	}

	agent := client.PaperclipAgent{
		CompanyID:            plan.CompanyID.ValueString(),
		Name:                 plan.Name.ValueString(),
		Role:                 role,
		Title:                plan.Title.ValueString(),
		Icon:                 plan.Icon.ValueString(),
		ReportsTo:            reportsTo,
		Capabilities:         plan.Capabilities.ValueString(),
		DefaultEnvironmentID: plan.DefaultEnvironmentID.ValueString(),
		AdapterType:          adapterType,
		RuntimeConfig:        runtimeMap,
		AdapterConfig:        adapterMap,
		Metadata:             metaMap,
		Permissions:          initialPerms,
	}

	created, err := r.client.CreateAgent(ctx, plan.CompanyID.ValueString(), agent)
	if err != nil {
		resp.Diagnostics.AddError("Error creating Paperclip agent", err.Error())
		return
	}

	if created.Status == "pending_approval" {
		approvalID := ""
		if created.HireApproval != nil {
			approvalID = created.HireApproval.ID
		}
		resp.Diagnostics.AddWarning(
			"Agent Pending Approval",
			fmt.Sprintf("Agent %s (%s) was submitted for board hiring approval (approval id: %s). The agent cannot be activated or modified until approved by the board.", created.ID, created.Name, approvalID),
		)
	} else {
		reconcileCanCreateSkills := canCreateSkills
		if plan.CanCreateSkills.IsNull() {
			if created.Permissions != nil {
				reconcileCanCreateSkills = created.Permissions.CanCreateSkills
			} else if current, err := r.client.GetAgent(ctx, created.ID); err == nil && current.Permissions != nil {
				reconcileCanCreateSkills = current.Permissions.CanCreateSkills
			}
		}

		if err := r.reconcilePermissions(ctx, created.ID, plan.CanAssignTasks.ValueBool(), plan.CanCreateAgents.ValueBool(), reconcileCanCreateSkills, trustPreset, authPolicyMap); err != nil {
			resp.Diagnostics.AddError("Error reconciling Paperclip agent permissions", err.Error())
			return
		}

		if plan.Paused.ValueBool() && created.Status != "paused" {
			if err := r.client.PauseAgent(ctx, created.ID); err != nil {
				resp.Diagnostics.AddError("Error pausing Paperclip agent", err.Error())
				return
			}
		}
	}

	latest, err := r.client.GetAgent(ctx, created.ID)
	if err == nil {
		created = latest
	}

	plan.ID = types.StringValue(created.ID)
	plan.CompanyID = types.StringValue(created.CompanyID)
	plan.Name = types.StringValue(created.Name)
	if created.Role != "" {
		plan.Role = types.StringValue(created.Role)
	} else if plan.Role.IsUnknown() {
		plan.Role = types.StringNull()
	}
	plan.Title = stringOrNull(created.Title)
	plan.Icon = stringOrNull(created.Icon)
	plan.ReportsTo = stringOrNull(created.ReportsTo)
	plan.Capabilities = stringOrNull(created.Capabilities)
	plan.DefaultEnvironmentID = stringOrNull(created.DefaultEnvironmentID)
	if created.AdapterType != "" {
		plan.AdapterType = types.StringValue(created.AdapterType)
	} else if plan.AdapterType.IsUnknown() {
		plan.AdapterType = types.StringNull()
	}
	plan.Status = stringOrNull(created.Status)
	plan.RuntimeConfig = refreshAgentRuntimeConfig(plan.RuntimeConfig, created.RuntimeConfig)
	plan.AdapterConfig = refreshAgentAdapterState(plan.AdapterConfig, created.AdapterConfig, plan.Env)
	plan.Metadata = refreshManagedJSONState(plan.Metadata, created.Metadata)
	plan.CanAssignTasks, plan.CanCreateAgents, plan.CanCreateSkills = permissionState(created.Permissions)
	if created.Permissions != nil {
		if created.Permissions.TrustPreset != "" {
			plan.TrustPreset = types.StringValue(created.Permissions.TrustPreset)
		} else {
			plan.TrustPreset = types.StringNull()
		}
		plan.AuthorizationPolicy = refreshManagedJSONState(plan.AuthorizationPolicy, created.Permissions.AuthorizationPolicy)
	} else {
		plan.TrustPreset = types.StringNull()
		plan.AuthorizationPolicy = types.StringNull()
	}
	plan.Paused = types.BoolValue(created.Status == "paused")
	if plan.Retirement.IsNull() || plan.Retirement.IsUnknown() {
		plan.Retirement = types.StringValue("delete")
	}

	if plan.Env != nil {
		plan.Env = refreshAgentEnvBindings(plan.Env, created.AdapterConfig)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipAgentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state PaperclipAgentResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	importing := state.Name.IsNull()

	agent, err := r.client.GetAgent(ctx, state.ID.ValueString())
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading Paperclip agent", err.Error())
		return
	}

	if agent.Status == "terminated" {
		resp.State.RemoveResource(ctx)
		return
	}

	state.ID = types.StringValue(agent.ID)
	if agent.CompanyID != "" {
		state.CompanyID = types.StringValue(agent.CompanyID)
	}
	state.Name = types.StringValue(agent.Name)
	if agent.Role != "" {
		state.Role = types.StringValue(agent.Role)
	} else {
		state.Role = types.StringNull()
	}
	state.Title = stringOrNull(agent.Title)
	state.Icon = stringOrNull(agent.Icon)
	state.ReportsTo = stringOrNull(agent.ReportsTo)
	state.Capabilities = stringOrNull(agent.Capabilities)
	state.DefaultEnvironmentID = stringOrNull(agent.DefaultEnvironmentID)
	if agent.AdapterType != "" {
		state.AdapterType = types.StringValue(agent.AdapterType)
	} else {
		state.AdapterType = types.StringNull()
	}
	state.Status = stringOrNull(agent.Status)

	if importing {
		state.RuntimeConfig = types.StringUnknown()
		if remoteEnv, exists := agent.AdapterConfig["env"]; exists {
			state.AdapterConfig = jsonStringOrNull(map[string]interface{}{"env": remoteEnv})
		}
	}
	state.RuntimeConfig = refreshAgentRuntimeConfig(state.RuntimeConfig, agent.RuntimeConfig)
	state.AdapterConfig = refreshAgentAdapterState(state.AdapterConfig, agent.AdapterConfig, state.Env)
	state.Metadata = refreshManagedJSONState(state.Metadata, agent.Metadata)
	state.CanAssignTasks, state.CanCreateAgents, state.CanCreateSkills = permissionState(agent.Permissions)
	if agent.Permissions != nil {
		if agent.Permissions.TrustPreset != "" {
			state.TrustPreset = types.StringValue(agent.Permissions.TrustPreset)
		} else {
			state.TrustPreset = types.StringNull()
		}
		state.AuthorizationPolicy = refreshManagedJSONState(state.AuthorizationPolicy, agent.Permissions.AuthorizationPolicy)
	} else {
		state.TrustPreset = types.StringNull()
		state.AuthorizationPolicy = types.StringNull()
	}

	state.Paused = types.BoolValue(agent.Status == "paused")
	if state.Retirement.IsNull() || state.Retirement.IsUnknown() {
		state.Retirement = types.StringValue("delete")
	}

	if state.Env != nil {
		state.Env = refreshAgentEnvBindings(state.Env, agent.AdapterConfig)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

var paperclipManagedAdapterConfigKeys = []string{
	"instructionsFilePath",
	"instructionsRootPath",
	"instructionsEntryFile",
	"instructionsBundleMode",
	"agentsMdPath",
	"paperclipSkillSync",
}

func isSecretRefBinding(val interface{}) bool {
	m, ok := val.(map[string]interface{})
	if !ok {
		return false
	}
	t, ok := m["type"].(string)
	return ok && (t == "secret_ref" || t == "user_secret_ref")
}

func filterPaperclipManagedAdapterConfig(remote map[string]interface{}, managed map[string]interface{}) map[string]interface{} {
	if remote == nil {
		return nil
	}
	filtered := make(map[string]interface{}, len(remote))
	for k, v := range remote {
		filtered[k] = v
	}

	for _, key := range paperclipManagedAdapterConfigKeys {
		if managed == nil {
			delete(filtered, key)
		} else if _, declared := managed[key]; !declared {
			delete(filtered, key)
		}
	}

	if remoteEnv, ok := filtered["env"].(map[string]interface{}); ok {
		var managedEnv map[string]interface{}
		managedHasEnv := false
		if managed != nil {
			managedEnv, managedHasEnv = managed["env"].(map[string]interface{})
		}

		filteredEnv := make(map[string]interface{}, len(remoteEnv))
		for k, v := range remoteEnv {
			if managedHasEnv {
				if _, declared := managedEnv[k]; declared {
					filteredEnv[k] = v
					continue
				}
			}
			if isSecretRefBinding(v) {
				continue
			}
			filteredEnv[k] = v
		}

		if len(filteredEnv) > 0 {
			filtered["env"] = filteredEnv
		} else if !managedHasEnv {
			delete(filtered, "env")
		} else {
			filtered["env"] = filteredEnv
		}
	}

	return filtered
}

func preservePaperclipManagedAdapterConfig(existing, target map[string]interface{}) {
	if existing == nil || target == nil {
		return
	}
	for _, key := range paperclipManagedAdapterConfigKeys {
		if val, ok := existing[key]; ok {
			if _, declared := target[key]; !declared {
				target[key] = val
			}
		}
	}
	if existingEnv, ok := existing["env"].(map[string]interface{}); ok {
		targetEnv, ok := target["env"].(map[string]interface{})
		if !ok {
			targetEnv = make(map[string]interface{})
		}
		for k, v := range existingEnv {
			if isSecretRefBinding(v) {
				if _, declared := targetEnv[k]; !declared {
					targetEnv[k] = v
				}
			}
		}
		if len(targetEnv) > 0 {
			target["env"] = targetEnv
		}
	}
}

func refreshAgentAdapterConfig(current types.String, remote map[string]interface{}) types.String {
	if remote == nil {
		return types.StringNull()
	}
	if current.IsNull() || current.IsUnknown() {
		filtered := filterPaperclipManagedAdapterConfig(remote, nil)
		if len(filtered) == 0 && current.IsNull() {
			return types.StringNull()
		}
		return jsonStringOrNull(filtered)
	}

	var managed map[string]interface{}
	if err := json.Unmarshal([]byte(current.ValueString()), &managed); err != nil {
		return jsonStringOrNull(remote)
	}

	filtered := filterPaperclipManagedAdapterConfig(remote, managed)
	if jsonContains(filtered, managed) && jsonContains(managed, filtered) {
		return current
	}
	return jsonStringOrNull(filtered)
}

func refreshManagedJSONState(current types.String, remote map[string]interface{}) types.String {
	if len(remote) == 0 && (current.IsNull() || current.IsUnknown() || current.ValueString() == "null") {
		return types.StringNull()
	}
	if current.IsNull() || current.IsUnknown() {
		return jsonStringOrNull(remote)
	}
	var managed map[string]interface{}
	if err := json.Unmarshal([]byte(current.ValueString()), &managed); err == nil && jsonContains(remote, managed) {
		return current
	}
	if len(remote) == 0 {
		return types.StringNull()
	}
	return jsonStringOrNull(remote)
}

func jsonContains(remote, managed interface{}) bool {
	managedMap, managedIsMap := managed.(map[string]interface{})
	if !managedIsMap {
		return reflect.DeepEqual(remote, managed)
	}
	remoteMap, remoteIsMap := remote.(map[string]interface{})
	if !remoteIsMap {
		return false
	}
	for key, managedValue := range managedMap {
		remoteValue, ok := remoteMap[key]
		if !ok || !jsonContains(remoteValue, managedValue) {
			return false
		}
	}
	return true
}

func refreshAgentEnvBindings(currentEnv map[string]EnvBindingModel, remoteAdapterConfig map[string]interface{}) map[string]EnvBindingModel {
	if currentEnv == nil {
		return nil
	}
	if remoteAdapterConfig == nil {
		return make(map[string]EnvBindingModel)
	}
	remoteEnv, ok := remoteAdapterConfig["env"].(map[string]interface{})
	if !ok || remoteEnv == nil {
		return make(map[string]EnvBindingModel)
	}

	result := make(map[string]EnvBindingModel, len(currentEnv))
	for k, currentBinding := range currentEnv {
		remoteVal, exists := remoteEnv[k]
		if !exists {
			continue
		}
		switch v := remoteVal.(type) {
		case string:
			currentBinding.Type = types.StringValue("plain")
			currentBinding.Value = types.StringValue(v)
			result[k] = currentBinding
		case map[string]interface{}:
			bType, _ := v["type"].(string)
			switch bType {
			case "plain":
				currentBinding.Type = types.StringValue("plain")
				if valStr, ok := v["value"].(string); ok {
					currentBinding.Value = types.StringValue(valStr)
				}
				result[k] = currentBinding
			case "secret_ref":
				currentBinding.Type = types.StringValue("secret_ref")
				if secID, ok := v["secretId"].(string); ok {
					currentBinding.SecretID = types.StringValue(secID)
				}
				if ver, ok := v["version"].(string); ok {
					currentBinding.Version = types.StringValue(ver)
				} else if currentBinding.Version.IsNull() || currentBinding.Version.IsUnknown() {
					currentBinding.Version = types.StringNull()
				}
				if proj, ok := v["projectionClass"].(string); ok {
					currentBinding.ProjectionClass = types.StringValue(proj)
				}
				if alk, ok := v["projectionAllowlistKey"].(string); ok {
					currentBinding.ProjectionAllowlistKey = types.StringValue(alk)
				}
				result[k] = currentBinding
			case "user_secret_ref":
				currentBinding.Type = types.StringValue("user_secret_ref")
				if keyVal, ok := v["key"].(string); ok {
					currentBinding.Key = types.StringValue(keyVal)
				}
				if ver, ok := v["version"].(string); ok {
					currentBinding.Version = types.StringValue(ver)
				} else if currentBinding.Version.IsNull() || currentBinding.Version.IsUnknown() {
					currentBinding.Version = types.StringNull()
				}
				if reqVal, ok := v["required"].(bool); ok {
					currentBinding.Required = types.BoolValue(reqVal)
				}
				if amoVal, ok := v["allowMissingOverride"].(bool); ok {
					currentBinding.AllowMissingOverride = types.BoolValue(amoVal)
				}
				result[k] = currentBinding
			default:
				result[k] = currentBinding
			}
		default:
			result[k] = currentBinding
		}
	}
	return result
}

func (r *PaperclipAgentResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan PaperclipAgentResourceModel
	var state PaperclipAgentResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(validateAgentConfigValues(plan)...)
	if err := validateAgentRuntimeTransition(state.RuntimeConfig, plan.RuntimeConfig); err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("runtime_config"), "Unsupported runtime transition", err.Error())
	}
	if err := validateAgentAdapterTransition(state, plan); err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("adapter_config"), "Unsupported adapter transition", err.Error())
	}
	nativePlan := plan
	if nativePlan.AdapterType.IsUnknown() || nativePlan.AdapterType.IsNull() {
		nativePlan.AdapterType = state.AdapterType
	}
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(r.validateAgentNativeConfig(ctx, nativePlan)...)
	}
	if resp.Diagnostics.HasError() {
		return
	}

	runtimeMap, _ := agentConfigObject(plan.RuntimeConfig)
	var adapterMap, metaMap map[string]interface{}
	var replaceAdapterConfig bool
	var currentAgent *client.PaperclipAgent
	getCurrentAgent := func() (*client.PaperclipAgent, error) {
		if currentAgent != nil {
			return currentAgent, nil
		}
		a, err := r.client.GetAgent(ctx, state.ID.ValueString())
		if err != nil {
			return nil, err
		}
		currentAgent = a
		return currentAgent, nil
	}
	if !plan.AdapterConfig.IsNull() && !plan.AdapterConfig.IsUnknown() && plan.AdapterConfig.ValueString() != "" {
		adapterMap, _ = agentConfigObject(plan.AdapterConfig)
		if adapterMap != nil {
			current, err := getCurrentAgent()
			if err != nil {
				resp.Diagnostics.AddError("Error reading Paperclip agent before update", err.Error())
				return
			}
			preservePaperclipManagedAdapterConfig(current.AdapterConfig, adapterMap)
			replaceAdapterConfig = true
		}
	} else if plan.AdapterConfig.IsNull() && !state.AdapterConfig.IsNull() {
		adapterMap = make(map[string]interface{})
		current, err := getCurrentAgent()
		if err != nil {
			resp.Diagnostics.AddError("Error reading Paperclip agent before update", err.Error())
			return
		}
		preservePaperclipManagedAdapterConfig(current.AdapterConfig, adapterMap)
		replaceAdapterConfig = true
	}
	if plan.Env != nil {
		resp.Diagnostics.Append(ValidateEnvBindingsApply(ctx, r.client, plan.CompanyID.ValueString(), plan.Env)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}
	if !plan.AdapterConfig.IsNull() && !plan.AdapterConfig.IsUnknown() && plan.AdapterConfig.ValueString() != "" {
		if rawEnv, ok := adapterMap["env"].(map[string]interface{}); ok {
			envJSON, _ := json.Marshal(rawEnv)
			resp.Diagnostics.Append(ValidateEnvBindingsJSONApply(ctx, r.client, plan.CompanyID.ValueString(), string(envJSON))...)
			if resp.Diagnostics.HasError() {
				return
			}
		}
	}
	if plan.Env != nil || state.Env != nil {
		if adapterMap == nil {
			adapterMap = make(map[string]interface{})
		}
		current, err := getCurrentAgent()
		if err != nil {
			resp.Diagnostics.AddError("Error reading Paperclip agent before update", err.Error())
			return
		}
		if current.AdapterConfig != nil {
			preservePaperclipManagedAdapterConfig(current.AdapterConfig, adapterMap)
		}
		if len(plan.Env) > 0 {
			apiEnv := EnvBindingsMapToAPI(plan.Env)
			existingEnv, ok := adapterMap["env"].(map[string]interface{})
			if !ok || existingEnv == nil {
				adapterMap["env"] = apiEnv
			} else {
				for k, v := range apiEnv {
					existingEnv[k] = v
				}
				for k := range state.Env {
					if _, stillPresent := plan.Env[k]; !stillPresent {
						delete(existingEnv, k)
					}
				}
				adapterMap["env"] = existingEnv
			}
		} else {
			if existingEnv, ok := adapterMap["env"].(map[string]interface{}); ok {
				for k := range state.Env {
					delete(existingEnv, k)
				}
				if len(existingEnv) == 0 {
					delete(adapterMap, "env")
				} else {
					adapterMap["env"] = existingEnv
				}
			}
		}
		replaceAdapterConfig = true
	}
	// Delete previously owned bindings after preservation so a removed secret
	// reference is not accidentally restored as an unowned server binding.
	if existingEnv, ok := adapterMap["env"].(map[string]interface{}); ok {
		for key := range state.Env {
			if _, exists := plan.Env[key]; !exists {
				delete(existingEnv, key)
			}
		}
		priorAdapter, _ := agentConfigObject(state.AdapterConfig)
		desiredAdapter, _ := agentConfigObject(plan.AdapterConfig)
		priorEnv, _ := priorAdapter["env"].(map[string]interface{})
		desiredEnv, _ := desiredAdapter["env"].(map[string]interface{})
		for key := range priorEnv {
			if _, exists := desiredEnv[key]; !exists {
				if _, topLevel := plan.Env[key]; !topLevel {
					delete(existingEnv, key)
				}
			}
		}
	}
	if !plan.Metadata.IsNull() && !plan.Metadata.IsUnknown() && plan.Metadata.ValueString() != "" {
		if err := json.Unmarshal([]byte(plan.Metadata.ValueString()), &metaMap); err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("metadata"), "Invalid JSON", err.Error())
			return
		}
	}

	var authPolicyMap map[string]interface{}
	if !plan.AuthorizationPolicy.IsNull() && !plan.AuthorizationPolicy.IsUnknown() && plan.AuthorizationPolicy.ValueString() != "" {
		var err error
		authPolicyMap, err = validateAuthorizationPolicy(plan.AuthorizationPolicy.ValueString())
		if err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("authorization_policy"), "Invalid Authorization Policy", err.Error())
			return
		}
	}

	reportsTo := ""
	if !plan.ReportsTo.IsNull() && !plan.ReportsTo.IsUnknown() && plan.ReportsTo.ValueString() != "" {
		reportsTo = plan.ReportsTo.ValueString()
	}
	if err := r.validateReportsToCycle(ctx, state.ID.ValueString(), state.CompanyID.ValueString(), reportsTo); err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("reports_to"), "Invalid reports_to", err.Error())
		return
	}
	// The native runtime PATCH replaces the object. Preserve runtime fields
	// outside the prior/current declaration, including an unowned AI binding.
	current, err := getCurrentAgent()
	if err != nil {
		resp.Diagnostics.AddError("Error reading Paperclip agent before update", err.Error())
		return
	}
	if runtimeMap == nil {
		runtimeMap = make(map[string]interface{})
	}
	priorRuntime, _ := agentConfigObject(state.RuntimeConfig)
	for key, value := range current.RuntimeConfig {
		if _, owned := priorRuntime[key]; !owned {
			if _, declared := runtimeMap[key]; !declared {
				runtimeMap[key] = value
			}
		}
	}
	if desiredHeartbeat, ok := runtimeMap["heartbeat"].(map[string]interface{}); ok {
		priorHeartbeat, _ := priorRuntime["heartbeat"].(map[string]interface{})
		remoteHeartbeat, _ := current.RuntimeConfig["heartbeat"].(map[string]interface{})
		for key, value := range remoteHeartbeat {
			if _, owned := priorHeartbeat[key]; !owned {
				if _, declared := desiredHeartbeat[key]; !declared {
					desiredHeartbeat[key] = value
				}
			}
		}
	}

	role := plan.Role.ValueString()
	if plan.Role.IsUnknown() {
		role = state.Role.ValueString()
	}
	adapterType := plan.AdapterType.ValueString()
	if plan.AdapterType.IsUnknown() {
		adapterType = state.AdapterType.ValueString()
	}

	title := ""
	if !plan.Title.IsNull() && !plan.Title.IsUnknown() {
		title = plan.Title.ValueString()
	}
	icon := ""
	if !plan.Icon.IsNull() && !plan.Icon.IsUnknown() {
		icon = plan.Icon.ValueString()
	}
	capabilities := ""
	if !plan.Capabilities.IsNull() && !plan.Capabilities.IsUnknown() {
		capabilities = plan.Capabilities.ValueString()
	}
	defaultEnvID := ""
	if !plan.DefaultEnvironmentID.IsNull() && !plan.DefaultEnvironmentID.IsUnknown() {
		defaultEnvID = plan.DefaultEnvironmentID.ValueString()
	}

	agent := client.PaperclipAgent{
		CompanyID:            state.CompanyID.ValueString(),
		Name:                 plan.Name.ValueString(),
		Role:                 role,
		Title:                title,
		Icon:                 icon,
		ReportsTo:            reportsTo,
		Capabilities:         capabilities,
		DefaultEnvironmentID: defaultEnvID,
		AdapterType:          adapterType,
		RuntimeConfig:        runtimeMap,
		AdapterConfig:        adapterMap,
		ReplaceAdapterConfig: replaceAdapterConfig,
		Metadata:             metaMap,
	}

	updated, err := r.client.UpdateAgent(ctx, state.ID.ValueString(), agent)
	if err != nil {
		resp.Diagnostics.AddError("Error updating Paperclip agent", err.Error())
		return
	}

	canCreateSkills := true
	if !plan.CanCreateSkills.IsNull() && !plan.CanCreateSkills.IsUnknown() {
		canCreateSkills = plan.CanCreateSkills.ValueBool()
	} else if !state.CanCreateSkills.IsNull() && !state.CanCreateSkills.IsUnknown() {
		canCreateSkills = state.CanCreateSkills.ValueBool()
	} else if updated.Permissions != nil {
		canCreateSkills = updated.Permissions.CanCreateSkills
	} else if current.Permissions != nil {
		canCreateSkills = current.Permissions.CanCreateSkills
	}
	trustPreset := ""
	if !plan.TrustPreset.IsNull() && !plan.TrustPreset.IsUnknown() {
		trustPreset = plan.TrustPreset.ValueString()
	}

	if err := r.reconcilePermissions(ctx, state.ID.ValueString(), plan.CanAssignTasks.ValueBool(), plan.CanCreateAgents.ValueBool(), canCreateSkills, trustPreset, authPolicyMap); err != nil {
		resp.Diagnostics.AddError("Error reconciling Paperclip agent permissions", err.Error())
		return
	}

	desiredPaused := plan.Paused.ValueBool()
	currentlyPaused := updated.Status == "paused"
	if desiredPaused != currentlyPaused {
		if desiredPaused {
			if err := r.client.PauseAgent(ctx, state.ID.ValueString()); err != nil {
				resp.Diagnostics.AddError("Error pausing Paperclip agent", err.Error())
				return
			}
		} else {
			if err := r.client.ResumeAgent(ctx, state.ID.ValueString()); err != nil {
				resp.Diagnostics.AddError("Error resuming Paperclip agent", err.Error())
				return
			}
		}
	}

	finalAgent, err := r.client.GetAgent(ctx, state.ID.ValueString())
	if err == nil {
		updated = finalAgent
	}

	plan.ID = types.StringValue(updated.ID)
	plan.CompanyID = types.StringValue(updated.CompanyID)
	plan.Name = types.StringValue(updated.Name)
	if plan.Role.IsUnknown() {
		plan.Role = stringOrNull(updated.Role)
	}
	plan.Title = stringOrNull(updated.Title)
	plan.Icon = stringOrNull(updated.Icon)
	plan.ReportsTo = stringOrNull(updated.ReportsTo)
	plan.Capabilities = stringOrNull(updated.Capabilities)
	plan.DefaultEnvironmentID = stringOrNull(updated.DefaultEnvironmentID)
	if plan.AdapterType.IsUnknown() {
		plan.AdapterType = stringOrNull(updated.AdapterType)
	}
	plan.Status = stringOrNull(updated.Status)
	plan.RuntimeConfig = refreshAgentRuntimeConfig(plan.RuntimeConfig, updated.RuntimeConfig)
	plan.AdapterConfig = refreshAgentAdapterState(plan.AdapterConfig, updated.AdapterConfig, plan.Env)
	plan.Metadata = refreshManagedJSONState(plan.Metadata, updated.Metadata)
	plan.CanAssignTasks, plan.CanCreateAgents, plan.CanCreateSkills = permissionState(updated.Permissions)
	if updated.Permissions != nil {
		if updated.Permissions.TrustPreset != "" {
			plan.TrustPreset = types.StringValue(updated.Permissions.TrustPreset)
		} else {
			plan.TrustPreset = types.StringNull()
		}
		plan.AuthorizationPolicy = refreshManagedJSONState(plan.AuthorizationPolicy, updated.Permissions.AuthorizationPolicy)
	} else {
		plan.TrustPreset = types.StringNull()
		plan.AuthorizationPolicy = types.StringNull()
	}

	plan.Paused = types.BoolValue(updated.Status == "paused")
	if plan.Retirement.IsNull() || plan.Retirement.IsUnknown() {
		if !state.Retirement.IsNull() && !state.Retirement.IsUnknown() {
			plan.Retirement = state.Retirement
		} else {
			plan.Retirement = types.StringValue("delete")
		}
	}

	if plan.Env != nil {
		plan.Env = refreshAgentEnvBindings(plan.Env, updated.AdapterConfig)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipAgentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state PaperclipAgentResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if state.Retirement.ValueString() == "terminate" {
		if err := r.client.TerminateAgent(ctx, state.ID.ValueString()); err != nil {
			if !errors.Is(err, client.ErrNotFound) {
				resp.Diagnostics.AddError("Error terminating Paperclip agent", err.Error())
				return
			}
		}
		return
	}

	if err := r.client.DeleteAgent(ctx, state.ID.ValueString()); err != nil {
		if !errors.Is(err, client.ErrNotFound) {
			resp.Diagnostics.AddError("Error deleting Paperclip agent", err.Error())
			return
		}
	}
}

func (r *PaperclipAgentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id := req.ID
	if strings.Contains(id, "/") {
		parts := strings.SplitN(id, "/", 2)
		if parts[0] != "" && parts[1] != "" {
			resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("company_id"), parts[0])...)
			resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
			return
		}
	} else if id != "" {
		resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
		return
	}
	resp.Diagnostics.AddError(
		"Invalid Agent Import ID",
		fmt.Sprintf("Expected <company_id>/<agent_id> or <agent_id>, got %q", id),
	)
}
