package provider

import (
	"context"
	"fmt"

	"git.guseynov.org/guseynov/paperclip/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// This resource owns PATH inheritance on an existing producer-owned Local row,
// not the row's creation, deletion, other bindings, or service runtime PATH.
type PaperclipLocalEnvironmentResource struct{ client *client.Client }

type PaperclipLocalEnvironmentModel struct {
	CompanyID          types.String `tfsdk:"company_id"`
	ID                 types.String `tfsdk:"id"`
	InheritServicePath types.Bool   `tfsdk:"inherit_service_path"`
}

func NewPaperclipLocalEnvironmentResource() resource.Resource {
	return &PaperclipLocalEnvironmentResource{}
}

func (r *PaperclipLocalEnvironmentResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_local_environment"
}

func (r *PaperclipLocalEnvironmentResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Owns service PATH inheritance on the existing Paperclip Local environment. Removing this resource releases management without deleting the environment or restoring an obsolete override.",
		Attributes: map[string]schema.Attribute{
			"company_id":           schema.StringAttribute{Required: true, Description: "Company context for native environment binding reconciliation."},
			"id":                   schema.StringAttribute{Computed: true, Description: "Existing Local environment ID; creation or replacement of the environment is not supported.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"inherit_service_path": schema.BoolAttribute{Required: true, Description: "Must be true. Read reports false when a persisted PATH binding overrides the Nix service PATH, so refresh plans its removal."},
		},
	}
}

func (r *PaperclipLocalEnvironmentResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Resource Configure Type", fmt.Sprintf("Expected *client.Client, got: %T", req.ProviderData))
		return
	}
	r.client = c
}

func (r *PaperclipLocalEnvironmentResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var model PaperclipLocalEnvironmentModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &model)...)
	if !model.InheritServicePath.IsUnknown() && !model.InheritServicePath.ValueBool() {
		resp.Diagnostics.AddError("Service PATH inheritance is required", "Local execution must inherit the Nix service PATH; persisted PATH overrides are not supported.")
	}
}

func (r *PaperclipLocalEnvironmentResource) apply(ctx context.Context, plan tfsdk.Plan, state *tfsdk.State) error {
	var model PaperclipLocalEnvironmentModel
	if diags := plan.Get(ctx, &model); diags.HasError() {
		return fmt.Errorf("invalid local environment plan: %s", diags.Errors())
	}
	if !model.InheritServicePath.ValueBool() {
		return fmt.Errorf("local execution must inherit the service PATH")
	}
	if model.ID.IsUnknown() || model.ID.IsNull() {
		env, err := r.client.FindLocalEnvironment(ctx, model.CompanyID.ValueString())
		if err != nil {
			return err
		}
		model.ID = types.StringValue(env.ID)
	}
	if _, err := r.client.InheritLocalServicePath(ctx, model.CompanyID.ValueString(), model.ID.ValueString()); err != nil {
		return err
	}
	if diags := state.Set(ctx, &model); diags.HasError() {
		return fmt.Errorf("invalid local environment state: %s", diags.Errors())
	}
	return nil
}

func (r *PaperclipLocalEnvironmentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if err := r.apply(ctx, req.Plan, &resp.State); err != nil {
		resp.Diagnostics.AddError("Error reconciling Local environment PATH", err.Error())
	}
}

func (r *PaperclipLocalEnvironmentResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if err := r.apply(ctx, req.Plan, &resp.State); err != nil {
		resp.Diagnostics.AddError("Error reconciling Local environment PATH", err.Error())
	}
}

func (r *PaperclipLocalEnvironmentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var model PaperclipLocalEnvironmentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}
	env, err := r.client.GetLocalEnvironment(ctx, model.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error reading Local environment PATH", err.Error())
		return
	}
	model.InheritServicePath = types.BoolValue(env.InheritsServicePath())
	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
}

func (r *PaperclipLocalEnvironmentResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
	// Relinquish this binding only; the producer owns the environment lifecycle.
}
