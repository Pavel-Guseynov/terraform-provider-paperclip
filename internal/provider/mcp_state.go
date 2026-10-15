package provider

import (
	"context"
	"crypto/rand"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func additiveMCPUpgrade(current schema.Schema, added map[string]attr.Value) map[int64]resource.StateUpgrader {
	prior := current
	prior.Version = 0
	prior.Attributes = map[string]schema.Attribute{}
	for key, attribute := range current.Attributes {
		if _, exists := added[key]; !exists {
			prior.Attributes[key] = attribute
		}
	}
	return map[int64]resource.StateUpgrader{0: {PriorSchema: &prior, StateUpgrader: func(ctx context.Context, req resource.UpgradeStateRequest, resp *resource.UpgradeStateResponse) {
		values := map[string]tftypes.Value{}
		if err := req.State.Raw.As(&values); err != nil {
			resp.Diagnostics.AddError("Invalid MCP state", err.Error())
			return
		}
		for key, value := range added {
			values[key], _ = value.ToTerraformValue(ctx)
		}
		resp.State.Raw = tftypes.NewValue(current.Type().TerraformType(ctx), values)
	}}}
}

func (r *PaperclipAppResource) UpgradeState(ctx context.Context) map[int64]resource.StateUpgrader {
	var response resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &response)
	return additiveMCPUpgrade(response.Schema, map[string]attr.Value{"status": types.StringNull(), "plugin_id": types.StringNull(), "owner_agent_id": types.StringNull(), "owner_user_id": types.StringNull()})
}
func (r *PaperclipConnectionResource) UpgradeState(ctx context.Context) map[int64]resource.StateUpgrader {
	var response resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &response)
	return additiveMCPUpgrade(response.Schema, map[string]attr.Value{"connection_purpose": types.StringNull(), "connection_kind": types.StringNull(), "ownership": types.StringNull(), "credential_source": types.StringNull(), "status": types.StringNull(), "enabled": types.BoolNull(), "transport_config": types.StringNull(), "credential_secret_refs": types.MapNull(secretRefType())})
}
func (r *PaperclipConnectionInstallResource) UpgradeState(ctx context.Context) map[int64]resource.StateUpgrader {
	var response resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &response)
	return additiveMCPUpgrade(response.Schema, map[string]attr.Value{"owner_id": types.StringValue(rand.Text())})
}
