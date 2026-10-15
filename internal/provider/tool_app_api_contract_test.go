package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestPaperclipAppReadPreservesStateOnLookupFailure(t *testing.T) {
	for _, tc := range []struct {
		name      string
		companyID types.String
		wantCalls int32
	}{
		{name: "authorization failure", companyID: types.StringValue("company-1"), wantCalls: 1},
		{name: "missing company identity", companyID: types.StringNull()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodGet || r.URL.RequestURI() != "/api/companies/company-1/tools/applications" {
					t.Errorf("unexpected application read: %s %s", r.Method, r.URL.RequestURI())
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				fmt.Fprint(w, `{"error":"Forbidden"}`)
			}))
			defer server.Close()
			ctx := context.Background()
			r, appSchema := newConfiguredPaperclipAppResource(t, server.URL)
			state := tfsdk.State{Schema: appSchema}
			if diagnostics := state.Set(ctx, &PaperclipAppResourceModel{
				ID: types.StringValue("app-1"), CompanyID: tc.companyID,
				Name: types.StringValue("original-name"), Type: types.StringValue("mcp_http"),
			}); diagnostics.HasError() {
				t.Fatal(diagnostics)
			}
			response := resource.ReadResponse{State: state}
			r.Read(ctx, resource.ReadRequest{State: state}, &response)
			if !response.Diagnostics.HasError() || !reflect.DeepEqual(response.State.Raw, state.Raw) {
				t.Fatal("failed lookup must report its error without changing or removing state")
			}
			if calls.Load() != tc.wantCalls {
				t.Fatalf("lookup made %d HTTP requests, expected %d", calls.Load(), tc.wantCalls)
			}
		})
	}
}

func TestPaperclipAppImportRequiresCompanyAndApplicationIdentity(t *testing.T) {
	ctx := context.Background()
	r, appSchema := newConfiguredPaperclipAppResource(t, "http://127.0.0.1:1")
	for _, id := range []string{"", "app-1", "/app-1", "company-1/", "company-1/app-1/extra", "company-1/app-1"} {
		t.Run(id, func(t *testing.T) {
			response := resource.ImportStateResponse{State: tfsdk.State{Schema: appSchema}}
			if diagnostics := response.State.Set(ctx, &PaperclipAppResourceModel{}); diagnostics.HasError() {
				t.Fatal(diagnostics)
			}
			r.ImportState(ctx, resource.ImportStateRequest{ID: id}, &response)
			if id != "company-1/app-1" {
				if !response.Diagnostics.HasError() {
					t.Fatal("incomplete or malformed import identity must be rejected")
				}
				return
			}
			if response.Diagnostics.HasError() {
				t.Fatal(response.Diagnostics)
			}
			var state PaperclipAppResourceModel
			if diagnostics := response.State.Get(ctx, &state); diagnostics.HasError() {
				t.Fatal(diagnostics)
			}
			if state.ID.ValueString() != "app-1" || state.CompanyID.ValueString() != "company-1" {
				t.Fatalf("import changed the declared identity: %+v", state)
			}
		})
	}
}
