package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAgentInstructionsBundleClientMethods(t *testing.T) {
	ctx := context.Background()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/agents/agent-1/instructions-bundle":
			_ = json.NewEncoder(w).Encode(AgentInstructionsBundle{
				Mode:      "managed",
				EntryFile: "AGENTS.md",
				Files: []AgentInstructionsFileSummary{
					{
						Path:    "AGENTS.md",
						Size:    10,
						Virtual: false,
					},
					{
						Path:    "promptTemplate.legacy.md",
						Size:    50,
						Virtual: true,
					},
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/agents/agent-404/instructions-bundle":
			http.NotFound(w, r)
		case r.Method == http.MethodGet && r.URL.Path == "/api/agents/agent-err/instructions-bundle":
			http.Error(w, `{"error":"server error"}`, http.StatusInternalServerError)
		case r.Method == http.MethodPatch && r.URL.Path == "/api/agents/agent-1/instructions-bundle":
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			rootPath, _ := body["rootPath"].(string)
			mode, _ := body["mode"].(string)
			entryFile, _ := body["entryFile"].(string)
			_ = json.NewEncoder(w).Encode(AgentInstructionsBundle{
				Mode:      mode,
				RootPath:  rootPath,
				EntryFile: entryFile,
			})
		case r.Method == http.MethodPatch && r.URL.Path == "/api/agents/agent-err/instructions-bundle":
			http.Error(w, `{"error":"patch error"}`, http.StatusInternalServerError)
		case r.Method == http.MethodGet && r.URL.Path == "/api/agents/agent-1/instructions-bundle/file":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"content": "hello", "contentHash": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"})
		case r.Method == http.MethodPut && r.URL.Path == "/api/agents/agent-1/instructions-bundle/file":
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["path"] == "fail.md" {
				http.Error(w, `{"error":"file error"}`, http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
		case r.Method == http.MethodDelete && r.URL.Path == "/api/agents/agent-1/instructions-bundle/file":
			p := r.URL.Query().Get("path")
			if p == "fail.md" {
				http.Error(w, `{"error":"delete error"}`, http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cli, err := NewClient(server.URL, "token")
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	// Validation checks
	if _, err := cli.GetAgentInstructionsBundle(ctx, ""); err == nil {
		t.Fatal("expected error for empty agentID in GetAgentInstructionsBundle")
	}
	if _, err := cli.UpdateAgentInstructionsBundle(ctx, "", UpdateAgentInstructionsBundleRequest{}); err == nil {
		t.Fatal("expected error for empty agentID in UpdateAgentInstructionsBundle")
	}
	if err := cli.DeleteAgentInstructionsFile(ctx, "", "file.md"); err == nil {
		t.Fatal("expected error for empty agentID in DeleteAgentInstructionsFile")
	}
	if err := cli.DeleteAgentInstructionsFile(ctx, "agent-1", ""); err == nil {
		t.Fatal("expected error for empty filePath in DeleteAgentInstructionsFile")
	}

	// GetAgentInstructionsBundle
	bundle, err := cli.GetAgentInstructionsBundle(ctx, "agent-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bundle.Mode != "managed" || bundle.EntryFile != "AGENTS.md" || len(bundle.Files) != 2 {
		t.Fatalf("unexpected bundle: %+v", bundle)
	}

	_, err = cli.GetAgentInstructionsBundle(ctx, "agent-404")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	_, err = cli.GetAgentInstructionsBundle(ctx, "agent-err")
	if err == nil {
		t.Fatal("expected error on 500")
	}

	// UpdateAgentInstructionsBundle
	root := "/path/to/root"
	updated, err := cli.UpdateAgentInstructionsBundle(ctx, "agent-1", UpdateAgentInstructionsBundleRequest{
		Mode:                      "external",
		RootPath:                  &root,
		EntryFile:                 "CUSTOM.md",
		ClearLegacyPromptTemplate: true,
	})
	if err != nil {
		t.Fatalf("unexpected update error: %v", err)
	}
	if updated.Mode != "external" || updated.RootPath != root || updated.EntryFile != "CUSTOM.md" {
		t.Fatalf("unexpected updated bundle: %+v", updated)
	}

	_, err = cli.UpdateAgentInstructionsBundle(ctx, "agent-err", UpdateAgentInstructionsBundleRequest{})
	if err == nil {
		t.Fatal("expected error on 500 in UpdateAgentInstructionsBundle")
	}

	// UpdateAgentInstructionsFileWithClear
	if err := cli.UpdateAgentInstructionsFileWithClear(ctx, "agent-1", "test.md", "hello", true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := cli.UpdateAgentInstructionsFile(ctx, "agent-1", "test.md", "hello"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := cli.UpdateAgentInstructionsFileWithClear(ctx, "agent-1", "fail.md", "bad", false); err == nil {
		t.Fatal("expected error on fail.md")
	}

	// DeleteAgentInstructionsFile
	if err := cli.DeleteAgentInstructionsFile(ctx, "agent-1", "test.md"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := cli.DeleteAgentInstructionsFile(ctx, "agent-1", "fail.md"); err == nil {
		t.Fatal("expected error on delete fail.md")
	}
}
