package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNativeAdapterSchemaResponse(t *testing.T) {
	for _, tc := range []struct {
		body   string
		status int
		want   string
	}{
		{`{"fields":[]}`, 200, ""},
		{`{}`, 200, "no fields array"},
		{`{"fields":null}`, 200, "no fields array"},
		{`{"error":"Board access required"}`, 403, "Board access required"},
		{`{"error":"Adapter does not provide a config schema"}`, 404, "does not provide"},
		{`malformed`, 200, "invalid character"},
	} {
		t.Run(tc.body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.EscapedPath() != "/api/adapters/custom%2Fadapter/config-schema" || r.Header.Get("Authorization") != "Bearer schema-test" {
					t.Errorf("unexpected schema request: %s %s", r.Method, r.URL)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			c, _ := NewClient(server.URL, "schema-test")
			s, err := c.GetAdapterConfigSchema(context.Background(), "custom/adapter")
			if tc.want == "" {
				if err != nil || s == nil || s.Fields == nil {
					t.Fatalf("schema=%v err=%v", s, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q got %v", tc.want, err)
			}
		})
	}
}
