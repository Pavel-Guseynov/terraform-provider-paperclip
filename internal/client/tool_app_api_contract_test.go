package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestGetToolAppUsesCompanyScopedAPI(t *testing.T) {
	for _, tc := range []struct {
		name         string
		body         string
		status       int
		wantError    bool
		wantNotFound bool
	}{
		{
			name: "refreshes the exact ID from the declared company",
			body: `{"applications":[{"id":"other-app","companyId":"company-1","name":"same-name","type":"mcp_http"},{"id":"app-1","companyId":"company-1","name":"refreshed-name","type":"mcp_stdio"}]}`,
		},
		{name: "does not adopt a same-name application", body: `{"applications":[{"id":"other-app","companyId":"company-1","name":"same-name","type":"mcp_http"}]}`, wantError: true, wantNotFound: true},
		{name: "reports absence from an empty list", body: `{"applications":[]}`, wantError: true, wantNotFound: true},
		{name: "rejects a different company", body: `{"applications":[{"id":"app-1","companyId":"other-company","type":"mcp_http"}]}`, wantError: true},
		{name: "rejects missing application identity", body: `{"applications":[{"companyId":"company-1","type":"mcp_http"}]}`, wantError: true},
		{name: "rejects duplicate application identities", body: `{"applications":[{"id":"app-1","companyId":"company-1"},{"id":"app-1","companyId":"company-1"}]}`, wantError: true},
		{name: "rejects a missing applications array", body: `{}`, wantError: true},
		{name: "rejects a null applications array", body: `{"applications":null}`, wantError: true},
		{name: "rejects malformed application rows", body: `{"applications":[{"id":42}]}`, wantError: true},
		{name: "preserves authorization failures", status: http.StatusForbidden, body: `{"error":"Forbidden"}`, wantError: true},
		{name: "preserves unsupported route errors", status: http.StatusNotFound, body: `{"error":"API route not found"}`, wantError: true},
		{name: "preserves server failures", status: http.StatusInternalServerError, body: `{"error":"database unavailable"}`, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				if r.Method != http.MethodGet || r.URL.RequestURI() != "/api/companies/company-1/tools/applications" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.RequestURI())
					w.WriteHeader(http.StatusNotFound)
					fmt.Fprint(w, `{"error":"API route not found"}`)
					return
				}
				if tc.status != 0 {
					w.WriteHeader(tc.status)
				}
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			api, err := NewClient(server.URL, "test-token")
			if err != nil {
				t.Fatal(err)
			}
			got, err := api.GetToolApp(context.Background(), "company-1", "app-1")
			if (err != nil) != tc.wantError || errors.Is(err, ErrNotFound) != tc.wantNotFound {
				t.Fatalf("unexpected refresh error: %v", err)
			}
			if calls.Load() != 1 {
				t.Fatalf("expected exactly one company-scoped request, got %d", calls.Load())
			}
			if tc.wantError {
				if got != nil {
					t.Fatalf("failed lookup returned an application: %+v", got)
				}
			} else if got.ID != "app-1" || got.CompanyID != "company-1" || got.Name != "refreshed-name" || got.Type != "mcp_stdio" {
				t.Fatalf("refresh did not preserve exact identity and read remote fields: %+v", got)
			}
		})
	}
}
