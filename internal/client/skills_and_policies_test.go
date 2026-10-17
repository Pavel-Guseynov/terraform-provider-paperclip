package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/Pavel-Guseynov/terraform-provider-paperclip/internal/fixtures"
)

func TestSkillsAndPoliciesClientMethods(t *testing.T) {
	ctx := context.Background()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/companies/comp-1/skills/skill-1/rename":
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"skill": map[string]interface{}{
					"id":        "skill-1",
					"companyId": "comp-1",
					"name":      body["name"],
					"slug":      body["slug"],
					"markdown":  "# Renamed Content",
				},
			})
		case r.Method == http.MethodPatch && r.URL.Path == "/api/companies/comp-1/skills/skill-1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":        "skill-1",
				"companyId": "comp-1",
				"name":      "patched-skill",
			})
		case r.Method == http.MethodPatch && r.URL.Path == "/api/companies/comp-1/skills/skill-1/files":
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"skillId": "skill-1",
				"path":    body["path"],
				"content": body["content"],
			})
		case r.Method == http.MethodDelete && r.URL.Path == "/api/companies/comp-1/skills/skill-1/files":
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-1/skills/skill-1/files":
			p := r.URL.Query().Get("path")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"skillId": "skill-1",
				"path":    p,
				"content": "file-content-for-" + p,
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/companies/comp-1/skills/skill-1/versions":
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			label, _ := body["label"].(string)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":             "ver-1",
				"companyId":      "comp-1",
				"companySkillId": "skill-1",
				"label":          label,
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-1/skills/skill-1/versions":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"id": "ver-1", "companyId": "comp-1", "companySkillId": "skill-1", "label": "v1.0.0"},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/companies/comp-1/skills/import":
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			source := body["source"].(string)
			if source == "empty" {
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"imported": []map[string]interface{}{},
				})
			} else {
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"imported": []map[string]interface{}{
						{
							"id":       "imported-1",
							"name":     "Imported Skill",
							"slug":     "imported-skill",
							"markdown": "# Imported",
						},
					},
				})
			}
		case r.Method == http.MethodPost && r.URL.Path == "/api/companies/comp-1/skills/install-catalog":
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"action": "installed",
				"skill": map[string]interface{}{
					"id":       "catalog-inst-1",
					"name":     "Catalog Installed",
					"slug":     body["slug"],
					"markdown": "# Catalog Content",
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/agents/agent-1/skills":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"adapterType":   "antigravity_local",
				"supported":     true,
				"mode":          "replace",
				"desiredSkills": []string{"skill-1"},
				"desiredSkillEntries": []map[string]interface{}{
					{"key": "skill-1", "versionId": "ver-1"},
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/agents/agent-1/skills/sync":
			snapshot, err := fixtures.SkillSyncSnapshot(r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(snapshot)
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-1/skill-policy":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"schemaVersion": 1,
				"revision":      1,
				"defaultEffect": "allow",
				"rules":         []map[string]interface{}{},
				"materialized":  true,
			})
		case r.Method == http.MethodPut && r.URL.Path == "/api/companies/comp-1/skill-policy":
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			expectedRev, _ := body["expectedRevision"].(float64)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"schemaVersion": 1,
				"revision":      int64(expectedRev) + 1,
				"defaultEffect": body["defaultEffect"],
				"rules":         body["rules"],
				"materialized":  true,
			})
		case r.Method == http.MethodDelete && r.URL.Path == "/api/companies/comp-1/skill-policy":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"schemaVersion": 1,
				"revision":      0,
				"defaultEffect": "allow",
				"rules":         []map[string]interface{}{},
				"materialized":  false,
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

	// 1. RenameSkill
	skill, err := cli.RenameSkill(ctx, "comp-1", "skill-1", "New Name", "new-name")
	if err != nil || skill == nil || skill.Name != "New Name" || skill.Content != "# Renamed Content" {
		t.Fatalf("RenameSkill failed: skill=%+v, err=%v", skill, err)
	}

	// 2. PatchSkillMetadata
	if _, err := cli.PatchSkillMetadata(ctx, "comp-1", "skill-1", map[string]interface{}{"description": "desc"}); err != nil {
		t.Fatalf("PatchSkillMetadata failed: %v", err)
	}

	// 3. UpdateSkillFile
	fileDetail, err := cli.UpdateSkillFile(ctx, "comp-1", "skill-1", "extra.txt", "extra-content")
	if err != nil || fileDetail == nil || fileDetail.Content != "extra-content" {
		t.Fatalf("UpdateSkillFile failed: detail=%+v, err=%v", fileDetail, err)
	}

	// 4. DeleteSkillFile
	if err := cli.DeleteSkillFile(ctx, "comp-1", "skill-1", "extra.txt"); err != nil {
		t.Fatalf("DeleteSkillFile failed: %v", err)
	}

	// 5. GetSkillFile
	detail, err := cli.GetSkillFile(ctx, "comp-1", "skill-1", "test.py")
	if err != nil || detail == nil || detail.Content != "file-content-for-test.py" {
		t.Fatalf("GetSkillFile failed: detail=%+v, err=%v", detail, err)
	}

	// 6. CreateSkillVersion
	v, err := cli.CreateSkillVersion(ctx, "comp-1", "skill-1", "v1.0.0")
	if err != nil || v == nil || v.ID != "ver-1" || v.Label != "v1.0.0" {
		t.Fatalf("CreateSkillVersion failed: v=%+v, err=%v", v, err)
	}
	vNoLabel, err := cli.CreateSkillVersion(ctx, "comp-1", "skill-1", "")
	if err != nil || vNoLabel == nil {
		t.Fatalf("CreateSkillVersion without label failed: %v", err)
	}

	// 7. ListSkillVersions
	versions, err := cli.ListSkillVersions(ctx, "comp-1", "skill-1")
	if err != nil || len(versions) != 1 {
		t.Fatalf("ListSkillVersions failed: len=%d, err=%v", len(versions), err)
	}

	// 8. ImportSkill
	imported, err := cli.ImportSkill(ctx, "comp-1", "https://github.com/example/skill")
	if err != nil || imported == nil || imported.Content != "# Imported" || imported.CompanyID != "comp-1" {
		t.Fatalf("ImportSkill failed: imported=%+v, err=%v", imported, err)
	}
	_, err = cli.ImportSkill(ctx, "comp-1", "empty")
	if err == nil {
		t.Fatalf("expected error on empty skill import")
	}

	// 9. InstallCatalogSkill
	installed, err := cli.InstallCatalogSkill(ctx, "comp-1", "catalog-1", "installed-slug")
	if err != nil || installed == nil || installed.Slug != "installed-slug" || installed.Content != "# Catalog Content" {
		t.Fatalf("InstallCatalogSkill failed: installed=%+v, err=%v", installed, err)
	}
	installedNoSlug, err := cli.InstallCatalogSkill(ctx, "comp-1", "catalog-1", "")
	if err != nil || installedNoSlug == nil {
		t.Fatalf("InstallCatalogSkill without slug failed: %v", err)
	}

	// 10. GetAgentSkills
	agentSkills, err := cli.GetAgentSkills(ctx, "agent-1")
	if err != nil || agentSkills == nil || len(agentSkills.DesiredSkillEntries) != 1 {
		t.Fatalf("GetAgentSkills failed: skills=%+v, err=%v", agentSkills, err)
	}
	_, err = cli.GetAgentSkills(ctx, "")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound on empty agentID")
	}

	// 11. SyncAgentSkills
	synced, err := cli.SyncAgentSkills(ctx, "agent-1", "replace", []PaperclipDesiredSkillEntry{
		{Key: "skill-1"},
	})
	if err != nil || synced == nil {
		t.Fatalf("SyncAgentSkills failed: %v", err)
	}
	syncedNil, err := cli.SyncAgentSkills(ctx, "agent-1", "replace", nil)
	if err != nil || syncedNil == nil {
		t.Fatalf("SyncAgentSkills with nil desiredSkills failed: %v", err)
	}
	_, err = cli.SyncAgentSkills(ctx, "", "replace", nil)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound on empty agentID")
	}

	// 12. GetSkillPolicy
	pol, err := cli.GetSkillPolicy(ctx, "comp-1")
	if err != nil || pol == nil || pol.DefaultEffect != "allow" {
		t.Fatalf("GetSkillPolicy failed: pol=%+v, err=%v", pol, err)
	}
	_, err = cli.GetSkillPolicy(ctx, "")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound on empty companyID")
	}

	// 13. ReplaceSkillPolicy
	rep, err := cli.ReplaceSkillPolicy(ctx, "comp-1", 1, "deny", []map[string]interface{}{
		{"name": "rule-1"},
	})
	if err != nil || rep == nil || rep.Revision != 2 || rep.DefaultEffect != "deny" {
		t.Fatalf("ReplaceSkillPolicy failed: rep=%+v, err=%v", rep, err)
	}
	repNil, err := cli.ReplaceSkillPolicy(ctx, "comp-1", 2, "allow", nil)
	if err != nil || repNil == nil {
		t.Fatalf("ReplaceSkillPolicy with nil rules failed: %v", err)
	}
	_, err = cli.ReplaceSkillPolicy(ctx, "", 1, "deny", nil)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound on empty companyID")
	}

	// 14. ResetSkillPolicy
	rst, err := cli.ResetSkillPolicy(ctx, "comp-1")
	if err != nil || rst == nil || rst.Revision != 0 {
		t.Fatalf("ResetSkillPolicy failed: rst=%+v, err=%v", rst, err)
	}
	_, err = cli.ResetSkillPolicy(ctx, "")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound on empty companyID")
	}
}

func TestSyncAgentSkillsSerializesServerSelections(t *testing.T) {
	empty := ""
	version := "11111111-1111-4111-8111-111111111111"
	for _, tc := range []struct {
		name        string
		mode        string
		entries     []PaperclipDesiredSkillEntry
		wire        string
		wantEntries []PaperclipDesiredSkillEntry
	}{
		{name: "nil pin", mode: "replace", entries: []PaperclipDesiredSkillEntry{{Key: "paperclip"}}, wire: `["paperclip"]`, wantEntries: []PaperclipDesiredSkillEntry{{Key: "paperclip"}}},
		{name: "empty pin", mode: "replace", entries: []PaperclipDesiredSkillEntry{{Key: "paperclip", VersionID: &empty}}, wire: `["paperclip"]`, wantEntries: []PaperclipDesiredSkillEntry{{Key: "paperclip"}}},
		{name: "mixed pins", mode: "add", entries: []PaperclipDesiredSkillEntry{{Key: "paperclip"}, {Key: "pinned", VersionID: &version}}, wire: `["paperclip",{"key":"pinned","versionId":"11111111-1111-4111-8111-111111111111"}]`, wantEntries: []PaperclipDesiredSkillEntry{{Key: "paperclip"}, {Key: "pinned", VersionID: &version}}},
		{name: "nil list and default mode", wire: `[]`, wantEntries: []PaperclipDesiredSkillEntry{}},
		{name: "empty list", mode: "remove", entries: []PaperclipDesiredSkillEntry{}, wire: `[]`, wantEntries: []PaperclipDesiredSkillEntry{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var received map[string]interface{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/api/agents/agent-1/skills/sync" {
					http.NotFound(w, r)
					return
				}
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				if err := json.Unmarshal(body, &received); err != nil {
					t.Error(err)
				}
				snapshot, err := fixtures.SkillSyncSnapshot(bytes.NewReader(body))
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				_ = json.NewEncoder(w).Encode(snapshot)
			}))
			defer server.Close()
			cli, err := NewClient(server.URL, "test-token")
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := cli.SyncAgentSkills(context.Background(), "agent-1", tc.mode, tc.entries)
			if err != nil {
				t.Fatalf("server rejected desiredSkills: %v; request=%#v", err, received)
			}
			var wantWire interface{}
			if err := json.Unmarshal([]byte(tc.wire), &wantWire); err != nil {
				t.Fatal(err)
			}
			mode := tc.mode
			if mode == "" {
				mode = "replace"
			}
			if !reflect.DeepEqual(received, map[string]interface{}{"mode": mode, "desiredSkills": wantWire}) {
				t.Fatalf("request=%#v; want mode=%s desiredSkills=%s", received, mode, tc.wire)
			}
			keys := make([]string, len(tc.wantEntries))
			for i, entry := range tc.wantEntries {
				keys[i] = entry.Key
			}
			if !reflect.DeepEqual(snapshot.DesiredSkills, keys) || !reflect.DeepEqual(snapshot.DesiredSkillEntries, tc.wantEntries) || snapshot.Mode != mode {
				t.Fatalf("unexpected response snapshot: %+v", snapshot)
			}
		})
	}
}

func TestSkillSyncHTTPRejectsObjectWithoutVersionID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := fixtures.SkillSyncSnapshot(r.Body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	cli, err := NewClient(server.URL, "test-token")
	if err != nil {
		t.Fatal(err)
	}
	err = cli.paperclipRequest(context.Background(), http.MethodPost, "/api/agents/agent-1/skills/sync", map[string]interface{}{
		"mode": "replace", "desiredSkills": []interface{}{map[string]interface{}{"key": "paperclip"}},
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "400") || !strings.Contains(err.Error(), "versionId is required") {
		t.Fatalf("expected HTTP 400 for missing versionId, got %v", err)
	}
}

func TestSkillsAndPoliciesClientErrors(t *testing.T) {
	ctx := context.Background()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/skills/sync") {
			if _, err := fixtures.SkillSyncSnapshot(r.Body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		}
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
	}))
	defer server.Close()

	cli, err := NewClient(server.URL, "test-token")
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	if _, err := cli.RenameSkill(ctx, "c", "s", "n", "sl"); err == nil {
		t.Fatal("expected error")
	}
	if _, err := cli.PatchSkillMetadata(ctx, "c", "s", nil); err == nil {
		t.Fatal("expected error")
	}
	if _, err := cli.UpdateSkillFile(ctx, "c", "s", "p", "c"); err == nil {
		t.Fatal("expected error")
	}
	if err := cli.DeleteSkillFile(ctx, "c", "s", "p"); err == nil {
		t.Fatal("expected error")
	}
	if _, err := cli.GetSkillFile(ctx, "c", "s", "p"); err == nil {
		t.Fatal("expected error")
	}
	if _, err := cli.CreateSkillVersion(ctx, "c", "s", "l"); err == nil {
		t.Fatal("expected error")
	}
	if _, err := cli.ListSkillVersions(ctx, "c", "s"); err == nil {
		t.Fatal("expected error")
	}
	if _, err := cli.ImportSkill(ctx, "c", "src"); err == nil {
		t.Fatal("expected error")
	}
	if _, err := cli.InstallCatalogSkill(ctx, "c", "cat", "sl"); err == nil {
		t.Fatal("expected error")
	}
	if _, err := cli.GetAgentSkills(ctx, "a"); err == nil {
		t.Fatal("expected error")
	}
	if _, err := cli.SyncAgentSkills(ctx, "a", "replace", nil); err == nil {
		t.Fatal("expected error")
	}
	if _, err := cli.GetSkillPolicy(ctx, "c"); err == nil {
		t.Fatal("expected error")
	}
	if _, err := cli.ReplaceSkillPolicy(ctx, "c", 1, "deny", nil); err == nil {
		t.Fatal("expected error")
	}
	if _, err := cli.ResetSkillPolicy(ctx, "c"); err == nil {
		t.Fatal("expected error")
	}
}

func TestSkillsClientEdgeCasesAndBranchCoverage(t *testing.T) {
	ctx := context.Background()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/companies/comp-1/skills":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":       "skill-created",
				"markdown": "# Created Markdown",
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/companies/comp-1/skills/skill-1/rename":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"skill": map[string]interface{}{},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/companies/comp-1/skills/skill-no-comp/rename":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"skill": map[string]interface{}{
					"id":   "skill-no-comp",
					"name": "Renamed Without Comp",
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-1/skills/skill-1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":       "skill-1",
				"markdown": "# Skill 1 Fallback Markdown",
			})
		case r.Method == http.MethodPatch && r.URL.Path == "/api/companies/comp-1/skills/skill-1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":       "skill-1",
				"markdown": "# Patched Markdown",
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

	// 1. CreateSkill with all optional fields
	s, err := cli.CreateSkill(ctx, "comp-1", PaperclipSkill{
		Name:         "All Fields",
		Slug:         "all-fields",
		Description:  "desc",
		Content:      "content",
		IconURL:      "https://example.com/icon.png",
		Color:        "#123456",
		Tagline:      "tagline",
		AuthorName:   "author",
		HomepageURL:  "https://example.com",
		Categories:   []string{"cat1", "cat2"},
		SharingScope: "company",
		FolderID:     "folder-1",
		ForkedFromID: "fork-1",
		Metadata:     map[string]interface{}{"extra": true},
	})
	if err != nil || s == nil || s.Content != "# Created Markdown" || s.CompanyID != "comp-1" {
		t.Fatalf("CreateSkill full fields failed: s=%+v, err=%v", s, err)
	}

	// 2. RenameSkill with empty slug and fallback to GetSkill
	renamed, err := cli.RenameSkill(ctx, "comp-1", "skill-1", "New Name Only", "")
	if err != nil || renamed == nil || renamed.Content != "# Skill 1 Fallback Markdown" {
		t.Fatalf("RenameSkill fallback failed: s=%+v, err=%v", renamed, err)
	}

	// 3. PatchSkillMetadata with empty content and markdown response
	patched, err := cli.PatchSkillMetadata(ctx, "comp-1", "skill-1", map[string]interface{}{"k": "v"})
	if err != nil || patched == nil || patched.Content != "# Patched Markdown" || patched.CompanyID != "comp-1" {
		t.Fatalf("PatchSkillMetadata markdown fallback failed: s=%+v, err=%v", patched, err)
	}

	// 4. RenameSkill backfills companyID when empty in response
	renamedNoComp, err := cli.RenameSkill(ctx, "comp-1", "skill-no-comp", "Renamed", "renamed")
	if err != nil || renamedNoComp == nil || renamedNoComp.CompanyID != "comp-1" {
		t.Fatalf("RenameSkill companyId backfill failed: s=%+v, err=%v", renamedNoComp, err)
	}
}

func TestUpdateSkillCoverageAndContract(t *testing.T) {
	ctx := context.Background()

	var receivedBody map[string]interface{}
	var fileUpdated bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPatch && r.URL.Path == "/api/companies/comp-1/skills/sk-all":
			_ = json.NewDecoder(r.Body).Decode(&receivedBody)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":        "sk-all",
				"companyId": "comp-1",
			})
		case r.Method == http.MethodPatch && r.URL.Path == "/api/companies/comp-1/skills/sk-all/files":
			fileUpdated = true
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"skillId": "sk-all",
				"path":    "SKILL.md",
				"content": "# New Content",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-1/skills/sk-all":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":        "sk-all",
				"companyId": "comp-1",
				"name":      "All Set",
				"slug":      "all-set",
				"markdown":  "# New Content",
			})
		case r.Method == http.MethodPatch && r.URL.Path == "/api/companies/comp-1/skills/sk-err":
			http.Error(w, `{"error":"patch failed"}`, http.StatusInternalServerError)
		case r.Method == http.MethodPatch && r.URL.Path == "/api/companies/comp-1/skills/sk-file-err":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "sk-file-err"})
		case r.Method == http.MethodPatch && r.URL.Path == "/api/companies/comp-1/skills/sk-file-err/files":
			http.Error(w, `{"error":"file upload failed"}`, http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cli, err := NewClient(server.URL, "token")
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	// 1. UpdateSkill with all fields set
	updSkill, err := cli.UpdateSkill(ctx, "comp-1", "sk-all", PaperclipSkill{
		Name:         "All Set",
		Slug:         "all-set",
		Description:  "All desc",
		Content:      "# New Content",
		IconURL:      "https://example.com/icon.png",
		Color:        "#abcdef",
		Tagline:      "tag",
		AuthorName:   "author",
		HomepageURL:  "https://example.com",
		Categories:   []string{"cat1", "cat2"},
		SharingScope: "company",
		Metadata:     map[string]interface{}{"extra": "value"},
	})
	if err != nil || updSkill == nil || !fileUpdated {
		t.Fatalf("UpdateSkill with all fields failed: s=%+v, err=%v", updSkill, err)
	}
	// Assert GUS-416-R2-F1 contract: categories is present and non-null, sharingScope is present and non-empty
	if _, ok := receivedBody["categories"]; !ok || receivedBody["categories"] == nil {
		t.Fatalf("expected categories to be non-null in PATCH payload: %v", receivedBody)
	}
	if v, ok := receivedBody["sharingScope"].(string); !ok || v != "company" {
		t.Fatalf("expected sharingScope to be 'company' in PATCH payload: %v", receivedBody)
	}

	// 2. UpdateSkill with nil categories and empty sharingScope (GUS-416-R2-F1: must NOT send null or empty string)
	receivedBody = nil
	updMinimal, err := cli.UpdateSkill(ctx, "comp-1", "sk-all", PaperclipSkill{
		Description: "Minimal",
		Content:     "# New Content",
	})
	if err != nil || updMinimal == nil {
		t.Fatalf("UpdateSkill minimal failed: %v", err)
	}
	if _, ok := receivedBody["categories"]; ok {
		t.Fatalf("categories must be omitted from PATCH payload when unset: %v", receivedBody)
	}
	if _, ok := receivedBody["sharingScope"]; ok {
		t.Fatalf("sharingScope must be omitted from PATCH payload when unset: %v", receivedBody)
	}

	// 3. Error branches
	if _, err := cli.UpdateSkill(ctx, "comp-1", "sk-err", PaperclipSkill{}); err == nil {
		t.Fatal("expected error when PATCH fails")
	}
	if _, err := cli.UpdateSkill(ctx, "comp-1", "sk-file-err", PaperclipSkill{Content: "fail"}); err == nil {
		t.Fatal("expected error when file upload fails")
	}
}
