package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"

	"github.com/Pavel-Guseynov/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func agentConfigObject(value types.String) (map[string]interface{}, error) {
	if value.IsNull() || value.IsUnknown() {
		return nil, nil
	}
	var object map[string]interface{}
	if err := json.Unmarshal([]byte(value.ValueString()), &object); err != nil {
		return nil, err
	}
	if object == nil {
		return nil, fmt.Errorf("must be a JSON object; use HCL null to relinquish configuration or {} for an empty object")
	}
	return object, nil
}

func validateAgentConfigValues(model PaperclipAgentResourceModel) diag.Diagnostics {
	var diags diag.Diagnostics
	adapter, err := agentConfigObject(model.AdapterConfig)
	if err != nil {
		diags.AddAttributeError(path.Root("adapter_config"), "Invalid JSON", err.Error())
	} else {
		for _, key := range paperclipManagedAdapterConfigKeys {
			if _, exists := adapter[key]; exists {
				diags.AddAttributeError(path.Root("adapter_config"), "Configuration owned by another resource", fmt.Sprintf("%s belongs to the agent instruction bundle or skill-assignment owner; remove it from adapter_config and manage it with that resource", key))
			}
		}
		if env, ok := adapter["env"].(map[string]interface{}); ok {
			for key := range model.Env {
				if _, duplicate := env[key]; duplicate {
					diags.AddAttributeError(path.Root("env"), "Duplicate environment ownership", fmt.Sprintf("env.%s is also declared in adapter_config.env; choose one declaration for this binding", key))
				}
			}
		}
	}
	runtime, err := agentConfigObject(model.RuntimeConfig)
	if err != nil {
		diags.AddAttributeError(path.Root("runtime_config"), "Invalid JSON", err.Error())
		return diags
	}
	if _, exists := runtime["modelProfiles"]; exists {
		diags.AddAttributeError(path.Root("runtime_config"), "Unsupported runtime configuration", "runtimeConfig.modelProfiles is no longer supported; configure the adapter's native model field instead")
	}
	if raw, exists := runtime["heartbeat"]; exists {
		if err := validateHeartbeat(raw); err != nil {
			diags.AddAttributeError(path.Root("runtime_config"), "Invalid heartbeat configuration", err.Error())
		}
	}
	return diags
}

func refreshAgentAdapterState(current types.String, remote map[string]interface{}, env map[string]EnvBindingModel) types.String {
	if len(env) == 0 {
		return refreshAgentAdapterConfig(current, remote)
	}
	filtered := make(map[string]interface{}, len(remote))
	for key, value := range remote {
		filtered[key] = value
	}
	if remoteEnv, ok := remote["env"].(map[string]interface{}); ok {
		remaining := make(map[string]interface{}, len(remoteEnv))
		for key, value := range remoteEnv {
			if _, owned := env[key]; !owned {
				remaining[key] = value
			}
		}
		if len(remaining) > 0 {
			filtered["env"] = remaining
		} else {
			delete(filtered, "env")
		}
	}
	return refreshAgentAdapterConfig(current, filtered)
}

func validateHeartbeat(value interface{}) error {
	heartbeat, ok := value.(map[string]interface{})
	if !ok {
		return fmt.Errorf("heartbeat must be an object")
	}
	keys := make([]string, 0, len(heartbeat))
	for key := range heartbeat {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := heartbeat[key]
		switch key {
		case "enabled", "wakeOnDemand", "wakeOnAssignment", "wakeOnOnDemand", "wakeOnAutomation", "skipTimerWhenNoActionableWork", "requireActionableTimerWork", "issueOnlyTimer":
			if _, ok := value.(bool); !ok {
				return fmt.Errorf("heartbeat.%s must be a boolean", key)
			}
		case "intervalSec":
			if n, ok := value.(float64); !ok || n < 0 {
				return fmt.Errorf("heartbeat.%s must be a non-negative number", key)
			}
		case "maxConcurrentRuns", "maxDailyRuns", "dailyRunLimit", "dailyRunCap", "maxRunsPerDay", "maxDailyCostCents", "dailyCostCentsLimit", "dailySpendCentsLimit", "dailyBudgetCents":
			if value == nil && key != "maxConcurrentRuns" {
				continue
			}
			n, ok := value.(float64)
			if !ok || n < 0 || n != math.Trunc(n) {
				return fmt.Errorf("heartbeat.%s must be a non-negative integer", key)
			}
			if key == "maxConcurrentRuns" && (n < 1 || n > 50) {
				return fmt.Errorf("heartbeat.maxConcurrentRuns must be between 1 and 50, the native concurrency limits")
			}
		default:
			return fmt.Errorf("heartbeat.%s is not a supported native heartbeat setting", key)
		}
	}
	return nil
}

func validatePublishedAdapterConfig(config map[string]interface{}, published *client.AdapterConfigSchema) error {
	known := map[string]bool{"env": true, "promptTemplate": true, "instructionsFilePath": true, "cwd": true, "timeoutSec": true, "graceSec": true, "bootstrapPromptTemplate": true, "paperclipSkillSync": true}
	for _, field := range published.Fields {
		known[field.Key] = true
		value, exists := config[field.Key]
		if !exists {
			if field.Required {
				return fmt.Errorf("adapter_config.%s is required by the published schema", field.Key)
			}
			continue
		}
		valid := false
		switch field.Type {
		case "text", "textarea", "combobox", "select":
			_, valid = value.(string)
		case "number":
			_, valid = value.(float64)
		case "toggle":
			_, valid = value.(bool)
		default:
			return fmt.Errorf("adapter schema field %s declares unsupported type %s; update the provider before managing this field", field.Key, field.Type)
		}
		if !valid {
			return fmt.Errorf("adapter_config.%s must match published field type %s", field.Key, field.Type)
		}
		if field.Type == "select" && len(field.Options) > 0 {
			allowed := false
			for _, option := range field.Options {
				if value == option.Value {
					allowed = true
				}
			}
			if !allowed {
				return fmt.Errorf("adapter_config.%s must match a published select option", field.Key)
			}
		}
	}
	if published.AdditionalProperties != nil && !*published.AdditionalProperties {
		for key := range config {
			if !known[key] {
				return fmt.Errorf("adapter_config.%s is rejected by the published additionalProperties=false policy", key)
			}
		}
	}
	return nil
}

func (r *PaperclipAgentResource) validateAgentNativeConfig(ctx context.Context, model PaperclipAgentResourceModel) diag.Diagnostics {
	var diags diag.Diagnostics
	config, err := agentConfigObject(model.AdapterConfig)
	if err != nil || config == nil || r.client == nil || model.AdapterType.IsUnknown() || model.AdapterType.IsNull() {
		return diags
	}
	published, err := r.client.GetAdapterConfigSchema(ctx, model.AdapterType.ValueString())
	if err != nil {
		diags.AddAttributeError(path.Root("adapter_config"), "Adapter schema unavailable", fmt.Sprintf("Read the adapter's config-schema using a board-authorized provider token and a registered adapter that publishes metadata; no configuration was written: %s", err))
		return diags
	}
	if err := validatePublishedAdapterConfig(config, published); err != nil {
		diags.AddAttributeError(path.Root("adapter_config"), "Invalid adapter configuration", err.Error())
	}
	return diags
}

func validateAgentRuntimeTransition(prior, desired types.String) error {
	previous, _ := agentConfigObject(prior)
	next, _ := agentConfigObject(desired)
	if desired.IsUnknown() {
		return nil
	}
	if binding, exists := previous["aiConnection"]; exists && binding != nil && next["aiConnection"] == nil {
		return fmt.Errorf("runtime_config.aiConnection cannot be cleared: the native PATCH API restores the existing binding; retain it or set a supported replacement binding")
	}
	return nil
}

func validateAgentAdapterTransition(prior, desired PaperclipAgentResourceModel) error {
	if prior.AdapterType.Equal(desired.AdapterType) || desired.AdapterType.IsUnknown() || desired.AdapterConfig.IsUnknown() {
		return nil
	}
	previous, _ := agentConfigObject(prior.AdapterConfig)
	next, _ := agentConfigObject(desired.AdapterConfig)
	for _, key := range []string{"env", "cwd", "timeoutSec", "graceSec", "promptTemplate", "bootstrapPromptTemplate"} {
		if _, exists := previous[key]; exists && next[key] == nil {
			return fmt.Errorf("adapter_type cannot change while removing adapter-agnostic key %s: the native API preserves it during adapter swaps; remove it with the original adapter_type first, then change adapter_type", key)
		}
	}
	return nil
}

// Runtime configuration owns declared top-level keys and declared heartbeat
// settings. Native defaults and other runtime owners remain outside that scope.
func refreshAgentRuntimeConfig(current types.String, remote map[string]interface{}) types.String {
	if current.IsNull() {
		return current
	}
	if current.IsUnknown() {
		return refreshManagedJSONState(current, remote)
	}
	declared, err := agentConfigObject(current)
	if err != nil {
		return jsonStringOrNull(remote)
	}
	owned := make(map[string]interface{}, len(declared))
	for key, value := range declared {
		actual, exists := remote[key]
		if !exists {
			continue
		}
		if key == "heartbeat" {
			if expected, ok := value.(map[string]interface{}); ok {
				if actualMap, ok := actual.(map[string]interface{}); ok {
					filtered := make(map[string]interface{}, len(expected))
					for field := range expected {
						if v, exists := actualMap[field]; exists {
							filtered[field] = v
						}
					}
					actual = filtered
				}
			}
		}
		owned[key] = actual
	}
	if reflect.DeepEqual(owned, declared) {
		return current
	}
	return jsonStringOrNull(owned)
}
