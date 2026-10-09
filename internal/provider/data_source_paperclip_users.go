package provider

import (
	"context"
	"fmt"
	"strings"

	"git.guseynov.org/guseynov/paperclip/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource              = &PaperclipUsersDataSource{}
	_ datasource.DataSourceWithConfigure = &PaperclipUsersDataSource{}
)

func NewPaperclipUsersDataSource() datasource.DataSource {
	return &PaperclipUsersDataSource{}
}

type PaperclipUsersDataSource struct {
	client *client.Client
}

type PaperclipUserDataModel struct {
	ID                           types.String `tfsdk:"id"`
	Email                        types.String `tfsdk:"email"`
	Name                         types.String `tfsdk:"name"`
	Image                        types.String `tfsdk:"image"`
	IsInstanceAdmin              types.Bool   `tfsdk:"is_instance_admin"`
	ActiveCompanyMembershipCount types.Int64  `tfsdk:"active_company_membership_count"`
}

type PaperclipUsersDataSourceModel struct {
	ID        types.String             `tfsdk:"id"`
	Query     types.String             `tfsdk:"query"`
	CompanyID types.String             `tfsdk:"company_id"`
	Users     []PaperclipUserDataModel `tfsdk:"users"`
}

func (d *PaperclipUsersDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_users"
}

func (d *PaperclipUsersDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Reads a list of Paperclip users by query or company scope.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Placeholder identifier for the data source.",
				Computed:    true,
			},
			"query": schema.StringAttribute{
				Description: "Optional search query string (matches name or email).",
				Optional:    true,
			},
			"company_id": schema.StringAttribute{
				Description: "Optional company identifier to restrict listing to company members.",
				Optional:    true,
			},
			"users": schema.ListNestedAttribute{
				Description: "List of matching users.",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							Description: "User ID.",
							Computed:    true,
						},
						"email": schema.StringAttribute{
							Description: "User email address.",
							Computed:    true,
						},
						"name": schema.StringAttribute{
							Description: "User display name.",
							Computed:    true,
						},
						"image": schema.StringAttribute{
							Description: "User image URL.",
							Computed:    true,
						},
						"is_instance_admin": schema.BoolAttribute{
							Description: "Whether the user is an instance administrator.",
							Computed:    true,
						},
						"active_company_membership_count": schema.Int64Attribute{
							Description: "Count of active company memberships.",
							Computed:    true,
						},
					},
				},
			},
		},
	}
}

func (d *PaperclipUsersDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	cli, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Provider Data", fmt.Sprintf("Expected *client.Client, got: %T", req.ProviderData))
		return
	}
	d.client = cli
}

func (d *PaperclipUsersDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config PaperclipUsersDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	query := ""
	if !config.Query.IsNull() && !config.Query.IsUnknown() {
		query = config.Query.ValueString()
	}

	needle := strings.ToLower(query)

	var userList []PaperclipUserDataModel

	if !config.CompanyID.IsNull() && !config.CompanyID.IsUnknown() && config.CompanyID.ValueString() != "" {
		cid := config.CompanyID.ValueString()
		dirUsers, err := d.client.GetCompanyUserDirectory(ctx, cid)
		if err != nil {
			resp.Diagnostics.AddError("Error reading company user directory", err.Error())
			return
		}
		for _, du := range dirUsers {
			if du.User == nil {
				continue
			}
			email := ""
			if du.User.Email != nil {
				email = *du.User.Email
			}
			name := ""
			if du.User.Name != nil {
				name = *du.User.Name
			}
			if needle != "" && !strings.Contains(strings.ToLower(email), needle) && !strings.Contains(strings.ToLower(name), needle) {
				continue
			}
			item := PaperclipUserDataModel{
				ID:                           types.StringValue(du.User.ID),
				IsInstanceAdmin:              types.BoolValue(false),
				ActiveCompanyMembershipCount: types.Int64Value(1),
			}
			if du.User.Email != nil {
				item.Email = types.StringValue(*du.User.Email)
			} else {
				item.Email = types.StringNull()
			}
			if du.User.Name != nil {
				item.Name = types.StringValue(*du.User.Name)
			} else {
				item.Name = types.StringNull()
			}
			if du.User.Image != nil {
				item.Image = types.StringValue(*du.User.Image)
			} else {
				item.Image = types.StringNull()
			}
			userList = append(userList, item)
		}
		config.ID = types.StringValue(fmt.Sprintf("company:%s", cid))
	} else {
		adminUsers, err := d.client.ListAdminUsers(ctx, query)
		if err != nil {
			resp.Diagnostics.AddError("Error reading admin users", err.Error())
			return
		}
		for _, au := range adminUsers {
			item := PaperclipUserDataModel{
				ID:                           types.StringValue(au.ID),
				IsInstanceAdmin:              types.BoolValue(au.IsInstanceAdmin),
				ActiveCompanyMembershipCount: types.Int64Value(int64(au.ActiveCompanyMembershipCount)),
			}
			if au.Email != nil {
				item.Email = types.StringValue(*au.Email)
			} else {
				item.Email = types.StringNull()
			}
			if au.Name != nil {
				item.Name = types.StringValue(*au.Name)
			} else {
				item.Name = types.StringNull()
			}
			if au.Image != nil {
				item.Image = types.StringValue(*au.Image)
			} else {
				item.Image = types.StringNull()
			}
			userList = append(userList, item)
		}
		config.ID = types.StringValue(fmt.Sprintf("query:%s", query))
	}

	config.Users = userList
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}
