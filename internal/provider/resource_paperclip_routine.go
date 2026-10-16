package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/Pavel-Guseynov/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type PaperclipRoutineResource struct{ client *client.Client }
type PaperclipRoutineModel struct {
	ID                 types.String               `tfsdk:"id"`
	CompanyID          types.String               `tfsdk:"company_id"`
	ProjectID          types.String               `tfsdk:"project_id"`
	GoalID             types.String               `tfsdk:"goal_id"`
	ParentIssueID      types.String               `tfsdk:"parent_issue_id"`
	Title              types.String               `tfsdk:"title"`
	Description        types.String               `tfsdk:"description"`
	AssigneeAgentID    types.String               `tfsdk:"assignee_agent_id"`
	Priority           types.String               `tfsdk:"priority"`
	Status             types.String               `tfsdk:"status"`
	ConcurrencyPolicy  types.String               `tfsdk:"concurrency_policy"`
	CatchUpPolicy      types.String               `tfsdk:"catch_up_policy"`
	ActivityGatePolicy types.String               `tfsdk:"activity_gate_policy"`
	ActivityGateScope  types.String               `tfsdk:"activity_gate_scope"`
	Variables          types.String               `tfsdk:"variables"`
	Env                map[string]EnvBindingModel `tfsdk:"env"`
	RevisionID         types.String               `tfsdk:"revision_id"`
}

func NewPaperclipRoutineResource() resource.Resource { return &PaperclipRoutineResource{} }
func (r *PaperclipRoutineResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_routine"
}
func (r *PaperclipRoutineResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	attrs := map[string]schema.Attribute{
		"id":          schema.StringAttribute{Computed: true, Description: "Native routine identity.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
		"company_id":  schema.StringAttribute{Required: true, Description: "Owning company; changes are rejected without replacing routine identity."},
		"title":       schema.StringAttribute{Required: true, Description: "Routine title; 1–200 characters with no surrounding whitespace."},
		"variables":   schema.StringAttribute{Optional: true, Validators: []validator.String{JSONAttributeValidator()}, PlanModifiers: []planmodifier.String{SemanticJSONPlanModifier()}, Description: "JSON array of native variable definitions. Names must match template placeholders in title/description in first-occurrence order. Omit or use [] to clear after removing placeholders."},
		"env":         EnvBindingMapAttribute("Routine environment bindings, the highest-precedence native environment layer. Omit to clear; secret values are never resolved."),
		"revision_id": schema.StringAttribute{Computed: true, Description: "Native latestRevisionId captured by refresh and sent as baseRevisionId on update and archive."},
	}
	for _, key := range []string{"project_id", "goal_id", "parent_issue_id", "description", "assignee_agent_id"} {
		attrs[key] = schema.StringAttribute{Optional: true, Description: "Native " + key + "; omitted configuration clears to null."}
	}
	for key, values := range map[string][]string{
		"priority": {"medium", "critical", "high", "low"}, "status": {"active", "paused", "archived"},
		"concurrency_policy": {"coalesce_if_active", "always_enqueue", "skip_if_active"}, "catch_up_policy": {"skip_missed", "enqueue_missed_with_cap"},
		"activity_gate_policy": {"always", "require_external_activity"}, "activity_gate_scope": {"company", "project"},
	} {
		attrs[key] = schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString(values[0]), Description: "Native " + key + "; default " + values[0] + ". Allowed: " + strings.Join(values, ", ") + ".", Validators: []validator.String{stringOneOfValidator{allowed: values}}}
	}
	resp.Schema = schema.Schema{Description: "Manages a native Paperclip routine. Destroy archives with a revision guard. Active routines require an assignee; native template normalization is checked before mutation.", Attributes: attrs}
}
func (r *PaperclipRoutineResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

var routinePlaceholder = regexp.MustCompile(`\{\{\s*([A-Za-z](?:\\_|[A-Za-z0-9_])*)\s*\}\}`)
var routineVariableName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)

// Normalize only documented native defaults. Refuse template-driven additions,
// removals or reordering instead of accepting a write that cannot converge.
func routineVariables(raw, title, description string) ([]map[string]interface{}, error) {
	variables := []map[string]interface{}{}
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &variables); err != nil || variables == nil {
			return nil, fmt.Errorf("variables must be a JSON array of objects")
		}
	}
	names := []string{}
	seen := map[string]bool{"date": true, "timestamp": true}
	for _, text := range []string{title, description} {
		for _, match := range routinePlaceholder.FindAllStringSubmatch(text, -1) {
			name := strings.ReplaceAll(match[1], `\_`, "_")
			if !seen[name] {
				names = append(names, name)
				seen[name] = true
			}
		}
	}
	if len(names) != len(variables) {
		return nil, fmt.Errorf("variables must match title/description placeholders (excluding date and timestamp): %s; remove placeholders before clearing variables", strings.Join(names, ", "))
	}
	for i, v := range variables {
		name, ok := v["name"].(string)
		if !ok || !routineVariableName.MatchString(name) || name != names[i] {
			return nil, fmt.Errorf("variable %d must be named %q to match native template order", i, names[i])
		}
		for key := range v {
			if key != "name" && key != "label" && key != "type" && key != "defaultValue" && key != "required" && key != "options" {
				return nil, fmt.Errorf("unknown routine variable field %q", key)
			}
		}
		if label, ok := v["label"].(string); ok && label != strings.TrimSpace(label) {
			return nil, fmt.Errorf("variable labels must not contain surrounding whitespace")
		}
		if options, ok := v["options"].([]interface{}); ok {
			for _, option := range options {
				if text, ok := option.(string); ok && text != strings.TrimSpace(text) {
					return nil, fmt.Errorf("variable options must not contain surrounding whitespace")
				}
			}
		}
		if _, ok := v["type"]; !ok {
			v["type"] = "text"
		}
		if _, ok := v["label"]; !ok {
			v["label"] = nil
		}
		if _, ok := v["defaultValue"]; !ok {
			v["defaultValue"] = nil
		}
		if _, ok := v["required"]; !ok {
			v["required"] = true
		}
		if _, ok := v["options"]; !ok {
			v["options"] = []interface{}{}
		}
	}
	return variables, nil
}

func validateRoutine(model PaperclipRoutineModel) diag.Diagnostics {
	var d diag.Diagnostics
	if !model.Title.IsUnknown() && (strings.TrimSpace(model.Title.ValueString()) != model.Title.ValueString() || len([]rune(model.Title.ValueString())) < 1 || len([]rune(model.Title.ValueString())) > 200) {
		d.AddAttributeError(path.Root("title"), "Invalid routine title", "Use 1–200 characters without surrounding whitespace.")
	}
	if model.Status.ValueString() == "active" && model.AssigneeAgentID.IsNull() {
		d.AddAttributeError(path.Root("assignee_agent_id"), "Default agent required", "An active routine requires assignee_agent_id. Use paused when no assignee is configured; native creation otherwise silently changes active to paused.")
	}
	if !model.Variables.IsUnknown() && !model.Title.IsUnknown() && !model.Description.IsUnknown() {
		if _, err := routineVariables(model.Variables.ValueString(), model.Title.ValueString(), model.Description.ValueString()); err != nil {
			d.AddAttributeError(path.Root("variables"), "Invalid routine variables", err.Error())
		}
	}
	d.Append(ValidateEnvBindingsMapPlan(path.Root("env"), model.Env, "routine")...)
	return d
}
func (r *PaperclipRoutineResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m PaperclipRoutineModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(validateRoutine(m)...)
	}
}

func (r *PaperclipRoutineResource) references(ctx context.Context, m PaperclipRoutineModel) diag.Diagnostics {
	var d diag.Diagnostics
	if r.client == nil || m.CompanyID.IsUnknown() {
		return d
	}
	cid := m.CompanyID.ValueString()
	for key, value := range map[string]types.String{"project_id": m.ProjectID, "goal_id": m.GoalID, "parent_issue_id": m.ParentIssueID, "assignee_agent_id": m.AssigneeAgentID} {
		if value.IsNull() || value.IsUnknown() {
			continue
		}
		var company string
		var err error
		switch key {
		case "project_id":
			var obj *client.PaperclipProject
			obj, err = r.client.GetProject(ctx, value.ValueString())
			if err == nil {
				company = obj.CompanyID
			}
		case "goal_id":
			var obj *client.PaperclipGoal
			obj, err = r.client.GetGoal(ctx, value.ValueString())
			if err == nil {
				company = obj.CompanyID
			}
		case "parent_issue_id":
			company, err = r.client.GetRoutineParentCompany(ctx, value.ValueString())
		case "assignee_agent_id":
			var obj *client.PaperclipAgent
			obj, err = r.client.GetAgent(ctx, value.ValueString())
			if err == nil {
				company = obj.CompanyID
				if obj.Status == "terminated" {
					err = fmt.Errorf("terminated agents cannot be assigned routines")
				}
			}
		}
		if err == nil && company != cid {
			err = fmt.Errorf("reference belongs to company %q, expected %q", company, cid)
		}
		if err != nil {
			d.AddAttributeError(path.Root(key), "Invalid routine reference", err.Error())
		}
	}
	d.Append(ValidateEnvBindingsApply(ctx, r.client, cid, m.Env)...)
	return d
}
func (r *PaperclipRoutineResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}
	var m PaperclipRoutineModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !req.State.Raw.IsNull() && !m.CompanyID.IsUnknown() {
		var previous PaperclipRoutineModel
		resp.Diagnostics.Append(req.State.Get(ctx, &previous)...)
		if m.CompanyID != previous.CompanyID {
			resp.Diagnostics.AddAttributeError(path.Root("company_id"), "Unsupported routine transition", "The native API cannot move a routine between companies; retain company_id without replacing its identity.")
		}
	}
	resp.Diagnostics.Append(validateRoutine(m)...)
	resp.Diagnostics.Append(r.references(ctx, m)...)
}
func routineInput(m PaperclipRoutineModel) map[string]interface{} {
	variables, _ := routineVariables(m.Variables.ValueString(), m.Title.ValueString(), m.Description.ValueString())
	return map[string]interface{}{"title": m.Title.ValueString(), "description": m.Description.ValueStringPointer(), "projectId": m.ProjectID.ValueStringPointer(), "goalId": m.GoalID.ValueStringPointer(), "parentIssueId": m.ParentIssueID.ValueStringPointer(), "assigneeAgentId": m.AssigneeAgentID.ValueStringPointer(), "priority": m.Priority.ValueString(), "status": m.Status.ValueString(), "concurrencyPolicy": m.ConcurrencyPolicy.ValueString(), "catchUpPolicy": m.CatchUpPolicy.ValueString(), "activityGatePolicy": m.ActivityGatePolicy.ValueString(), "activityGateScope": m.ActivityGateScope.ValueString(), "variables": variables, "env": EnvBindingsMapToAPI(m.Env)}
}
func refreshRoutine(m *PaperclipRoutineModel, remote *client.PaperclipRoutine) {
	m.ID = types.StringValue(remote.ID)
	m.CompanyID = types.StringValue(remote.CompanyID)
	m.RevisionID = types.StringValue(remote.LatestRevisionID)
	m.Title = types.StringValue(remote.Title)
	m.Description = types.StringPointerValue(remote.Description)
	m.ProjectID = types.StringPointerValue(remote.ProjectID)
	m.GoalID = types.StringPointerValue(remote.GoalID)
	m.ParentIssueID = types.StringPointerValue(remote.ParentIssueID)
	m.AssigneeAgentID = types.StringPointerValue(remote.AssigneeAgentID)
	m.Priority = types.StringValue(remote.Priority)
	m.Status = types.StringValue(remote.Status)
	m.ConcurrencyPolicy = types.StringValue(remote.ConcurrencyPolicy)
	m.CatchUpPolicy = types.StringValue(remote.CatchUpPolicy)
	m.ActivityGatePolicy = types.StringValue(remote.ActivityGatePolicy)
	m.ActivityGateScope = types.StringValue(remote.ActivityGateScope)
	variables, err := routineVariables(m.Variables.ValueString(), remote.Title, m.Description.ValueString())
	if err != nil || !semanticJSONEqual(marshalEnvironmentJSON(variables), marshalEnvironmentJSON(remote.Variables)) {
		m.Variables = types.StringValue(marshalEnvironmentJSON(remote.Variables))
	}
	m.Env = refreshEnvBindings(m.Env, remote.Env)
}
func (r *PaperclipRoutineResource) save(ctx context.Context, m *PaperclipRoutineModel, revision string, d *diag.Diagnostics) {
	d.Append(validateRoutine(*m)...)
	d.Append(r.references(ctx, *m)...)
	if d.HasError() {
		return
	}
	input := routineInput(*m)
	var remote *client.PaperclipRoutine
	var err error
	if m.ID.ValueString() == "" {
		remote, err = r.client.CreateRoutine(ctx, m.CompanyID.ValueString(), input)
	} else {
		input["baseRevisionId"] = revision
		remote, err = r.client.UpdateRoutine(ctx, m.ID.ValueString(), input)
	}
	if err != nil {
		d.AddError("Error saving Paperclip routine", err.Error())
		return
	}
	refreshRoutine(m, remote)
}
func (r *PaperclipRoutineResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m PaperclipRoutineModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.save(ctx, &m, "", &resp.Diagnostics)
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
	}
}
func (r *PaperclipRoutineResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m, previous PaperclipRoutineModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &previous)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if m.CompanyID != previous.CompanyID {
		resp.Diagnostics.AddError("Unsupported routine transition", "The native API cannot move a routine between companies; retain company_id without replacing its identity.")
		return
	}
	m.ID = previous.ID
	r.save(ctx, &m, previous.RevisionID.ValueString(), &resp.Diagnostics)
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
	}
}
func (r *PaperclipRoutineResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m PaperclipRoutineModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	remote, err := r.client.GetRoutine(ctx, m.ID.ValueString())
	if errors.Is(err, client.ErrNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading Paperclip routine", err.Error())
		return
	}
	refreshRoutine(&m, remote)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (r *PaperclipRoutineResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m PaperclipRoutineModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	_, err := r.client.UpdateRoutine(ctx, m.ID.ValueString(), map[string]interface{}{"status": "archived", "baseRevisionId": m.RevisionID.ValueString()})
	if err != nil && !errors.Is(err, client.ErrNotFound) {
		resp.Diagnostics.AddError("Error archiving Paperclip routine", err.Error())
	}
}
func (r *PaperclipRoutineResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
