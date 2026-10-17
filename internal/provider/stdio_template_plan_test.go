package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func stdioTemplatePlan(t *testing.T, companyID, templateID types.String) tfsdk.Plan {
	t.Helper()
	var s resource.SchemaResponse
	(&PaperclipStdioTemplateResource{}).Schema(context.Background(), resource.SchemaRequest{}, &s)
	plan := tfsdk.Plan{Schema: s.Schema}
	model := PaperclipStdioTemplateResourceModel{
		ID: types.StringUnknown(), CompanyID: companyID, TemplateID: templateID,
		Name: types.StringValue("Planned template"), Command: types.StringValue("nix-mcp"),
		Args: types.ListNull(types.StringType), EnvKeys: types.ListNull(types.StringType),
	}
	if d := plan.Set(context.Background(), model); d.HasError() {
		t.Fatal(d)
	}
	return plan
}

func TestConnectionPlanDefersDeclaredTemplateButCreateRequiresLiveApproval(t *testing.T) {
	ctx := context.Background()
	f, c := newMCPFixture(t)
	template := &PaperclipStdioTemplateResource{client: c}
	plan := stdioTemplatePlan(t, types.StringValue("company-1"), types.StringValue("planned-template"))
	templateResponse := resource.ModifyPlanResponse{Plan: plan}
	template.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan}, &templateResponse)
	if templateResponse.Diagnostics.HasError() {
		t.Fatal(templateResponse.Diagnostics)
	}
	connection := &PaperclipConnectionResource{client: c}
	var s resource.SchemaResponse
	connection.Schema(ctx, resource.SchemaRequest{}, &s)
	model := legacyConnectionModel(t)
	model.ID = types.StringUnknown()
	model.Transport = types.StringValue("local_stdio")
	model.Config = types.StringValue(`{"templateId":"planned-template"}`)
	model.CredentialRefs = types.StringNull()
	connectionPlan := tfsdk.Plan{Schema: s.Schema}
	if d := connectionPlan.Set(ctx, model); d.HasError() {
		t.Fatal(d)
	}
	response := resource.ModifyPlanResponse{Plan: connectionPlan}
	connection.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: connectionPlan}, &response)
	if response.Diagnostics.HasError() || len(f.mutations) != 0 {
		t.Fatalf("declared template must plan without mutation: %v writes=%v", response.Diagnostics, f.mutations)
	}
	created := resource.CreateResponse{State: tfsdk.State{Schema: s.Schema}}
	connection.Create(ctx, resource.CreateRequest{Plan: connectionPlan}, &created)
	if !created.Diagnostics.HasError() || created.Diagnostics.Errors()[0].Summary() != "Invalid approved stdio template" || len(f.mutations) != 0 || !created.State.Raw.IsNull() {
		t.Fatalf("plan declaration bypassed apply-time approval: %v writes=%v", created.Diagnostics, f.mutations)
	}
}

func TestConnectionPlanRejectsUndeclaredOrDifferentlyScopedTemplate(t *testing.T) {
	for _, tc := range []struct {
		name, companyID, templateID string
		independentClient           bool
	}{
		{name: "undeclared", companyID: "company-1", templateID: "another-template"},
		{name: "other company", companyID: "other-company", templateID: "missing-template"},
		{name: "other provider", companyID: "company-1", templateID: "missing-template", independentClient: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			f, c := newMCPFixture(t)
			templateClient := c
			if tc.independentClient {
				_, templateClient = newMCPFixture(t)
			}
			template := &PaperclipStdioTemplateResource{client: templateClient}
			plan := stdioTemplatePlan(t, types.StringValue(tc.companyID), types.StringValue(tc.templateID))
			response := resource.ModifyPlanResponse{Plan: plan}
			template.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan}, &response)
			if response.Diagnostics.HasError() {
				t.Fatal(response.Diagnostics)
			}
			model := legacyConnectionModel(t)
			model.ID = types.StringUnknown()
			model.Transport = types.StringValue("local_stdio")
			model.Config = types.StringValue(`{"templateId":"missing-template"}`)
			d := (&PaperclipConnectionResource{client: c}).validateConnection(ctx, model, PaperclipConnectionResourceModel{}, true)
			if !d.HasError() || d.Errors()[0].Summary() != "Invalid approved stdio template" || len(f.mutations) != 0 {
				t.Fatalf("missing template did not fail at plan: %v writes=%v", d, f.mutations)
			}
		})
	}
}

func TestStdioTemplatePlanDoesNotApproveUnknownOrDestroyIdentities(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		companyID, templateID types.String
		destroy, unconfigured bool
	}{
		{name: "unknown company", companyID: types.StringUnknown(), templateID: types.StringValue("planned-template")},
		{name: "unknown template", companyID: types.StringValue("company-1"), templateID: types.StringUnknown()},
		{name: "destroy", companyID: types.StringValue("company-1"), templateID: types.StringValue("planned-template"), destroy: true},
		{name: "unconfigured", companyID: types.StringValue("company-1"), templateID: types.StringValue("planned-template"), unconfigured: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, c := newMCPFixture(t)
			r := &PaperclipStdioTemplateResource{client: c}
			if tc.unconfigured {
				r.client = nil
			}
			plan := stdioTemplatePlan(t, tc.companyID, tc.templateID)
			if tc.destroy {
				plan.Raw = tftypes.NewValue(plan.Schema.Type().TerraformType(context.Background()), nil)
			}
			response := resource.ModifyPlanResponse{Plan: plan}
			r.ModifyPlan(context.Background(), resource.ModifyPlanRequest{Plan: plan}, &response)
			if response.Diagnostics.HasError() || c.HasPlannedStdioTemplate(tc.companyID.ValueString(), tc.templateID.ValueString()) || len(f.mutations) != 0 {
				t.Fatalf("non-create identity was approved: %v", response.Diagnostics)
			}
		})
	}
}

func TestStdioTemplatePlanReportsMalformedPlanWithoutApprovingIdentity(t *testing.T) {
	_, c := newMCPFixture(t)
	r := &PaperclipStdioTemplateResource{client: c}
	plan := stdioTemplatePlan(t, types.StringValue("company-1"), types.StringValue("planned-template"))
	plan.Raw = tftypes.NewValue(tftypes.String, "malformed-plan")
	response := resource.ModifyPlanResponse{Plan: plan}
	r.ModifyPlan(context.Background(), resource.ModifyPlanRequest{Plan: plan}, &response)
	if !response.Diagnostics.HasError() || c.HasPlannedStdioTemplate("company-1", "planned-template") {
		t.Fatalf("malformed plan approved a template: %v", response.Diagnostics)
	}
}
