package provider

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	ephemeralschema "github.com/hashicorp/terraform-plugin-framework/ephemeral/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func webhookOpen(t *testing.T, r *PaperclipRoutineWebhookSecretEphemeralResource, m PaperclipRoutineWebhookSecretModel, private bool) (ephemeral.OpenRequest, ephemeral.OpenResponse) {
	t.Helper()
	ctx := context.Background()
	var s ephemeral.SchemaResponse
	r.Schema(ctx, ephemeral.SchemaRequest{}, &s)
	state := tfsdk.State{Schema: s.Schema}
	assertRoutineOK(t, state.Set(ctx, &m))
	req := ephemeral.OpenRequest{Config: tfsdk.Config{Schema: s.Schema, Raw: state.Raw}}
	resp := ephemeral.OpenResponse{Result: tfsdk.EphemeralResultData{Schema: s.Schema, Raw: state.Raw}}
	if private {
		initOpenResponsePrivate(&resp)
	}
	return req, resp
}
func webhookModel() PaperclipRoutineWebhookSecretModel {
	return PaperclipRoutineWebhookSecretModel{RoutineID: types.StringValue("routine"), Name: types.StringValue("hook"), Generation: types.Int64Value(1), Applying: types.BoolValue(true), SigningMode: types.StringValue("bearer"), ReplayWindowSec: types.Int64Value(300)}
}

func TestRoutineWebhookInitialDeliveryAndUnchangedGeneration(t *testing.T) {
	f := newRoutineFixture(t)
	ctx := context.Background()
	managed := NewPaperclipRoutineTriggerResource().(*PaperclipRoutineTriggerResource)
	s := configureTestResource(t, f.server.URL, managed)
	r := NewPaperclipRoutineWebhookSecretEphemeralResource().(*PaperclipRoutineWebhookSecretEphemeralResource)
	r.client = managed.client
	req, opened := webhookOpen(t, r, webhookModel(), true)
	r.Open(ctx, req, &opened)
	assertRoutineOK(t, opened.Diagnostics)
	var value PaperclipRoutineWebhookSecretModel
	assertRoutineOK(t, opened.Result.Get(ctx, &value))
	if value.TriggerID.ValueString() != "trigger" || value.Secret.ValueString() != "one-time-webhook-fixture-secret" || f.triggers[0]["setupPending"] != true {
		t.Fatal("initial webhook was not pending with its one-time output")
	}
	_, again := webhookOpen(t, r, webhookModel(), true)
	r.Open(ctx, req, &again)
	assertRoutineOK(t, again.Diagnostics)
	if len(f.writes) != 1 {
		t.Fatal("duplicate mint in one apply")
	}
	m := PaperclipRoutineTriggerModel{RoutineID: types.StringValue("routine"), Kind: types.StringValue("webhook"), SigningMode: types.StringValue("bearer"), ReplayWindowSec: types.Int64Value(300), Enabled: types.BoolValue(true), WebhookDeliveryName: types.StringValue("hook"), WebhookGeneration: types.Int64Value(1)}
	created := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	managed.Create(ctx, resource.CreateRequest{Plan: routinePlan(t, s, &m)}, &created)
	assertRoutineOK(t, created.Diagnostics)
	if f.triggers[0]["setupPending"] != true {
		t.Fatal("identity adoption activated an undelivered webhook")
	}
	delivery := NewPaperclipRoutineWebhookDeliveryResource().(*PaperclipRoutineWebhookDeliveryResource)
	delivery.client = managed.client
	var deliverySchema resource.SchemaResponse
	delivery.Schema(ctx, resource.SchemaRequest{}, &deliverySchema)
	deliveryModel := PaperclipRoutineWebhookDeliveryModel{RoutineID: m.RoutineID, TriggerID: value.TriggerID, Generation: types.Int64Value(1)}
	confirmed := resource.CreateResponse{State: tfsdk.State{Schema: deliverySchema.Schema}}
	delivery.Create(ctx, resource.CreateRequest{Plan: routinePlan(t, deliverySchema.Schema, &deliveryModel)}, &confirmed)
	assertRoutineOK(t, confirmed.Diagnostics)
	if f.triggers[0]["setupPending"] != false {
		t.Fatal("delivery receipt did not activate confirmed setup")
	}
	if strings.Contains(created.State.Raw.String(), "one-time-webhook-fixture-secret") {
		t.Fatal("secret persisted in managed state")
	}
	closeReq := ephemeral.CloseRequest{}
	initCloseRequestPrivate(&closeReq, opened.Private)
	closed := ephemeral.CloseResponse{}
	r.Close(ctx, closeReq, &closed)
	assertRoutineOK(t, closed.Diagnostics)
	if len(f.triggers) != 1 {
		t.Fatal("confirmed webhook deleted")
	}
	read := resource.ReadResponse{State: created.State}
	managed.Read(ctx, resource.ReadRequest{State: created.State}, &read)
	assertRoutineOK(t, read.Diagnostics)
	before := len(f.writes)
	req, unchanged := webhookOpen(t, r, webhookModel(), true)
	r.Open(ctx, req, &unchanged)
	assertRoutineOK(t, unchanged.Diagnostics)
	assertRoutineOK(t, unchanged.Result.Get(ctx, &value))
	if !value.Secret.IsNull() || value.TriggerID.ValueString() != "trigger" || len(f.writes) != before {
		t.Fatal("unchanged generation minted or lost its identity")
	}
	rotated := webhookModel()
	rotated.Generation = types.Int64Value(2)
	req, rotation := webhookOpen(t, r, rotated, true)
	r.Open(ctx, req, &rotation)
	assertDiagContains(t, rotation.Diagnostics, "Unsupported webhook", "No mutation was sent")
	if len(f.writes) != before {
		t.Fatal("unsupported rotation mutated")
	}
	m.WebhookGeneration = types.Int64Value(2)
	updated := resource.UpdateResponse{State: read.State}
	managed.Update(ctx, resource.UpdateRequest{Plan: routinePlan(t, s, &m), State: read.State}, &updated)
	assertDiagContains(t, updated.Diagnostics, "Unsupported webhook", "No mutation was sent")
	invalidCreate := resource.CreateResponse{}
	managed.Create(ctx, resource.CreateRequest{Plan: routinePlan(t, s, &m)}, &invalidCreate)
	assertDiagContains(t, invalidCreate.Diagnostics, "Unsupported webhook", "No mutation was sent")
	m.WebhookGeneration = types.Int64Value(1)
	m.WebhookDeliveryName = types.StringValue("other")
	updated = resource.UpdateResponse{State: read.State}
	managed.Update(ctx, resource.UpdateRequest{Plan: routinePlan(t, s, &m), State: read.State}, &updated)
	assertDiagContains(t, updated.Diagnostics, "Unsupported trigger transition", "delivery identity")
}

func TestRoutineWebhookFailedDeliveryDeletesOnlyUnconfirmedTrigger(t *testing.T) {
	for _, failure := range []string{"consumer", "cancelled close", "private unavailable", "result state", "delete failure", "mint failure", "reference failure", "missing pending", "already active", "activation failure", "settings mismatch"} {
		t.Run(failure, func(t *testing.T) {
			f := newRoutineFixture(t)
			ctx := context.Background()
			managed := NewPaperclipRoutineTriggerResource().(*PaperclipRoutineTriggerResource)
			s := configureTestResource(t, f.server.URL, managed)
			r := NewPaperclipRoutineWebhookSecretEphemeralResource().(*PaperclipRoutineWebhookSecretEphemeralResource)
			r.client = managed.client
			if failure == "mint failure" {
				f.failWrite = 500
			}
			if failure == "reference failure" {
				f.fail = 403
			}
			req, opened := webhookOpen(t, r, webhookModel(), failure != "private unavailable")
			if failure == "result state" {
				opened.Result.Schema = ephemeralschema.Schema{}
			}
			r.Open(ctx, req, &opened)
			if failure == "mint failure" || failure == "reference failure" {
				if !opened.Diagnostics.HasError() || len(f.triggers) != 0 {
					t.Fatal("failed mint/reference changed remote")
				}
				return
			}
			if failure == "private unavailable" || failure == "result state" {
				if !opened.Diagnostics.HasError() || len(f.triggers) != 0 {
					t.Fatal("private state failure did not compensate")
				}
				return
			}
			assertRoutineOK(t, opened.Diagnostics)
			if failure == "missing pending" {
				f.triggers = nil
			}
			if failure == "already active" {
				f.triggers[0]["setupPending"] = false
			}
			if failure == "activation failure" {
				f.failWrite = 500
			}
			if failure == "missing pending" || failure == "already active" || failure == "activation failure" || failure == "settings mismatch" {
				m := PaperclipRoutineTriggerModel{RoutineID: types.StringValue("routine"), Kind: types.StringValue("webhook"), Enabled: types.BoolValue(true), SigningMode: types.StringValue("bearer"), ReplayWindowSec: types.Int64Value(300), WebhookDeliveryName: types.StringValue("hook")}
				if failure == "settings mismatch" {
					m.SigningMode = types.StringValue("none")
				}
				created := resource.CreateResponse{State: tfsdk.State{Schema: s}}
				managed.Create(ctx, resource.CreateRequest{Plan: routinePlan(t, s, &m)}, &created)
				if !created.Diagnostics.HasError() {
					t.Fatal("invalid activation succeeded")
				}
				f.failWrite = 0
			}
			if failure == "delete failure" {
				f.failWrite = 500
			}
			closeReq := ephemeral.CloseRequest{}
			initCloseRequestPrivate(&closeReq, opened.Private)
			closed := ephemeral.CloseResponse{}
			closeCtx := ctx
			if failure == "cancelled close" {
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				closeCtx = cancelled
			}
			r.Close(closeCtx, closeReq, &closed)
			if failure == "delete failure" {
				assertDiagContains(t, closed.Diagnostics, "Error deleting unconfirmed webhook", "500")
				if len(f.triggers) != 1 {
					t.Fatal("failed deletion lost recovery identity")
				}
				f.failWrite = 0
				if err := r.client.CleanupUnconfirmedKeys(ctx); err != nil {
					t.Fatal(err)
				}
			} else {
				assertDiagContains(t, closed.Diagnostics, "Webhook delivery unconfirmed", "Deleted pending setup")
			}
			if len(f.triggers) != 0 {
				t.Fatal("unconfirmed trigger survived failed delivery")
			}
		})
	}
}

func TestRoutineWebhookPlanDeferralAndInputErrors(t *testing.T) {
	f := newRoutineFixture(t)
	ctx := context.Background()
	r := NewPaperclipRoutineWebhookSecretEphemeralResource().(*PaperclipRoutineWebhookSecretEphemeralResource)
	var c ephemeral.ConfigureResponse
	r.Configure(ctx, ephemeral.ConfigureRequest{}, &c)
	assertRoutineOK(t, c.Diagnostics)
	r.Configure(ctx, ephemeral.ConfigureRequest{ProviderData: "invalid"}, &c)
	if !c.Diagnostics.HasError() {
		t.Fatal("invalid client accepted")
	}
	req, resp := webhookOpen(t, r, webhookModel(), true)
	r.Open(ctx, req, &resp)
	assertDiagContains(t, resp.Diagnostics, "Unconfigured Paperclip Client", "configured")
	managed := NewPaperclipRoutineTriggerResource().(*PaperclipRoutineTriggerResource)
	configureTestResource(t, f.server.URL, managed)
	r.Configure(ctx, ephemeral.ConfigureRequest{ProviderData: managed.client}, &ephemeral.ConfigureResponse{})
	r.client.RegisterRoutineWebhook("pending", "", "hook", "routine")
	unavailableReq, unavailable := webhookOpen(t, r, webhookModel(), true)
	r.Open(ctx, unavailableReq, &unavailable)
	assertDiagContains(t, unavailable.Diagnostics, "Pending webhook secret unavailable", "cannot be recovered")
	r.client.ClearMintedToken("pending")
	for _, tc := range []struct {
		name  string
		edit  func(*PaperclipRoutineWebhookSecretModel)
		valid bool
	}{
		{"empty name", func(m *PaperclipRoutineWebhookSecretModel) { m.Name = types.StringValue("") }, false},
		{"replay", func(m *PaperclipRoutineWebhookSecretModel) { m.ReplayWindowSec = types.Int64Value(0) }, false},
		{"signing", func(m *PaperclipRoutineWebhookSecretModel) { m.SigningMode = types.StringValue("invalid") }, false},
		{"plan", func(m *PaperclipRoutineWebhookSecretModel) { m.Applying = types.BoolValue(false) }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := webhookModel()
			tc.edit(&m)
			req, resp := webhookOpen(t, r, m, true)
			r.Open(ctx, req, &resp)
			if resp.Diagnostics.HasError() == tc.valid {
				t.Fatal(resp.Diagnostics)
			}
			if len(f.writes) != 0 {
				t.Fatal("invalid input or plan mutated")
			}
			if tc.valid {
				req.ClientCapabilities.DeferralAllowed = true
				r.Open(ctx, req, &resp)
				if resp.Deferred == nil {
					t.Fatal("plan not deferred")
				}
			}
		})
	}
	bad := ephemeral.OpenResponse{}
	r.Open(ctx, ephemeral.OpenRequest{Config: tfsdk.Config{Schema: req.Config.Schema, Raw: tftypes.NewValue(tftypes.String, "bad")}}, &bad)
	if !bad.Diagnostics.HasError() {
		t.Fatal("malformed config accepted")
	}
	r.Close(ctx, ephemeral.CloseRequest{}, &ephemeral.CloseResponse{})
	r.client = nil
	r.Close(ctx, ephemeral.CloseRequest{}, &ephemeral.CloseResponse{})
	var empty ephemeral.CloseRequest
	initCloseRequestPrivate(&empty, resp.Private)
	r.client = managed.client
	r.Close(ctx, empty, &ephemeral.CloseResponse{})
	assertRoutineOK(t, resp.Private.SetKey(ctx, "trigger_id", []byte("123")))
	closed := ephemeral.CloseResponse{}
	r.Close(ctx, empty, &closed)
	if !closed.Diagnostics.HasError() {
		t.Fatal("corrupt private state accepted")
	}
}
