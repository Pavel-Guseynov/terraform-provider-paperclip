package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/Pavel-Guseynov/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type PaperclipInstanceSettingsResource struct{ client *client.Client }
type instanceSettingsModel struct {
	ID                   types.String `tfsdk:"id"`
	DefaultEnvironmentID types.String `tfsdk:"default_environment_id"`
	General              types.String `tfsdk:"general"`
	Experimental         types.String `tfsdk:"experimental"`
}

// Writable booleans in the pinned native PATCH schema, excluding deprecated keys
// and server-owned activation bookkeeping. Unknown keys would be silently stripped.
const writableExperimental = `enableEnvironments enableNativeRunner enableManagedSandboxOnly enableIsolatedWorkspaces enableIsolatedWorkspacesByDefault enableStreamlinedLeftNavigation enableStreamlinedUi enableChatConnectors enableMcpAggregators enablePipelines enableCases enableAgentChat enableConferenceRoomChat enableClassicTaskInterface enableIssuePlanDecompositions enableExperimentalFileViewer enableExternalObjects enableSmokeLab enableBuiltInAgents enableBetaSkills enableSummaries enableStatusCards enableDecisions enableGoalsSidebarLink enableServerInfoDebugView enablePaperclipDeveloperMode enableSimplifiedEnglishInteractions enableFirstTaskPlanProposal autoRestartDevServerWhenIdle enableWorkspaceBranchReconcileForward enableWorkspaceDirtyQuarantineRepair enableOwnerInstanceAdmin enableSandboxDuplexBridge enableWorktreeRunExecution`

func NewPaperclipInstanceSettingsResource() resource.Resource {
	return &PaperclipInstanceSettingsResource{}
}
func (r *PaperclipInstanceSettingsResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_instance_settings"
}
func (r *PaperclipInstanceSettingsResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{Description: "Owns only declared instance settings. Deployment-owned keys fail before mutation. Requires instance admin access. Destroy relinquishes ownership and retains settings. Import instance to adopt writable values; configure the imported keys. Native section PATCHes are independent, so partial failures retain observed state for reconciliation.", Attributes: map[string]schema.Attribute{
		"id":                     schema.StringAttribute{Computed: true, Description: "Singleton identity: instance.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
		"default_environment_id": schema.StringAttribute{Optional: true, Description: "Active environment UUID. Empty string clears the default to native null; omission relinquishes management."},
		"general":                schema.StringAttribute{Optional: true, Description: "Partial JSON object using native keys: censorUsernameInLogs, keyboardShortcuts, feedbackDataSharingPreference (allowed/not_allowed/prompt), backupRetention (complete dailyDays 3/7/14, weeklyWeeks 1/2/4, monthlyMonths 1/3/6), executionMode (any/kubernetes). Omission or key removal relinquishes ownership without resetting values.", PlanModifiers: []planmodifier.String{SemanticJSONPlanModifier()}},
		"experimental":           schema.StringAttribute{Optional: true, Description: "Partial JSON object of writable boolean experimental flags in the supported native schema. Unknown, deprecated and server bookkeeping keys are rejected. Omission or key removal relinquishes ownership. Supported keys: " + writableExperimental, PlanModifiers: []planmodifier.String{SemanticJSONPlanModifier()}},
	}}
}
func (r *PaperclipInstanceSettingsResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	var ok bool
	r.client, ok = req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Resource Configure Type", fmt.Sprintf("Expected *client.Client, got: %T", req.ProviderData))
	}
}

func settingsObject(value types.String, section string) (map[string]interface{}, error) {
	result := map[string]interface{}{}
	if value.IsNull() || value.IsUnknown() {
		return result, nil
	}
	if err := json.Unmarshal([]byte(value.ValueString()), &result); err != nil || result == nil {
		return nil, fmt.Errorf("%s must be a JSON object", section)
	}
	for key, value := range result {
		valid := false
		if section == "experimental" {
			_, boolean := value.(bool)
			valid = boolean && strings.Contains(" "+writableExperimental+" ", " "+key+" ")
		} else {
			switch key {
			case "censorUsernameInLogs", "keyboardShortcuts":
				_, valid = value.(bool)
			case "feedbackDataSharingPreference":
				valid = value == "allowed" || value == "not_allowed" || value == "prompt"
			case "executionMode":
				valid = value == "any" || value == "kubernetes"
			case "backupRetention":
				retention, ok := value.(map[string]interface{})
				valid = ok && len(retention) == 3 && settingsPreset(retention["dailyDays"], 3, 7, 14) && settingsPreset(retention["weeklyWeeks"], 1, 2, 4) && settingsPreset(retention["monthlyMonths"], 1, 3, 6)
			}
		}
		if !valid {
			return nil, fmt.Errorf("unsupported %s key or value %q; use a writable native setting with its documented type", section, key)
		}
	}
	return result, nil
}
func settingsPreset(value interface{}, allowed ...float64) bool {
	for _, option := range allowed {
		if value == option {
			return true
		}
	}
	return false
}
func settingsBodies(m instanceSettingsModel) ([]map[string]interface{}, error) {
	general, err := settingsObject(m.General, "general")
	if err != nil {
		return nil, err
	}
	experimental, err := settingsObject(m.Experimental, "experimental")
	if err != nil {
		return nil, err
	}
	root := map[string]interface{}{}
	if !m.DefaultEnvironmentID.IsNull() && !m.DefaultEnvironmentID.IsUnknown() {
		id := m.DefaultEnvironmentID.ValueString()
		if id != "" && !environmentSecretID.MatchString(id) {
			return nil, fmt.Errorf("default_environment_id must be an environment UUID or an empty string to clear it")
		}
		root["defaultEnvironmentId"] = nil
		if id != "" {
			root["defaultEnvironmentId"] = id
		}
	}
	return []map[string]interface{}{root, general, experimental}, nil
}
func (r *PaperclipInstanceSettingsResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m instanceSettingsModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if _, err := settingsBodies(m); err != nil {
		resp.Diagnostics.AddError("Invalid instance settings", err.Error())
	}
}

func settingOwner(h *client.HealthResponse, remote *client.InstanceSettings, section, key string) string {
	for _, hidden := range h.HiddenSettings {
		if hidden == "instance."+section+"."+key || (section == "experimental" && hidden == "instance.experimental") {
			return "hosting operator (hidden)"
		}
	}
	if section == "general" && key == "executionMode" && h.Cloud != nil && h.Cloud.Managed {
		return h.Cloud.ManagedBy + " (platform-managed)"
	}
	if section == "experimental" {
		if keys, ok := remote.Experimental["managedKeys"].(map[string]interface{}); ok {
			if metadata, exists := keys[key]; exists {
				if fields, ok := metadata.(map[string]interface{}); ok {
					if owner, ok := fields["managedBy"].(string); ok && owner != "" {
						return owner
					}
				}
				return "deployment (managedKeys)"
			}
		}
	}
	return ""
}
func (r *PaperclipInstanceSettingsResource) preflight(ctx context.Context, m instanceSettingsModel) (*client.InstanceSettings, []map[string]interface{}, error) {
	bodies, err := settingsBodies(m)
	if err != nil {
		return nil, nil, err
	}
	remote, err := r.client.GetInstanceSettings(ctx)
	if err != nil {
		return nil, nil, err
	}
	health, err := r.client.Health(ctx)
	if err != nil {
		return nil, nil, err
	}
	for index, section := range []string{"", "general", "experimental"} {
		values := remote.General
		if section == "experimental" {
			values = remote.Experimental
		}
		for key := range bodies[index] {
			if owner := settingOwner(health, remote, section, key); owner != "" {
				return nil, nil, fmt.Errorf("%s.%s is managedBy %s; remove this key from configuration", section, key, owner)
			}
			if _, present := values[key]; index > 0 && !present && !(section == "general" && key == "executionMode") {
				return nil, nil, fmt.Errorf("target API does not expose %s.%s; remove this unsupported setting from configuration", section, key)
			}
		}
	}
	if id, ok := bodies[0]["defaultEnvironmentId"].(string); ok {
		environment, err := r.client.GetExecutionEnvironment(ctx, id)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid default environment: %w", err)
		}
		if environment.Status != "active" || (environment.Driver == "sandbox" && environment.Config["provider"] == "fake") {
			return nil, nil, fmt.Errorf("default environment must be active and executable; archived or fake sandbox environments cannot be selected")
		}
	}
	return remote, bodies, nil
}
func (r *PaperclipInstanceSettingsResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if r.client == nil || req.Plan.Raw.IsNull() {
		return
	}
	var m instanceSettingsModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if _, _, err := r.preflight(ctx, m); err != nil {
		resp.Diagnostics.AddError("Instance settings plan refused", err.Error())
	}
}

func refreshSettings(m *instanceSettingsModel, remote *client.InstanceSettings, adopt bool, h *client.HealthResponse) {
	m.ID = types.StringValue("instance")
	if adopt || !m.DefaultEnvironmentID.IsNull() {
		id := ""
		if remote.DefaultEnvironmentID != nil {
			id = *remote.DefaultEnvironmentID
		}
		m.DefaultEnvironmentID = types.StringValue(id)
	}
	for index, section := range []string{"general", "experimental"} {
		current := &m.General
		values := remote.General
		if index == 1 {
			current = &m.Experimental
			values = remote.Experimental
		}
		if current.IsNull() && !adopt {
			continue
		}
		selected, _ := settingsObject(*current, section)
		if adopt {
			selected = map[string]interface{}{}
			for key, value := range values {
				raw, _ := json.Marshal(map[string]interface{}{key: value})
				if _, err := settingsObject(types.StringValue(string(raw)), section); err == nil && settingOwner(h, remote, section, key) == "" {
					selected[key] = value
				}
			}
		} else {
			for key := range selected {
				selected[key] = instanceSettingValue(values, section, key)
			}
		}
		*current = refreshSemanticJSON(*current, selected)
		if adopt {
			raw, _ := json.Marshal(selected)
			*current = types.StringValue(string(raw))
		}
	}
}

func instanceSettingValue(values map[string]interface{}, section, key string) interface{} {
	if section == "general" && key == "executionMode" && values[key] == nil {
		return "any"
	}
	return values[key]
}
func (r *PaperclipInstanceSettingsResource) apply(ctx context.Context, plan tfsdk.Plan, state *tfsdk.State) diag.Diagnostics {
	var m instanceSettingsModel
	d := plan.Get(ctx, &m)
	if d.HasError() {
		return d
	}
	if m.General.IsUnknown() || m.Experimental.IsUnknown() || m.DefaultEnvironmentID.IsUnknown() {
		d.AddError("Unknown instance settings at apply", "Resolve all declared settings before applying.")
		return d
	}
	remote, bodies, err := r.preflight(ctx, m)
	if err != nil {
		d.AddError("Instance settings apply refused", err.Error())
		return d
	}
	// Persist the existing singleton before independent mutations, so a later
	// section failure retains its identity and observed values for the next plan.
	observed := m
	refreshSettings(&observed, remote, false, nil)
	d.Append(state.Set(ctx, &observed)...)
	if d.HasError() {
		return d
	}
	for index, section := range []string{"", "/general", "/experimental"} {
		if len(bodies[index]) == 0 {
			continue
		}
		if err = r.client.PatchInstanceSettings(ctx, section, bodies[index]); err != nil {
			d.AddError("Instance settings PATCH failed", err.Error())
			break
		}
	}
	remote, err = r.client.GetInstanceSettings(ctx)
	if err != nil {
		d.AddError("Instance settings readback failed", err.Error())
		return d
	}
	refreshSettings(&m, remote, false, nil)
	d.Append(state.Set(ctx, &m)...)
	health, err := r.client.Health(ctx)
	if err != nil {
		d.AddError("Instance settings ownership readback failed", err.Error())
		return d
	}
	for index, section := range []string{"", "general", "experimental"} {
		values := remote.General
		if index == 0 {
			values = map[string]interface{}{"defaultEnvironmentId": nil}
			if remote.DefaultEnvironmentID != nil {
				values["defaultEnvironmentId"] = *remote.DefaultEnvironmentID
			}
		}
		if index == 2 {
			values = remote.Experimental
		}
		for key, expected := range bodies[index] {
			if owner := settingOwner(health, remote, section, key); owner != "" {
				d.AddError("Instance settings ownership changed", fmt.Sprintf("%s.%s is managedBy %s; relinquish this key", section, key, owner))
			} else if !reflect.DeepEqual(instanceSettingValue(values, section, key), expected) {
				d.AddError("Instance settings did not converge", fmt.Sprintf("%s.%s differs after native readback; the server may constrain or override this setting", section, key))
			}
		}
	}
	return d
}
func (r *PaperclipInstanceSettingsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	resp.Diagnostics.Append(r.apply(ctx, req.Plan, &resp.State)...)
}
func (r *PaperclipInstanceSettingsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.Append(r.apply(ctx, req.Plan, &resp.State)...)
}
func (r *PaperclipInstanceSettingsResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m instanceSettingsModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	remote, err := r.client.GetInstanceSettings(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Error reading instance settings", err.Error())
		return
	}
	health, err := r.client.Health(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Error reading settings ownership", err.Error())
		return
	}
	adopt := m.ID.ValueString() == "import"
	if !adopt {
		_, e := settingsBodies(m)
		if e != nil {
			resp.Diagnostics.AddError("Invalid settings state", e.Error())
			return
		}
	}
	refreshSettings(&m, remote, adopt, health)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (r *PaperclipInstanceSettingsResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
}
func (r *PaperclipInstanceSettingsResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if req.ID != "instance" {
		resp.Diagnostics.AddError("Invalid instance settings import ID", "Use instance for the instance-settings singleton.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), "import")...)
}
