package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"syscall"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	rschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "set to true to run the provider with support for debuggers like delve")
	flag.Parse()

	opts := providerserver.ServeOpts{
		Address: "registry.opentofu.org/example/testconsumer",
		Debug:   debug,
	}

	err := providerserver.Serve(context.Background(), NewTestConsumerProvider, opts)
	if err != nil {
		log.Fatal(err.Error())
	}
}

type TestConsumerProvider struct{}

func NewTestConsumerProvider() provider.Provider {
	return &TestConsumerProvider{}
}

func (p *TestConsumerProvider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "testconsumer"
	resp.Version = "0.1.0"
}

func (p *TestConsumerProvider) Schema(ctx context.Context, req provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Test-owned write-only secret consumer provider.",
	}
}

func (p *TestConsumerProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
}

func (p *TestConsumerProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return nil
}

func (p *TestConsumerProvider) EphemeralResources(ctx context.Context) []func() ephemeral.EphemeralResource {
	return nil
}

func (p *TestConsumerProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewTestConsumerSecretResource,
	}
}

type TestConsumerSecretResource struct{}

type TestConsumerSecretResourceModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	ValueWO     types.String `tfsdk:"value_wo"`
	FailOnWrite types.Bool   `tfsdk:"fail_on_write"`
	Endpoint    types.String `tfsdk:"endpoint"`
	Rotation    types.String `tfsdk:"rotation"`
}

func NewTestConsumerSecretResource() resource.Resource {
	return &TestConsumerSecretResource{}
}

func (r *TestConsumerSecretResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_secret"
}

func (r *TestConsumerSecretResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = rschema.Schema{
		Description: "A test write-only secret consumer resource.",
		Attributes: map[string]rschema.Attribute{
			"id": rschema.StringAttribute{
				Description: "Secret identifier.",
				Computed:    true,
			},
			"name": rschema.StringAttribute{
				Description: "Consumer secret name.",
				Required:    true,
			},
			"value_wo": rschema.StringAttribute{
				Description: "Sensitive write-only secret credential value.",
				Optional:    true,
				Sensitive:   true,
				WriteOnly:   true,
			},
			"fail_on_write": rschema.BoolAttribute{
				Description: "Optionally inject write failure.",
				Optional:    true,
			},
			"endpoint": rschema.StringAttribute{
				Description: "Optional HTTP endpoint to deliver the write-only payload to.",
				Optional:    true,
			},
			"rotation": rschema.StringAttribute{
				Description: "Arbitrary trigger to force secret rewrite/rotation.",
				Optional:    true,
			},
		},
	}
}

func (r *TestConsumerSecretResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan TestConsumerSecretResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.handleWrite(ctx, &plan); err != nil {
		resp.Diagnostics.AddError("Test Consumer Write Error", err.Error())
		return
	}

	plan.ID = types.StringValue("tc-" + plan.Name.ValueString())
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *TestConsumerSecretResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state TestConsumerSecretResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if state.ID.IsNull() || state.ID.ValueString() == "" {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *TestConsumerSecretResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan TestConsumerSecretResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.handleWrite(ctx, &plan); err != nil {
		resp.Diagnostics.AddError("Test Consumer Write Error", err.Error())
		return
	}

	plan.ID = types.StringValue("tc-" + plan.Name.ValueString())
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *TestConsumerSecretResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
}

func (r *TestConsumerSecretResource) handleWrite(ctx context.Context, plan *TestConsumerSecretResourceModel) error {
	// Fault injection 1: mid-apply process restart
	if os.Getenv("TESTCONSUMER_KILL_ON_WRITE") == "1" {
		ppid := os.Getppid()
		_ = syscall.Kill(ppid, syscall.SIGINT)
		return fmt.Errorf("interrupted by SIGINT")
	}

	// Fault injection 2: consumer write failure
	if plan.FailOnWrite.ValueBool() || os.Getenv("TESTCONSUMER_FAIL_WRITE") == "1" {
		return fmt.Errorf("injected consumer write failure (HTTP 500)")
	}

	endpoint := plan.Endpoint.ValueString()
	if endpoint == "" {
		endpoint = os.Getenv("TESTCONSUMER_ENDPOINT")
	}

	if endpoint != "" {
		payload := map[string]string{
			"name":  plan.Name.ValueString(),
			"value": plan.ValueWO.ValueString(),
		}
		b, _ := json.Marshal(payload)
		res, err := http.Post(endpoint, "application/json", bytes.NewReader(b))
		if err != nil {
			return fmt.Errorf("consumer endpoint error: %w", err)
		}
		defer res.Body.Close()
		if res.StatusCode >= 400 {
			return fmt.Errorf("consumer endpoint returned HTTP %d", res.StatusCode)
		}
	}
	return nil
}
