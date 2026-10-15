package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"math"

	"github.com/Pavel-Guseynov/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// The native projection carries protected IDs and placement metadata only.
func validateCredentialProjection(raw types.String) ([]map[string]interface{}, error) {
	if raw.IsNull() || raw.IsUnknown() {
		return nil, nil
	}
	var refs []map[string]interface{}
	if err := json.Unmarshal([]byte(raw.ValueString()), &refs); err != nil || refs == nil {
		return nil, fmt.Errorf("credential_refs must be a native reference array; use [] to clear it")
	}
	for _, ref := range refs {
		for key := range ref {
			switch key {
			case "name", "secretId", "version", "placement", "key", "prefix":
			default:
				return nil, fmt.Errorf("credential_refs accepts protected secret identities and native placement metadata only; plaintext credential fields are prohibited")
			}
		}
		for _, key := range []string{"name", "secretId", "key"} {
			if value, ok := ref[key].(string); !ok || value == "" {
				return nil, fmt.Errorf("native credential_refs requires nonempty name, secretId and key strings")
			}
		}
		if placement := ref["placement"]; placement != "env" && placement != "header" && placement != "query" {
			return nil, fmt.Errorf("native credential_refs placement must be env, header or query")
		}
		if version, exists := ref["version"]; exists {
			switch value := version.(type) {
			case string:
				if value != "latest" {
					return nil, fmt.Errorf("native credential_refs version must be latest or a positive integer")
				}
			case float64:
				if value < 1 || math.Trunc(value) != value {
					return nil, fmt.Errorf("native credential_refs version must be latest or a positive integer")
				}
			default:
				return nil, fmt.Errorf("native credential_refs version must be latest or a positive integer")
			}
		}
	}
	return refs, nil
}

type nativeCredentialProjectionValidator struct{}

func (nativeCredentialProjectionValidator) Description(context.Context) string {
	return "A native credential reference array containing protected IDs and placement metadata only."
}
func (v nativeCredentialProjectionValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}
func (nativeCredentialProjectionValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	refs, err := validateCredentialProjection(req.ConfigValue)
	if err == nil {
		_, err = client.ConnectionCredentialSecretRefs(refs)
	}
	if err != nil {
		resp.Diagnostics.AddError("Invalid credential references", err.Error())
	}
}
