package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Pavel-Guseynov/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func configureProviderForTest(t *testing.T, model PaperclipProviderModel) provider.ConfigureResponse {
	t.Helper()
	p := New("test")()
	var schema provider.SchemaResponse
	p.Schema(context.Background(), provider.SchemaRequest{}, &schema)
	plan := tfsdk.Plan{Schema: schema.Schema}
	if diags := plan.Set(context.Background(), &model); diags.HasError() {
		t.Fatal(diags)
	}
	var resp provider.ConfigureResponse
	p.Configure(context.Background(), provider.ConfigureRequest{Config: tfsdk.Config{Schema: schema.Schema, Raw: plan.Raw}}, &resp)
	return resp
}

func TestProviderConfigurationHasOnlyCanonicalAttributes(t *testing.T) {
	var resp provider.SchemaResponse
	New("test")().Schema(context.Background(), provider.SchemaRequest{}, &resp)
	if len(resp.Schema.Attributes) != 2 {
		t.Fatalf("expected only endpoint and token, got %d provider attributes", len(resp.Schema.Attributes))
	}
	for _, name := range []string{"endpoint", "token"} {
		attribute, exists := resp.Schema.Attributes[name]
		if !exists || !attribute.IsOptional() || attribute.IsRequired() || attribute.IsComputed() || attribute.IsSensitive() != (name == "token") {
			t.Fatalf("invalid public configuration contract for %s", name)
		}
	}
}

func TestProviderConfigurationSelectsDeclaredConnection(t *testing.T) {
	for _, name := range []string{"default", "environment", "explicit", "explicit empty token", "legacy environment ignored"} {
		t.Run(name, func(t *testing.T) {
			for _, key := range []string{"PAPERCLIP_API_URL", "PAPERCLIP_API_KEY", "PAPERCLIP_ENDPOINT", "PAPERCLIP_URL", "PAPERCLIP_BOARD_TOKEN", "PAPERCLIP_TOKEN"} {
				t.Setenv(key, "")
			}
			var requestPath, authorization string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				requestPath, authorization = req.URL.Path, req.Header.Get("Authorization")
				_, _ = w.Write([]byte(`{"status":"ok"}`))
			}))
			defer server.Close()
			model := PaperclipProviderModel{Endpoint: types.StringNull(), Token: types.StringNull()}
			wantEndpoint, wantToken := "http://127.0.0.1:3100", ""
			wantPath := ""
			if name == "environment" || name == "explicit" || name == "explicit empty token" {
				t.Setenv("PAPERCLIP_API_URL", server.URL+"/environment")
				t.Setenv("PAPERCLIP_API_KEY", "environment-token")
				wantEndpoint, wantToken, wantPath = server.URL+"/environment", "environment-token", "/environment/api/health"
			}
			if name == "explicit" || name == "explicit empty token" {
				model.Endpoint = types.StringValue(server.URL + "/configured/")
				model.Token = types.StringValue("configured-token")
				wantEndpoint, wantToken, wantPath = server.URL+"/configured", "configured-token", "/configured/api/health"
			}
			if name == "explicit empty token" {
				model.Token, wantToken = types.StringValue(""), ""
			}
			if name == "legacy environment ignored" {
				t.Setenv("PAPERCLIP_ENDPOINT", server.URL+"/legacy-endpoint")
				t.Setenv("PAPERCLIP_URL", server.URL+"/legacy-url")
				t.Setenv("PAPERCLIP_BOARD_TOKEN", "legacy-board-token")
				t.Setenv("PAPERCLIP_TOKEN", "legacy-token")
			}
			resp := configureProviderForTest(t, model)
			if resp.Diagnostics.HasError() {
				t.Fatal(resp.Diagnostics)
			}
			c, ok := resp.ResourceData.(*client.Client)
			if !ok || resp.DataSourceData != c || resp.EphemeralResourceData != c || c.Endpoint != wantEndpoint || c.Token != wantToken {
				t.Fatal("configured connection or shared provider client differs from the declared source")
			}
			if wantPath != "" {
				if _, err := c.Health(context.Background()); err != nil {
					t.Fatal(err)
				}
				wantAuthorization := ""
				if wantToken != "" {
					wantAuthorization = "Bearer " + wantToken
				}
				if requestPath != wantPath || authorization != wantAuthorization {
					t.Fatal("request used a different endpoint or credential source")
				}
			}
		})
	}
}

func TestProviderConfigurationRejectsUnknownValues(t *testing.T) {
	for _, name := range []string{"endpoint", "token", "both"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("PAPERCLIP_API_URL", "http://127.0.0.1:1")
			t.Setenv("PAPERCLIP_API_KEY", "must-not-be-selected")
			model := PaperclipProviderModel{Endpoint: types.StringNull(), Token: types.StringNull()}
			if name != "token" {
				model.Endpoint = types.StringUnknown()
			}
			if name != "endpoint" {
				model.Token = types.StringUnknown()
			}
			resp := configureProviderForTest(t, model)
			wantErrors := 1
			if name == "both" {
				wantErrors = 2
			}
			if resp.Diagnostics.ErrorsCount() != wantErrors || resp.ResourceData != nil || resp.DataSourceData != nil || resp.EphemeralResourceData != nil {
				t.Fatalf("unknown configuration selected a connection: %v", resp.Diagnostics)
			}
			for _, diagnostic := range resp.Diagnostics {
				if !strings.HasPrefix(diagnostic.Summary(), "Unknown Paperclip ") || strings.Contains(diagnostic.Detail(), "must-not-be-selected") {
					t.Fatal("unknown-value diagnostic is not actionable and secret-safe")
				}
			}
		})
	}
}

func TestProviderConfigurationRejectsExplicitEmptyEndpoint(t *testing.T) {
	t.Setenv("PAPERCLIP_API_URL", "http://127.0.0.1:1")
	resp := configureProviderForTest(t, PaperclipProviderModel{Endpoint: types.StringValue(""), Token: types.StringNull()})
	if !resp.Diagnostics.HasError() || resp.ResourceData != nil {
		t.Fatal("explicit empty endpoint silently selected an environment/default endpoint")
	}
}

func TestProviderConfigurationRejectsInvalidEndpointWithoutDisclosingInput(t *testing.T) {
	resp := configureProviderForTest(t, PaperclipProviderModel{Endpoint: types.StringValue("http://user:fixture-secret@example.invalid/\n"), Token: types.StringValue("fixture-token")})
	if resp.Diagnostics.ErrorsCount() != 1 || resp.ResourceData != nil || resp.DataSourceData != nil || resp.EphemeralResourceData != nil {
		t.Fatal("invalid provider endpoint selected a client")
	}
	diagnostic := resp.Diagnostics[0]
	if diagnostic.Summary() != "Invalid Paperclip Client Configuration" || !strings.Contains(diagnostic.Detail(), "absolute HTTP(S) URL") || strings.Contains(diagnostic.Detail(), "fixture-secret") || strings.Contains(diagnostic.Detail(), "fixture-token") {
		t.Fatal("invalid endpoint diagnostic is not actionable and secret-safe")
	}
}

func TestProviderConfigurationRejectsMalformedValue(t *testing.T) {
	p := New("test")()
	var schema provider.SchemaResponse
	p.Schema(context.Background(), provider.SchemaRequest{}, &schema)
	var resp provider.ConfigureResponse
	p.Configure(context.Background(), provider.ConfigureRequest{Config: tfsdk.Config{Schema: schema.Schema, Raw: tftypes.NewValue(tftypes.String, "invalid")}}, &resp)
	if !resp.Diagnostics.HasError() || resp.ResourceData != nil {
		t.Fatal("malformed provider configuration was accepted")
	}
}
