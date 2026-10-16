package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestRoutineWebhookDeliveryImportReadAndRetirement(t *testing.T) {
	f := newRoutineFixture(t)
	ctx := context.Background()
	r := NewPaperclipRoutineWebhookDeliveryResource().(*PaperclipRoutineWebhookDeliveryResource)
	s := configureTestResource(t, f.server.URL, r)
	f.triggers = []map[string]interface{}{{"id": "trigger", "routineId": "routine", "kind": "webhook", "enabled": true, "setupPending": false}}
	imported := resource.ImportStateResponse{State: tfsdk.State{Schema: s}}
	assertRoutineOK(t, imported.State.Set(ctx, &PaperclipRoutineWebhookDeliveryModel{}))
	r.ImportState(ctx, resource.ImportStateRequest{ID: "routine/trigger"}, &imported)
	assertRoutineOK(t, imported.Diagnostics)
	read := resource.ReadResponse{State: imported.State}
	r.Read(ctx, resource.ReadRequest{State: imported.State}, &read)
	assertRoutineOK(t, read.Diagnostics)
	var m PaperclipRoutineWebhookDeliveryModel
	assertRoutineOK(t, read.State.Get(ctx, &m))
	if m.ID.ValueString() != "trigger" || m.TriggerID.ValueString() != "trigger" || m.Generation.ValueInt64() != 1 {
		t.Fatal(m)
	}
	plan := routinePlan(t, s, &m)
	planned := resource.ModifyPlanResponse{Plan: plan}
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan, State: read.State}, &planned)
	assertRoutineOK(t, planned.Diagnostics)
	deleted := resource.DeleteResponse{}
	r.Delete(ctx, resource.DeleteRequest{State: read.State}, &deleted)
	assertRoutineOK(t, deleted.Diagnostics)
	if len(f.writes) != 0 || len(f.triggers) != 1 {
		t.Fatal("receipt retirement revoked the independently managed trigger")
	}
	for _, id := range []string{"", "trigger", "routine/", "/trigger", "a/b/c"} {
		resp := resource.ImportStateResponse{}
		r.ImportState(ctx, resource.ImportStateRequest{ID: id}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Fatal("invalid import accepted")
		}
	}
	for _, tc := range []struct {
		name             string
		status           int
		pending, missing bool
	}{{"auth", 403, false, false}, {"pending", 0, true, false}, {"missing", 0, false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			f.fail = tc.status
			f.missing = tc.missing
			f.triggers[0]["setupPending"] = tc.pending
			resp := resource.ReadResponse{State: read.State}
			r.Read(ctx, resource.ReadRequest{State: read.State}, &resp)
			if tc.missing {
				assertRoutineOK(t, resp.Diagnostics)
				if !resp.State.Raw.IsNull() {
					t.Fatal("missing trigger retained")
				}
			} else if !resp.Diagnostics.HasError() || resp.State.Raw.IsNull() {
				t.Fatal("failed read lost identity")
			}
		})
	}
}

func TestRoutineWebhookDeliveryRejectsInvalidReferencesAndActivation(t *testing.T) {
	for _, failure := range []string{"kind", "reference", "missing secret", "already active", "activation", "not converged", "state write"} {
		t.Run(failure, func(t *testing.T) {
			f := newRoutineFixture(t)
			ctx := context.Background()
			r := NewPaperclipRoutineWebhookDeliveryResource().(*PaperclipRoutineWebhookDeliveryResource)
			s := configureTestResource(t, f.server.URL, r)
			f.triggers = []map[string]interface{}{{"id": "trigger", "routineId": "routine", "kind": "webhook", "setupPending": true}}
			r.client.RegisterRoutineWebhook("trigger", "one-time-value", "hook", "routine")
			switch failure {
			case "kind":
				f.triggers[0]["kind"] = "api"
			case "reference":
				f.fail = 403
			case "missing secret":
				r.client.ClearMintedToken("trigger")
			case "already active":
				f.triggers[0]["setupPending"] = false
			case "activation":
				f.failWrite = 500
			case "not converged":
				f.ignoreSetup = true
			}
			m := PaperclipRoutineWebhookDeliveryModel{RoutineID: types.StringValue("routine"), TriggerID: types.StringValue("trigger"), Generation: types.Int64Value(1)}
			resp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
			if failure == "state write" {
				resp.State = tfsdk.State{Schema: schema.Schema{}}
			}
			r.Create(ctx, resource.CreateRequest{Plan: routinePlan(t, s, &m)}, &resp)
			if !resp.Diagnostics.HasError() || r.client.IsKeyDeliveryAcknowledged("trigger") {
				t.Fatal("invalid activation was acknowledged")
			}
			if failure != "not converged" && failure != "state write" && len(f.writes) != 0 {
				t.Fatal("invalid input mutated")
			}
			r.client.ClearMintedToken("trigger")
		})
	}
}

func TestRoutineWebhookDeliveryRejectsGenerationAndIdentityChanges(t *testing.T) {
	f := newRoutineFixture(t)
	ctx := context.Background()
	r := NewPaperclipRoutineWebhookDeliveryResource().(*PaperclipRoutineWebhookDeliveryResource)
	s := configureTestResource(t, f.server.URL, r)
	f.triggers = []map[string]interface{}{{"id": "trigger", "routineId": "routine", "kind": "webhook", "setupPending": false}}
	m := PaperclipRoutineWebhookDeliveryModel{ID: types.StringValue("trigger"), RoutineID: types.StringValue("routine"), TriggerID: types.StringValue("trigger"), Generation: types.Int64Value(1)}
	p := routinePlan(t, s, &m)
	state := tfsdk.State{Schema: s, Raw: p.Raw}
	for _, tc := range []string{"generation", "routine", "trigger", "unknown"} {
		t.Run(tc, func(t *testing.T) {
			changed := m
			switch tc {
			case "generation":
				changed.Generation = types.Int64Value(2)
			case "routine":
				changed.RoutineID = types.StringValue("other")
			case "trigger":
				changed.TriggerID = types.StringValue("other")
			case "unknown":
				changed.RoutineID = types.StringUnknown()
				changed.TriggerID = types.StringUnknown()
				changed.Generation = types.Int64Unknown()
			}
			plan := routinePlan(t, s, &changed)
			resp := resource.ModifyPlanResponse{Plan: plan}
			r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan, State: state}, &resp)
			if tc == "unknown" {
				assertRoutineOK(t, resp.Diagnostics)
			} else if !resp.Diagnostics.HasError() {
				t.Fatal("invalid change accepted")
			}
			config := resource.ValidateConfigResponse{}
			r.ValidateConfig(ctx, resource.ValidateConfigRequest{Config: tfsdk.Config{Schema: s, Raw: plan.Raw}}, &config)
			if tc == "generation" {
				assertDiagContains(t, config.Diagnostics, "Unsupported webhook", "No mutation was sent")
				created := resource.CreateResponse{}
				r.Create(ctx, resource.CreateRequest{Plan: plan}, &created)
				assertDiagContains(t, created.Diagnostics, "Unsupported webhook", "No mutation was sent")
			}
		})
	}
	f.fail = 403
	planned := resource.ModifyPlanResponse{Plan: p}
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: p}, &planned)
	assertDiagContains(t, planned.Diagnostics, "Invalid webhook delivery reference", "403")
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: tfsdk.Plan{Schema: s, Raw: tftypes.NewValue(p.Raw.Type(), nil)}}, &resource.ModifyPlanResponse{})
	updated := resource.UpdateResponse{}
	r.Update(ctx, resource.UpdateRequest{}, &updated)
	assertDiagContains(t, updated.Diagnostics, "Immutable webhook delivery", "generation 1")
	if len(f.writes) != 0 {
		t.Fatal("invalid delivery changed remote")
	}
}
