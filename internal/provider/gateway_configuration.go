package provider

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

// These are the pinned API's policy defaults. Send complete objects: PATCH
// replaces policies, and omission must clear a formerly configured policy.
var gatewayPolicyDefaults = map[string]string{
	"authConfig":          `{"version":1,"bearer":{"enabled":true,"tokenPrefix":"pcgw","defaultTtlSeconds":7776000,"requireFiniteExpiry":true,"longLivedTokenRequiresOverride":true},"oauth":{"enabled":false,"reservedFor":"v1_5"}}`,
	"headerPolicy":        `{"version":1,"callerPassthrough":{"enabled":false,"allowedHeaders":[]},"staticHeaders":[],"generatedMetadata":{"enabled":false,"allowedHeaders":[]},"responseHeaders":{"forwardMcpRequiredHeaders":true,"forwardSafeCacheHeaders":true}}`,
	"metadataPolicy":      `{"version":1,"forwardCompanyId":false,"forwardGatewayId":false,"forwardProjectId":false,"forwardIssueId":false,"forwardAgentId":false,"forwardRunId":false,"forwardCorrelationId":true}`,
	"onDemandToolsConfig": `{"enabled":false,"searchToolName":"search_tools","runToolName":"run_tool"}`,
}

var gatewayHeaderName = regexp.MustCompile("^[!#$%&'*+\\-.^_`|~0-9A-Za-z]+$")
var gatewaySafeKey = regexp.MustCompile(`(?i)^[a-z0-9][a-z0-9._:-]*$`)

func gatewayText(s string, limit int, nonempty bool) bool {
	return len([]rune(s)) <= limit && (!nonempty || (s != "" && strings.TrimSpace(s) == s))
}

func gatewayPolicy(kind string, value types.String) (map[string]interface{}, error) {
	var defaults map[string]interface{}
	_ = json.Unmarshal([]byte(gatewayPolicyDefaults[kind]), &defaults)
	if value.IsUnknown() {
		return defaults, nil
	}
	input, err := decodeApplicationMetadata(value)
	if err != nil {
		return nil, err
	}
	if err := mergeGatewayPolicy(defaults, input, kind); err != nil {
		return nil, err
	}
	return defaults, nil
}

// Native optional OAuth fields are equivalent to their disabled/null absence.
// Canonicalization lets an imported complete policy match a partial declaration.
func normalizeGatewayPolicy(kind string, value interface{}) interface{} {
	obj, err := gatewayPolicy(kind, types.StringValue(marshalJSON(value)))
	if err != nil {
		return value
	}
	if kind == "authConfig" {
		oauth := obj["oauth"].(map[string]interface{})
		for _, key := range []string{"dynamicClientRegistration", "authorizationCodePkce", "protectedResourceMetadataPath"} {
			if oauth[key] == nil || oauth[key] == false {
				delete(oauth, key)
			}
		}
	}
	return obj
}

func mergeGatewayPolicy(dst, src map[string]interface{}, path string) error {
	for key, v := range src {
		p := path + "." + key
		old, exists := dst[key]
		// Optional native OAuth metadata and fixed disabled capabilities.
		if !exists && path == "authConfig.oauth" {
			if key == "protectedResourceMetadataPath" && (v == nil || (isGatewayString(v, 240, false) && strings.TrimSpace(v.(string)) == v)) {
				dst[key] = v
				continue
			}
			if (key == "dynamicClientRegistration" || key == "authorizationCodePkce") && v == false {
				dst[key] = v
				continue
			}
		}
		if !exists {
			return fmt.Errorf("unsupported gateway policy field %s", p)
		}
		switch expected := old.(type) {
		case map[string]interface{}:
			obj, ok := v.(map[string]interface{})
			if !ok {
				return fmt.Errorf("%s must be an object", p)
			}
			if err := mergeGatewayPolicy(expected, obj, p); err != nil {
				return err
			}
		case bool:
			if _, ok := v.(bool); !ok || (p == "authConfig.oauth.enabled" && v != false) {
				return fmt.Errorf("%s must be a supported boolean (OAuth is fixed to false)", p)
			}
		case string:
			if v != expected {
				return fmt.Errorf("%s is fixed to %s by the native API", p, expected)
			}
		case float64:
			if p == "authConfig.bearer.defaultTtlSeconds" {
				if v != nil {
					n, ok := v.(float64)
					if !ok || n < 1 || n > 31536000 || n != float64(int64(n)) {
						return fmt.Errorf("%s must be null or an integer from 1 to 31536000", p)
					}
				}
			} else if v != float64(1) {
				return fmt.Errorf("%s must be 1", p)
			}
		case []interface{}:
			items, ok := v.([]interface{})
			if !ok {
				return fmt.Errorf("%s must be an array", p)
			}
			limit := 50
			if path == "headerPolicy.generatedMetadata" {
				limit = 20
			}
			if len(items) > limit {
				return fmt.Errorf("%s exceeds native limit %d", p, limit)
			}
			for _, item := range items {
				if key == "staticHeaders" {
					h, ok := item.(map[string]interface{})
					if !ok {
						return fmt.Errorf("staticHeaders entries must be objects")
					}
					if err := validateGatewayHeader(h); err != nil {
						return err
					}
				} else if !isGatewayString(item, 120, true) || !gatewayHeaderName.MatchString(item.(string)) {
					return fmt.Errorf("%s contains an invalid header name", p)
				}
			}
		}
		dst[key] = v
		if _, ok := old.(map[string]interface{}); ok {
			dst[key] = old
		}
	}
	return nil
}

func isGatewayString(v interface{}, limit int, nonempty bool) bool {
	s, ok := v.(string)
	return ok && gatewayText(s, limit, nonempty)
}

func validateGatewayHeader(h map[string]interface{}) error {
	if !isGatewayString(h["name"], 120, true) || !gatewayHeaderName.MatchString(h["name"].(string)) {
		return fmt.Errorf("static header requires a valid name")
	}
	for k, v := range h {
		switch k {
		case "name":
		case "valueRef":
			if v != nil && (!isGatewayString(v, 240, false) || strings.TrimSpace(v.(string)) != v) {
				return fmt.Errorf("static header valueRef must contain at most 240 characters")
			}
		case "value":
			if v != nil && !isGatewayString(v, 4000, false) {
				return fmt.Errorf("static header value must contain at most 4000 characters")
			}
		default:
			return fmt.Errorf("unsupported static header field %s", k)
		}
	}
	return nil
}
