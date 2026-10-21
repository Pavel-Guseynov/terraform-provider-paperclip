package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func parseStdioTemplateTools(raw types.String) ([]map[string]interface{}, error) {
	if raw.IsNull() || raw.IsUnknown() {
		return nil, nil
	}
	var tools []map[string]interface{}
	if err := json.Unmarshal([]byte(raw.ValueString()), &tools); err != nil || tools == nil {
		return nil, fmt.Errorf("tools must be a JSON array of native descriptor objects")
	}
	if len(tools) > 500 {
		return nil, fmt.Errorf("tools cannot contain more than 500 descriptors")
	}
	names := map[string]bool{}
	for index, tool := range tools {
		name, ok := tool["name"].(string)
		name = strings.TrimSpace(name)
		if !ok || name == "" || names[name] {
			return nil, fmt.Errorf("tools[%d].name must be a nonempty unique string", index)
		}
		names[name] = true
		for key, value := range tool {
			switch key {
			case "name":
			case "title", "description":
				if value != nil {
					if _, ok := value.(string); !ok {
						return nil, fmt.Errorf("tools[%d].%s must be a string or null", index, key)
					}
				}
			case "inputSchema", "annotations":
				if value != nil {
					if _, ok := value.(map[string]interface{}); !ok {
						return nil, fmt.Errorf("tools[%d].%s must be an object or null", index, key)
					}
				}
			default:
				return nil, fmt.Errorf("tools[%d] contains unsupported native descriptor field %q", index, key)
			}
		}
	}
	return tools, nil
}

// Native template responses make optional descriptor fields explicit. Compare
// those native defaults without changing producer schemas or approval hints.
func normalizeStdioTemplateTools(value interface{}) interface{} {
	tools, ok := value.([]interface{})
	if !ok {
		return value
	}
	for _, raw := range tools {
		tool, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		for _, key := range []string{"name", "title"} {
			if text, ok := tool[key].(string); ok {
				tool[key] = strings.TrimSpace(text)
			}
		}
		for key, nativeDefault := range map[string]interface{}{
			"title": nil, "description": nil,
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
			"annotations": map[string]interface{}{},
		} {
			if tool[key] == nil {
				tool[key] = nativeDefault
			}
		}
	}
	return tools
}

type stdioTemplateToolsValidator struct{}

func (stdioTemplateToolsValidator) Description(context.Context) string {
	return "tools must be producer-supplied native descriptor objects with unique names"
}

func (v stdioTemplateToolsValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (stdioTemplateToolsValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if _, err := parseStdioTemplateTools(req.ConfigValue); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid native tool descriptors", err.Error())
	}
}
