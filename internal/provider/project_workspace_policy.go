package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// This field tree is the single HCL/wire/type authority for the native policy.
// Opaque native record fields remain JSON objects; structured policy sections
// expose only keys that the assignment evaluator can interpret.
type projectPolicyField struct {
	wire       string
	boolean    bool
	required   bool
	jsonObject bool
	choices    []string
	fields     map[string]projectPolicyField
}

var projectPolicyFields = map[string]projectPolicyField{
	"enabled":                      {wire: "enabled", boolean: true, required: true},
	"shared_workspace_concurrency": {wire: "sharedWorkspaceConcurrency", choices: []string{"auto", "serialize", "allow"}},
	"default_mode":                 {wire: "defaultMode", choices: []string{"shared_workspace", "isolated_workspace", "operator_branch", "adapter_default"}},
	"allow_issue_override":         {wire: "allowIssueOverride", boolean: true},
	"default_project_workspace_id": {wire: "defaultProjectWorkspaceId"},
	"environment_id":               {wire: "environmentId"},
	"workspace_strategy": {wire: "workspaceStrategy", fields: map[string]projectPolicyField{
		"type":     {wire: "type", choices: []string{"project_primary", "git_worktree", "adapter_managed", "cloud_sandbox"}},
		"base_ref": {wire: "baseRef"}, "branch_template": {wire: "branchTemplate"},
		"worktree_parent_dir": {wire: "worktreeParentDir"}, "provision_command": {wire: "provisionCommand"},
		"runtime_provision_command": {wire: "runtimeProvisionCommand"}, "teardown_command": {wire: "teardownCommand"},
	}},
	"workspace_runtime":   {wire: "workspaceRuntime", jsonObject: true},
	"branch_policy":       {wire: "branchPolicy", jsonObject: true},
	"pull_request_policy": {wire: "pullRequestPolicy", jsonObject: true},
	"runtime_policy":      {wire: "runtimePolicy", jsonObject: true},
	"cleanup_policy":      {wire: "cleanupPolicy", jsonObject: true},
	"authorization_policy": {wire: "authorizationPolicy", fields: map[string]projectPolicyField{
		"agent_visibility": {wire: "agentVisibility", fields: map[string]projectPolicyField{
			"mode": {wire: "mode", choices: []string{"discoverable", "private"}},
		}},
		"assignment_policy": {wire: "assignmentPolicy", fields: map[string]projectPolicyField{
			"mode": {wire: "mode", choices: []string{"company_default", "protected"}},
		}},
		"protected_agent": {wire: "protectedAgent", fields: map[string]projectPolicyField{
			"block_assignment": {wire: "blockAssignment", boolean: true}, "block_reason": {wire: "blockReason"},
		}},
		"managed_by": {wire: "managedBy", jsonObject: true},
	}},
}

func policyBlock(fields map[string]projectPolicyField) schema.SingleNestedBlock {
	block := schema.SingleNestedBlock{Description: "Typed native policy section. Omit this block to clear it; unknown arguments, including authorization trust keys, are rejected.", Attributes: make(map[string]schema.Attribute), Blocks: make(map[string]schema.Block), Validators: []validator.Object{projectPolicyObjectValidator{fields: fields}}}
	for name, field := range fields {
		switch {
		case field.fields != nil:
			block.Blocks[name] = policyBlock(field.fields)
		case field.boolean:
			description := "Native " + field.wire + " setting."
			if field.required {
				description += " Required when this block is configured."
			}
			block.Attributes[name] = schema.BoolAttribute{Optional: true, Description: description}
		default:
			a := schema.StringAttribute{Optional: true, Description: "Native " + field.wire + " setting."}
			if len(field.choices) > 0 {
				a.Validators = []validator.String{stringOneOfValidator{allowed: field.choices}}
				a.Description += " Allowed values: " + strings.Join(field.choices, ", ") + "."
			}
			if field.jsonObject {
				a.Description += " JSON object."
				a.Validators = []validator.String{nativeObjectValidator{}}
				a.PlanModifiers = []planmodifier.String{SemanticJSONPlanModifier()}
			}
			block.Attributes[name] = a
		}
	}
	return block
}

func policyTypes(fields map[string]projectPolicyField) map[string]attr.Type {
	return policyBlock(fields).Type().(types.ObjectType).AttrTypes
}

func policyToAPI(value types.Object, fields map[string]projectPolicyField) map[string]interface{} {
	if value.IsNull() || value.IsUnknown() {
		return nil
	}
	result := map[string]interface{}{}
	for name, val := range value.Attributes() {
		if val.IsNull() || val.IsUnknown() {
			continue
		}
		field := fields[name]
		switch v := val.(type) {
		case types.Object:
			result[field.wire] = policyToAPI(v, field.fields)
		case types.Bool:
			result[field.wire] = v.ValueBool()
		case types.String:
			if field.jsonObject {
				var obj map[string]interface{}
				_ = json.Unmarshal([]byte(v.ValueString()), &obj)
				result[field.wire] = obj
			} else {
				result[field.wire] = v.ValueString()
			}
		}
	}
	return result
}

func policyFromAPI(fields map[string]projectPolicyField, remote map[string]interface{}) (types.Object, diag.Diagnostics) {
	ts := policyTypes(fields)
	if remote == nil {
		return types.ObjectNull(ts), nil
	}
	attrs := map[string]attr.Value{}
	var diagnostics diag.Diagnostics
	for wire := range remote {
		found := false
		for _, field := range fields {
			if field.wire == wire {
				found = true
				break
			}
		}
		if !found {
			diagnostics.AddError("Unsupported native workspace policy", fmt.Sprintf("Native policy key %q cannot be represented by the strict provider schema. Remove unsupported or trust keys through the owning configuration before importing/managing this policy.", wire))
		}
	}
	for name, field := range fields {
		v := remote[field.wire]
		switch {
		case field.fields != nil:
			obj, ok := v.(map[string]interface{})
			if v != nil && !ok {
				diagnostics.AddError("Invalid native workspace policy", field.wire+" must be an object")
			}
			nested, ds := policyFromAPI(field.fields, obj)
			diagnostics.Append(ds...)
			attrs[name] = nested
		case field.boolean:
			attrs[name] = types.BoolNull()
			if v != nil {
				b, ok := v.(bool)
				if !ok {
					diagnostics.AddError("Invalid native workspace policy", field.wire+" must be a boolean")
				} else {
					attrs[name] = types.BoolValue(b)
				}
			}
		default:
			attrs[name] = types.StringNull()
			if v != nil {
				if field.jsonObject {
					if _, ok := v.(map[string]interface{}); !ok {
						diagnostics.AddError("Invalid native workspace policy", field.wire+" must be a JSON object")
					} else {
						raw, _ := json.Marshal(v)
						attrs[name] = types.StringValue(string(raw))
					}
				} else {
					str, ok := v.(string)
					if !ok {
						diagnostics.AddError("Invalid native workspace policy", field.wire+" must be a string")
					} else {
						attrs[name] = types.StringValue(str)
					}
				}
			}
		}
	}
	result, ds := types.ObjectValue(ts, attrs)
	diagnostics.Append(ds...)
	return result, diagnostics
}

func preservePolicyJSON(current, next types.Object, fields map[string]projectPolicyField) types.Object {
	if current.IsNull() || current.IsUnknown() || next.IsNull() {
		return next
	}
	attrs := next.Attributes()
	for name, old := range current.Attributes() {
		field := fields[name]
		if field.fields != nil {
			attrs[name] = preservePolicyJSON(old.(types.Object), attrs[name].(types.Object), field.fields)
		}
		if field.jsonObject && !old.IsNull() && !old.IsUnknown() && !attrs[name].IsNull() && semanticJSONEqual(old.(types.String).ValueString(), attrs[name].(types.String).ValueString()) {
			attrs[name] = old
		}
	}
	return types.ObjectValueMust(policyTypes(fields), attrs)
}

func validatePolicyObject(value types.Object, fields map[string]projectPolicyField) diag.Diagnostics {
	var ds diag.Diagnostics
	if value.IsNull() || value.IsUnknown() {
		return ds
	}
	for name, val := range value.Attributes() {
		field := fields[name]
		if field.required && val.IsNull() {
			ds.AddError("Missing project workspace policy field", name+" is required when the policy block is configured")
		}
		if val.IsNull() || val.IsUnknown() {
			continue
		}
		if field.fields != nil {
			ds.Append(validatePolicyObject(val.(types.Object), field.fields)...)
			continue
		}
		if field.jsonObject {
			if _, err := nativeJSONObject(val.(types.String)); err != nil {
				ds.AddError("Invalid project workspace policy", name+": "+err.Error())
			}
		}
	}
	authorization, ok := value.Attributes()["authorization_policy"].(types.Object)
	if ok && !authorization.IsNull() && !authorization.IsUnknown() {
		evaluable := false
		for name, v := range authorization.Attributes() {
			if name != "managed_by" && !v.IsNull() {
				evaluable = true
			}
		}
		if !evaluable && len(policyToAPI(authorization, fields["authorization_policy"].fields)) != 0 {
			ds.AddError("Unsupported project authorization policy", "authorization_policy requires agent_visibility, assignment_policy or protected_agent; managed_by alone makes native task assignment fail")
		}
	}
	return ds
}

type projectPolicyObjectValidator struct{ fields map[string]projectPolicyField }

func (projectPolicyObjectValidator) Description(context.Context) string {
	return "validates required policy fields only when their optional block is present"
}
func (v projectPolicyObjectValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}
func (v projectPolicyObjectValidator) ValidateObject(ctx context.Context, req validator.ObjectRequest, resp *validator.ObjectResponse) {
	resp.Diagnostics.Append(validatePolicyObject(req.ConfigValue, v.fields)...)
}

func refreshProjectEnv(current map[string]EnvBindingModel, remote map[string]interface{}) map[string]EnvBindingModel {
	if len(remote) == 0 && current == nil {
		return nil
	}
	result := make(map[string]EnvBindingModel, len(remote))
	for key, value := range remote {
		if str, ok := value.(string); ok {
			result[key] = envBindingFromJSON(map[string]interface{}{"type": "plain", "value": str})
			continue
		}
		if obj, ok := value.(map[string]interface{}); ok {
			result[key] = envBindingFromJSON(obj)
		}
	}
	return result
}

func nativeJSONObject(value types.String) (map[string]interface{}, error) {
	if value.IsNull() || value.IsUnknown() {
		return nil, nil
	}
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(value.ValueString()), &result); err != nil {
		return nil, fmt.Errorf("must be a JSON object: %w", err)
	}
	if result == nil {
		return nil, fmt.Errorf("must be a JSON object, not null")
	}
	return result, nil
}

type nativeObjectValidator struct{}

func (nativeObjectValidator) Description(context.Context) string { return "must be a JSON object" }
func (v nativeObjectValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}
func (nativeObjectValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if _, err := nativeJSONObject(req.ConfigValue); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid JSON object", err.Error())
	}
}
