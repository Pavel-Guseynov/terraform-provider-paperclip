package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"git.guseynov.org/guseynov/paperclip/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	ephemeralschema "github.com/hashicorp/terraform-plugin-framework/ephemeral/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ provider.Provider                        = &PaperclipProvider{}
	_ provider.ProviderWithEphemeralResources  = &PaperclipProvider{}
	_ ephemeral.EphemeralResource              = &PaperclipBoardKeyEphemeralResource{}
	_ ephemeral.EphemeralResourceWithConfigure = &PaperclipBoardKeyEphemeralResource{}
	_ ephemeral.EphemeralResourceWithClose     = &PaperclipBoardKeyEphemeralResource{}
)

type PaperclipProvider struct {
	version string
}

type PaperclipProviderModel struct {
	Endpoint          types.String `tfsdk:"endpoint"`
	Token             types.String `tfsdk:"token"`
	PaperclipEndpoint types.String `tfsdk:"paperclip_endpoint"`
	PaperclipToken    types.String `tfsdk:"paperclip_token"`
}

// stringOrNull resolves an API string into a known attribute value: empty API
// values become null so computed attributes never remain unknown after apply.
func stringOrNull(v string) types.String {
	if v == "" {
		return types.StringNull()
	}
	return types.StringValue(v)
}

// jsonStringOrNull resolves an API JSON object into a known attribute value:
// absent objects become null so computed attributes never remain unknown
// after apply.
func jsonStringOrNull(m map[string]interface{}) types.String {
	if m == nil {
		return types.StringNull()
	}
	data, err := json.Marshal(m)
	if err != nil {
		return types.StringNull()
	}
	return types.StringValue(string(data))
}

func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &PaperclipProvider{
			version: version,
		}
	}
}

func (p *PaperclipProvider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "paperclip"
	resp.Version = p.version
}

func (p *PaperclipProvider) Schema(ctx context.Context, req provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "OpenTofu and Terraform provider for Paperclip desired-state reconciliation.",
		Attributes: map[string]schema.Attribute{
			"endpoint": schema.StringAttribute{
				Description: "The HTTP endpoint of the Paperclip board API (defaults to http://127.0.0.1:3100, or PAPERCLIP_API_URL / PAPERCLIP_ENDPOINT env var).",
				Optional:    true,
			},
			"token": schema.StringAttribute{
				Description: "The API bearer token for Paperclip (or PAPERCLIP_API_KEY / PAPERCLIP_BOARD_TOKEN env var).",
				Optional:    true,
				Sensitive:   true,
			},
			"paperclip_endpoint": schema.StringAttribute{
				Description: "Optional alias for endpoint for backward compatibility with the legacy mixed provider configuration.",
				Optional:    true,
			},
			"paperclip_token": schema.StringAttribute{
				Description: "Optional alias for token for backward compatibility with the legacy mixed provider configuration.",
				Optional:    true,
				Sensitive:   true,
			},
		},
	}
}

func (p *PaperclipProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var data PaperclipProviderModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpoint := os.Getenv("PAPERCLIP_API_URL")
	if endpoint == "" {
		endpoint = os.Getenv("PAPERCLIP_ENDPOINT")
	}
	if endpoint == "" {
		endpoint = os.Getenv("PAPERCLIP_URL")
	}
	if !data.PaperclipEndpoint.IsNull() && !data.PaperclipEndpoint.IsUnknown() {
		endpoint = data.PaperclipEndpoint.ValueString()
	}
	if !data.Endpoint.IsNull() && !data.Endpoint.IsUnknown() {
		endpoint = data.Endpoint.ValueString()
	}
	if endpoint == "" {
		endpoint = "http://127.0.0.1:3100"
	}

	token := os.Getenv("PAPERCLIP_API_KEY")
	if token == "" {
		token = os.Getenv("PAPERCLIP_BOARD_TOKEN")
	}
	if token == "" {
		token = os.Getenv("PAPERCLIP_TOKEN")
	}
	if !data.PaperclipToken.IsNull() && !data.PaperclipToken.IsUnknown() {
		token = data.PaperclipToken.ValueString()
	}
	if !data.Token.IsNull() && !data.Token.IsUnknown() {
		token = data.Token.ValueString()
	}

	c, err := client.NewClient(endpoint, token)
	if err != nil {
		resp.Diagnostics.AddAttributeError(
			path.Root("endpoint"),
			"Invalid Paperclip Client Configuration",
			err.Error(),
		)
		return
	}

	resp.DataSourceData = c
	resp.EphemeralResourceData = c
	resp.ResourceData = c
}

type PaperclipBoardKeyEphemeralResource struct {
	client *client.Client
	keyID  string
}

type PaperclipBoardKeyEphemeralResourceModel struct {
	KeyID types.String `tfsdk:"key_id"`
	Token types.String `tfsdk:"token"`
}

func NewPaperclipBoardKeyEphemeralResource() ephemeral.EphemeralResource {
	return &PaperclipBoardKeyEphemeralResource{}
}

func (r *PaperclipBoardKeyEphemeralResource) Metadata(ctx context.Context, req ephemeral.MetadataRequest, resp *ephemeral.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_board_key"
}

func (r *PaperclipBoardKeyEphemeralResource) Schema(ctx context.Context, req ephemeral.SchemaRequest, resp *ephemeral.SchemaResponse) {
	resp.Schema = ephemeralschema.Schema{
		Description: "Produces a Paperclip board key token only during apply for ephemeral or write-only delivery.",
		Attributes: map[string]ephemeralschema.Attribute{
			"key_id": ephemeralschema.StringAttribute{
				Description: "Managed Paperclip board key ID.",
				Required:    true,
			},
			"token": ephemeralschema.StringAttribute{
				Description: "Ephemeral bearer token delivered only to an ephemeral or write-only consumer.",
				Computed:    true,
				Sensitive:   true,
			},
		},
	}
}

func (r *PaperclipBoardKeyEphemeralResource) Configure(ctx context.Context, req ephemeral.ConfigureRequest, resp *ephemeral.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Ephemeral Resource Configure Type",
			fmt.Sprintf("Expected *client.Client, got: %T", req.ProviderData),
		)
		return
	}
	r.client = c
}

func (r *PaperclipBoardKeyEphemeralResource) Open(ctx context.Context, req ephemeral.OpenRequest, resp *ephemeral.OpenResponse) {
	var data PaperclipBoardKeyEphemeralResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Paperclip Client", "Expected a configured Paperclip provider client.")
		return
	}

	if data.KeyID.IsNull() || data.KeyID.IsUnknown() || data.KeyID.ValueString() == "" {
		if req.ClientCapabilities.DeferralAllowed {
			resp.Deferred = &ephemeral.Deferred{
				Reason: ephemeral.DeferredReasonEphemeralResourceConfigUnknown,
			}
			return
		}
		resp.Diagnostics.AddError("Missing key_id", "A valid managed key_id is required to deliver the ephemeral board key token.")
		return
	}

	keyID := data.KeyID.ValueString()
	token, ok := r.client.GetMintedToken(keyID)
	if !ok {
		if r.client.IsTokenDelivered(keyID) {
			data.Token = types.StringNull()
			resp.Diagnostics.Append(resp.Result.Set(ctx, &data)...)
			return
		}
		if req.ClientCapabilities.DeferralAllowed {
			resp.Deferred = &ephemeral.Deferred{
				Reason: ephemeral.DeferredReasonEphemeralResourceConfigUnknown,
			}
			return
		}
		resp.Diagnostics.AddError(
			"Token Unavailable",
			fmt.Sprintf("The bearer token for board key %q is unavailable. Tokens are only delivered during the initial apply or generation rotation in which they are minted.", keyID),
		)
		return
	}

	data.Token = types.StringValue(token)
	r.keyID = keyID
	r.client.PushOpenedBoardKey(keyID)

	resp.Diagnostics.Append(resp.Result.Set(ctx, &data)...)
}

func (r *PaperclipBoardKeyEphemeralResource) Close(ctx context.Context, req ephemeral.CloseRequest, resp *ephemeral.CloseResponse) {
	if r.client == nil {
		return
	}
	keyID := r.client.PopOpenedBoardKey()
	if keyID == "" {
		keyID = r.keyID
	}
	if keyID != "" {
		if err := r.client.ConfirmDelivery(ctx, keyID); err != nil {
			resp.Diagnostics.AddError("Error confirming board key delivery", err.Error())
		}
	}
}


func (p *PaperclipProvider) EphemeralResources(ctx context.Context) []func() ephemeral.EphemeralResource {
	return []func() ephemeral.EphemeralResource{
		NewPaperclipBoardKeyEphemeralResource,
		NewPaperclipAgentKeyEphemeralResource,
	}
}

func (p *PaperclipProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewPaperclipCompanyResource,
		NewPaperclipProjectResource,
		NewPaperclipProjectLeadResource,
		NewPaperclipWorkspaceResource,
		NewPaperclipLocalEnvironmentResource,
		NewPaperclipSkillResource,
		NewPaperclipSecretResource,
		NewPaperclipAgentResource,
		NewPaperclipAppResource,
		NewPaperclipConnectionResource,
		NewPaperclipPolicyResource,
		NewPaperclipStdioTemplateResource,
		NewPaperclipConnectionInstallResource,
		NewPaperclipAppPermissionResource,
		NewPaperclipBoardKeyResource,
		NewPaperclipAgentKeyResource,
	}
}

func (p *PaperclipProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewPaperclipCompanyDataSource,
		NewPaperclipProjectDataSource,
		NewPaperclipAgentDataSource,
		NewPaperclipSkillDataSource,
	}
}
