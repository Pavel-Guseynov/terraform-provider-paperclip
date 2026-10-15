package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestLocalEnvironmentPathReconciliation(t *testing.T) {
	ctx := context.Background()
	for _, mode := range []string{"remove", "inherited", "null", "missing", "duplicate", "remote", "identity", "list-error", "read-error", "write-error", "verify-error", "ignored-write"} {
		t.Run(mode, func(t *testing.T) {
			bindings := map[string]interface{}{"PATH": "/obsolete/bin", "EDITOR": "vi", "CREDENTIAL": map[string]interface{}{"type": "secret_ref", "secretId": "reference-only", "version": "latest"}}
			if mode == "inherited" {
				delete(bindings, "PATH")
			}
			if mode == "null" {
				bindings = nil
			}
			writes := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				env := PaperclipEnvironment{ID: "local-id", Driver: "local", EnvVars: bindings}
				if mode == "remote" {
					env.Driver = "remote"
				}
				if mode == "identity" {
					env.ID = "other-id"
				}
				switch req.URL.Path {
				case "/api/companies/company/environments":
					if req.URL.Query().Get("driver") != "local" {
						t.Error("missing driver filter")
					}
					if mode == "list-error" {
						w.WriteHeader(403)
						return
					}
					envs := []PaperclipEnvironment{env}
					if mode == "missing" {
						envs = nil
					}
					if mode == "duplicate" {
						envs = append(envs, env)
					}
					_ = json.NewEncoder(w).Encode(envs)
				case "/api/environments/local-id":
					if req.Method == http.MethodGet {
						if mode == "read-error" || (mode == "verify-error" && writes > 0) {
							w.WriteHeader(403)
							return
						}
						_ = json.NewEncoder(w).Encode(env)
						return
					}
					if req.Method != http.MethodPatch || req.URL.Query().Get("companyId") != "company" {
						t.Fatal("unexpected mutation")
					}
					writes++
					if mode == "write-error" {
						w.WriteHeader(403)
						return
					}
					var body map[string]interface{}
					if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					if len(body) != 1 {
						t.Fatal("mutation touched other environment fields")
					}
					updated := body["envVars"].(map[string]interface{})
					if _, present := updated["PATH"]; present {
						t.Fatal("PATH override survived")
					}
					for key, value := range bindings {
						if key != "PATH" && !reflect.DeepEqual(updated[key], value) {
							t.Fatal("unmanaged binding changed")
						}
					}
					if mode != "ignored-write" {
						bindings = updated
					}
					_, _ = w.Write([]byte(`{}`))
				default:
					t.Fatalf("unexpected endpoint %s", req.URL.Path)
				}
			}))
			defer server.Close()
			c, _ := NewClient(server.URL, "fixture-token")
			_, findErr := c.FindLocalEnvironment(ctx, "company")
			findFails := mode == "missing" || mode == "duplicate" || mode == "remote" || mode == "list-error"
			if (findErr != nil) != findFails {
				t.Fatalf("unexpected discovery result: %v", findErr)
			}
			if findFails {
				return
			}
			env, err := c.InheritLocalServicePath(ctx, "company", "local-id")
			wantError := mode != "remove" && mode != "inherited" && mode != "null"
			if (err != nil) != wantError {
				t.Fatalf("unexpected reconciliation result: %v", err)
			}
			if err != nil {
				return
			}
			if !env.InheritsServicePath() {
				t.Fatal("inheritance was not verified")
			}
			firstWrites := writes
			if _, err := c.InheritLocalServicePath(ctx, "company", "local-id"); err != nil || writes != firstWrites {
				t.Fatal("converged reconciliation wrote again")
			}
		})
	}
}

func TestExecutionEnvironmentClientUsesNativeRoutesAndVerifiesReadback(t *testing.T) {
	for _, mode := range []string{"create", "update", "write-error", "invalid-identity", "changed-identity", "read-error", "incomplete", "delete-error"} {
		t.Run(mode, func(t *testing.T) {
			var requests []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				requests = append(requests, req.Method+" "+req.URL.RequestURI())
				w.Header().Set("Content-Type", "application/json")
				if mode == "write-error" || (mode == "read-error" && req.Method == "GET") || (mode == "delete-error" && req.Method == "DELETE") {
					w.WriteHeader(403)
					_, _ = w.Write([]byte(`{"error":"Instance admin required"}`))
					return
				}
				id := "env-id"
				if mode == "invalid-identity" {
					id = ""
				}
				if mode == "changed-identity" {
					id = "different"
				}
				if mode == "incomplete" {
					_, _ = w.Write([]byte(`{"id":"env-id"}`))
					return
				}
				_ = json.NewEncoder(w).Encode(PaperclipEnvironment{ID: id, Name: "remote", Driver: "ssh", Status: "active", Config: map[string]interface{}{}, EnvVars: map[string]interface{}{}})
			}))
			defer server.Close()
			c, _ := NewClient(server.URL, "fixture-token")
			id := "env-id"
			if mode == "create" || mode == "invalid-identity" {
				id = ""
			}
			_, err := c.SaveExecutionEnvironment(context.Background(), "company", id, map[string]interface{}{"name": "remote"})
			wantErr := mode != "create" && mode != "update" && mode != "delete-error"
			if (err != nil) != wantErr {
				t.Fatalf("unexpected save result: %v", err)
			}
			if mode == "create" && requests[0] != "POST /api/companies/company/environments" {
				t.Fatal(requests)
			}
			if mode == "update" && !reflect.DeepEqual(requests, []string{"PATCH /api/environments/env-id?companyId=company", "GET /api/environments/env-id"}) {
				t.Fatal(requests)
			}
			if mode == "read-error" && !strings.Contains(err.Error(), "Instance admin required") {
				t.Fatal("authorization reason missing", err)
			}
			err = c.DeleteExecutionEnvironment(context.Background(), "env-id")
			if (err != nil) != (mode == "write-error" || mode == "delete-error") {
				t.Fatalf("unexpected delete result: %v", err)
			}
		})
	}
}
