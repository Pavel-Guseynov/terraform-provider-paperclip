package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGatewayInventoryRejectsIncompleteAndContradictoryResponses(t *testing.T) {
	valid := `{"id":"g","companyId":"c","name":"Gateway","displaySlug":"gateway","status":"active","profileId":"profile","defaultProfileMode":"gateway_only","contextScopeType":"none","metadata":{},"tokens":[],"authConfig":{},"headerPolicy":{},"metadataPolicy":{},"onDemandToolsConfig":{}}`
	for _, body := range []string{`{}`, `false`, `{"gateways":[{}]}`, `{"gateways":[` + valid + `,` + valid + `]}`, `{"gateways":[` + strings.Replace(valid, `"companyId":"c"`, `"companyId":"other"`, 1) + `]}`, `{"gateways":[` + strings.Replace(valid, `"tokens":[]`, `"tokens":[{"id":"t","companyId":"other","gatewayId":"g"}]`, 1) + `]}`} {
		t.Run(body, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
			defer s.Close()
			c, _ := NewClient(s.URL, "")
			if _, err := c.GetGateway(context.Background(), "c", "g"); err == nil {
				t.Fatal("invalid inventory inferred absence or success")
			}
		})
	}
}
func TestGatewayMintRejectsInvalidResponseAndCompensates(t *testing.T) {
	for _, mode := range []string{"missing-id", "missing-token", "wrong-company", "missing-expiry", "revoke-failed", "http-failed"} {
		t.Run(mode, func(t *testing.T) {
			revoked := false
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/revoke") {
					revoked = true
					if mode == "revoke-failed" {
						w.WriteHeader(403)
						fmt.Fprint(w, `{"error":"revoke refused"}`)
					} else {
						fmt.Fprint(w, `{}`)
					}
					return
				}
				if mode == "http-failed" {
					w.WriteHeader(403)
					fmt.Fprint(w, `{"error":"mint refused","token":"pcgw_do_not_emit"}`)
					return
				}
				m := map[string]interface{}{"id": "t", "companyId": "c", "gatewayId": "g", "token": "pcgw_do_not_emit", "expiresAt": "2100-01-01T00:00:00Z"}
				switch mode {
				case "missing-id":
					delete(m, "id")
				case "missing-token", "revoke-failed":
					delete(m, "token")
				case "wrong-company":
					m["companyId"] = "other"
				case "missing-expiry":
					delete(m, "expiresAt")
				}
				_ = json.NewEncoder(w).Encode(m)
			}))
			defer s.Close()
			c, _ := NewClient(s.URL, "")
			_, err := c.CreateGatewayToken(context.Background(), "c", "g", map[string]interface{}{})
			if err == nil || strings.Contains(err.Error(), "pcgw_do_not_emit") {
				t.Fatal("invalid mint succeeded or exposed token")
			}
			if revoked != (mode != "missing-id" && mode != "http-failed") {
				t.Fatal("invalid mint was not compensated")
			}
		})
	}
}
func TestGatewayWriteRejectsIdentityMismatch(t *testing.T) {
	for _, response := range []string{`{}`, `{"id":"other","companyId":"c"}`} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, response) }))
		c, _ := NewClient(s.URL, "")
		if _, err := c.WriteGateway(context.Background(), "c", "g", map[string]interface{}{}); err == nil {
			t.Error("write accepted a changed identity")
		}
		s.Close()
	}
}
func TestGatewayDeliveryCacheIsScopedAndGenerationBound(t *testing.T) {
	c, _ := NewClient("http://fixture", "")
	c.RegisterGatewayToken("c", "g", "n", 1, "t1", "secret1", "configuration1")
	c.RegisterGatewayToken("c", "g", "n", 2, "t2", "secret2", "configuration2")
	if id, _, _ := c.GatewayTokenGeneration("c", "g", "n", 1); id != "" {
		t.Fatal("another generation reused pending token")
	}
	c.ClearMintedToken("t2")
	if id, token, _ := c.GatewayTokenGeneration("c", "g", "n", 1); id != "t1" || token != "secret1" {
		t.Fatal("cleanup lost earlier pending identity")
	}
	if id, _, _ := c.GatewayTokenGeneration("other", "g", "n", 1); id != "" {
		t.Fatal("cross-company token reused")
	}
	c.RecordGatewayTokenDelivery("c", "g", "n", 1, "t1")
	if _, _, delivered := c.GatewayTokenGeneration("c", "g", "n", 1); !delivered {
		t.Fatal("delivery was not recorded")
	}
	if err := c.CleanupUnconfirmedKeys(context.Background()); err != nil {
		t.Fatal("confirmed token cleanup sent revocation", err)
	}
}

func TestGatewayIssueReferencesRejectWrongCompanyProjectAndUnreadableIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		valid      bool
	}{
		{"same-project", `{"id":"i","companyId":"c","projectId":"p"}`, 200, true},
		{"no-project", `{"id":"i","companyId":"c","projectId":null}`, 200, true},
		{"wrong-project", `{"id":"i","companyId":"c","projectId":"other"}`, 200, false},
		{"wrong-company", `{"id":"i","companyId":"other"}`, 200, false},
		{"wrong-identity", `{"id":"other","companyId":"c"}`, 200, false},
		{"denied", `{"error":"reference denied"}`, 403, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/api/issues/i" {
					t.Error("reference check mutated or guessed endpoint")
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer s.Close()
			c, _ := NewClient(s.URL, "")
			if err := c.AssertGatewayIssue(context.Background(), "c", "i", "p"); (err == nil) != tc.valid {
				t.Fatal(err)
			}
		})
	}
}

func TestGatewayErrorsRedactNativeTokenInFreeText(t *testing.T) {
	secret := "pcgw_11111111-1111-4111-8111-111111111111.testsecret"
	for name, body := range map[string]string{"json": `{"error":"injected failure for ` + secret + `"}`, "text": "injected failure for " + secret, "policy_prefix": `{"error":"injected failure for ` + secret + `"}`} {
		t.Run(name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500); fmt.Fprint(w, body) }))
			defer s.Close()
			c, _ := NewClient(s.URL, "")
			var err error
			if name == "policy_prefix" {
				_, err = c.WriteGateway(context.Background(), "c", "g", map[string]interface{}{"authConfig": map[string]interface{}{"bearer": map[string]interface{}{"tokenPrefix": "pcgw"}}})
			} else {
				_, err = c.CreateGatewayToken(context.Background(), "c", "g", map[string]interface{}{})
			}
			if err == nil || strings.Contains(err.Error(), "testsecret") || !strings.Contains(err.Error(), "injected failure") {
				t.Fatal("native token leaked or original failure was lost")
			}
		})
	}
}
