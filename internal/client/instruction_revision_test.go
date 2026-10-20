package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestInstructionFileMutationsCarryObservedBase(t *testing.T) {
	const revision = "11111111-1111-4111-8111-111111111111"
	const hash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	for _, tc := range []struct {
		name       string
		operation  string
		readStatus int
		detail     map[string]interface{}
		conflict   bool
		wantError  string
	}{
		{"existing entry", "write", 200, map[string]interface{}{"content": "old", "revision": map[string]string{"id": revision}}, false, ""},
		{"entry uses the observed revision when hash is also present", "write", 200, map[string]interface{}{"content": "old", "revision": map[string]string{"id": revision}, "contentHash": hash}, false, ""},
		{"new entry has explicit null", "write", 404, nil, false, ""},
		{"managed supporting file", "write", 200, map[string]interface{}{"content": "old", "contentHash": hash}, false, ""},
		{"stale entry preserves concurrent edit", "write", 200, map[string]interface{}{"content": "old", "revision": map[string]string{"id": revision}}, true, ""},
		{"concurrent creation preserves the new file", "write", 404, nil, true, ""},
		{"stale supporting file preserves concurrent edit", "write", 200, map[string]interface{}{"content": "old", "contentHash": hash}, true, ""},
		{"read failure prevents write", "write", 500, nil, false, "native read failed"},
		{"managed deletion carries hash", "delete", 200, map[string]interface{}{"content": "old", "contentHash": hash}, false, ""},
		{"stale deletion preserves concurrent edit", "delete", 200, map[string]interface{}{"content": "old", "contentHash": hash}, true, ""},
		{"absent deletion is idempotent", "delete", 404, nil, false, ""},
		{"read failure prevents deletion", "delete", 500, nil, false, "native read failed"},
		{"missing write metadata prevents mutation", "write", 200, map[string]interface{}{"content": "old"}, false, "no revision or content hash"},
		{"empty revision prevents mutation", "write", 200, map[string]interface{}{"content": "old", "revision": map[string]string{"id": ""}}, false, "empty revision"},
		{"missing deletion hash prevents mutation", "delete", 200, map[string]interface{}{"content": "old"}, false, "no content hash"},
		{"empty deletion hash prevents mutation", "delete", 200, map[string]interface{}{"content": "old", "contentHash": ""}, false, "no content hash"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests []string
			remoteContent := "old"
			if tc.readStatus == 404 {
				remoteContent = ""
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				requests = append(requests, req.Method)
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Request-Id", "instruction-conflict-test")
				if req.URL.Path != "/api/agents/agent-1/instructions-bundle/file" {
					t.Errorf("unexpected path: %s", req.URL.Path)
				}
				if req.Method == http.MethodGet {
					if req.URL.Query().Get("path") != "rules/AGENTS.md" {
						t.Errorf("wrong file read: %s", req.URL.RawQuery)
					}
					w.WriteHeader(tc.readStatus)
					if tc.readStatus != 200 {
						if tc.conflict {
							remoteContent = "concurrent edit"
						}
						_ = json.NewEncoder(w).Encode(map[string]string{"error": "native read failed"})
						return
					}
					_ = json.NewEncoder(w).Encode(tc.detail)
					if tc.conflict {
						remoteContent = "concurrent edit"
					}
					return
				}
				if tc.operation == "write" {
					var body map[string]interface{}
					if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					want := map[string]interface{}{"path": "rules/AGENTS.md", "content": "desired", "clearLegacyPromptTemplate": true, "baseRevisionId": nil}
					if tc.detail != nil && tc.detail["revision"] != nil {
						want["baseRevisionId"] = revision
					}
					if tc.readStatus == 404 {
						want["baseHash"] = nil
					} else if tc.detail["revision"] == nil && tc.detail["contentHash"] != nil {
						want["baseHash"] = hash
					}
					if req.Method != http.MethodPut || !reflect.DeepEqual(body, want) {
						t.Errorf("wrong guarded write: %s %#v; want %#v", req.Method, body, want)
					}
				} else if req.Method != http.MethodDelete || req.URL.Query().Get("baseHash") != hash || req.URL.Query().Get("path") != "rules/AGENTS.md" {
					t.Errorf("wrong guarded deletion: %s %s", req.Method, req.URL.RawQuery)
				}
				if tc.conflict {
					w.WriteHeader(http.StatusConflict)
					_, _ = w.Write([]byte(`{"error":"Instructions changed since the base revision","code":"INSTRUCTION_REVISION_CONFLICT"}`))
					return
				}
				remoteContent = "desired"
				if tc.operation == "delete" {
					remoteContent = ""
				}
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			defer server.Close()
			cli, err := NewClient(server.URL, "test-token")
			if err != nil {
				t.Fatal(err)
			}
			if tc.operation == "write" {
				err = cli.UpdateAgentInstructionsFileWithClear(context.Background(), "agent-1", "rules/AGENTS.md", "desired", true)
			} else {
				err = cli.DeleteAgentInstructionsFile(context.Background(), "agent-1", "rules/AGENTS.md")
			}
			wantRequests := []string{http.MethodGet}
			if tc.wantError == "" && !(tc.operation == "delete" && tc.readStatus == 404) {
				method := http.MethodPut
				if tc.operation == "delete" {
					method = http.MethodDelete
				}
				wantRequests = append(wantRequests, method)
			}
			if !reflect.DeepEqual(requests, wantRequests) {
				t.Fatalf("unexpected mutation or retry: %v; want %v; error: %v", requests, wantRequests, err)
			}
			if tc.conflict {
				var apiError *APIError
				if !errors.Is(err, ErrConflict) || !errors.As(err, &apiError) || apiError.StatusCode != 409 || !strings.Contains(apiError.Body, "INSTRUCTION_REVISION_CONFLICT") || apiError.CorrelationID != "instruction-conflict-test" || remoteContent != "concurrent edit" {
					t.Fatalf("lost native conflict evidence or concurrent content: %v; content=%q", err, remoteContent)
				}
			} else if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) || remoteContent != "old" {
					t.Fatalf("read failure did not stop mutation: %v; content=%q", err, remoteContent)
				}
			} else if err != nil {
				t.Fatal(err)
			} else {
				wantContent := "desired"
				if tc.operation == "delete" {
					wantContent = ""
				}
				if remoteContent != wantContent {
					t.Fatalf("unexpected native file state: %q; want %q", remoteContent, wantContent)
				}
			}
		})
	}
}
