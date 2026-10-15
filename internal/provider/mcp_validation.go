package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type nativeEnumValidator struct {
	values            []string
	archiveViaDestroy bool
}

func decodeApplicationMetadata(value types.String) (map[string]interface{}, error) {
	if value.IsNull() || value.IsUnknown() {
		return nil, nil
	}
	var metadata map[string]interface{}
	if err := json.Unmarshal([]byte(value.ValueString()), &metadata); err != nil || metadata == nil {
		return nil, fmt.Errorf("invalid metadata JSON: expected an object; use {} to clear metadata")
	}
	return metadata, nil
}

type applicationMetadataValidator struct{}

func (applicationMetadataValidator) Description(context.Context) string {
	return "A JSON metadata object; use {} to clear metadata."
}
func (v applicationMetadataValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}
func (applicationMetadataValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if _, err := decodeApplicationMetadata(req.ConfigValue); err != nil {
		resp.Diagnostics.AddError("Invalid application metadata", err.Error())
	}
}

func nativeStringPointer(value types.String) *string {
	if value.IsNull() || value.IsUnknown() {
		return nil
	}
	return new(value.ValueString())
}

func (v nativeEnumValidator) Description(context.Context) string {
	return "Supported declarative values: " + strings.Join(v.values, ", ")
}
func (v nativeEnumValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}
func (v nativeEnumValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if v.archiveViaDestroy {
		if err := rejectDeclaredArchive(req.ConfigValue); err != nil {
			resp.Diagnostics.AddError("Archive via resource destruction", err.Error())
			return
		}
	}
	for _, value := range v.values {
		if value == req.ConfigValue.ValueString() {
			return
		}
	}
	resp.Diagnostics.AddAttributeError(req.Path, "Unsupported native value", v.Description(ctx))
}

func rejectDeclaredArchive(status types.String) error {
	if status.ValueString() == "archived" {
		return fmt.Errorf("use resource destruction to archive; the native archived object is retired and reads back as absent. No mutation was sent")
	}
	return nil
}

func validateApplicationTransition(plan, state PaperclipAppResourceModel) error {
	for _, field := range []struct {
		name           string
		planned, prior types.String
	}{
		{"description", plan.Description, state.Description}, {"plugin_id", plan.PluginID, state.PluginID},
		{"owner_agent_id", plan.OwnerAgentID, state.OwnerAgentID}, {"owner_user_id", plan.OwnerUserID, state.OwnerUserID},
	} {
		if field.planned.IsNull() && !field.prior.IsNull() && !field.prior.IsUnknown() {
			return fmt.Errorf("native application API cannot clear %s; retain its current value or choose another supported non-null value. No mutation was sent", field.name)
		}
	}
	for _, field := range []struct {
		name           string
		planned, prior types.String
	}{
		{"type", plan.Type, state.Type}, {"application_key", plan.ApplicationKey, state.ApplicationKey},
	} {
		if !field.prior.IsNull() && !field.prior.IsUnknown() && !field.planned.IsUnknown() && !field.planned.Equal(field.prior) {
			return fmt.Errorf("native application API cannot change %s; retain its current value. No mutation or replacement was performed", field.name)
		}
	}
	return nil
}

func (r *PaperclipAppResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}
	var plan, state PaperclipAppResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.validateApplicationReferences(ctx, plan); err != nil {
		resp.Diagnostics.AddError("Invalid application owner", err.Error())
	}
	if req.State.Raw.IsNull() {
		return
	}
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := validateApplicationTransition(plan, state); err != nil {
		resp.Diagnostics.AddError("Unsupported application transition", err.Error())
	}
}

func (r *PaperclipAppResource) validateApplicationReferences(ctx context.Context, plan PaperclipAppResourceModel) error {
	if r.client == nil || plan.CompanyID.IsUnknown() {
		return nil
	}
	if plan.OwnerAgentID.ValueString() != "" {
		agent, err := r.client.GetAgent(ctx, plan.OwnerAgentID.ValueString())
		if err != nil {
			return err
		}
		if agent.CompanyID != plan.CompanyID.ValueString() {
			return fmt.Errorf("application owner agent must belong to its company; no mutation was sent")
		}
	}
	if plan.PluginID.ValueString() != "" {
		if _, err := r.client.GetPlugin(ctx, plan.PluginID.ValueString()); err != nil {
			return err
		}
	}
	return nil
}
