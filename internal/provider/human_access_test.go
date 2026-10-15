package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Pavel-Guseynov/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestHumanAccessProviderResourcesAndDataSources(t *testing.T) {
	ctx := context.Background()

	t.Run("Invitation Resource", func(t *testing.T) {
		r1 := NewPaperclipInvitationResource().(*PaperclipInvitationResource)
		r2 := NewPaperclipInviteResource().(*PaperclipInvitationResource)

		var metaResp resource.MetadataResponse
		r1.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "paperclip"}, &metaResp)
		if metaResp.TypeName != "paperclip_invitation" {
			t.Fatalf("expected paperclip_invitation, got: %s", metaResp.TypeName)
		}
		r2.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "paperclip"}, &metaResp)
		if metaResp.TypeName != "paperclip_invite" {
			t.Fatalf("expected paperclip_invite, got: %s", metaResp.TypeName)
		}

		var schemaResp resource.SchemaResponse
		r1.Schema(ctx, resource.SchemaRequest{}, &schemaResp)
		if len(schemaResp.Schema.Attributes) == 0 {
			t.Fatal("expected schema attributes")
		}

		// Configure
		var confResp resource.ConfigureResponse
		r1.Configure(ctx, resource.ConfigureRequest{}, &confResp)
		r1.Configure(ctx, resource.ConfigureRequest{ProviderData: "invalid"}, &confResp)
		if !confResp.Diagnostics.HasError() {
			t.Fatal("expected error with invalid provider data")
		}

		// Mock server
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case req.Method == http.MethodPost && req.URL.Path == "/api/companies/comp-1/invites":
				var body map[string]interface{}
				_ = json.NewDecoder(req.Body).Decode(&body)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"id":               "inv-1",
					"companyId":        "comp-1",
					"inviteType":       "company_join",
					"allowedJoinTypes": body["allowedJoinTypes"],
					"humanRole":        body["humanRole"],
					"inviteMessage":    body["agentMessage"],
					"defaultsPayload":  body["defaultsPayload"],
					"state":            "active",
					"expiresAt":        "2026-10-12T00:00:00Z",
					"createdAt":        "2026-10-09T00:00:00Z",
					"token":            "secret-token-redacted",
				})
			case req.URL.Path == "/api/companies/comp-err/invites":
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": "server error"})
			case req.Method == http.MethodGet && req.URL.Path == "/api/companies/comp-1/invites":
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"invites": []map[string]interface{}{
						{
							"id":               "inv-1",
							"companyId":        "comp-1",
							"allowedJoinTypes": "both",
							"humanRole":        "admin",
							"inviteMessage":    "msg",
							"defaultsPayload":  map[string]interface{}{"k": "v"},
							"state":            "active",
							"expiresAt":        "2026-10-12T00:00:00Z",
							"createdAt":        "2026-10-09T00:00:00Z",
						},
						{
							"id":        "inv-revoked",
							"companyId": "comp-1",
							"state":     "revoked",
							"revokedAt": "2026-10-09T01:00:00Z",
						},
					},
				})
			case req.Method == http.MethodPost && req.URL.Path == "/api/invites/inv-1/revoke":
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "inv-1", "state": "revoked"})
			case req.Method == http.MethodPost && req.URL.Path == "/api/invites/inv-error/revoke":
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": "server error"})
			case req.Method == http.MethodPost && req.URL.Path == "/api/invites/inv-404/revoke":
				http.NotFound(w, req)
			default:
				http.NotFound(w, req)
			}
		}))
		defer server.Close()

		cli, _ := client.NewClient(server.URL, "token")
		r1.client = cli

		// Create success
		plan := tfsdk.Plan{Schema: schemaResp.Schema}
		_ = plan.Set(ctx, &PaperclipInvitationResourceModel{
			CompanyID:        types.StringValue("comp-1"),
			AllowedJoinTypes: types.StringValue("both"),
			HumanRole:        types.StringValue("admin"),
			AgentMessage:     types.StringValue("msg"),
			DefaultsPayload:  types.StringValue(`{"k":"v"}`),
		})
		var createResp resource.CreateResponse
		createResp.State = tfsdk.State{Schema: schemaResp.Schema}
		r1.Create(ctx, resource.CreateRequest{Plan: plan}, &createResp)
		if createResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error creating invitation: %v", createResp.Diagnostics)
		}

		var createdState PaperclipInvitationResourceModel
		_ = createResp.State.Get(ctx, &createdState)
		if createdState.ID.ValueString() != "inv-1" {
			t.Fatalf("expected inv-1, got: %v", createdState.ID)
		}

		// Create with invalid defaults payload JSON
		planBadJSON := tfsdk.Plan{Schema: schemaResp.Schema}
		_ = planBadJSON.Set(ctx, &PaperclipInvitationResourceModel{
			CompanyID:       types.StringValue("comp-1"),
			DefaultsPayload: types.StringValue("not-json"),
		})
		var createBadJSONResp resource.CreateResponse
		r1.Create(ctx, resource.CreateRequest{Plan: planBadJSON}, &createBadJSONResp)
		if !createBadJSONResp.Diagnostics.HasError() {
			t.Fatal("expected error on invalid defaults payload JSON")
		}

		// Create API error
		planBadComp := tfsdk.Plan{Schema: schemaResp.Schema}
		_ = planBadComp.Set(ctx, &PaperclipInvitationResourceModel{
			CompanyID: types.StringValue("comp-bad"),
		})
		var createBadCompResp resource.CreateResponse
		r1.Create(ctx, resource.CreateRequest{Plan: planBadComp}, &createBadCompResp)
		if !createBadCompResp.Diagnostics.HasError() {
			t.Fatal("expected error on bad company")
		}

		// Read success
		stateActive := tfsdk.State{Schema: schemaResp.Schema}
		_ = stateActive.Set(ctx, &PaperclipInvitationResourceModel{
			ID:        types.StringValue("inv-1"),
			CompanyID: types.StringValue("comp-1"),
		})
		var readResp resource.ReadResponse
		readResp.State = tfsdk.State{Schema: schemaResp.Schema}
		r1.Read(ctx, resource.ReadRequest{State: stateActive}, &readResp)
		if readResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error reading invitation: %v", readResp.Diagnostics)
		}

		// Read revoked -> removes resource
		stateRevoked := tfsdk.State{Schema: schemaResp.Schema}
		_ = stateRevoked.Set(ctx, &PaperclipInvitationResourceModel{
			ID:        types.StringValue("inv-revoked"),
			CompanyID: types.StringValue("comp-1"),
		})
		var readRevokedResp resource.ReadResponse
		readRevokedResp.State = tfsdk.State{Schema: schemaResp.Schema}
		r1.Read(ctx, resource.ReadRequest{State: stateRevoked}, &readRevokedResp)
		var postRevokeState PaperclipInvitationResourceModel
		_ = readRevokedResp.State.Get(ctx, &postRevokeState)
		if !postRevokeState.ID.IsNull() && postRevokeState.ID.ValueString() != "" {
			t.Fatal("expected revoked invitation to be removed from state")
		}

		// Read missing -> removes resource
		stateMissing := tfsdk.State{Schema: schemaResp.Schema}
		_ = stateMissing.Set(ctx, &PaperclipInvitationResourceModel{
			ID:        types.StringValue("inv-missing"),
			CompanyID: types.StringValue("comp-1"),
		})
		var readMissingResp resource.ReadResponse
		readMissingResp.State = tfsdk.State{Schema: schemaResp.Schema}
		r1.Read(ctx, resource.ReadRequest{State: stateMissing}, &readMissingResp)

		// Read API error
		stateBadComp := tfsdk.State{Schema: schemaResp.Schema}
		_ = stateBadComp.Set(ctx, &PaperclipInvitationResourceModel{
			ID:        types.StringValue("inv-1"),
			CompanyID: types.StringValue("comp-err"),
		})
		var readBadCompResp resource.ReadResponse
		readBadCompResp.State = tfsdk.State{Schema: schemaResp.Schema}
		r1.Read(ctx, resource.ReadRequest{State: stateBadComp}, &readBadCompResp)
		if !readBadCompResp.Diagnostics.HasError() {
			t.Fatal("expected error on read bad company")
		}

		// Update not supported error
		var updateResp resource.UpdateResponse
		r1.Update(ctx, resource.UpdateRequest{}, &updateResp)
		if !updateResp.Diagnostics.HasError() {
			t.Fatal("expected error on update")
		}

		// Delete success
		var delResp resource.DeleteResponse
		r1.Delete(ctx, resource.DeleteRequest{State: stateActive}, &delResp)
		if delResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error deleting invitation: %v", delResp.Diagnostics)
		}

		// Delete 404 ignored
		state404 := tfsdk.State{Schema: schemaResp.Schema}
		_ = state404.Set(ctx, &PaperclipInvitationResourceModel{
			ID: types.StringValue("inv-404"),
		})
		var del404Resp resource.DeleteResponse
		r1.Delete(ctx, resource.DeleteRequest{State: state404}, &del404Resp)
		if del404Resp.Diagnostics.HasError() {
			t.Fatalf("unexpected error on 404 delete: %v", del404Resp.Diagnostics)
		}

		// Delete error
		stateErr := tfsdk.State{Schema: schemaResp.Schema}
		_ = stateErr.Set(ctx, &PaperclipInvitationResourceModel{
			ID: types.StringValue("inv-error"),
		})
		var delErrResp resource.DeleteResponse
		r1.Delete(ctx, resource.DeleteRequest{State: stateErr}, &delErrResp)
		if !delErrResp.Diagnostics.HasError() {
			t.Fatal("expected error on delete")
		}

		// ImportState
		var impResp resource.ImportStateResponse
		impResp.State = tfsdk.State{Schema: schemaResp.Schema}
		_ = impResp.State.Set(ctx, &PaperclipInvitationResourceModel{})
		r1.ImportState(ctx, resource.ImportStateRequest{ID: "comp-1/inv-1"}, &impResp)
		if impResp.Diagnostics.HasError() {
			t.Fatalf("unexpected import error: %v", impResp.Diagnostics)
		}
		var impSingleResp resource.ImportStateResponse
		impSingleResp.State = tfsdk.State{Schema: schemaResp.Schema}
		_ = impSingleResp.State.Set(ctx, &PaperclipInvitationResourceModel{})
		r1.ImportState(ctx, resource.ImportStateRequest{ID: "inv-1"}, &impSingleResp)
		var impEmptyResp resource.ImportStateResponse
		impEmptyResp.State = tfsdk.State{Schema: schemaResp.Schema}
		_ = impEmptyResp.State.Set(ctx, &PaperclipInvitationResourceModel{})
		r1.ImportState(ctx, resource.ImportStateRequest{ID: ""}, &impEmptyResp)
		if !impEmptyResp.Diagnostics.HasError() {
			t.Fatal("expected error on empty import ID")
		}
	})

	t.Run("Membership Resource", func(t *testing.T) {
		r := NewPaperclipMembershipResource().(*PaperclipMembershipResource)
		var metaResp resource.MetadataResponse
		r.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "paperclip"}, &metaResp)
		if metaResp.TypeName != "paperclip_membership" {
			t.Fatalf("expected paperclip_membership, got: %s", metaResp.TypeName)
		}

		var schemaResp resource.SchemaResponse
		r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)

		var confResp resource.ConfigureResponse
		r.Configure(ctx, resource.ConfigureRequest{}, &confResp)
		r.Configure(ctx, resource.ConfigureRequest{ProviderData: "bad"}, &confResp)
		if !confResp.Diagnostics.HasError() {
			t.Fatal("expected error")
		}

		members := []map[string]interface{}{
			{
				"id":             "mem-1",
				"companyId":      "comp-1",
				"principalType":  "user",
				"principalId":    "user-1",
				"membershipRole": "operator",
				"status":         "active",
			},
			{
				"id":             "mem-owner",
				"companyId":      "comp-1",
				"principalType":  "user",
				"principalId":    "user-owner",
				"membershipRole": "owner",
				"status":         "active",
			},
			{
				"id":             "mem-archived",
				"companyId":      "comp-1",
				"principalType":  "user",
				"principalId":    "user-archived",
				"membershipRole": "viewer",
				"status":         "archived",
			},
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case req.URL.Path == "/api/companies/comp-err/members":
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": "server error"})
			case req.Method == http.MethodGet && req.URL.Path == "/api/companies/comp-1/members":
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"members": members})
			case req.Method == http.MethodPatch && req.URL.Path == "/api/companies/comp-1/members/mem-1":
				var body map[string]interface{}
				_ = json.NewDecoder(req.Body).Decode(&body)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"id":             "mem-1",
					"companyId":      "comp-1",
					"principalId":    "user-1",
					"membershipRole": body["membershipRole"],
					"status":         body["status"],
				})
			case req.Method == http.MethodPatch && req.URL.Path == "/api/companies/comp-1/members/mem-owner":
				w.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"error": "Cannot remove the last active owner",
				})
			case req.Method == http.MethodPost && req.URL.Path == "/api/companies/comp-1/members/mem-1/archive":
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
			case req.Method == http.MethodPost && req.URL.Path == "/api/companies/comp-1/members/mem-owner/archive":
				w.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"error": "Cannot remove the last active owner",
				})
			default:
				http.NotFound(w, req)
			}
		}))
		defer server.Close()

		cli, _ := client.NewClient(server.URL, "token")
		r.client = cli

		// Create adopts existing membership and updates role
		plan := tfsdk.Plan{Schema: schemaResp.Schema}
		_ = plan.Set(ctx, &PaperclipMembershipResourceModel{
			CompanyID: types.StringValue("comp-1"),
			UserID:    types.StringValue("user-1"),
			Role:      types.StringValue("admin"),
			Status:    types.StringValue("active"),
		})
		var createResp resource.CreateResponse
		createResp.State = tfsdk.State{Schema: schemaResp.Schema}
		r.Create(ctx, resource.CreateRequest{Plan: plan}, &createResp)
		if createResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error creating membership: %v", createResp.Diagnostics)
		}
		var mState PaperclipMembershipResourceModel
		_ = createResp.State.Get(ctx, &mState)
		if mState.ID.ValueString() != "mem-1" || mState.Role.ValueString() != "admin" {
			t.Fatalf("unexpected created membership state: %+v", mState)
		}

		// Create when membership does not exist -> explicit error
		planMissing := tfsdk.Plan{Schema: schemaResp.Schema}
		_ = planMissing.Set(ctx, &PaperclipMembershipResourceModel{
			CompanyID: types.StringValue("comp-1"),
			UserID:    types.StringValue("user-missing"),
		})
		var createMissingResp resource.CreateResponse
		r.Create(ctx, resource.CreateRequest{Plan: planMissing}, &createMissingResp)
		if !createMissingResp.Diagnostics.HasError() {
			t.Fatal("expected error when adopting non-existent membership")
		}

		// Create when update returns error (last active owner)
		planOwner := tfsdk.Plan{Schema: schemaResp.Schema}
		_ = planOwner.Set(ctx, &PaperclipMembershipResourceModel{
			CompanyID: types.StringValue("comp-1"),
			UserID:    types.StringValue("user-owner"),
			Role:      types.StringValue("viewer"),
			Status:    types.StringValue("active"),
		})
		var createOwnerResp resource.CreateResponse
		r.Create(ctx, resource.CreateRequest{Plan: planOwner}, &createOwnerResp)
		if !createOwnerResp.Diagnostics.HasError() {
			t.Fatal("expected last active owner error")
		}

		// Read success (and defaults archive_on_destroy to true for zero-change plan)
		stateRead := tfsdk.State{Schema: schemaResp.Schema}
		_ = stateRead.Set(ctx, &PaperclipMembershipResourceModel{
			ID:        types.StringValue("mem-1"),
			CompanyID: types.StringValue("comp-1"),
		})
		var readResp resource.ReadResponse
		readResp.State = tfsdk.State{Schema: schemaResp.Schema}
		r.Read(ctx, resource.ReadRequest{State: stateRead}, &readResp)
		if readResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error reading membership: %v", readResp.Diagnostics)
		}
		var readModel PaperclipMembershipResourceModel
		_ = readResp.State.Get(ctx, &readModel)
		if !readModel.ArchiveOnDestroy.ValueBool() {
			t.Fatal("expected archive_on_destroy to default to true")
		}

		// Read archived -> removed from state
		stateArchived := tfsdk.State{Schema: schemaResp.Schema}
		_ = stateArchived.Set(ctx, &PaperclipMembershipResourceModel{
			ID:        types.StringValue("mem-archived"),
			CompanyID: types.StringValue("comp-1"),
		})
		var readArchResp resource.ReadResponse
		readArchResp.State = tfsdk.State{Schema: schemaResp.Schema}
		r.Read(ctx, resource.ReadRequest{State: stateArchived}, &readArchResp)

		// Read missing -> removed from state
		stateMiss := tfsdk.State{Schema: schemaResp.Schema}
		_ = stateMiss.Set(ctx, &PaperclipMembershipResourceModel{
			ID:        types.StringValue("mem-404"),
			CompanyID: types.StringValue("comp-1"),
		})
		var readMissResp resource.ReadResponse
		readMissResp.State = tfsdk.State{Schema: schemaResp.Schema}
		r.Read(ctx, resource.ReadRequest{State: stateMiss}, &readMissResp)

		// Read API error
		stateErr := tfsdk.State{Schema: schemaResp.Schema}
		_ = stateErr.Set(ctx, &PaperclipMembershipResourceModel{
			ID:        types.StringValue("mem-1"),
			CompanyID: types.StringValue("comp-err"),
		})
		var readErrResp resource.ReadResponse
		readErrResp.State = tfsdk.State{Schema: schemaResp.Schema}
		r.Read(ctx, resource.ReadRequest{State: stateErr}, &readErrResp)
		if !readErrResp.Diagnostics.HasError() {
			t.Fatal("expected error on bad company")
		}

		// Update success
		planUp := tfsdk.Plan{Schema: schemaResp.Schema}
		_ = planUp.Set(ctx, &PaperclipMembershipResourceModel{
			Role:   types.StringValue("operator"),
			Status: types.StringValue("suspended"),
		})
		var upResp resource.UpdateResponse
		upResp.State = tfsdk.State{Schema: schemaResp.Schema}
		r.Update(ctx, resource.UpdateRequest{Plan: planUp, State: stateRead}, &upResp)
		if upResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error updating membership: %v", upResp.Diagnostics)
		}

		// Update last owner conflict
		stateOwner := tfsdk.State{Schema: schemaResp.Schema}
		_ = stateOwner.Set(ctx, &PaperclipMembershipResourceModel{
			ID:        types.StringValue("mem-owner"),
			CompanyID: types.StringValue("comp-1"),
			Role:      types.StringValue("owner"),
			Status:    types.StringValue("active"),
		})
		planOwnerUp := tfsdk.Plan{Schema: schemaResp.Schema}
		_ = planOwnerUp.Set(ctx, &PaperclipMembershipResourceModel{
			Role:   types.StringValue("viewer"),
			Status: types.StringValue("active"),
		})
		var upOwnerResp resource.UpdateResponse
		r.Update(ctx, resource.UpdateRequest{Plan: planOwnerUp, State: stateOwner}, &upOwnerResp)
		if !upOwnerResp.Diagnostics.HasError() {
			t.Fatal("expected last active owner error on update")
		}

		// Delete with archive_on_destroy false
		stateNoArchive := tfsdk.State{Schema: schemaResp.Schema}
		_ = stateNoArchive.Set(ctx, &PaperclipMembershipResourceModel{
			ArchiveOnDestroy: types.BoolValue(false),
		})
		var delNoArchResp resource.DeleteResponse
		r.Delete(ctx, resource.DeleteRequest{State: stateNoArchive}, &delNoArchResp)

		// Delete with archive_on_destroy true and reassignment
		stateDelReassign := tfsdk.State{Schema: schemaResp.Schema}
		_ = stateDelReassign.Set(ctx, &PaperclipMembershipResourceModel{
			ID:                  types.StringValue("mem-1"),
			CompanyID:           types.StringValue("comp-1"),
			ArchiveOnDestroy:    types.BoolValue(true),
			ReassignmentAgentID: types.StringValue("agent-1"),
			ReassignmentUserID:  types.StringValue("user-2"),
		})
		var delResp resource.DeleteResponse
		r.Delete(ctx, resource.DeleteRequest{State: stateDelReassign}, &delResp)
		if delResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error deleting membership: %v", delResp.Diagnostics)
		}

		// Delete last owner error
		stateOwnerDel := tfsdk.State{Schema: schemaResp.Schema}
		_ = stateOwnerDel.Set(ctx, &PaperclipMembershipResourceModel{
			ID:               types.StringValue("mem-owner"),
			CompanyID:        types.StringValue("comp-1"),
			ArchiveOnDestroy: types.BoolValue(true),
		})
		var delOwnerResp resource.DeleteResponse
		r.Delete(ctx, resource.DeleteRequest{State: stateOwnerDel}, &delOwnerResp)
		if !delOwnerResp.Diagnostics.HasError() {
			t.Fatal("expected error archiving last active owner")
		}

		// ImportState
		var impResp resource.ImportStateResponse
		impResp.State = tfsdk.State{Schema: schemaResp.Schema}
		_ = impResp.State.Set(ctx, &PaperclipMembershipResourceModel{})
		r.ImportState(ctx, resource.ImportStateRequest{ID: "comp-1/mem-1"}, &impResp)
		var impSingleResp resource.ImportStateResponse
		impSingleResp.State = tfsdk.State{Schema: schemaResp.Schema}
		_ = impSingleResp.State.Set(ctx, &PaperclipMembershipResourceModel{})
		r.ImportState(ctx, resource.ImportStateRequest{ID: "mem-1"}, &impSingleResp)
		var impEmptyResp resource.ImportStateResponse
		impEmptyResp.State = tfsdk.State{Schema: schemaResp.Schema}
		_ = impEmptyResp.State.Set(ctx, &PaperclipMembershipResourceModel{})
		r.ImportState(ctx, resource.ImportStateRequest{ID: ""}, &impEmptyResp)
		if !impEmptyResp.Diagnostics.HasError() {
			t.Fatal("expected error on empty import ID")
		}
	})

	t.Run("Permission Grants Resource", func(t *testing.T) {
		r1 := NewPaperclipPermissionGrantsResource().(*PaperclipPermissionGrantsResource)
		r2 := NewPaperclipMemberPermissionsResource().(*PaperclipPermissionGrantsResource)

		var metaResp resource.MetadataResponse
		r1.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "paperclip"}, &metaResp)
		if metaResp.TypeName != "paperclip_permission_grants" {
			t.Fatalf("expected paperclip_permission_grants, got: %s", metaResp.TypeName)
		}
		r2.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "paperclip"}, &metaResp)
		if metaResp.TypeName != "paperclip_member_permissions" {
			t.Fatalf("expected paperclip_member_permissions, got: %s", metaResp.TypeName)
		}

		var schemaResp resource.SchemaResponse
		r1.Schema(ctx, resource.SchemaRequest{}, &schemaResp)

		var confResp resource.ConfigureResponse
		r1.Configure(ctx, resource.ConfigureRequest{}, &confResp)
		r1.Configure(ctx, resource.ConfigureRequest{ProviderData: "bad"}, &confResp)
		if !confResp.Diagnostics.HasError() {
			t.Fatal("expected error")
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case req.Method == http.MethodPatch && req.URL.Path == "/api/companies/comp-1/members/mem-1/permissions":
				var body map[string]interface{}
				_ = json.NewDecoder(req.Body).Decode(&body)
				grantsList, _ := body["grants"].([]interface{})
				resGrants := []map[string]interface{}{}
				for _, g := range grantsList {
					gm, _ := g.(map[string]interface{})
					resGrants = append(resGrants, map[string]interface{}{
						"permissionKey": gm["permissionKey"],
						"scope":         gm["scope"],
					})
				}
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"id":        "mem-1",
					"companyId": "comp-1",
					"grants":    resGrants,
				})
			case req.Method == http.MethodGet && req.URL.Path == "/api/companies/comp-1/members":
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"members": []map[string]interface{}{
						{
							"id":        "mem-1",
							"companyId": "comp-1",
							"status":    "active",
							"grants": []map[string]interface{}{
								{
									"permissionKey": "tasks:assign",
									"scope":         map[string]interface{}{"projectIds": []string{"p1"}},
								},
							},
						},
						{
							"id":        "mem-archived",
							"companyId": "comp-1",
							"status":    "archived",
						},
					},
				})
			default:
				http.NotFound(w, req)
			}
		}))
		defer server.Close()

		cli, _ := client.NewClient(server.URL, "token")
		r1.client = cli

		// Create success
		plan := tfsdk.Plan{Schema: schemaResp.Schema}
		_ = plan.Set(ctx, &PaperclipPermissionGrantsResourceModel{
			CompanyID: types.StringValue("comp-1"),
			MemberID:  types.StringValue("mem-1"),
			Grants: []PaperclipGrantModel{
				{
					PermissionKey: types.StringValue("tasks:assign"),
					Scope:         types.StringValue(`{"projectIds":["p1"]}`),
				},
			},
		})
		var createResp resource.CreateResponse
		createResp.State = tfsdk.State{Schema: schemaResp.Schema}
		r1.Create(ctx, resource.CreateRequest{Plan: plan}, &createResp)
		if createResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error creating grants: %v", createResp.Diagnostics)
		}

		// Create invalid scope JSON
		planBadScope := tfsdk.Plan{Schema: schemaResp.Schema}
		_ = planBadScope.Set(ctx, &PaperclipPermissionGrantsResourceModel{
			CompanyID: types.StringValue("comp-1"),
			MemberID:  types.StringValue("mem-1"),
			Grants: []PaperclipGrantModel{
				{
					PermissionKey: types.StringValue("tasks:assign"),
					Scope:         types.StringValue("bad-json"),
				},
			},
		})
		var createBadScopeResp resource.CreateResponse
		r1.Create(ctx, resource.CreateRequest{Plan: planBadScope}, &createBadScopeResp)
		if !createBadScopeResp.Diagnostics.HasError() {
			t.Fatal("expected error with bad scope JSON")
		}

		// Read success
		stateRead := tfsdk.State{Schema: schemaResp.Schema}
		_ = stateRead.Set(ctx, &PaperclipPermissionGrantsResourceModel{
			ID:        types.StringValue("comp-1/mem-1"),
			CompanyID: types.StringValue("comp-1"),
			MemberID:  types.StringValue("mem-1"),
		})
		var readResp resource.ReadResponse
		readResp.State = tfsdk.State{Schema: schemaResp.Schema}
		r1.Read(ctx, resource.ReadRequest{State: stateRead}, &readResp)
		if readResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error reading grants: %v", readResp.Diagnostics)
		}

		// Read archived -> removed from state
		stateArch := tfsdk.State{Schema: schemaResp.Schema}
		_ = stateArch.Set(ctx, &PaperclipPermissionGrantsResourceModel{
			ID:        types.StringValue("comp-1/mem-archived"),
			CompanyID: types.StringValue("comp-1"),
			MemberID:  types.StringValue("mem-archived"),
		})
		var readArchResp resource.ReadResponse
		readArchResp.State = tfsdk.State{Schema: schemaResp.Schema}
		r1.Read(ctx, resource.ReadRequest{State: stateArch}, &readArchResp)

		// Read not found -> removed from state
		stateMiss := tfsdk.State{Schema: schemaResp.Schema}
		_ = stateMiss.Set(ctx, &PaperclipPermissionGrantsResourceModel{
			ID:        types.StringValue("comp-1/mem-404"),
			CompanyID: types.StringValue("comp-1"),
			MemberID:  types.StringValue("mem-404"),
		})
		var readMissResp resource.ReadResponse
		readMissResp.State = tfsdk.State{Schema: schemaResp.Schema}
		r1.Read(ctx, resource.ReadRequest{State: stateMiss}, &readMissResp)

		// Update success
		var upResp resource.UpdateResponse
		upResp.State = tfsdk.State{Schema: schemaResp.Schema}
		r1.Update(ctx, resource.UpdateRequest{Plan: plan}, &upResp)
		if upResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error updating grants: %v", upResp.Diagnostics)
		}

		// Update error with bad scope
		var upBadResp resource.UpdateResponse
		r1.Update(ctx, resource.UpdateRequest{Plan: planBadScope}, &upBadResp)
		if !upBadResp.Diagnostics.HasError() {
			t.Fatal("expected error with bad scope on update")
		}

		// Delete success
		var delResp resource.DeleteResponse
		r1.Delete(ctx, resource.DeleteRequest{State: stateRead}, &delResp)
		if delResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error deleting grants: %v", delResp.Diagnostics)
		}

		// ImportState
		var impResp resource.ImportStateResponse
		impResp.State = tfsdk.State{Schema: schemaResp.Schema}
		_ = impResp.State.Set(ctx, &PaperclipPermissionGrantsResourceModel{})
		r1.ImportState(ctx, resource.ImportStateRequest{ID: "comp-1/mem-1"}, &impResp)
		if impResp.Diagnostics.HasError() {
			t.Fatalf("unexpected import error: %v", impResp.Diagnostics)
		}
		var impBadResp resource.ImportStateResponse
		impBadResp.State = tfsdk.State{Schema: schemaResp.Schema}
		_ = impBadResp.State.Set(ctx, &PaperclipPermissionGrantsResourceModel{})
		r1.ImportState(ctx, resource.ImportStateRequest{ID: "bad-id"}, &impBadResp)
		if !impBadResp.Diagnostics.HasError() {
			t.Fatal("expected error on bad import ID")
		}
	})

	t.Run("Instance Admin and Company Access Resources", func(t *testing.T) {
		rAdmin := NewPaperclipInstanceAdminResource().(*PaperclipInstanceAdminResource)
		rAccess := NewPaperclipUserCompanyAccessResource().(*PaperclipUserCompanyAccessResource)
		rInstAccess := NewPaperclipUserInstanceAccessResource().(*PaperclipUserInstanceAccessResource)

		var schemaAdmin resource.SchemaResponse
		rAdmin.Schema(ctx, resource.SchemaRequest{}, &schemaAdmin)
		var schemaAccess resource.SchemaResponse
		rAccess.Schema(ctx, resource.SchemaRequest{}, &schemaAccess)
		var schemaInst resource.SchemaResponse
		rInstAccess.Schema(ctx, resource.SchemaRequest{}, &schemaInst)

		isAdm := false
		currentAccess := []string{"comp-1"}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case req.Method == http.MethodPost && req.URL.Path == "/api/admin/users/user-1/promote-instance-admin":
				isAdm = true
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
			case req.Method == http.MethodPost && req.URL.Path == "/api/admin/users/user-1/demote-instance-admin":
				isAdm = false
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
			case req.Method == http.MethodGet && req.URL.Path == "/api/admin/users/user-1/company-access":
				cAccess := make([]map[string]interface{}, 0, len(currentAccess))
				for _, cid := range currentAccess {
					cAccess = append(cAccess, map[string]interface{}{"companyId": cid})
				}
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"user": map[string]interface{}{
						"id":              "user-1",
						"isInstanceAdmin": isAdm,
					},
					"companyAccess": cAccess,
				})
			case req.Method == http.MethodPut && req.URL.Path == "/api/admin/users/user-1/company-access":
				var body map[string]interface{}
				_ = json.NewDecoder(req.Body).Decode(&body)
				cids, _ := body["companyIds"].([]interface{})
				currentAccess = nil
				cAccess := make([]map[string]interface{}, 0, len(cids))
				for _, cid := range cids {
					currentAccess = append(currentAccess, cid.(string))
					cAccess = append(cAccess, map[string]interface{}{"companyId": cid})
				}
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"user": map[string]interface{}{
						"id":              "user-1",
						"isInstanceAdmin": isAdm,
					},
					"companyAccess": cAccess,
				})
			default:
				http.NotFound(w, req)
			}
		}))
		defer server.Close()

		cli, _ := client.NewClient(server.URL, "token")
		rAdmin.client = cli
		rAccess.client = cli
		rInstAccess.client = cli

		// InstanceAdmin CRUD
		planAdmin := tfsdk.Plan{Schema: schemaAdmin.Schema}
		_ = planAdmin.Set(ctx, &PaperclipInstanceAdminResourceModel{
			UserID:          types.StringValue("user-1"),
			IsInstanceAdmin: types.BoolValue(true),
		})
		var createAdminResp resource.CreateResponse
		createAdminResp.State = tfsdk.State{Schema: schemaAdmin.Schema}
		rAdmin.Create(ctx, resource.CreateRequest{Plan: planAdmin}, &createAdminResp)
		if createAdminResp.Diagnostics.HasError() || !isAdm {
			t.Fatalf("unexpected error promoting: %v", createAdminResp.Diagnostics)
		}

		stateAdmin := tfsdk.State{Schema: schemaAdmin.Schema}
		_ = stateAdmin.Set(ctx, &PaperclipInstanceAdminResourceModel{
			ID:              types.StringValue("user-1"),
			UserID:          types.StringValue("user-1"),
			IsInstanceAdmin: types.BoolValue(true),
		})
		var readAdminResp resource.ReadResponse
		readAdminResp.State = tfsdk.State{Schema: schemaAdmin.Schema}
		rAdmin.Read(ctx, resource.ReadRequest{State: stateAdmin}, &readAdminResp)
		if readAdminResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error reading admin: %v", readAdminResp.Diagnostics)
		}

		planAdminDemote := tfsdk.Plan{Schema: schemaAdmin.Schema}
		_ = planAdminDemote.Set(ctx, &PaperclipInstanceAdminResourceModel{
			UserID:          types.StringValue("user-1"),
			IsInstanceAdmin: types.BoolValue(false),
		})
		var upAdminResp resource.UpdateResponse
		upAdminResp.State = tfsdk.State{Schema: schemaAdmin.Schema}
		rAdmin.Update(ctx, resource.UpdateRequest{Plan: planAdminDemote, State: stateAdmin}, &upAdminResp)
		if upAdminResp.Diagnostics.HasError() || isAdm {
			t.Fatalf("unexpected error demoting: %v", upAdminResp.Diagnostics)
		}

		var delAdminResp resource.DeleteResponse
		rAdmin.Delete(ctx, resource.DeleteRequest{State: stateAdmin}, &delAdminResp)

		var impAdminResp resource.ImportStateResponse
		impAdminResp.State = tfsdk.State{Schema: schemaAdmin.Schema}
		_ = impAdminResp.State.Set(ctx, &PaperclipInstanceAdminResourceModel{})
		rAdmin.ImportState(ctx, resource.ImportStateRequest{ID: "user-1"}, &impAdminResp)
		if impAdminResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error importing admin: %v", impAdminResp.Diagnostics)
		}

		// UserCompanyAccess CRUD
		cidsList := stringSliceToTypesList(ctx, []string{"comp-1"})
		planAccess := tfsdk.Plan{Schema: schemaAccess.Schema}
		_ = planAccess.Set(ctx, &PaperclipUserCompanyAccessResourceModel{
			UserID:     types.StringValue("user-1"),
			CompanyIDs: cidsList,
		})
		var createAccessResp resource.CreateResponse
		createAccessResp.State = tfsdk.State{Schema: schemaAccess.Schema}
		rAccess.Create(ctx, resource.CreateRequest{Plan: planAccess}, &createAccessResp)
		if createAccessResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error creating access: %v", createAccessResp.Diagnostics)
		}

		stateAccess := tfsdk.State{Schema: schemaAccess.Schema}
		_ = stateAccess.Set(ctx, &PaperclipUserCompanyAccessResourceModel{
			ID:         types.StringValue("user-1"),
			UserID:     types.StringValue("user-1"),
			CompanyIDs: cidsList,
		})
		var readAccessResp resource.ReadResponse
		readAccessResp.State = tfsdk.State{Schema: schemaAccess.Schema}
		rAccess.Read(ctx, resource.ReadRequest{State: stateAccess}, &readAccessResp)
		if readAccessResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error reading access: %v", readAccessResp.Diagnostics)
		}

		var upAccessResp resource.UpdateResponse
		upAccessResp.State = tfsdk.State{Schema: schemaAccess.Schema}
		rAccess.Update(ctx, resource.UpdateRequest{Plan: planAccess, State: stateAccess}, &upAccessResp)
		if upAccessResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error updating access: %v", upAccessResp.Diagnostics)
		}

		var delAccessResp resource.DeleteResponse
		rAccess.Delete(ctx, resource.DeleteRequest{State: stateAccess}, &delAccessResp)

		var impAccessResp resource.ImportStateResponse
		impAccessResp.State = tfsdk.State{Schema: schemaAccess.Schema}
		_ = impAccessResp.State.Set(ctx, &PaperclipUserCompanyAccessResourceModel{
			CompanyIDs: types.ListNull(types.StringType),
		})
		rAccess.ImportState(ctx, resource.ImportStateRequest{ID: "user-1"}, &impAccessResp)
		if impAccessResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error importing access: %v", impAccessResp.Diagnostics)
		}

		// UserInstanceAccess CRUD
		planInst := tfsdk.Plan{Schema: schemaInst.Schema}
		_ = planInst.Set(ctx, &PaperclipUserInstanceAccessResourceModel{
			UserID:          types.StringValue("user-1"),
			IsInstanceAdmin: types.BoolValue(true),
			CompanyIDs:      cidsList,
		})
		var createInstResp resource.CreateResponse
		createInstResp.State = tfsdk.State{Schema: schemaInst.Schema}
		rInstAccess.Create(ctx, resource.CreateRequest{Plan: planInst}, &createInstResp)
		if createInstResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error creating inst access: %v", createInstResp.Diagnostics)
		}

		stateInst := tfsdk.State{Schema: schemaInst.Schema}
		_ = stateInst.Set(ctx, &PaperclipUserInstanceAccessResourceModel{
			ID:              types.StringValue("user-1"),
			UserID:          types.StringValue("user-1"),
			IsInstanceAdmin: types.BoolValue(true),
			CompanyIDs:      cidsList,
		})
		var readInstResp resource.ReadResponse
		readInstResp.State = tfsdk.State{Schema: schemaInst.Schema}
		rInstAccess.Read(ctx, resource.ReadRequest{State: stateInst}, &readInstResp)
		if readInstResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error reading inst access: %v", readInstResp.Diagnostics)
		}

		var upInstResp resource.UpdateResponse
		upInstResp.State = tfsdk.State{Schema: schemaInst.Schema}
		rInstAccess.Update(ctx, resource.UpdateRequest{Plan: planInst, State: stateInst}, &upInstResp)
		if upInstResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error updating inst access: %v", upInstResp.Diagnostics)
		}

		var delInstResp resource.DeleteResponse
		rInstAccess.Delete(ctx, resource.DeleteRequest{State: stateInst}, &delInstResp)
		if delInstResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error deleting inst access: %v", delInstResp.Diagnostics)
		}
		if isAdm {
			t.Fatal("expected isAdm to be false after successful Delete of user instance access")
		}
		if len(currentAccess) != 0 {
			t.Fatalf("expected currentAccess to be empty after Delete, got %v", currentAccess)
		}

		var impInstResp resource.ImportStateResponse
		impInstResp.State = tfsdk.State{Schema: schemaInst.Schema}
		_ = impInstResp.State.Set(ctx, &PaperclipUserInstanceAccessResourceModel{
			CompanyIDs: types.ListNull(types.StringType),
		})
		rInstAccess.ImportState(ctx, resource.ImportStateRequest{ID: "user-1"}, &impInstResp)
		if impInstResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error importing inst access: %v", impInstResp.Diagnostics)
		}
	})

	t.Run("Data Sources", func(t *testing.T) {
		dUser := NewPaperclipUserDataSource().(*PaperclipUserDataSource)
		dUsers := NewPaperclipUsersDataSource().(*PaperclipUsersDataSource)

		var metaUser datasource.MetadataResponse
		dUser.Metadata(ctx, datasource.MetadataRequest{ProviderTypeName: "paperclip"}, &metaUser)
		var metaUsers datasource.MetadataResponse
		dUsers.Metadata(ctx, datasource.MetadataRequest{ProviderTypeName: "paperclip"}, &metaUsers)

		var schemaUser datasource.SchemaResponse
		dUser.Schema(ctx, datasource.SchemaRequest{}, &schemaUser)
		var schemaUsers datasource.SchemaResponse
		dUsers.Schema(ctx, datasource.SchemaRequest{}, &schemaUsers)

		var confResp datasource.ConfigureResponse
		dUser.Configure(ctx, datasource.ConfigureRequest{}, &confResp)
		dUser.Configure(ctx, datasource.ConfigureRequest{ProviderData: "bad"}, &confResp)
		if !confResp.Diagnostics.HasError() {
			t.Fatal("expected configure error")
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case req.Method == http.MethodGet && req.URL.Path == "/api/companies/comp-1/user-directory":
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
			case req.Method == http.MethodGet && req.URL.Path == "/api/admin/users":
				_ = json.NewEncoder(w).Encode([]map[string]interface{}{
					{
						"id":                           "user-admin",
						"email":                        "admin@example.com",
						"name":                         "Admin User",
						"isInstanceAdmin":              true,
						"activeCompanyMembershipCount": 1,
					},
				})
			default:
				http.NotFound(w, req)
			}
		}))
		defer server.Close()

		cli, _ := client.NewClient(server.URL, "token")
		dUser.client = cli
		dUsers.client = cli

		// dUser: missing identifier error
		planEmpty := tfsdk.Plan{Schema: schemaUser.Schema}
		_ = planEmpty.Set(ctx, &PaperclipUserDataSourceModel{})
		var readEmptyResp datasource.ReadResponse
		dUser.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: schemaUser.Schema, Raw: planEmpty.Raw}}, &readEmptyResp)
		if !readEmptyResp.Diagnostics.HasError() {
			t.Fatal("expected error on empty user lookup")
		}

		// dUser: lookup in company user directory by email
		planComp := tfsdk.Plan{Schema: schemaUser.Schema}
		_ = planComp.Set(ctx, &PaperclipUserDataSourceModel{
			CompanyID: types.StringValue("comp-1"),
			Email:     types.StringValue("user1@example.com"),
		})
		var readCompResp datasource.ReadResponse
		readCompResp.State = tfsdk.State{Schema: schemaUser.Schema}
		dUser.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: schemaUser.Schema, Raw: planComp.Raw}}, &readCompResp)
		if readCompResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error reading user in company: %v", readCompResp.Diagnostics)
		}

		// dUser: lookup in admin users by id
		planAdmin := tfsdk.Plan{Schema: schemaUser.Schema}
		_ = planAdmin.Set(ctx, &PaperclipUserDataSourceModel{
			ID: types.StringValue("user-admin"),
		})
		var readAdminResp datasource.ReadResponse
		readAdminResp.State = tfsdk.State{Schema: schemaUser.Schema}
		dUser.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: schemaUser.Schema, Raw: planAdmin.Raw}}, &readAdminResp)
		if readAdminResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error reading admin user: %v", readAdminResp.Diagnostics)
		}

		// dUsers: list in company
		planUsersComp := tfsdk.Plan{Schema: schemaUsers.Schema}
		_ = planUsersComp.Set(ctx, &PaperclipUsersDataSourceModel{
			CompanyID: types.StringValue("comp-1"),
		})
		var readUsersCompResp datasource.ReadResponse
		readUsersCompResp.State = tfsdk.State{Schema: schemaUsers.Schema}
		dUsers.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: schemaUsers.Schema, Raw: planUsersComp.Raw}}, &readUsersCompResp)
		if readUsersCompResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error reading users in comp: %v", readUsersCompResp.Diagnostics)
		}

		// dUsers: list admin users
		planUsersAdmin := tfsdk.Plan{Schema: schemaUsers.Schema}
		_ = planUsersAdmin.Set(ctx, &PaperclipUsersDataSourceModel{
			Query: types.StringValue("admin"),
		})
		var readUsersAdminResp datasource.ReadResponse
		readUsersAdminResp.State = tfsdk.State{Schema: schemaUsers.Schema}
		dUsers.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: schemaUsers.Schema, Raw: planUsersAdmin.Raw}}, &readUsersAdminResp)
		if readUsersAdminResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error reading users admin: %v", readUsersAdminResp.Diagnostics)
		}
	})
}

func TestHumanAccessBranchCoverage(t *testing.T) {
	ctx := context.Background()

	t.Run("Invitation Branches", func(t *testing.T) {
		r := NewPaperclipInvitationResource().(*PaperclipInvitationResource)
		var sResp resource.SchemaResponse
		r.Schema(ctx, resource.SchemaRequest{}, &sResp)

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			revAt := "2026-10-09T01:00:00Z"
			accAt := "2026-10-09T02:00:00Z"
			switch req.Method {
			case http.MethodPost:
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"id":         "inv-full",
					"companyId":  "comp-1",
					"state":      "active",
					"revokedAt":  revAt,
					"acceptedAt": accAt,
				})
			case http.MethodGet:
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"invites": []map[string]interface{}{
						{
							"id":         "inv-full",
							"companyId":  "comp-1",
							"state":      "active",
							"revokedAt":  revAt,
							"acceptedAt": accAt,
						},
						{
							"id":            "inv-accepted-only",
							"companyId":     "comp-1",
							"state":         "active",
							"acceptedAt":    accAt,
							"humanRole":     nil,
							"inviteMessage": nil,
						},
					},
				})
			}
		}))
		defer server.Close()

		cli, _ := client.NewClient(server.URL, "token")
		r.client = cli

		plan := tfsdk.Plan{Schema: sResp.Schema}
		_ = plan.Set(ctx, &PaperclipInvitationResourceModel{
			CompanyID: types.StringValue("comp-1"),
		})
		var cResp resource.CreateResponse
		cResp.State = tfsdk.State{Schema: sResp.Schema}
		r.Create(ctx, resource.CreateRequest{Plan: plan}, &cResp)
		if cResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error creating: %v", cResp.Diagnostics)
		}

		state := tfsdk.State{Schema: sResp.Schema}
		_ = state.Set(ctx, &PaperclipInvitationResourceModel{
			ID:        types.StringValue("inv-full"),
			CompanyID: types.StringValue("comp-1"),
		})
		var rResp resource.ReadResponse
		rResp.State = tfsdk.State{Schema: sResp.Schema}
		r.Read(ctx, resource.ReadRequest{State: state}, &rResp)

		stateAccepted := tfsdk.State{Schema: sResp.Schema}
		_ = stateAccepted.Set(ctx, &PaperclipInvitationResourceModel{
			ID:        types.StringValue("inv-accepted-only"),
			CompanyID: types.StringValue("comp-1"),
		})
		var rAccResp resource.ReadResponse
		rAccResp.State = tfsdk.State{Schema: sResp.Schema}
		r.Read(ctx, resource.ReadRequest{State: stateAccepted}, &rAccResp)
		if rAccResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error reading accepted invite: %v", rAccResp.Diagnostics)
		}
	})

	t.Run("Membership Branches", func(t *testing.T) {
		r := NewPaperclipMembershipResource().(*PaperclipMembershipResource)
		var sResp resource.SchemaResponse
		r.Schema(ctx, resource.SchemaRequest{}, &sResp)

		statusChanged := false
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case req.URL.Path == "/api/companies/comp-err/members":
				http.Error(w, `{"error":"err"}`, http.StatusInternalServerError)
			case req.Method == http.MethodGet && req.URL.Path == "/api/companies/comp-1/members":
				role := "admin"
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"members": []map[string]interface{}{
						{
							"id":             "mem-1",
							"companyId":      "comp-1",
							"principalType":  "user",
							"principalId":    "user-1",
							"membershipRole": role,
							"status":         "active",
						},
					},
				})
			case req.Method == http.MethodPatch:
				statusChanged = true
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"id":             "mem-1",
					"companyId":      "comp-1",
					"principalId":    "user-1",
					"membershipRole": "admin",
					"status":         "suspended",
				})
			}
		}))
		defer server.Close()

		cli, _ := client.NewClient(server.URL, "token")
		r.client = cli

		// Create with GetCompanyMember error
		planErr := tfsdk.Plan{Schema: sResp.Schema}
		_ = planErr.Set(ctx, &PaperclipMembershipResourceModel{
			CompanyID: types.StringValue("comp-err"),
			UserID:    types.StringValue("user-1"),
		})
		var cErrResp resource.CreateResponse
		r.Create(ctx, resource.CreateRequest{Plan: planErr}, &cErrResp)
		if !cErrResp.Diagnostics.HasError() {
			t.Fatal("expected error")
		}

		// Create with null Role and Status (adopts existing)
		planAdopt := tfsdk.Plan{Schema: sResp.Schema}
		_ = planAdopt.Set(ctx, &PaperclipMembershipResourceModel{
			CompanyID: types.StringValue("comp-1"),
			UserID:    types.StringValue("user-1"),
		})
		var cAdoptResp resource.CreateResponse
		cAdoptResp.State = tfsdk.State{Schema: sResp.Schema}
		r.Create(ctx, resource.CreateRequest{Plan: planAdopt}, &cAdoptResp)
		if cAdoptResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error: %v", cAdoptResp.Diagnostics)
		}

		// Create matching existing role and status (no update needed, enters else branch)
		planMatch := tfsdk.Plan{Schema: sResp.Schema}
		_ = planMatch.Set(ctx, &PaperclipMembershipResourceModel{
			CompanyID: types.StringValue("comp-1"),
			UserID:    types.StringValue("user-1"),
			Role:      types.StringValue("admin"),
			Status:    types.StringValue("active"),
		})
		var cMatchResp resource.CreateResponse
		cMatchResp.State = tfsdk.State{Schema: sResp.Schema}
		r.Create(ctx, resource.CreateRequest{Plan: planMatch}, &cMatchResp)
		if cMatchResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error: %v", cMatchResp.Diagnostics)
		}

		// Update with status change
		planUp := tfsdk.Plan{Schema: sResp.Schema}
		_ = planUp.Set(ctx, &PaperclipMembershipResourceModel{
			Role:   types.StringValue("admin"),
			Status: types.StringValue("suspended"),
		})
		stateUp := tfsdk.State{Schema: sResp.Schema}
		_ = stateUp.Set(ctx, &PaperclipMembershipResourceModel{
			ID:        types.StringValue("mem-1"),
			CompanyID: types.StringValue("comp-1"),
			Role:      types.StringValue("admin"),
			Status:    types.StringValue("active"),
		})
		var uResp resource.UpdateResponse
		uResp.State = tfsdk.State{Schema: sResp.Schema}
		r.Update(ctx, resource.UpdateRequest{Plan: planUp, State: stateUp}, &uResp)
		if uResp.Diagnostics.HasError() || !statusChanged {
			t.Fatalf("unexpected error on status change: %v", uResp.Diagnostics)
		}
	})

	t.Run("Permission Grants Branches", func(t *testing.T) {
		r := NewPaperclipPermissionGrantsResource().(*PaperclipPermissionGrantsResource)
		var sResp resource.SchemaResponse
		r.Schema(ctx, resource.SchemaRequest{}, &sResp)

		failSet := false
		failGet := false
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if failSet && req.Method == http.MethodPatch {
				http.Error(w, `{"error":"err"}`, http.StatusInternalServerError)
				return
			}
			if failGet && req.Method == http.MethodGet {
				http.Error(w, `{"error":"err"}`, http.StatusInternalServerError)
				return
			}
			switch req.Method {
			case http.MethodPatch:
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"id": "mem-1", "grants": []map[string]interface{}{
						{"permissionKey": "b:perm"},
						{"permissionKey": "a:perm"},
					},
				})
			case http.MethodGet:
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"id": "mem-1", "status": "active", "grants": []map[string]interface{}{
						{"permissionKey": "b:perm"},
						{"permissionKey": "a:perm"},
					},
				})
			}
		}))
		defer server.Close()

		cli, _ := client.NewClient(server.URL, "token")
		r.client = cli

		// Create with multiple grants (sort coverage)
		plan := tfsdk.Plan{Schema: sResp.Schema}
		_ = plan.Set(ctx, &PaperclipPermissionGrantsResourceModel{
			CompanyID: types.StringValue("comp-1"),
			MemberID:  types.StringValue("mem-1"),
			Grants: []PaperclipGrantModel{
				{PermissionKey: types.StringValue("b:perm")},
				{PermissionKey: types.StringValue("a:perm")},
			},
		})
		var cResp resource.CreateResponse
		cResp.State = tfsdk.State{Schema: sResp.Schema}
		r.Create(ctx, resource.CreateRequest{Plan: plan}, &cResp)
		if cResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error: %v", cResp.Diagnostics)
		}

		// Read error
		failGet = true
		state := tfsdk.State{Schema: sResp.Schema}
		_ = state.Set(ctx, &PaperclipPermissionGrantsResourceModel{
			ID:        types.StringValue("comp-1/mem-1"),
			CompanyID: types.StringValue("comp-1"),
			MemberID:  types.StringValue("mem-1"),
		})
		var rResp resource.ReadResponse
		rResp.State = tfsdk.State{Schema: sResp.Schema}
		r.Read(ctx, resource.ReadRequest{State: state}, &rResp)
		if !rResp.Diagnostics.HasError() {
			t.Fatal("expected read error")
		}
		failGet = false

		// Create error
		failSet = true
		var cErrResp resource.CreateResponse
		cErrResp.State = tfsdk.State{Schema: sResp.Schema}
		r.Create(ctx, resource.CreateRequest{Plan: plan}, &cErrResp)
		if !cErrResp.Diagnostics.HasError() {
			t.Fatal("expected create error")
		}

		// Update error
		var uErrResp resource.UpdateResponse
		uErrResp.State = tfsdk.State{Schema: sResp.Schema}
		r.Update(ctx, resource.UpdateRequest{Plan: plan}, &uErrResp)
		if !uErrResp.Diagnostics.HasError() {
			t.Fatal("expected update error")
		}

		// Delete error
		var dErrResp resource.DeleteResponse
		dErrResp.State = tfsdk.State{Schema: sResp.Schema}
		r.Delete(ctx, resource.DeleteRequest{State: state}, &dErrResp)
		if !dErrResp.Diagnostics.HasError() {
			t.Fatal("expected delete error")
		}
	})

	t.Run("Instance Admin Branches", func(t *testing.T) {
		r := NewPaperclipInstanceAdminResource().(*PaperclipInstanceAdminResource)
		var sResp resource.SchemaResponse
		r.Schema(ctx, resource.SchemaRequest{}, &sResp)

		failPromote := false
		failDemote := false
		return404 := false
		return500 := false
		return403 := false

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if return403 {
				http.Error(w, `{"error":"instance user administration is managed by the hosting operator on this instance (instance.access hidden)"}`, http.StatusForbidden)
				return
			}
			if return404 {
				http.NotFound(w, req)
				return
			}
			if return500 {
				http.Error(w, `{"error":"err"}`, http.StatusInternalServerError)
				return
			}
			if failPromote && strings.Contains(req.URL.Path, "promote") {
				http.Error(w, `{"error":"err"}`, http.StatusInternalServerError)
				return
			}
			if failDemote && strings.Contains(req.URL.Path, "demote") {
				http.Error(w, `{"error":"err"}`, http.StatusInternalServerError)
				return
			}
			switch req.Method {
			case http.MethodGet:
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"user": map[string]interface{}{"id": "user-1", "isInstanceAdmin": true},
				})
			default:
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
			}
		}))
		defer server.Close()

		cli, _ := client.NewClient(server.URL, "token")
		r.client = cli

		// Create error
		failPromote = true
		plan := tfsdk.Plan{Schema: sResp.Schema}
		_ = plan.Set(ctx, &PaperclipInstanceAdminResourceModel{
			UserID:          types.StringValue("user-1"),
			IsInstanceAdmin: types.BoolValue(true),
		})
		var cErrResp resource.CreateResponse
		r.Create(ctx, resource.CreateRequest{Plan: plan}, &cErrResp)
		if !cErrResp.Diagnostics.HasError() {
			t.Fatal("expected create error")
		}
		failPromote = false

		// Read 404
		return404 = true
		state := tfsdk.State{Schema: sResp.Schema}
		_ = state.Set(ctx, &PaperclipInstanceAdminResourceModel{
			ID:     types.StringValue("user-1"),
			UserID: types.StringValue("user-1"),
		})
		var r404Resp resource.ReadResponse
		r404Resp.State = tfsdk.State{Schema: sResp.Schema}
		r.Read(ctx, resource.ReadRequest{State: state}, &r404Resp)
		if r404Resp.Diagnostics.HasError() {
			t.Fatal("expected no error on 404")
		}
		return404 = false

		// Read 500
		return500 = true
		var r500Resp resource.ReadResponse
		r.Read(ctx, resource.ReadRequest{State: state}, &r500Resp)
		if !r500Resp.Diagnostics.HasError() {
			t.Fatal("expected error on 500")
		}
		return500 = false

		// Update promote (false to true)
		planPromote := tfsdk.Plan{Schema: sResp.Schema}
		_ = planPromote.Set(ctx, &PaperclipInstanceAdminResourceModel{
			UserID:          types.StringValue("user-1"),
			IsInstanceAdmin: types.BoolValue(true),
		})
		stateDemoted := tfsdk.State{Schema: sResp.Schema}
		_ = stateDemoted.Set(ctx, &PaperclipInstanceAdminResourceModel{
			ID:              types.StringValue("user-1"),
			UserID:          types.StringValue("user-1"),
			IsInstanceAdmin: types.BoolValue(false),
		})
		var uPromoteResp resource.UpdateResponse
		uPromoteResp.State = tfsdk.State{Schema: sResp.Schema}
		r.Update(ctx, resource.UpdateRequest{Plan: planPromote, State: stateDemoted}, &uPromoteResp)
		if uPromoteResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error: %v", uPromoteResp.Diagnostics)
		}

		// Update demote error
		failDemote = true
		planDemote := tfsdk.Plan{Schema: sResp.Schema}
		_ = planDemote.Set(ctx, &PaperclipInstanceAdminResourceModel{
			UserID:          types.StringValue("user-1"),
			IsInstanceAdmin: types.BoolValue(false),
		})
		statePromoted := tfsdk.State{Schema: sResp.Schema}
		_ = statePromoted.Set(ctx, &PaperclipInstanceAdminResourceModel{
			ID:              types.StringValue("user-1"),
			UserID:          types.StringValue("user-1"),
			IsInstanceAdmin: types.BoolValue(true),
		})
		var uDemoteErrResp resource.UpdateResponse
		r.Update(ctx, resource.UpdateRequest{Plan: planDemote, State: statePromoted}, &uDemoteErrResp)
		if !uDemoteErrResp.Diagnostics.HasError() {
			t.Fatal("expected demote error")
		}

		// Delete demote error
		var dErrResp resource.DeleteResponse
		r.Delete(ctx, resource.DeleteRequest{State: statePromoted}, &dErrResp)
		if !dErrResp.Diagnostics.HasError() {
			t.Fatal("expected delete error")
		}

		// ModifyPlan: nil client, null plan, success, and 403 error
		rNil := NewPaperclipInstanceAdminResource().(*PaperclipInstanceAdminResource)
		var mpNilResp resource.ModifyPlanResponse
		rNil.ModifyPlan(ctx, resource.ModifyPlanRequest{}, &mpNilResp)
		var mpNullPlan resource.ModifyPlanResponse
		r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: tfsdk.Plan{}}, &mpNullPlan)

		var mpSuccessResp resource.ModifyPlanResponse
		r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan}, &mpSuccessResp)
		if mpSuccessResp.Diagnostics.HasError() {
			t.Fatalf("unexpected ModifyPlan error: %v", mpSuccessResp.Diagnostics)
		}

		return403 = true
		var mp403Resp resource.ModifyPlanResponse
		r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan}, &mp403Resp)
		if !mp403Resp.Diagnostics.HasError() {
			t.Fatal("expected ModifyPlan error when instance access is operator-managed")
		}
		return403 = false
	})

	t.Run("Access Resources Branches", func(t *testing.T) {
		badList := types.ListValueMust(types.Int64Type, []attr.Value{types.Int64Value(123)})
		if len(extractCompanyIDsFromList(ctx, badList)) != 0 {
			t.Fatal("expected empty list on bad list")
		}
		if len(extractCompanyIDsFromList(ctx, types.ListNull(types.StringType))) != 0 {
			t.Fatal("expected empty on null list")
		}
		if len(extractCompanyIDsFromList(ctx, types.ListUnknown(types.StringType))) != 0 {
			t.Fatal("expected empty on unknown list")
		}

		rAcc := NewPaperclipUserCompanyAccessResource().(*PaperclipUserCompanyAccessResource)
		var sAccResp resource.SchemaResponse
		rAcc.Schema(ctx, resource.SchemaRequest{}, &sAccResp)

		rInst := NewPaperclipUserInstanceAccessResource().(*PaperclipUserInstanceAccessResource)
		var sInstResp resource.SchemaResponse
		rInst.Schema(ctx, resource.SchemaRequest{}, &sInstResp)

		failSet := false
		return404 := false
		return500 := false
		failPromote := false
		failDemote := false
		return403 := false

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if return403 {
				http.Error(w, `{"error":"instance user administration is managed by the hosting operator on this instance (instance.access hidden)"}`, http.StatusForbidden)
				return
			}
			if return404 {
				http.NotFound(w, req)
				return
			}
			if return500 {
				http.Error(w, `{"error":"err"}`, http.StatusInternalServerError)
				return
			}
			if failPromote && strings.Contains(req.URL.Path, "promote") {
				http.Error(w, `{"error":"err"}`, http.StatusInternalServerError)
				return
			}
			if failDemote && strings.Contains(req.URL.Path, "demote") {
				http.Error(w, `{"error":"err"}`, http.StatusInternalServerError)
				return
			}
			if failSet && req.Method == http.MethodPut {
				http.Error(w, `{"error":"err"}`, http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"user": map[string]interface{}{"id": "user-1", "isInstanceAdmin": true},
				"companyAccess": []map[string]interface{}{
					{"companyId": "comp-1"},
				},
			})
		}))
		defer server.Close()

		cli, _ := client.NewClient(server.URL, "token")
		rAcc.client = cli
		rInst.client = cli

		cidsList := stringSliceToTypesList(ctx, []string{"comp-1"})

		// rAcc: Create error with badList
		planBadList := tfsdk.Plan{Schema: sAccResp.Schema}
		_ = planBadList.Set(ctx, &PaperclipUserCompanyAccessResourceModel{
			UserID:     types.StringValue("user-1"),
			CompanyIDs: badList,
		})
		var cBadListResp resource.CreateResponse
		rAcc.Create(ctx, resource.CreateRequest{Plan: planBadList}, &cBadListResp)
		if !cBadListResp.Diagnostics.HasError() {
			t.Fatal("expected error with badList")
		}

		// rAcc: Create error with server failure
		failSet = true
		planValid := tfsdk.Plan{Schema: sAccResp.Schema}
		_ = planValid.Set(ctx, &PaperclipUserCompanyAccessResourceModel{
			UserID:     types.StringValue("user-1"),
			CompanyIDs: cidsList,
		})
		var cErrResp resource.CreateResponse
		rAcc.Create(ctx, resource.CreateRequest{Plan: planValid}, &cErrResp)
		if !cErrResp.Diagnostics.HasError() {
			t.Fatal("expected error on server fail")
		}
		failSet = false

		// rAcc: Read 404
		return404 = true
		state := tfsdk.State{Schema: sAccResp.Schema}
		_ = state.Set(ctx, &PaperclipUserCompanyAccessResourceModel{
			ID:         types.StringValue("user-1"),
			UserID:     types.StringValue("user-1"),
			CompanyIDs: types.ListNull(types.StringType),
		})
		var r404Resp resource.ReadResponse
		r404Resp.State = tfsdk.State{Schema: sAccResp.Schema}
		rAcc.Read(ctx, resource.ReadRequest{State: state}, &r404Resp)
		if r404Resp.Diagnostics.HasError() {
			t.Fatal("expected no error on 404")
		}
		return404 = false

		// rAcc: Read 500
		return500 = true
		var r500Resp resource.ReadResponse
		rAcc.Read(ctx, resource.ReadRequest{State: state}, &r500Resp)
		if !r500Resp.Diagnostics.HasError() {
			t.Fatal("expected error on 500")
		}
		return500 = false

		// rAcc: Update with badList
		var uBadListResp resource.UpdateResponse
		rAcc.Update(ctx, resource.UpdateRequest{Plan: planBadList}, &uBadListResp)
		if !uBadListResp.Diagnostics.HasError() {
			t.Fatal("expected error with badList on update")
		}

		// rAcc: Update error with server failure
		failSet = true
		var uErrResp resource.UpdateResponse
		rAcc.Update(ctx, resource.UpdateRequest{Plan: planValid}, &uErrResp)
		if !uErrResp.Diagnostics.HasError() {
			t.Fatal("expected error on server fail update")
		}

		// rAcc: Delete error
		var dErrResp resource.DeleteResponse
		rAcc.Delete(ctx, resource.DeleteRequest{State: state}, &dErrResp)
		if !dErrResp.Diagnostics.HasError() {
			t.Fatal("expected error on delete")
		}
		failSet = false

		// rInst: Create error on promote
		failPromote = true
		planInst := tfsdk.Plan{Schema: sInstResp.Schema}
		_ = planInst.Set(ctx, &PaperclipUserInstanceAccessResourceModel{
			UserID:          types.StringValue("user-1"),
			IsInstanceAdmin: types.BoolValue(true),
			CompanyIDs:      cidsList,
		})
		var cInstPromoteErrResp resource.CreateResponse
		rInst.Create(ctx, resource.CreateRequest{Plan: planInst}, &cInstPromoteErrResp)
		if !cInstPromoteErrResp.Diagnostics.HasError() {
			t.Fatal("expected promote error on create")
		}
		failPromote = false

		// rInst: Create error with badList
		planInstBadList := tfsdk.Plan{Schema: sInstResp.Schema}
		_ = planInstBadList.Set(ctx, &PaperclipUserInstanceAccessResourceModel{
			UserID:          types.StringValue("user-1"),
			IsInstanceAdmin: types.BoolValue(false),
			CompanyIDs:      badList,
		})
		var cInstBadListResp resource.CreateResponse
		rInst.Create(ctx, resource.CreateRequest{Plan: planInstBadList}, &cInstBadListResp)
		if !cInstBadListResp.Diagnostics.HasError() {
			t.Fatal("expected bad list error on create")
		}

		// rInst: Create error on set company access
		failSet = true
		var cInstSetErrResp resource.CreateResponse
		rInst.Create(ctx, resource.CreateRequest{Plan: planInst}, &cInstSetErrResp)
		if !cInstSetErrResp.Diagnostics.HasError() {
			t.Fatal("expected set error on create")
		}
		failSet = false

		// rInst: Read 404 and 500
		stateInst := tfsdk.State{Schema: sInstResp.Schema}
		_ = stateInst.Set(ctx, &PaperclipUserInstanceAccessResourceModel{
			ID:              types.StringValue("user-1"),
			UserID:          types.StringValue("user-1"),
			CompanyIDs:      types.ListNull(types.StringType),
			IsInstanceAdmin: types.BoolValue(false),
		})
		return404 = true
		var rInst404Resp resource.ReadResponse
		rInst404Resp.State = tfsdk.State{Schema: sInstResp.Schema}
		rInst.Read(ctx, resource.ReadRequest{State: stateInst}, &rInst404Resp)
		if rInst404Resp.Diagnostics.HasError() {
			t.Fatal("expected no error on 404")
		}
		return404 = false

		return500 = true
		var rInst500Resp resource.ReadResponse
		rInst.Read(ctx, resource.ReadRequest{State: stateInst}, &rInst500Resp)
		if !rInst500Resp.Diagnostics.HasError() {
			t.Fatal("expected error on 500")
		}
		return500 = false

		// rInst: Update toggle admin promote error
		failPromote = true
		stateDemotedInst := tfsdk.State{Schema: sInstResp.Schema}
		_ = stateDemotedInst.Set(ctx, &PaperclipUserInstanceAccessResourceModel{
			ID:              types.StringValue("user-1"),
			UserID:          types.StringValue("user-1"),
			IsInstanceAdmin: types.BoolValue(false),
			CompanyIDs:      cidsList,
		})
		var uInstPromoteErrResp resource.UpdateResponse
		rInst.Update(ctx, resource.UpdateRequest{Plan: planInst, State: stateDemotedInst}, &uInstPromoteErrResp)
		if !uInstPromoteErrResp.Diagnostics.HasError() {
			t.Fatal("expected promote error on update")
		}
		failPromote = false

		// rInst: Update with badList
		var uInstBadListResp resource.UpdateResponse
		rInst.Update(ctx, resource.UpdateRequest{Plan: planInstBadList, State: stateDemotedInst}, &uInstBadListResp)
		if !uInstBadListResp.Diagnostics.HasError() {
			t.Fatal("expected bad list error on update")
		}

		// rInst: Update error on set company access
		failSet = true
		var uInstSetErrResp resource.UpdateResponse
		rInst.Update(ctx, resource.UpdateRequest{Plan: planInst, State: stateDemotedInst}, &uInstSetErrResp)
		if !uInstSetErrResp.Diagnostics.HasError() {
			t.Fatal("expected set error on update")
		}
		failSet = false

		// rInst: Update toggle admin demote success
		planDemoteInst := tfsdk.Plan{Schema: sInstResp.Schema}
		_ = planDemoteInst.Set(ctx, &PaperclipUserInstanceAccessResourceModel{
			UserID:          types.StringValue("user-1"),
			IsInstanceAdmin: types.BoolValue(false),
			CompanyIDs:      cidsList,
		})
		statePromotedInst := tfsdk.State{Schema: sInstResp.Schema}
		_ = statePromotedInst.Set(ctx, &PaperclipUserInstanceAccessResourceModel{
			ID:              types.StringValue("user-1"),
			UserID:          types.StringValue("user-1"),
			IsInstanceAdmin: types.BoolValue(true),
			CompanyIDs:      cidsList,
		})
		var uInstDemoteResp resource.UpdateResponse
		uInstDemoteResp.State = tfsdk.State{Schema: sInstResp.Schema}
		rInst.Update(ctx, resource.UpdateRequest{Plan: planDemoteInst, State: statePromotedInst}, &uInstDemoteResp)
		if uInstDemoteResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error on demote update: %v", uInstDemoteResp.Diagnostics)
		}

		// rInst: Update toggle admin demote error
		failDemote = true
		var uInstDemoteErrResp resource.UpdateResponse
		rInst.Update(ctx, resource.UpdateRequest{Plan: planDemoteInst, State: statePromotedInst}, &uInstDemoteErrResp)
		if !uInstDemoteErrResp.Diagnostics.HasError() {
			t.Fatal("expected demote error on update")
		}
		failDemote = false

		// rAcc ModifyPlan tests
		rAccNil := NewPaperclipUserCompanyAccessResource().(*PaperclipUserCompanyAccessResource)
		var mpAccNilResp resource.ModifyPlanResponse
		rAccNil.ModifyPlan(ctx, resource.ModifyPlanRequest{}, &mpAccNilResp)
		var mpAccNullPlan resource.ModifyPlanResponse
		rAcc.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: tfsdk.Plan{}}, &mpAccNullPlan)

		var mpAccSuccessResp resource.ModifyPlanResponse
		rAcc.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: planValid}, &mpAccSuccessResp)
		if mpAccSuccessResp.Diagnostics.HasError() {
			t.Fatalf("unexpected ModifyPlan error on rAcc: %v", mpAccSuccessResp.Diagnostics)
		}

		return403 = true
		var mpAcc403Resp resource.ModifyPlanResponse
		rAcc.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: planValid}, &mpAcc403Resp)
		if !mpAcc403Resp.Diagnostics.HasError() {
			t.Fatal("expected ModifyPlan error on rAcc when instance access is operator-managed")
		}
		return403 = false

		// rInst ModifyPlan tests
		rInstNil := NewPaperclipUserInstanceAccessResource().(*PaperclipUserInstanceAccessResource)
		var mpInstNilResp resource.ModifyPlanResponse
		rInstNil.ModifyPlan(ctx, resource.ModifyPlanRequest{}, &mpInstNilResp)
		var mpInstNullPlan resource.ModifyPlanResponse
		rInst.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: tfsdk.Plan{}}, &mpInstNullPlan)

		var mpInstSuccessResp resource.ModifyPlanResponse
		rInst.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: planInst}, &mpInstSuccessResp)
		if mpInstSuccessResp.Diagnostics.HasError() {
			t.Fatalf("unexpected ModifyPlan error on rInst: %v", mpInstSuccessResp.Diagnostics)
		}

		return403 = true
		var mpInst403Resp resource.ModifyPlanResponse
		rInst.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: planInst}, &mpInst403Resp)
		if !mpInst403Resp.Diagnostics.HasError() {
			t.Fatal("expected ModifyPlan error on rInst when instance access is operator-managed")
		}
		return403 = false
	})

	t.Run("Data Sources Branches", func(t *testing.T) {
		dUser := NewPaperclipUserDataSource().(*PaperclipUserDataSource)
		dUsers := NewPaperclipUsersDataSource().(*PaperclipUsersDataSource)
		var sUserResp datasource.SchemaResponse
		dUser.Schema(ctx, datasource.SchemaRequest{}, &sUserResp)
		var sUsersResp datasource.SchemaResponse
		dUsers.Schema(ctx, datasource.SchemaRequest{}, &sUsersResp)

		img := "https://example.com/avatar.png"
		failDir := false
		failAdmin := false

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if failDir && strings.Contains(req.URL.Path, "user-directory") {
				http.Error(w, `{"error":"err"}`, http.StatusInternalServerError)
				return
			}
			if failAdmin && strings.Contains(req.URL.Path, "admin/users") {
				http.Error(w, `{"error":"err"}`, http.StatusInternalServerError)
				return
			}
			switch {
			case strings.Contains(req.URL.Path, "user-directory"):
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"users": []map[string]interface{}{
						{"principalId": "nil-user", "user": nil},
						{"principalId": "user-minimal", "user": map[string]interface{}{
							"id":    "user-minimal",
							"email": nil,
							"name":  nil,
							"image": &img,
						}},
						{"principalId": "user-exact", "user": map[string]interface{}{
							"id":    "user-exact",
							"email": "exact@example.com",
							"name":  "Exact Name",
						}},
					},
				})
			default:
				_ = json.NewEncoder(w).Encode([]map[string]interface{}{
					{
						"id":              "admin-minimal",
						"email":           nil,
						"name":            nil,
						"image":           &img,
						"isInstanceAdmin": false,
					},
				})
			}
		}))
		defer server.Close()

		cli, _ := client.NewClient(server.URL, "token")
		dUser.client = cli
		dUsers.client = cli

		// dUser: directory error
		failDir = true
		planCompErr := tfsdk.Plan{Schema: sUserResp.Schema}
		_ = planCompErr.Set(ctx, &PaperclipUserDataSourceModel{
			CompanyID: types.StringValue("comp-1"),
			ID:        types.StringValue("user-1"),
		})
		var rUserDirErr datasource.ReadResponse
		dUser.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: sUserResp.Schema, Raw: planCompErr.Raw}}, &rUserDirErr)
		if !rUserDirErr.Diagnostics.HasError() {
			t.Fatal("expected error on dir failure")
		}
		failDir = false

		// dUser: found user with nil email/name and non-nil image in directory
		planMinimal := tfsdk.Plan{Schema: sUserResp.Schema}
		_ = planMinimal.Set(ctx, &PaperclipUserDataSourceModel{
			CompanyID: types.StringValue("comp-1"),
			ID:        types.StringValue("user-minimal"),
		})
		var rUserMinResp datasource.ReadResponse
		rUserMinResp.State = tfsdk.State{Schema: sUserResp.Schema}
		dUser.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: sUserResp.Schema, Raw: planMinimal.Raw}}, &rUserMinResp)
		if rUserMinResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error: %v", rUserMinResp.Diagnostics)
		}

		// dUser: user not found in company directory
		planNotFoundDir := tfsdk.Plan{Schema: sUserResp.Schema}
		_ = planNotFoundDir.Set(ctx, &PaperclipUserDataSourceModel{
			CompanyID: types.StringValue("comp-1"),
			ID:        types.StringValue("not-found"),
		})
		var rUserNotFoundDir datasource.ReadResponse
		dUser.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: sUserResp.Schema, Raw: planNotFoundDir.Raw}}, &rUserNotFoundDir)
		if !rUserNotFoundDir.Diagnostics.HasError() {
			t.Fatal("expected not found in dir")
		}

		// dUser: admin error
		failAdmin = true
		planAdminErr := tfsdk.Plan{Schema: sUserResp.Schema}
		_ = planAdminErr.Set(ctx, &PaperclipUserDataSourceModel{
			ID: types.StringValue("admin-1"),
		})
		var rUserAdminErr datasource.ReadResponse
		dUser.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: sUserResp.Schema, Raw: planAdminErr.Raw}}, &rUserAdminErr)
		if !rUserAdminErr.Diagnostics.HasError() {
			t.Fatal("expected error on admin failure")
		}
		failAdmin = false

		// dUser: admin user with nil email/name and non-nil image
		planAdminMin := tfsdk.Plan{Schema: sUserResp.Schema}
		_ = planAdminMin.Set(ctx, &PaperclipUserDataSourceModel{
			ID: types.StringValue("admin-minimal"),
		})
		var rUserAdminMin datasource.ReadResponse
		rUserAdminMin.State = tfsdk.State{Schema: sUserResp.Schema}
		dUser.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: sUserResp.Schema, Raw: planAdminMin.Raw}}, &rUserAdminMin)
		if rUserAdminMin.Diagnostics.HasError() {
			t.Fatalf("unexpected error: %v", rUserAdminMin.Diagnostics)
		}

		// dUser: not found in admin users
		planAdminNotFound := tfsdk.Plan{Schema: sUserResp.Schema}
		_ = planAdminNotFound.Set(ctx, &PaperclipUserDataSourceModel{
			ID: types.StringValue("not-found"),
		})
		var rUserAdminNotFound datasource.ReadResponse
		dUser.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: sUserResp.Schema, Raw: planAdminNotFound.Raw}}, &rUserAdminNotFound)
		if !rUserAdminNotFound.Diagnostics.HasError() {
			t.Fatal("expected not found in admin")
		}

		// dUsers: directory error
		failDir = true
		planUsersDirErr := tfsdk.Plan{Schema: sUsersResp.Schema}
		_ = planUsersDirErr.Set(ctx, &PaperclipUsersDataSourceModel{
			CompanyID: types.StringValue("comp-1"),
		})
		var rUsersDirErr datasource.ReadResponse
		dUsers.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: sUsersResp.Schema, Raw: planUsersDirErr.Raw}}, &rUsersDirErr)
		if !rUsersDirErr.Diagnostics.HasError() {
			t.Fatal("expected dir error")
		}
		failDir = false

		// dUsers: directory filter with needle mismatch
		planUsersNeedle := tfsdk.Plan{Schema: sUsersResp.Schema}
		_ = planUsersNeedle.Set(ctx, &PaperclipUsersDataSourceModel{
			CompanyID: types.StringValue("comp-1"),
			Query:     types.StringValue("exact"),
		})
		var rUsersNeedle datasource.ReadResponse
		rUsersNeedle.State = tfsdk.State{Schema: sUsersResp.Schema}
		dUsers.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: sUsersResp.Schema, Raw: planUsersNeedle.Raw}}, &rUsersNeedle)
		if rUsersNeedle.Diagnostics.HasError() {
			t.Fatalf("unexpected error: %v", rUsersNeedle.Diagnostics)
		}

		// dUsers: directory listing without query (covers nil email/name and non-nil image)
		planUsersDirNoQuery := tfsdk.Plan{Schema: sUsersResp.Schema}
		_ = planUsersDirNoQuery.Set(ctx, &PaperclipUsersDataSourceModel{
			CompanyID: types.StringValue("comp-1"),
		})
		var rUsersDirNoQuery datasource.ReadResponse
		rUsersDirNoQuery.State = tfsdk.State{Schema: sUsersResp.Schema}
		dUsers.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: sUsersResp.Schema, Raw: planUsersDirNoQuery.Raw}}, &rUsersDirNoQuery)
		if rUsersDirNoQuery.Diagnostics.HasError() {
			t.Fatalf("unexpected error: %v", rUsersDirNoQuery.Diagnostics)
		}

		// dUsers: admin error
		failAdmin = true
		planUsersAdminErr := tfsdk.Plan{Schema: sUsersResp.Schema}
		_ = planUsersAdminErr.Set(ctx, &PaperclipUsersDataSourceModel{
			Query: types.StringValue("adm"),
		})
		var rUsersAdminErr datasource.ReadResponse
		dUsers.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: sUsersResp.Schema, Raw: planUsersAdminErr.Raw}}, &rUsersAdminErr)
		if !rUsersAdminErr.Diagnostics.HasError() {
			t.Fatal("expected admin error")
		}
		failAdmin = false

		// dUsers: admin users listing with nil email/name and non-nil image
		planUsersAdminOk := tfsdk.Plan{Schema: sUsersResp.Schema}
		_ = planUsersAdminOk.Set(ctx, &PaperclipUsersDataSourceModel{})
		var rUsersAdminOk datasource.ReadResponse
		rUsersAdminOk.State = tfsdk.State{Schema: sUsersResp.Schema}
		dUsers.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: sUsersResp.Schema, Raw: planUsersAdminOk.Raw}}, &rUsersAdminOk)
		if rUsersAdminOk.Diagnostics.HasError() {
			t.Fatalf("unexpected error: %v", rUsersAdminOk.Diagnostics)
		}
	})
}

func TestUserInstanceAccessDeleteRetirementContract(t *testing.T) {
	ctx := context.Background()
	r := NewPaperclipUserInstanceAccessResource().(*PaperclipUserInstanceAccessResource)
	var sResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sResp)

	t.Run("Failed Demote Returns Diagnostic And Retains State", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if strings.Contains(req.URL.Path, "demote-instance-admin") {
				http.Error(w, `{"error":"internal failure demoting instance admin"}`, http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"user": map[string]interface{}{"id": "user-1", "isInstanceAdmin": true}, "companyAccess": []map[string]interface{}{}})
		}))
		defer server.Close()

		cli, _ := client.NewClient(server.URL, "token")
		r.client = cli

		state := tfsdk.State{Schema: sResp.Schema}
		_ = state.Set(ctx, &PaperclipUserInstanceAccessResourceModel{
			ID:              types.StringValue("user-1"),
			UserID:          types.StringValue("user-1"),
			IsInstanceAdmin: types.BoolValue(true),
			CompanyIDs:      types.ListNull(types.StringType),
		})

		var delResp resource.DeleteResponse
		delResp.State = state
		r.Delete(ctx, resource.DeleteRequest{State: state}, &delResp)
		if !delResp.Diagnostics.HasError() {
			t.Fatal("expected error diagnostic when demoting instance admin fails")
		}
		var found bool
		for _, d := range delResp.Diagnostics.Errors() {
			if strings.Contains(d.Summary(), "Error demoting instance admin on destroy") && strings.Contains(d.Detail(), "internal failure demoting instance admin") {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected demote error diagnostic with safe API context, got: %v", delResp.Diagnostics)
		}
		var retainedState PaperclipUserInstanceAccessResourceModel
		if err := delResp.State.Get(ctx, &retainedState); err != nil || retainedState.UserID.ValueString() != "user-1" {
			t.Fatalf("expected state to be retained after failed destroy, got: %+v", retainedState)
		}
	})

	t.Run("Failed Company Access Clear Returns Diagnostic And Retains State", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if strings.Contains(req.URL.Path, "demote-instance-admin") {
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
				return
			}
			if req.Method == http.MethodPut && strings.Contains(req.URL.Path, "company-access") {
				http.Error(w, `{"error":"internal failure clearing company access"}`, http.StatusInternalServerError)
				return
			}
			http.NotFound(w, req)
		}))
		defer server.Close()

		cli, _ := client.NewClient(server.URL, "token")
		r.client = cli

		state := tfsdk.State{Schema: sResp.Schema}
		_ = state.Set(ctx, &PaperclipUserInstanceAccessResourceModel{
			ID:              types.StringValue("user-1"),
			UserID:          types.StringValue("user-1"),
			IsInstanceAdmin: types.BoolValue(true),
			CompanyIDs:      stringSliceToTypesList(ctx, []string{"comp-1"}),
		})

		var delResp resource.DeleteResponse
		delResp.State = state
		r.Delete(ctx, resource.DeleteRequest{State: state}, &delResp)
		if !delResp.Diagnostics.HasError() {
			t.Fatal("expected error diagnostic when clearing user company access fails")
		}
		var found bool
		for _, d := range delResp.Diagnostics.Errors() {
			if strings.Contains(d.Summary(), "Error clearing user company access on destroy") && strings.Contains(d.Detail(), "internal failure clearing company access") {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected clear company access error diagnostic with safe API context, got: %v", delResp.Diagnostics)
		}
		var retainedState PaperclipUserInstanceAccessResourceModel
		if err := delResp.State.Get(ctx, &retainedState); err != nil || retainedState.UserID.ValueString() != "user-1" {
			t.Fatalf("expected state to be retained after failed destroy, got: %+v", retainedState)
		}
	})

	t.Run("Hidden Instance Access Rejection On Destroy", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			http.Error(w, `{"error":"instance user administration is managed by the hosting operator on this instance (instance.access hidden)"}`, http.StatusForbidden)
		}))
		defer server.Close()

		cli, _ := client.NewClient(server.URL, "token")
		r.client = cli

		state := tfsdk.State{Schema: sResp.Schema}
		_ = state.Set(ctx, &PaperclipUserInstanceAccessResourceModel{
			ID:              types.StringValue("user-1"),
			UserID:          types.StringValue("user-1"),
			IsInstanceAdmin: types.BoolValue(true),
			CompanyIDs:      stringSliceToTypesList(ctx, []string{"comp-1"}),
		})

		var delResp resource.DeleteResponse
		delResp.State = state
		r.Delete(ctx, resource.DeleteRequest{State: state}, &delResp)
		if !delResp.Diagnostics.HasError() {
			t.Fatal("expected error diagnostic when instance.access is hidden on destroy")
		}
		var demoteHiddenFound, clearHiddenFound bool
		for _, d := range delResp.Diagnostics.Errors() {
			if strings.Contains(d.Summary(), "Error demoting instance admin on destroy") && strings.Contains(d.Detail(), "instance user administration is managed by the hosting operator") {
				demoteHiddenFound = true
			}
			if strings.Contains(d.Summary(), "Error clearing user company access on destroy") && strings.Contains(d.Detail(), "instance user administration is managed by the hosting operator") {
				clearHiddenFound = true
			}
		}
		if !demoteHiddenFound || !clearHiddenFound {
			t.Fatalf("expected hidden instance.access error diagnostics for both demote and clear, got: %v", delResp.Diagnostics)
		}
		var retainedState PaperclipUserInstanceAccessResourceModel
		if err := delResp.State.Get(ctx, &retainedState); err != nil || retainedState.UserID.ValueString() != "user-1" {
			t.Fatalf("expected state to be retained after failed destroy, got: %+v", retainedState)
		}
	})

	t.Run("Explicit Not Found Semantics On Destroy", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			http.NotFound(w, req)
		}))
		defer server.Close()

		cli, _ := client.NewClient(server.URL, "token")
		r.client = cli

		state := tfsdk.State{Schema: sResp.Schema}
		_ = state.Set(ctx, &PaperclipUserInstanceAccessResourceModel{
			ID:              types.StringValue("user-not-found"),
			UserID:          types.StringValue("user-not-found"),
			IsInstanceAdmin: types.BoolValue(true),
			CompanyIDs:      stringSliceToTypesList(ctx, []string{"comp-1"}),
		})

		var delResp resource.DeleteResponse
		r.Delete(ctx, resource.DeleteRequest{State: state}, &delResp)
		if delResp.Diagnostics.HasError() {
			t.Fatalf("expected clean retirement without error on 404 not found, got: %v", delResp.Diagnostics)
		}
	})

	t.Run("Successful Retirement Revokes Privileges", func(t *testing.T) {
		isAdm := true
		companyAccess := []string{"comp-1", "comp-2"}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if strings.Contains(req.URL.Path, "demote-instance-admin") && req.Method == http.MethodPost {
				isAdm = false
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
				return
			}
			if req.Method == http.MethodPut && strings.Contains(req.URL.Path, "company-access") {
				var body map[string]interface{}
				_ = json.NewDecoder(req.Body).Decode(&body)
				cids, _ := body["companyIds"].([]interface{})
				companyAccess = nil
				for _, cid := range cids {
					companyAccess = append(companyAccess, cid.(string))
				}
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"user":          map[string]interface{}{"id": "user-1", "isInstanceAdmin": isAdm},
					"companyAccess": []map[string]interface{}{},
				})
				return
			}
			http.NotFound(w, req)
		}))
		defer server.Close()

		cli, _ := client.NewClient(server.URL, "token")
		r.client = cli

		state := tfsdk.State{Schema: sResp.Schema}
		_ = state.Set(ctx, &PaperclipUserInstanceAccessResourceModel{
			ID:              types.StringValue("user-1"),
			UserID:          types.StringValue("user-1"),
			IsInstanceAdmin: types.BoolValue(true),
			CompanyIDs:      stringSliceToTypesList(ctx, []string{"comp-1", "comp-2"}),
		})

		var delResp resource.DeleteResponse
		r.Delete(ctx, resource.DeleteRequest{State: state}, &delResp)
		if delResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error on successful delete: %v", delResp.Diagnostics)
		}
		if isAdm {
			t.Fatal("expected isInstanceAdmin to be false after successful retirement")
		}
		if len(companyAccess) != 0 {
			t.Fatalf("expected companyAccess to be empty after successful retirement, got: %v", companyAccess)
		}
	})
}

