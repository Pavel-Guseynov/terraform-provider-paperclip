package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientPlugins(t *testing.T) {
	ctx := context.Background()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/plugins/install":
			var body PaperclipPluginInstallInput
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if body.PackageName == "bad-pkg" {
				http.Error(w, "invalid package", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(PaperclipPlugin{
				ID:          "plug-1",
				PluginKey:   "linear",
				PackageName: body.PackageName,
				Version:     body.Version,
				Status:      "ready",
				APIVersion:  1,
			})

		case r.Method == http.MethodGet && r.URL.Path == "/api/plugins/plug-1":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(PaperclipPlugin{
				ID:          "plug-1",
				PluginKey:   "linear",
				PackageName: "@paperclipai/plugin-linear",
				Version:     "1.0.0",
				Status:      "ready",
				APIVersion:  1,
			})

		case r.Method == http.MethodGet && r.URL.Path == "/api/plugins/plug-missing":
			http.Error(w, "not found", http.StatusNotFound)
		case r.URL.Path == "/api/plugins/plug-err" || (len(r.URL.Path) > 19 && r.URL.Path[:19] == "/api/plugins/plug-err"):
			http.Error(w, "server error", http.StatusInternalServerError)

		case r.Method == http.MethodPost && r.URL.Path == "/api/plugins/plug-1/enable":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(PaperclipPlugin{
				ID:     "plug-1",
				Status: "ready",
			})

		case r.Method == http.MethodPost && r.URL.Path == "/api/plugins/plug-1/disable":
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(PaperclipPlugin{
				ID:     "plug-1",
				Status: "disabled",
			})

		case r.Method == http.MethodPost && r.URL.Path == "/api/plugins/plug-1/upgrade":
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			ver, _ := body["version"].(string)
			status := "ready"
			if ver == "2.0.0" {
				status = "upgrade_pending"
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(PaperclipPlugin{
				ID:      "plug-1",
				Version: ver,
				Status:  status,
			})

		case r.Method == http.MethodDelete && r.URL.Path == "/api/plugins/plug-1":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(PaperclipPlugin{
				ID:     "plug-1",
				Status: "uninstalled",
			})

		case r.Method == http.MethodGet && r.URL.Path == "/api/plugins/plug-1/config":
			compID := r.URL.Query().Get("companyId")
			if compID == "comp-null" {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte("null"))
				return
			}
			if compID == "comp-empty-id" {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(PaperclipPluginConfig{})
				return
			}
			if compID == "comp-missing" {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(PaperclipPluginConfig{
				ID:        "cfg-1",
				PluginID:  "plug-1",
				CompanyID: compID,
				ConfigJSON: map[string]interface{}{
					"apiKey": "secret",
				},
			})

		case r.Method == http.MethodPost && r.URL.Path == "/api/plugins/plug-1/config":
			var body PaperclipPluginConfigInput
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if body.CompanyID == "comp-invalid-schema" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(PluginConfigValidationError{
					Error: "Configuration does not match the plugin's instanceConfigSchema",
					FieldErrors: []PluginConfigFieldError{
						{Field: "/apiKey", Message: "must have required property 'apiKey'"},
					},
				})
				return
			}
			if body.CompanyID == "comp-500" {
				http.Error(w, "server error", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(PaperclipPluginConfig{
				ID:         "cfg-1",
				PluginID:   "plug-1",
				CompanyID:  body.CompanyID,
				ConfigJSON: body.ConfigJSON,
			})

		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cli, err := NewClient(server.URL, "test-token")
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	// 1. InstallPlugin
	installed, err := cli.InstallPlugin(ctx, PaperclipPluginInstallInput{
		PackageName: "@paperclipai/plugin-linear",
		Version:     "1.0.0",
		IsLocalPath: false,
	})
	if err != nil || installed.ID != "plug-1" {
		t.Fatalf("unexpected result from InstallPlugin: %v, err: %v", installed, err)
	}
	if _, err := cli.InstallPlugin(ctx, PaperclipPluginInstallInput{PackageName: "bad-pkg"}); err == nil {
		t.Fatal("expected error from InstallPlugin on bad package")
	}

	// 2. GetPlugin
	gotPlugin, err := cli.GetPlugin(ctx, "plug-1")
	if err != nil || gotPlugin.PluginKey != "linear" {
		t.Fatalf("unexpected result from GetPlugin: %v, err: %v", gotPlugin, err)
	}
	if _, err := cli.GetPlugin(ctx, "plug-missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for missing plugin, got: %v", err)
	}

	// 3. EnablePlugin
	enabled, err := cli.EnablePlugin(ctx, "plug-1")
	if err != nil || enabled.Status != "ready" {
		t.Fatalf("unexpected result from EnablePlugin: %v, err: %v", enabled, err)
	}

	// 4. DisablePlugin (with and without reason)
	disabled, err := cli.DisablePlugin(ctx, "plug-1", "Maintenance")
	if err != nil || disabled.Status != "disabled" {
		t.Fatalf("unexpected result from DisablePlugin with reason: %v, err: %v", disabled, err)
	}
	disabledNoReason, err := cli.DisablePlugin(ctx, "plug-1", "")
	if err != nil || disabledNoReason.Status != "disabled" {
		t.Fatalf("unexpected result from DisablePlugin without reason: %v, err: %v", disabledNoReason, err)
	}

	// 5. UpgradePlugin (with version and without)
	upgraded, err := cli.UpgradePlugin(ctx, "plug-1", "1.1.0")
	if err != nil || upgraded.Version != "1.1.0" {
		t.Fatalf("unexpected result from UpgradePlugin: %v, err: %v", upgraded, err)
	}
	upgradedPending, err := cli.UpgradePlugin(ctx, "plug-1", "2.0.0")
	if err != nil || upgradedPending.Status != "upgrade_pending" {
		t.Fatalf("unexpected result from UpgradePlugin upgrade_pending: %v, err: %v", upgradedPending, err)
	}
	upgradedLatest, err := cli.UpgradePlugin(ctx, "plug-1", "")
	if err != nil {
		t.Fatalf("unexpected result from UpgradePlugin without version: %v", err)
	}
	_ = upgradedLatest

	// 6. UninstallPlugin (purge = false, purge = true)
	if _, err := cli.UninstallPlugin(ctx, "plug-1", false); err != nil {
		t.Fatalf("UninstallPlugin (purge=false) failed: %v", err)
	}
	if _, err := cli.UninstallPlugin(ctx, "plug-1", true); err != nil {
		t.Fatalf("UninstallPlugin (purge=true) failed: %v", err)
	}
	if _, err := cli.EnablePlugin(ctx, "plug-err"); err == nil {
		t.Fatal("expected error from EnablePlugin")
	}
	if _, err := cli.DisablePlugin(ctx, "plug-err", "test"); err == nil {
		t.Fatal("expected error from DisablePlugin")
	}
	if _, err := cli.UpgradePlugin(ctx, "plug-err", "1.0.0"); err == nil {
		t.Fatal("expected error from UpgradePlugin")
	}
	if _, err := cli.UninstallPlugin(ctx, "plug-err", true); err == nil {
		t.Fatal("expected error from UninstallPlugin")
	}

	// 7. GetPluginConfig
	cfg, err := cli.GetPluginConfig(ctx, "plug-1", "comp-1")
	if err != nil || cfg.ID != "cfg-1" {
		t.Fatalf("unexpected result from GetPluginConfig: %v, err: %v", cfg, err)
	}
	if _, err := cli.GetPluginConfig(ctx, "plug-1", "comp-null"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for comp-null, got: %v", err)
	}
	if _, err := cli.GetPluginConfig(ctx, "plug-1", "comp-empty-id"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for comp-empty-id, got: %v", err)
	}
	if _, err := cli.GetPluginConfig(ctx, "plug-1", "comp-missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for comp-missing, got: %v", err)
	}

	// 8. UpsertPluginConfig
	upserted, err := cli.UpsertPluginConfig(ctx, "plug-1", "comp-1", map[string]interface{}{"apiKey": "valid"})
	if err != nil || upserted.ID != "cfg-1" {
		t.Fatalf("unexpected result from UpsertPluginConfig: %v, err: %v", upserted, err)
	}
	_, errValidation := cli.UpsertPluginConfig(ctx, "plug-1", "comp-invalid-schema", map[string]interface{}{"wrong": true})
	if errValidation == nil {
		t.Fatal("expected validation error from UpsertPluginConfig")
	}
	valErr, ok := ParsePluginConfigValidationError(errValidation)
	if !ok || valErr == nil || len(valErr.FieldErrors) != 1 || valErr.FieldErrors[0].Field != "/apiKey" {
		t.Fatalf("expected parsed validation error with /apiKey field error, got: %v, ok=%v", valErr, ok)
	}

	// 9. ParsePluginConfigValidationError edge cases
	if _, ok := ParsePluginConfigValidationError(nil); ok {
		t.Fatal("expected false for nil error")
	}
	if _, ok := ParsePluginConfigValidationError(errors.New("generic error")); ok {
		t.Fatal("expected false for non-APIError")
	}
	apiErrEmpty := &APIError{StatusCode: 400, Body: "{}"}
	if _, ok := ParsePluginConfigValidationError(apiErrEmpty); ok {
		t.Fatal("expected false for APIError with no fieldErrors")
	}
	apiErrBadJSON := &APIError{StatusCode: 400, Body: "not-json"}
	if _, ok := ParsePluginConfigValidationError(apiErrBadJSON); ok {
		t.Fatal("expected false for APIError with invalid JSON")
	}
}
