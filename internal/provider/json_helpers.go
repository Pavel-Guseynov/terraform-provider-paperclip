package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// semanticJSONEqual parses two JSON strings and compares them deeply.
func semanticJSONEqual(a, b string) bool {
	if a == b {
		return true
	}
	if a == "" || b == "" {
		return false
	}
	var objA, objB interface{}
	if err := json.Unmarshal([]byte(a), &objA); err != nil {
		return false
	}
	if err := json.Unmarshal([]byte(b), &objB); err != nil {
		return false
	}
	return reflect.DeepEqual(objA, objB)
}

func isNilInterface(v interface{}) bool {
	if v == nil {
		return true
	}
	val := reflect.ValueOf(v)
	switch val.Kind() {
	case reflect.Chan, reflect.Func, reflect.Map, reflect.Pointer, reflect.UnsafePointer, reflect.Interface, reflect.Slice:
		return val.IsNil()
	}
	return false
}

func isEmptyCollection(v interface{}) bool {
	val := reflect.ValueOf(v)
	switch val.Kind() {
	case reflect.Map, reflect.Slice, reflect.Array:
		return val.Len() == 0
	}
	return false
}

// refreshSemanticJSON refreshes a managed JSON attribute from the remote API.
// If remote is nil, it returns null. If remote is an empty map or slice and current is null,
// it preserves null. If the remote JSON is semantically equal to the prior state,
// it preserves the prior state string to avoid spurious diffs.
func refreshSemanticJSON(current types.String, remote interface{}) types.String {
	if isNilInterface(remote) {
		return types.StringNull()
	}
	if isEmptyCollection(remote) && current.IsNull() {
		return types.StringNull()
	}
	data, err := json.Marshal(remote)
	if err != nil || string(data) == "null" {
		return types.StringNull()
	}
	if !current.IsNull() && !current.IsUnknown() && semanticJSONEqual(current.ValueString(), string(data)) {
		return current
	}
	return types.StringValue(string(data))
}

type jsonAttributeValidator struct{}

func (jsonAttributeValidator) Description(context.Context) string {
	return "value must be a valid JSON string"
}

func (v jsonAttributeValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (jsonAttributeValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	val := req.ConfigValue.ValueString()
	if val == "" {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Invalid JSON",
			"Attribute cannot be an empty string; specify a valid JSON object/array or omit/set to null.",
		)
		return
	}
	var parsed interface{}
	if err := json.Unmarshal([]byte(val), &parsed); err != nil {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Invalid JSON",
			fmt.Sprintf("Attribute must be a valid JSON string: %s", err.Error()),
		)
	}
}

func JSONAttributeValidator() validator.String {
	return jsonAttributeValidator{}
}

type semanticJSONPlanModifier struct{}

func (semanticJSONPlanModifier) Description(context.Context) string {
	return "suppresses plan diffs when the planned and state JSON values are semantically equal"
}

func (v semanticJSONPlanModifier) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (semanticJSONPlanModifier) PlanModifyString(ctx context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if req.StateValue.IsNull() || req.StateValue.IsUnknown() || req.PlanValue.IsNull() || req.PlanValue.IsUnknown() {
		return
	}
	if semanticJSONEqual(req.PlanValue.ValueString(), req.StateValue.ValueString()) {
		resp.PlanValue = req.StateValue
	}
}

func SemanticJSONPlanModifier() planmodifier.String {
	return semanticJSONPlanModifier{}
}

func validateJSONString(attributePath path.Path, val string) error {
	if val == "" {
		return fmt.Errorf("attribute %s cannot be empty; must be valid JSON", attributePath.String())
	}
	var parsed interface{}
	if err := json.Unmarshal([]byte(val), &parsed); err != nil {
		return fmt.Errorf("attribute %s must be valid JSON: %w", attributePath.String(), err)
	}
	return nil
}

type interactionResolverGovernanceValidator struct{}

func (interactionResolverGovernanceValidator) Description(context.Context) string {
	return "validates interaction resolver governance kinds and policy values"
}

func (v interactionResolverGovernanceValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (interactionResolverGovernanceValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() || req.ConfigValue.ValueString() == "" {
		return
	}
	var root map[string]interface{}
	if err := json.Unmarshal([]byte(req.ConfigValue.ValueString()), &root); err != nil {
		return
	}

	validKinds := map[string]bool{
		"suggest_tasks":                 true,
		"ask_user_questions":            true,
		"request_confirmation":          true,
		"request_checkbox_confirmation": true,
		"request_item_verdicts":         true,
	}

	validPolicies := map[string]bool{
		"anyone":          true,
		"not_creator":     true,
		"human_only":      true,
		"board_or_agents": true,
		"board_only":      true,
	}

	for kind, val := range root {
		if !validKinds[kind] {
			resp.Diagnostics.AddAttributeError(
				req.Path,
				"Invalid Interaction Resolver Governance Kind",
				fmt.Sprintf("Kind %q is not valid. Valid kinds are: suggest_tasks, ask_user_questions, request_confirmation, request_checkbox_confirmation, request_item_verdicts", kind),
			)
			continue
		}
		policyObj, ok := val.(map[string]interface{})
		if !ok {
			resp.Diagnostics.AddAttributeError(
				req.Path,
				"Invalid Interaction Resolver Governance Kind Value",
				fmt.Sprintf("Value for kind %q must be a JSON object with defaultPolicy and/or cap", kind),
			)
			continue
		}
		for k, p := range policyObj {
			if k != "defaultPolicy" && k != "cap" {
				resp.Diagnostics.AddAttributeError(
					req.Path,
					"Invalid Interaction Resolver Governance Policy Key",
					fmt.Sprintf("Key %q for kind %q is not valid. Expected defaultPolicy or cap", k, kind),
				)
				continue
			}
			pStr, ok := p.(string)
			if !ok || !validPolicies[pStr] {
				resp.Diagnostics.AddAttributeError(
					req.Path,
					"Invalid Interaction Resolver Governance Policy Value",
					fmt.Sprintf("Policy %q for %s.%s is not valid. Valid policies are: anyone, not_creator, human_only, board_or_agents, board_only", fmt.Sprintf("%v", p), kind, k),
				)
			}
		}
	}
}

func InteractionResolverGovernanceValidator() validator.String {
	return interactionResolverGovernanceValidator{}
}

func parseInteractionResolverGovernance(raw string) (map[string]interface{}, error) {
	if raw == "" {
		return map[string]interface{}{}, nil
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil, err
	}
	return m, nil
}
