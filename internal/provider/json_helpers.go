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
