package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestPaperclipAppReadPreservesStateOnCompanyListFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	state.Set(ctx, &PaperclipAppResourceModel{
		ID: types.StringValue("app-1"), CompanyID: types.StringValue("company-1"),
		Name: types.StringValue("original-name"), Type: types.StringValue("mcp_http"),
	})
	response := resource.ReadResponse{State: state}
	r.Read(ctx, resource.ReadRequest{State: state}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("failed application refresh must report its API error")
	}
	if !reflect.DeepEqual(response.State.Raw, state.Raw) {
		t.Fatal("failed application refresh changed or removed existing state")
	}
}
