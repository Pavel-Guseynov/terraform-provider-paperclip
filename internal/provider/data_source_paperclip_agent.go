package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"git.guseynov.org/guseynov/paperclip/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource              = &PaperclipAgentDataSource{}
	_ datasource.DataSourceWithConfigure = &PaperclipAgentDataSource{}
)

func NewPaperclipAgentDataSource() datasource.DataSource {
	return &PaperclipAgentDataSource{}
}

type PaperclipAgentDataSource struct {
	client *client.Client
}

type PaperclipAgentDataSourceModel struct {
	ID            types.String `tfsdk:"id"`
	CompanyID     types.String `tfsdk:"company_id"`
	Name          types.String `tfsdk:"name"`
	Role          types.String `tfsdk:"role"`
	Title         types.String `tfsdk:"title"`
	AdapterType   types.String `tfsdk:"adapter_type"`
	Status        types.String `tfsdk:"status"`
	RuntimeConfig types.String `tfsdk:"runtime_config"`
	AdapterConfig types.String `tfsdk:"adapter_config"`
	Metadata      types.String `tfsdk:"metadata"`
	Instructions  types.String `tfsdk:"instructions"`
}

func (d *PaperclipAgentDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_agent"
}

func (d *PaperclipAgentDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Reads Paperclip agent details.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Agent unique identifier.",
				Optional:    true,
				Computed:    true,
			},
			"company_id": schema.StringAttribute{
				Description: "The Paperclip company ID.",
				Optional:    true,
				Computed:    true,
			},
			"name": schema.StringAttribute{
				Description: "Agent name.",
				Optional:    true,
				Computed:    true,
			},
			"role": schema.StringAttribute{
				Description: "Agent role.",
				Computed:    true,
			},
			"title": schema.StringAttribute{
				Description: "Agent title.",
				Computed:    true,
			},
			"adapter_type": schema.StringAttribute{
				Description: "Adapter type.",
				Computed:    true,
			},
			"status": schema.StringAttribute{
				Description: "Agent status.",
				Computed:    true,
			},
			"runtime_config": schema.StringAttribute{
				Description: "JSON runtime configuration string.",
				Computed:    true,
			},
			"adapter_config": schema.StringAttribute{
				Description: "JSON adapter configuration string.",
				Computed:    true,
			},
			"metadata": schema.StringAttribute{
				Description: "JSON metadata object.",
				Computed:    true,
			},
			"instructions": schema.StringAttribute{
				Description: "Managed AGENTS.md instructions for this agent.",
				Computed:    true,
			},
		},
	}
}

func (d *PaperclipAgentDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *PaperclipAgentDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config PaperclipAgentDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var agent *client.PaperclipAgent
	if config.ID.ValueString() != "" {
		a, err := d.client.GetAgent(ctx, config.ID.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Error reading Paperclip agent", err.Error())
			return
		}
		if config.Name.ValueString() != "" && !strings.EqualFold(a.Name, config.Name.ValueString()) {
			resp.Diagnostics.AddError("Agent Not Found", fmt.Sprintf("Agent with id %q has name %q, which does not match requested name %q", a.ID, a.Name, config.Name.ValueString()))
			return
		}
		if config.CompanyID.ValueString() != "" && a.CompanyID != config.CompanyID.ValueString() {
			resp.Diagnostics.AddError("Agent Not Found", fmt.Sprintf("Agent with id %q belongs to company %q, not %q", a.ID, a.CompanyID, config.CompanyID.ValueString()))
			return
		}
		agent = a
	} else if config.CompanyID.ValueString() != "" && config.Name.ValueString() != "" {
		agents, err := d.client.ListAgents(ctx, config.CompanyID.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Error listing Paperclip agents", err.Error())
			return
		}
		var matches []client.PaperclipAgent
		for _, a := range agents {
			if strings.EqualFold(a.Name, config.Name.ValueString()) {
				matches = append(matches, a)
			}
		}
		if len(matches) == 0 {
			resp.Diagnostics.AddError("Agent Not Found", fmt.Sprintf("Agent %s in company %s not found", config.Name.ValueString(), config.CompanyID.ValueString()))
			return
		}
		if len(matches) > 1 {
			var ids []string
			for _, m := range matches {
				ids = append(ids, m.ID)
			}
			resp.Diagnostics.AddError("Ambiguous Agent Name", fmt.Sprintf("multiple agents found with name %q in company %s: candidate IDs: %s", config.Name.ValueString(), config.CompanyID.ValueString(), strings.Join(ids, ", ")))
			return
		}
		agent = &matches[0]
	} else {
		resp.Diagnostics.AddError("Invalid Arguments", "Must specify either 'id' or both 'company_id' and 'name'")
		return
	}

	config.ID = types.StringValue(agent.ID)
	if agent.CompanyID != "" {
		config.CompanyID = types.StringValue(agent.CompanyID)
	}
	config.Name = types.StringValue(agent.Name)
	config.Role = types.StringValue(agent.Role)
	if agent.Title != "" {
		config.Title = types.StringValue(agent.Title)
	} else {
		config.Title = types.StringNull()
	}
	config.AdapterType = types.StringValue(agent.AdapterType)
	if agent.Status != "" {
		config.Status = types.StringValue(agent.Status)
	} else {
		config.Status = types.StringNull()
	}
	if agent.RuntimeConfig != nil {
		data, _ := json.Marshal(agent.RuntimeConfig)
		config.RuntimeConfig = types.StringValue(string(data))
	} else {
		config.RuntimeConfig = types.StringNull()
	}
	if agent.AdapterConfig != nil {
		data, _ := json.Marshal(agent.AdapterConfig)
		config.AdapterConfig = types.StringValue(string(data))
	} else {
		config.AdapterConfig = types.StringNull()
	}
	if agent.Metadata != nil {
		data, _ := json.Marshal(agent.Metadata)
		config.Metadata = types.StringValue(string(data))
	} else {
		config.Metadata = types.StringNull()
	}

	instructions, err := d.client.GetAgentInstructionsFile(ctx, agent.ID, "AGENTS.md")
	if err == nil && instructions != "" {
		config.Instructions = types.StringValue(instructions)
	} else {
		config.Instructions = types.StringNull()
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}
