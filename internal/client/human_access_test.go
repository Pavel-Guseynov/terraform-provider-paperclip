package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHumanAccessClient(t *testing.T) {
	ctx := context.Background()

	t.Run("CreateCompanyInvite", func(t *testing.T) {
		role := "admin"
		msg := "Welcome"
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.Method != http.MethodPost || req.URL.Path != "/api/companies/comp-1/invites" {
				http.NotFound(w, req)
				return
			}
			var body map[string]interface{}
			_ = json.NewDecoder(req.Body).Decode(&body)
			if body["allowedJoinTypes"] != "human" || body["humanRole"] != "admin" || body["agentMessage"] != "Welcome" {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":               "inv-1",
				"companyId":        "comp-1",
				"inviteType":       "company_join",
				"allowedJoinTypes": "human",
				"humanRole":        "admin",
				"agentMessage":     "Welcome",
				"state":            "active",
				"expiresAt":        "2026-10-12T00:00:00Z",
				"createdAt":        "2026-10-09T00:00:00Z",
				"token":            "secret-token-12345",
			})
		}))
		defer server.Close()

		cli, _ := NewClient(server.URL, "token")
		inv, err := cli.CreateCompanyInvite(ctx, "comp-1", CreateCompanyInviteInput{
			AllowedJoinTypes: "human",
			HumanRole:        &role,
			AgentMessage:     &msg,
			DefaultsPayload:  map[string]interface{}{"k": "v"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if inv.ID != "inv-1" || inv.HumanRole == nil || *inv.HumanRole != "admin" {
			t.Fatalf("unexpected invite result: %+v", inv)
		}

		// Default allowedJoinTypes
		_, _ = cli.CreateCompanyInvite(ctx, "comp-1", CreateCompanyInviteInput{})

		// Error path
		_, err = cli.CreateCompanyInvite(ctx, "comp-bad", CreateCompanyInviteInput{})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("ListCompanyInvites and GetCompanyInvite", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.Method != http.MethodGet || req.URL.Path != "/api/companies/comp-1/invites" {
				http.NotFound(w, req)
				return
			}
			q := req.URL.Query()
			offset := q.Get("offset")
			w.Header().Set("Content-Type", "application/json")
			if offset == "0" || offset == "" {
				next := 1
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"invites": []map[string]interface{}{
						{"id": "inv-1", "companyId": "comp-1", "state": "active"},
					},
					"nextOffset": next,
				})
			} else {
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"invites": []map[string]interface{}{
						{"id": "inv-2", "companyId": "comp-1", "state": "active"},
					},
					"nextOffset": nil,
				})
			}
		}))
		defer server.Close()

		cli, _ := NewClient(server.URL, "token")
		// Test list options query parameters
		resp, err := cli.ListCompanyInvites(ctx, "comp-1", ListCompanyInvitesOptions{
			State:  "active",
			Limit:  10,
			Offset: 5,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(resp.Invites) == 0 {
			t.Fatal("expected invites, got 0")
		}

		// Test list error
		_, err = cli.ListCompanyInvites(ctx, "comp-bad", ListCompanyInvitesOptions{})
		if err == nil {
			t.Fatal("expected error")
		}

		// Find on first page
		inv1, err := cli.GetCompanyInvite(ctx, "comp-1", "inv-1")
		if err != nil || inv1.ID != "inv-1" {
			t.Fatalf("expected inv-1, got: %v, err: %v", inv1, err)
		}

		// Find on second page (pagination followed)
		inv2, err := cli.GetCompanyInvite(ctx, "comp-1", "inv-2")
		if err != nil || inv2.ID != "inv-2" {
			t.Fatalf("expected inv-2, got: %v, err: %v", inv2, err)
		}

		// Not found
		_, err = cli.GetCompanyInvite(ctx, "comp-1", "inv-missing")
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got: %v", err)
		}

		// Error from list
		_, err = cli.GetCompanyInvite(ctx, "comp-bad", "inv-1")
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("RevokeCompanyInvite", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.Method == http.MethodPost && req.URL.Path == "/api/invites/inv-1/revoke" {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"id":        "inv-1",
					"state":     "revoked",
					"revokedAt": "2026-10-09T01:00:00Z",
				})
				return
			}
			http.NotFound(w, req)
		}))
		defer server.Close()

		cli, _ := NewClient(server.URL, "token")
		inv, err := cli.RevokeCompanyInvite(ctx, "inv-1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if inv.State != "revoked" {
			t.Fatalf("expected revoked, got: %v", inv.State)
		}

		_, err = cli.RevokeCompanyInvite(ctx, "inv-bad")
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("Members CRUD", func(t *testing.T) {
		roleAdmin := "admin"
		statusActive := "active"
		agentReassign := "agent-1"
		userReassign := "user-2"

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case req.Method == http.MethodGet && req.URL.Path == "/api/companies/comp-1/members":
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"members": []map[string]interface{}{
						{
							"id":             "mem-1",
							"companyId":      "comp-1",
							"principalType":  "user",
							"principalId":    "user-1",
							"membershipRole": "operator",
							"status":         "active",
							"user": map[string]interface{}{
								"id":    "user-1",
								"email": "user1@example.com",
								"name":  "User One",
							},
							"grants": []map[string]interface{}{
								{
									"id":            "grant-1",
									"companyId":     "comp-1",
									"principalType": "user",
									"principalId":   "user-1",
									"permissionKey": "tasks:assign",
									"scope":         map[string]interface{}{"projectIds": []string{"p1"}},
								},
							},
						},
					},
				})
			case req.Method == http.MethodPatch && req.URL.Path == "/api/companies/comp-1/members/mem-1":
				var body map[string]interface{}
				_ = json.NewDecoder(req.Body).Decode(&body)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"id":             "mem-1",
					"companyId":      "comp-1",
					"principalType":  "user",
					"principalId":    "user-1",
					"membershipRole": body["membershipRole"],
					"status":         body["status"],
				})
			case req.Method == http.MethodPost && req.URL.Path == "/api/companies/comp-1/members/mem-1/archive":
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
			case req.Method == http.MethodPatch && req.URL.Path == "/api/companies/comp-1/members/mem-1/permissions":
				var body map[string]interface{}
				_ = json.NewDecoder(req.Body).Decode(&body)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"id":        "mem-1",
					"companyId": "comp-1",
					"grants":    body["grants"],
				})
			default:
				http.NotFound(w, req)
			}
		}))
		defer server.Close()

		cli, _ := NewClient(server.URL, "token")

		// List members
		members, err := cli.ListCompanyMembers(ctx, "comp-1")
		if err != nil || len(members) != 1 {
			t.Fatalf("unexpected list members: %v, err: %v", members, err)
		}

		// List error
		_, err = cli.ListCompanyMembers(ctx, "comp-bad")
		if err == nil {
			t.Fatal("expected error")
		}

		// Get member by member ID
		m1, err := cli.GetCompanyMember(ctx, "comp-1", "mem-1")
		if err != nil || m1.ID != "mem-1" {
			t.Fatalf("expected mem-1, got: %v, err: %v", m1, err)
		}

		// Get member by user ID
		m2, err := cli.GetCompanyMember(ctx, "comp-1", "user-1")
		if err != nil || m2.ID != "mem-1" {
			t.Fatalf("expected mem-1 via user-1, got: %v, err: %v", m2, err)
		}

		// Get member not found
		_, err = cli.GetCompanyMember(ctx, "comp-1", "user-missing")
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got: %v", err)
		}

		// Get member error
		_, err = cli.GetCompanyMember(ctx, "comp-bad", "mem-1")
		if err == nil {
			t.Fatal("expected error")
		}

		// Update member
		updated, err := cli.UpdateCompanyMember(ctx, "comp-1", "mem-1", UpdateCompanyMemberInput{
			MembershipRole: &roleAdmin,
			Status:         &statusActive,
		})
		if err != nil || updated.MembershipRole == nil || *updated.MembershipRole != "admin" {
			t.Fatalf("unexpected update result: %v, err: %v", updated, err)
		}

		// Update member error
		_, err = cli.UpdateCompanyMember(ctx, "comp-bad", "mem-1", UpdateCompanyMemberInput{})
		if err == nil {
			t.Fatal("expected error")
		}

		// Archive member with agent reassignment
		err = cli.ArchiveCompanyMember(ctx, "comp-1", "mem-1", &MemberReassignment{
			AssigneeAgentID: &agentReassign,
		})
		if err != nil {
			t.Fatalf("unexpected archive error: %v", err)
		}

		// Archive member with user reassignment
		err = cli.ArchiveCompanyMember(ctx, "comp-1", "mem-1", &MemberReassignment{
			AssigneeUserID: &userReassign,
		})
		if err != nil {
			t.Fatalf("unexpected archive error: %v", err)
		}

		// Archive member error
		err = cli.ArchiveCompanyMember(ctx, "comp-bad", "mem-1", nil)
		if err == nil {
			t.Fatal("expected error")
		}

		// Set member permissions
		permRes, err := cli.SetMemberPermissions(ctx, "comp-1", "mem-1", []PaperclipMemberGrantInput{
			{PermissionKey: "tasks:assign", Scope: map[string]interface{}{"projectIds": []string{"p1"}}},
		})
		if err != nil || permRes.ID != "mem-1" {
			t.Fatalf("unexpected set permissions result: %v, err: %v", permRes, err)
		}

		// Set member permissions error
		_, err = cli.SetMemberPermissions(ctx, "comp-bad", "mem-1", nil)
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("User Directory", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.Method == http.MethodGet && req.URL.Path == "/api/companies/comp-1/user-directory" {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"users": []map[string]interface{}{
						{
							"principalId": "user-1",
							"status":      "active",
							"user": map[string]interface{}{
								"id":    "user-1",
								"email": "user1@example.com",
								"name":  "User One",
							},
						},
					},
				})
				return
			}
			http.NotFound(w, req)
		}))
		defer server.Close()

		cli, _ := NewClient(server.URL, "token")
		users, err := cli.GetCompanyUserDirectory(ctx, "comp-1")
		if err != nil || len(users) != 1 {
			t.Fatalf("unexpected user directory result: %v, err: %v", users, err)
		}

		_, err = cli.GetCompanyUserDirectory(ctx, "comp-bad")
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("Admin and Instance Access", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case req.Method == http.MethodGet && req.URL.Path == "/api/admin/users":
				if req.URL.Query().Get("query") == "forbidden" {
					w.WriteHeader(http.StatusForbidden)
					_ = json.NewEncoder(w).Encode(map[string]interface{}{
						"error": "Instance user administration is managed by the hosting operator on this instance",
						"code":  "settings_operator_managed",
					})
					return
				}
				_ = json.NewEncoder(w).Encode([]map[string]interface{}{
					{
						"id":                           "user-1",
						"email":                        "admin@example.com",
						"name":                         "Admin User",
						"isInstanceAdmin":              true,
						"activeCompanyMembershipCount": 2,
					},
				})
			case req.Method == http.MethodPost && req.URL.Path == "/api/admin/users/user-1/promote-instance-admin":
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
			case req.Method == http.MethodPost && req.URL.Path == "/api/admin/users/user-1/demote-instance-admin":
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
			case req.Method == http.MethodGet && req.URL.Path == "/api/admin/users/user-1/company-access":
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"user": map[string]interface{}{
						"id":              "user-1",
						"email":           "admin@example.com",
						"name":            "Admin User",
						"isInstanceAdmin": true,
					},
					"companyAccess": []map[string]interface{}{
						{
							"id":        "mem-1",
							"companyId": "comp-1",
							"status":    "active",
						},
					},
				})
			case req.Method == http.MethodPut && req.URL.Path == "/api/admin/users/user-1/company-access":
				var body map[string]interface{}
				_ = json.NewDecoder(req.Body).Decode(&body)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"user": map[string]interface{}{
						"id":              "user-1",
						"isInstanceAdmin": true,
					},
					"companyAccess": []map[string]interface{}{},
				})
			case req.URL.Path == "/api/admin/users/user-forbidden/company-access" ||
				req.URL.Path == "/api/admin/users/user-forbidden/promote-instance-admin" ||
				req.URL.Path == "/api/admin/users/user-forbidden/demote-instance-admin":
				w.WriteHeader(http.StatusForbidden)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"error": "Instance user administration is managed by the hosting operator on this instance",
					"code":  "settings_operator_managed",
				})
			default:
				http.NotFound(w, req)
			}
		}))
		defer server.Close()

		cli, _ := NewClient(server.URL, "token")

		// ListAdminUsers success with and without query
		users, err := cli.ListAdminUsers(ctx, "")
		if err != nil || len(users) != 1 {
			t.Fatalf("unexpected admin users: %v, err: %v", users, err)
		}
		users, err = cli.ListAdminUsers(ctx, "admin")
		if err != nil || len(users) != 1 {
			t.Fatalf("unexpected admin users with query: %v, err: %v", users, err)
		}

		// ListAdminUsers hidden settings error
		_, err = cli.ListAdminUsers(ctx, "forbidden")
		if err == nil || !errors.Is(err, err) {
			t.Fatalf("expected error, got: %v", err)
		}

		// PromoteInstanceAdmin success and error
		err = cli.PromoteInstanceAdmin(ctx, "user-1")
		if err != nil {
			t.Fatalf("unexpected error promoting: %v", err)
		}
		err = cli.PromoteInstanceAdmin(ctx, "user-forbidden")
		if err == nil {
			t.Fatal("expected forbidden error")
		}

		// DemoteInstanceAdmin success and error
		err = cli.DemoteInstanceAdmin(ctx, "user-1")
		if err != nil {
			t.Fatalf("unexpected error demoting: %v", err)
		}
		err = cli.DemoteInstanceAdmin(ctx, "user-forbidden")
		if err == nil {
			t.Fatal("expected forbidden error")
		}

		// GetUserCompanyAccess success and error
		ca, err := cli.GetUserCompanyAccess(ctx, "user-1")
		if err != nil || ca.User == nil || !ca.User.IsInstanceAdmin {
			t.Fatalf("unexpected company access: %v, err: %v", ca, err)
		}
		_, err = cli.GetUserCompanyAccess(ctx, "user-forbidden")
		if err == nil {
			t.Fatal("expected forbidden error")
		}

		// SetUserCompanyAccess success and error
		setCa, err := cli.SetUserCompanyAccess(ctx, "user-1", []string{"comp-1"})
		if err != nil || setCa.User == nil {
			t.Fatalf("unexpected set company access: %v, err: %v", setCa, err)
		}
		_, err = cli.SetUserCompanyAccess(ctx, "user-forbidden", []string{})
		if err == nil {
			t.Fatal("expected forbidden error")
		}
	})

	t.Run("checkInstanceAccessError", func(t *testing.T) {
		if err := checkInstanceAccessError(nil); err != nil {
			t.Fatalf("expected nil, got: %v", err)
		}
		normalErr := errors.New("other failure")
		if err := checkInstanceAccessError(normalErr); err != normalErr {
			t.Fatalf("expected normalErr, got: %v", err)
		}
		apiErr := &APIError{
			StatusCode: http.StatusForbidden,
			Body:       `{"code":"settings_operator_managed"}`,
		}
		if err := checkInstanceAccessError(apiErr); err == nil {
			t.Fatal("expected transformed error")
		}
		strErr := errors.New("paperclip http 403: hosting operator on instance.access")
		if err := checkInstanceAccessError(strErr); err == nil {
			t.Fatal("expected transformed error")
		}
	})
}
