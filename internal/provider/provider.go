package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/Pavel-Guseynov/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	ephemeralschema "github.com/hashicorp/terraform-plugin-framework/ephemeral/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var jsonMarshal = json.Marshal

type privateStateSetter interface {
	SetKey(context.Context, string, []byte) diag.Diagnostics
}

type privateStateGetter interface {
	GetKey(context.Context, string) ([]byte, diag.Diagnostics)
}

func setPrivateStateKey(ctx context.Context, private privateStateSetter, key string, val any, diags *diag.Diagnostics) {
	if private == nil {
		return
	}
	b, err := jsonMarshal(val)
	if err != nil {
		diags.AddError("Error encoding private state "+key, err.Error())
		return
	}
	diags.Append(private.SetKey(ctx, key, b)...)
}

func getPrivateStateKey(ctx context.Context, private privateStateGetter, key string, target any, diags *diag.Diagnostics) {
	if private == nil {
		return
	}
	b, d := private.GetKey(ctx, key)
	diags.Append(d...)
	if diags.HasError() || len(b) == 0 {
		return
	}
	if err := json.Unmarshal(b, target); err != nil {
		diags.AddError("Error decoding private state "+key, err.Error())
	}
}

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
	Endpoint types.String `tfsdk:"endpoint"`
	Token    types.String `tfsdk:"token"`
}

// stringOrNull resolves an API string into a known attribute value: empty API
// values become null so computed attributes never remain unknown after apply.
func stringOrNull(v string) types.String {
	if v == "" {
		return types.StringNull()
	}
	return types.StringValue(v)
}

func stringPointerOrNull(v *string) types.String {
	if v == nil || *v == "" {
		return types.StringNull()
	}
	return types.StringValue(*v)
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
				Description: "Absolute HTTP(S) base URL of the Paperclip board API, without credentials, query or fragment. When omitted, uses PAPERCLIP_API_URL or defaults to http://127.0.0.1:3100. An explicitly empty or unknown value is invalid.",
				Optional:    true,
			},
			"token": schema.StringAttribute{
				Description: "Sensitive Paperclip bearer token. When omitted, uses PAPERCLIP_API_KEY. An explicitly empty value selects unauthenticated native access; an unknown value is invalid.",
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

	if data.Endpoint.IsUnknown() {
		resp.Diagnostics.AddAttributeError(path.Root("endpoint"), "Unknown Paperclip Endpoint", "Set endpoint to a known HTTP(S) URL, or omit it to use PAPERCLIP_API_URL or the documented local default.")
	}
	if data.Token.IsUnknown() {
		resp.Diagnostics.AddAttributeError(path.Root("token"), "Unknown Paperclip Token", "Set token to a known value, or omit it to use PAPERCLIP_API_KEY or native unauthenticated access.")
	}
	if resp.Diagnostics.HasError() {
		return
	}

	endpoint := os.Getenv("PAPERCLIP_API_URL")
	if !data.Endpoint.IsNull() {
		endpoint = data.Endpoint.ValueString()
		if endpoint == "" {
			resp.Diagnostics.AddAttributeError(path.Root("endpoint"), "Empty Paperclip Endpoint", "Set endpoint to an HTTP(S) URL, or omit it to use PAPERCLIP_API_URL or the documented local default.")
			return
		}
	}
	token := os.Getenv("PAPERCLIP_API_KEY")
	if !data.Token.IsNull() {
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
}

type PaperclipBoardKeyEphemeralResourceModel struct {
	Name               types.String `tfsdk:"name"`
	Generation         types.Int64  `tfsdk:"generation"`
	ExpiresAt          types.String `tfsdk:"expires_at"`
	RequestedCompanyID types.String `tfsdk:"requested_company_id"`
	KeyID              types.String `tfsdk:"key_id"`
	Token              types.String `tfsdk:"token"`
	Applying           types.Bool   `tfsdk:"applying"`
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
			"name": ephemeralschema.StringAttribute{
				Description: "Board key name.",
				Optional:    true,
			},
			"generation": ephemeralschema.Int64Attribute{
				Description: "Monotonic generation number.",
				Optional:    true,
			},
			"expires_at": ephemeralschema.StringAttribute{
				Description: "Optional RFC3339 expiration timestamp.",
				Optional:    true,
			},
			"requested_company_id": ephemeralschema.StringAttribute{
				Description: "Optional company ID to scope board key activity records.",
				Optional:    true,
			},
			"key_id": ephemeralschema.StringAttribute{
				Description: "Managed Paperclip board key ID.",
				Optional:    true,
				Computed:    true,
			},
			"token": ephemeralschema.StringAttribute{
				Description: "Ephemeral bearer token delivered only to an ephemeral or write-only consumer.",
				Computed:    true,
				Sensitive:   true,
			},
			"applying": ephemeralschema.BoolAttribute{
				Description: "Indicates whether the apply phase is actively running (e.g. tofu.applying). When false, credential minting is deferred.",
				Optional:    true,
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

	name := data.Name.ValueString()
	gen := int64(1)
	if !data.Generation.IsNull() && !data.Generation.IsUnknown() {
		gen = data.Generation.ValueInt64()
	}
	// 0. Plan-phase deferral when applying is false (e.g. via tofu.applying)
	if !data.Applying.IsNull() && !data.Applying.IsUnknown() && !data.Applying.ValueBool() {
		if req.ClientCapabilities.DeferralAllowed {
			resp.Deferred = &ephemeral.Deferred{
				Reason: ephemeral.DeferredReasonUnknown,
			}
			return
		}
		data.KeyID = types.StringNull()
		data.Token = types.StringNull()
		resp.Diagnostics.Append(resp.Result.Set(ctx, &data)...)
		return
	}

	// 1. If key_id is provided directly without name, handle key_id lookup
	if name == "" && !data.KeyID.IsNull() && !data.KeyID.IsUnknown() && data.KeyID.ValueString() != "" {
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
		if resp.Private != nil {
			setPrivateStateKey(ctx, resp.Private, "key_id", keyID, &resp.Diagnostics)
			setPrivateStateKey(ctx, resp.Private, "is_lookup", true, &resp.Diagnostics)
			if resp.Diagnostics.HasError() {
				return
			}
		}
		resp.Diagnostics.Append(resp.Result.Set(ctx, &data)...)
		return
	}

	if name == "" {
		if req.ClientCapabilities.DeferralAllowed {
			resp.Deferred = &ephemeral.Deferred{
				Reason: ephemeral.DeferredReasonUnknown,
			}
			return
		}
		resp.Diagnostics.AddError("Missing name", "A valid name or key_id is required for ephemeral board key.")
		return
	}

	// 2. Check if this generation is already delivered
	if r.client.IsGenerationDelivered(name, true, "", gen) {
		data.Token = types.StringNull()
		if keyID, _, ok := r.client.GetMintedKeyForName(name, true, ""); ok && keyID != "" {
			data.KeyID = types.StringValue(keyID)
		}
		resp.Diagnostics.Append(resp.Result.Set(ctx, &data)...)
		return
	}

	// 3. Defer during plan so plan never mints
	if req.ClientCapabilities.DeferralAllowed {
		resp.Deferred = &ephemeral.Deferred{
			Reason: ephemeral.DeferredReasonUnknown,
		}
		return
	}

	// 4. Mint key at apply time
	var expiresAt *time.Time
	if !data.ExpiresAt.IsNull() && !data.ExpiresAt.IsUnknown() && data.ExpiresAt.ValueString() != "" {
		t, err := time.Parse(time.RFC3339, data.ExpiresAt.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Invalid expires_at format", fmt.Sprintf("expires_at must be RFC3339 format: %s", err))
			return
		}
		expiresAt = &t
	}

	newKey, err := r.client.CreateBoardKey(ctx, name, expiresAt, data.RequestedCompanyID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error minting Paperclip board key", err.Error())
		return
	}

	r.client.RegisterMintedKey(newKey.ID, newKey.Token, name, true, "")

	data.KeyID = types.StringValue(newKey.ID)
	data.Token = types.StringValue(newKey.Token)

	if resp.Private != nil {
		setPrivateStateKey(ctx, resp.Private, "key_id", newKey.ID, &resp.Diagnostics)
		if resp.Diagnostics.HasError() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := r.client.RevokeBoardKey(cleanupCtx, newKey.ID); err != nil {
				resp.Diagnostics.AddError(
					"Failed to revoke unconfirmed board key after private state failure",
					fmt.Sprintf("Failed to revoke unconfirmed board key %s: %s", newKey.ID, err.Error()),
				)
			} else {
				r.client.ClearMintedToken(newKey.ID)
			}
			return
		}
	}

	resp.Diagnostics.Append(resp.Result.Set(ctx, &data)...)
}

func (r *PaperclipBoardKeyEphemeralResource) Close(ctx context.Context, req ephemeral.CloseRequest, resp *ephemeral.CloseResponse) {
	if r.client == nil || req.Private == nil {
		return
	}
	var keyID string
	var isLookup bool
	getPrivateStateKey(ctx, req.Private, "key_id", &keyID, &resp.Diagnostics)
	getPrivateStateKey(ctx, req.Private, "is_lookup", &isLookup, &resp.Diagnostics)
	if resp.Diagnostics.HasError() || keyID == "" {
		return
	}

	if isLookup {
		r.client.MarkTokenDelivered(keyID)
		r.client.ClearMintedToken(keyID)
		return
	}

	if !r.client.IsKeyDeliveryAcknowledged(keyID) {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := r.client.RevokeBoardKey(cleanupCtx, keyID); err != nil {
			resp.Diagnostics.AddError("Error revoking unconfirmed board key", err.Error())
			return
		}
		r.client.ClearMintedToken(keyID)
		return
	}

	r.client.ClearMintedToken(keyID)
}

func (p *PaperclipProvider) EphemeralResources(ctx context.Context) []func() ephemeral.EphemeralResource {
	return []func() ephemeral.EphemeralResource{
		NewPaperclipBoardKeyEphemeralResource,
		NewPaperclipAgentKeyEphemeralResource,
		NewPaperclipRoutineWebhookSecretEphemeralResource,
	}
}

func (p *PaperclipProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewPaperclipCompanyResource,
		NewPaperclipProjectResource,
		NewPaperclipProjectLeadResource,
		NewPaperclipWorkspaceResource,
		NewPaperclipLocalEnvironmentResource,
		NewPaperclipEnvironmentResource,
		NewPaperclipSkillResource,
		NewPaperclipSecretResource,
		NewPaperclipSecretProviderConfigResource,
		NewPaperclipUserSecretDefinitionResource,
		NewPaperclipAgentResource,
		NewPaperclipAppResource,
		NewPaperclipConnectionResource,
		NewPaperclipPolicyResource,
		NewPaperclipToolProfileResource,
		NewPaperclipToolProfileBindingResource,
		NewPaperclipStdioTemplateResource,
		NewPaperclipConnectionInstallResource,
		NewPaperclipConnectionGrantsResource,
		NewPaperclipAppPermissionResource,
		NewPaperclipSkillAssignmentResource,
		NewPaperclipSkillPolicyResource,
		NewPaperclipGoalResource,
		NewPaperclipRoutineResource,
		NewPaperclipRoutineTriggerResource,
		NewPaperclipRoutineWebhookDeliveryResource,
		NewPaperclipBudgetPolicyResource,
		NewPaperclipBoardKeyResource,
		NewPaperclipAgentKeyResource,
		NewPaperclipAgentInstructionBundleResource,
		NewPaperclipInvitationResource,
		NewPaperclipInviteResource,
		NewPaperclipMembershipResource,
		NewPaperclipPermissionGrantsResource,
		NewPaperclipMemberPermissionsResource,
		NewPaperclipInstanceAdminResource,
		NewPaperclipUserCompanyAccessResource,
		NewPaperclipUserInstanceAccessResource,
		NewPaperclipPluginResource,
		NewPaperclipPluginConfigResource,
	}
}

func (p *PaperclipProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewPaperclipCompanyDataSource,
		NewPaperclipProjectDataSource,
		NewPaperclipAgentDataSource,
		NewPaperclipSkillDataSource,
		NewPaperclipUserDataSource,
		NewPaperclipUsersDataSource,
	}
}
