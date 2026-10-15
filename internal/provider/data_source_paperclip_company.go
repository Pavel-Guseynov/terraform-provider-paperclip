package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/Pavel-Guseynov/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource              = &PaperclipCompanyDataSource{}
	_ datasource.DataSourceWithConfigure = &PaperclipCompanyDataSource{}
)

func NewPaperclipCompanyDataSource() datasource.DataSource {
	return &PaperclipCompanyDataSource{}
}

type PaperclipCompanyDataSource struct {
	client *client.Client
}

type PaperclipCompanyDataSourceModel struct {
	ID                               types.String `tfsdk:"id"`
	Name                             types.String `tfsdk:"name"`
	Description                      types.String `tfsdk:"description"`
	Status                           types.String `tfsdk:"status"`
	DefaultResponsibleUserID         types.String `tfsdk:"default_responsible_user_id"`
	RequireBoardApprovalForNewAgents types.Bool   `tfsdk:"require_board_approval_for_new_agents"`
	InteractionResolverGovernance    types.String `tfsdk:"interaction_resolver_governance"`
	FeedbackDataSharingEnabled       types.Bool   `tfsdk:"feedback_data_sharing_enabled"`
	LogoAssetID                      types.String `tfsdk:"logo_asset_id"`
}

func (d *PaperclipCompanyDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_company"
}

func (d *PaperclipCompanyDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Reads Paperclip company details.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Company unique identifier.",
				Optional:    true,
				Computed:    true,
			},
			"name": schema.StringAttribute{
				Description: "Company name.",
				Optional:    true,
				Computed:    true,
			},
			"description": schema.StringAttribute{
				Description: "Company description.",
				Computed:    true,
			},
			"status": schema.StringAttribute{
				Description: "Administrative status of the company (active, paused, or archived).",
				Computed:    true,
			},
			"default_responsible_user_id": schema.StringAttribute{
				Description: "Default responsible user identifier.",
				Computed:    true,
			},
			"require_board_approval_for_new_agents": schema.BoolAttribute{
				Description: "Whether adding new agents requires board approval.",
				Computed:    true,
			},
			"interaction_resolver_governance": schema.StringAttribute{
				Description: "Interaction resolver governance policy configuration as a JSON object.",
				Computed:    true,
			},
			"feedback_data_sharing_enabled": schema.BoolAttribute{
				Description: "Whether feedback data sharing is enabled for the company.",
				Computed:    true,
			},
			"logo_asset_id": schema.StringAttribute{
				Description: "Logo asset reference identifier.",
				Computed:    true,
			},
		},
	}
}

func (d *PaperclipCompanyDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Data Source Configure Type",
			fmt.Sprintf("Expected *client.Client, got: %T", req.ProviderData),
		)
		return
	}
	d.client = c
}

func (d *PaperclipCompanyDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config PaperclipCompanyDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var comp *client.PaperclipCompany
	if config.ID.ValueString() != "" {
		c, err := d.client.GetCompany(ctx, config.ID.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Error reading Paperclip company", err.Error())
			return
		}
		if config.Name.ValueString() != "" && !strings.EqualFold(c.Name, config.Name.ValueString()) {
			resp.Diagnostics.AddError("Company Not Found", fmt.Sprintf("Company with id %q has name %q, which does not match requested name %q", c.ID, c.Name, config.Name.ValueString()))
			return
		}
		comp = c
	} else if config.Name.ValueString() != "" {
		c, err := d.client.FindCompanyByName(ctx, config.Name.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Error reading Paperclip company", err.Error())
			return
		}
		comp = c
	} else {
		resp.Diagnostics.AddError("Invalid Arguments", "Either id or name must be specified")
		return
	}

	config.ID = types.StringValue(comp.ID)
	config.Name = types.StringValue(comp.Name)
	config.Description = stringOrNull(comp.Description)
	if comp.Status != "" {
		config.Status = types.StringValue(comp.Status)
	} else {
		config.Status = types.StringValue("active")
	}
	config.DefaultResponsibleUserID = stringOrNull(comp.DefaultResponsibleUserID)
	config.RequireBoardApprovalForNewAgents = types.BoolValue(comp.RequireBoardApprovalForNewAgents)
	config.InteractionResolverGovernance = refreshSemanticJSON(config.InteractionResolverGovernance, comp.InteractionResolverGovernance)
	config.FeedbackDataSharingEnabled = types.BoolValue(comp.FeedbackDataSharingEnabled)
	config.LogoAssetID = stringOrNull(comp.LogoAssetID)

	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}
