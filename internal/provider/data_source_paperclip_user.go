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
	_ datasource.DataSource              = &PaperclipUserDataSource{}
	_ datasource.DataSourceWithConfigure = &PaperclipUserDataSource{}
)

func NewPaperclipUserDataSource() datasource.DataSource {
	return &PaperclipUserDataSource{}
}

type PaperclipUserDataSource struct {
	client *client.Client
}

type PaperclipUserDataSourceModel struct {
	ID                           types.String `tfsdk:"id"`
	Email                        types.String `tfsdk:"email"`
	Name                         types.String `tfsdk:"name"`
	CompanyID                    types.String `tfsdk:"company_id"`
	Image                        types.String `tfsdk:"image"`
	IsInstanceAdmin              types.Bool   `tfsdk:"is_instance_admin"`
	ActiveCompanyMembershipCount types.Int64  `tfsdk:"active_company_membership_count"`
}

func (d *PaperclipUserDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_user"
}

func (d *PaperclipUserDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Reads details for a single Paperclip user by ID, email, or name.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "User unique identifier.",
				Optional:    true,
				Computed:    true,
			},
			"email": schema.StringAttribute{
				Description: "User email address.",
				Optional:    true,
				Computed:    true,
			},
			"name": schema.StringAttribute{
				Description: "User display name.",
				Optional:    true,
				Computed:    true,
			},
			"company_id": schema.StringAttribute{
				Description: "Optional company identifier to restrict lookup to company user directory.",
				Optional:    true,
			},
			"image": schema.StringAttribute{
				Description: "Profile image URL or asset reference.",
				Computed:    true,
			},
			"is_instance_admin": schema.BoolAttribute{
				Description: "Whether user is an instance administrator.",
				Computed:    true,
			},
			"active_company_membership_count": schema.Int64Attribute{
				Description: "Number of active company memberships.",
				Computed:    true,
			},
		},
	}
}

func (d *PaperclipUserDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *PaperclipUserDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config PaperclipUserDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	wantedID := config.ID.ValueString()
	wantedEmail := strings.ToLower(config.Email.ValueString())
	wantedName := strings.ToLower(config.Name.ValueString())

	if wantedID == "" && wantedEmail == "" && wantedName == "" {
		resp.Diagnostics.AddError(
			"Missing User Lookup Identifier",
			"At least one of id, email, or name must be specified to look up a user.",
		)
		return
	}

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
			matchID := wantedID != "" && (du.PrincipalID == wantedID || du.User.ID == wantedID)
			matchEmail := wantedEmail != "" && du.User.Email != nil && strings.ToLower(*du.User.Email) == wantedEmail
			matchName := wantedName != "" && du.User.Name != nil && strings.ToLower(*du.User.Name) == wantedName
			if matchID || matchEmail || matchName {
				config.ID = types.StringValue(du.User.ID)
				if du.User.Email != nil {
					config.Email = types.StringValue(*du.User.Email)
				} else {
					config.Email = types.StringNull()
				}
				if du.User.Name != nil {
					config.Name = types.StringValue(*du.User.Name)
				} else {
					config.Name = types.StringNull()
				}
				if du.User.Image != nil {
					config.Image = types.StringValue(*du.User.Image)
				} else {
					config.Image = types.StringNull()
				}
				config.IsInstanceAdmin = types.BoolValue(false)
				config.ActiveCompanyMembershipCount = types.Int64Value(1)
				resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
				return
			}
		}
		resp.Diagnostics.AddError("User Not Found", fmt.Sprintf("User matching requested criteria not found in company %s user directory", cid))
		return
	}

	query := wantedEmail
	if query == "" {
		query = wantedName
	}
	if query == "" {
		query = wantedID
	}

	adminUsers, err := d.client.ListAdminUsers(ctx, query)
	if err != nil {
		resp.Diagnostics.AddError("Error reading admin users", err.Error())
		return
	}

	for _, au := range adminUsers {
		matchID := wantedID != "" && au.ID == wantedID
		matchEmail := wantedEmail != "" && au.Email != nil && strings.ToLower(*au.Email) == wantedEmail
		matchName := wantedName != "" && au.Name != nil && strings.ToLower(*au.Name) == wantedName
		if matchID || matchEmail || matchName {
			config.ID = types.StringValue(au.ID)
			if au.Email != nil {
				config.Email = types.StringValue(*au.Email)
			} else {
				config.Email = types.StringNull()
			}
			if au.Name != nil {
				config.Name = types.StringValue(*au.Name)
			} else {
				config.Name = types.StringNull()
			}
			if au.Image != nil {
				config.Image = types.StringValue(*au.Image)
			} else {
				config.Image = types.StringNull()
			}
			config.IsInstanceAdmin = types.BoolValue(au.IsInstanceAdmin)
			config.ActiveCompanyMembershipCount = types.Int64Value(int64(au.ActiveCompanyMembershipCount))
			resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
			return
		}
	}

	resp.Diagnostics.AddError("User Not Found", "User matching requested identifier was not found.")
}
