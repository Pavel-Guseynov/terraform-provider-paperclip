package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	ephemeralschema "github.com/hashicorp/terraform-plugin-framework/ephemeral/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

type routineFixture struct {
	server    *httptest.Server
	routine   map[string]interface{}
	triggers  []map[string]interface{}
	writes    []map[string]interface{}
	methods   []string
	fail      int
	conflict  bool
	missing   bool
	failWrite int
}

func newRoutineFixture(t *testing.T) *routineFixture {
	t.Helper()
	f := &routineFixture{routine: map[string]interface{}{"id": "routine", "companyId": "company", "title": "Daily", "status": "paused", "priority": "medium", "concurrencyPolicy": "coalesce_if_active", "catchUpPolicy": "skip_missed", "activityGatePolicy": "always", "activityGateScope": "company", "latestRevisionId": "revision", "variables": []interface{}{}, "env": nil}}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if f.fail != 0 {
			w.WriteHeader(f.fail)
			_, _ = fmt.Fprintf(w, `{"error":"injected API failure"}`)
			return
		}
		if req.Method == "GET" {
			if f.missing {
				w.WriteHeader(404)
				_, _ = fmt.Fprintf(w, `{"error":"Routine not found"}`)
				return
			}
			if req.URL.Path == "/api/routines/routine" {
				body := map[string]interface{}{}
				for key, value := range f.routine {
					body[key] = value
				}
				body["triggers"] = f.triggers
				_ = json.NewEncoder(w).Encode(body)
				return
			}
			company := "company"
			if strings.Contains(req.URL.Path, "foreign") {
				company = "foreign"
			}
			id := req.URL.Path[strings.LastIndex(req.URL.Path, "/")+1:]
			status := "active"
			if id == "terminated" {
				status = "terminated"
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": id, "companyId": company, "status": status})
			return
		}
		if f.failWrite != 0 {
			w.WriteHeader(f.failWrite)
			_, _ = fmt.Fprint(w, `{"error":"injected mutation failure"}`)
			return
		}
		var input map[string]interface{}
		if req.Method != "DELETE" {
			_ = json.NewDecoder(req.Body).Decode(&input)
		}
		if req.Method == "PATCH" && req.URL.Path == "/api/routines/routine" && (f.conflict || input["baseRevisionId"] != f.routine["latestRevisionId"]) {
			w.WriteHeader(409)
			_, _ = fmt.Fprintf(w, `{"error":"Routine was updated by someone else","details":{"currentRevisionId":"concurrent"}}`)
			return
		}
		f.writes = append(f.writes, input)
		f.methods = append(f.methods, req.Method+" "+req.URL.Path)
		switch {
		case req.URL.Path == "/api/companies/company/routines" || req.URL.Path == "/api/routines/routine":
			for key, value := range input {
				if key != "baseRevisionId" {
					f.routine[key] = value
				}
			}
			f.routine["latestRevisionId"] = "next-revision"
			_ = json.NewEncoder(w).Encode(f.routine)
		case req.URL.Path == "/api/routines/routine/triggers":
			input["id"] = "trigger"
			input["routineId"] = "routine"
			input["companyId"] = "company"
			f.triggers = append(f.triggers, input)
			var material interface{}
			if input["kind"] == "webhook" {
				material = map[string]interface{}{"webhookSecret": "one-time-webhook-fixture-secret"}
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"trigger": input, "secretMaterial": material})
		case req.Method == "DELETE":
			f.triggers = nil
			w.WriteHeader(204)
		default:
			if len(f.triggers) == 0 {
				w.WriteHeader(404)
				_, _ = fmt.Fprint(w, `{"error":"Routine trigger not found"}`)
				return
			}
			for key, value := range input {
				f.triggers[0][key] = value
			}
			_ = json.NewEncoder(w).Encode(f.triggers[0])
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}
func routinePlan(t *testing.T, s schema.Schema, model interface{}) tfsdk.Plan {
	t.Helper()
	p := tfsdk.Plan{Schema: s}
	if d := p.Set(context.Background(), model); d.HasError() {
		t.Fatal(d)
	}
	return p
}
func assertRoutineOK(t *testing.T, d diag.Diagnostics) {
	t.Helper()
	if d.HasError() {
		t.Fatal(d)
	}
}
func baseRoutineModel() PaperclipRoutineModel {
	return PaperclipRoutineModel{CompanyID: types.StringValue("company"), Title: types.StringValue("Daily"), Status: types.StringValue("paused"), Priority: types.StringValue("medium"), ConcurrencyPolicy: types.StringValue("coalesce_if_active"), CatchUpPolicy: types.StringValue("skip_missed"), ActivityGatePolicy: types.StringValue("always"), ActivityGateScope: types.StringValue("company")}
}

func TestRoutineFieldsClearAndArchive(t *testing.T) {
	f := newRoutineFixture(t)
	ctx := context.Background()
	r := NewPaperclipRoutineResource().(*PaperclipRoutineResource)
	s := configureTestResource(t, f.server.URL, r)
	m := baseRoutineModel()
	m.Title = types.StringValue("Daily {{name}}")
	m.Description = types.StringValue("Report")
	m.ProjectID = types.StringValue("project")
	m.GoalID = types.StringValue("goal")
	m.ParentIssueID = types.StringValue("parent")
	m.AssigneeAgentID = types.StringValue("agent")
	m.Status = types.StringValue("active")
	m.Priority = types.StringValue("high")
	m.ConcurrencyPolicy = types.StringValue("always_enqueue")
	m.CatchUpPolicy = types.StringValue("enqueue_missed_with_cap")
	m.ActivityGatePolicy = types.StringValue("require_external_activity")
	m.ActivityGateScope = types.StringValue("project")
	m.Variables = types.StringValue(`[{"name":"name","defaultValue":"World"}]`)
	m.Env = map[string]EnvBindingModel{"MODE": {Type: types.StringValue("plain"), Value: types.StringValue("report")}}
	p := routinePlan(t, s, &m)
	created := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(ctx, resource.CreateRequest{Plan: p}, &created)
	assertRoutineOK(t, created.Diagnostics)
	var got PaperclipRoutineModel
	assertRoutineOK(t, created.State.Get(ctx, &got))
	if got.ID.ValueString() != "routine" || got.RevisionID.ValueString() != "next-revision" || got.Variables != m.Variables || !reflect.DeepEqual(got.Env, m.Env) {
		t.Fatalf("create projection: %#v", got)
	}
	read := resource.ReadResponse{State: created.State}
	r.Read(ctx, resource.ReadRequest{State: created.State}, &read)
	assertRoutineOK(t, read.Diagnostics)
	assertRoutineOK(t, read.State.Get(ctx, &got))
	if !reflect.DeepEqual(got, routineWithIdentity(m, "routine", "next-revision")) {
		t.Fatalf("roundtrip: %#v", got)
	}
	cleared := baseRoutineModel()
	cleared.ID = got.ID
	cleared.Env = map[string]EnvBindingModel{}
	p = routinePlan(t, s, &cleared)
	updated := resource.UpdateResponse{State: read.State}
	r.Update(ctx, resource.UpdateRequest{Plan: p, State: read.State}, &updated)
	assertRoutineOK(t, updated.Diagnostics)
	for _, key := range []string{"projectId", "goalId", "parentIssueId", "description", "assigneeAgentId"} {
		if f.routine[key] != nil {
			t.Fatalf("%s not cleared: %#v", key, f.routine)
		}
	}
	if f.writes[1]["baseRevisionId"] != "next-revision" || len(f.routine["variables"].([]interface{})) != 0 || len(f.routine["env"].(map[string]interface{})) != 0 {
		t.Fatalf("clear/revision: %#v", f.routine)
	}
	del := resource.DeleteResponse{}
	r.Delete(ctx, resource.DeleteRequest{State: updated.State}, &del)
	assertRoutineOK(t, del.Diagnostics)
	if f.routine["status"] != "archived" || f.methods[len(f.methods)-1] != "PATCH /api/routines/routine" {
		t.Fatal(f.methods)
	}
}
func routineWithIdentity(m PaperclipRoutineModel, id, revision string) PaperclipRoutineModel {
	m.ID = types.StringValue(id)
	m.RevisionID = types.StringValue(revision)
	return m
}

func TestRoutineConflictPreservesConcurrentEdit(t *testing.T) {
	f := newRoutineFixture(t)
	ctx := context.Background()
	r := NewPaperclipRoutineResource().(*PaperclipRoutineResource)
	s := configureTestResource(t, f.server.URL, r)
	m := routineWithIdentity(baseRoutineModel(), "routine", "revision")
	p := routinePlan(t, s, &m)
	state := tfsdk.State{Schema: s, Raw: p.Raw}
	f.conflict = true
	f.routine["title"] = "Concurrent edit"
	m.Title = types.StringValue("Attempted overwrite")
	updated := resource.UpdateResponse{State: state}
	r.Update(ctx, resource.UpdateRequest{Plan: routinePlan(t, s, &m), State: state}, &updated)
	assertDiagContains(t, updated.Diagnostics, "Error saving", "409")
	if f.routine["title"] != "Concurrent edit" || len(f.writes) != 0 {
		t.Fatal("concurrent edit overwritten")
	}
	del := resource.DeleteResponse{}
	r.Delete(ctx, resource.DeleteRequest{State: state}, &del)
	assertDiagContains(t, del.Diagnostics, "Error archiving", "409")
	m.CompanyID = types.StringValue("other")
	plan := routinePlan(t, s, &m)
	planned := resource.ModifyPlanResponse{Plan: plan}
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan, State: state}, &planned)
	assertDiagContains(t, planned.Diagnostics, "Unsupported routine transition", "cannot move")
	updated = resource.UpdateResponse{State: state}
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: state}, &updated)
	assertDiagContains(t, updated.Diagnostics, "Unsupported routine transition", "cannot move")
	if len(f.writes) != 0 {
		t.Fatal("unsupported company move mutated")
	}
}

func TestRoutineReferencesFailBeforeMutation(t *testing.T) {
	f := newRoutineFixture(t)
	ctx := context.Background()
	r := NewPaperclipRoutineResource().(*PaperclipRoutineResource)
	s := configureTestResource(t, f.server.URL, r)
	for _, field := range []string{"project", "goal", "parent", "agent", "secret"} {
		t.Run(field, func(t *testing.T) {
			m := baseRoutineModel()
			switch field {
			case "project":
				m.ProjectID = types.StringValue("foreign")
			case "goal":
				m.GoalID = types.StringValue("foreign")
			case "parent":
				m.ParentIssueID = types.StringValue("foreign")
			case "agent":
				m.AssigneeAgentID = types.StringValue("foreign")
			case "secret":
				m.Env = map[string]EnvBindingModel{"TOKEN": {Type: types.StringValue("secret_ref"), SecretID: types.StringValue("foreign")}}
			}
			p := routinePlan(t, s, &m)
			planned := resource.ModifyPlanResponse{Plan: p}
			r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: p}, &planned)
			if !planned.Diagnostics.HasError() {
				t.Fatal("invalid plan reference accepted")
			}
			created := resource.CreateResponse{State: tfsdk.State{Schema: s}}
			r.Create(ctx, resource.CreateRequest{Plan: p}, &created)
			if !created.Diagnostics.HasError() || len(f.writes) != 0 {
				t.Fatal("invalid apply-time reference mutated")
			}
		})
	}
}

func TestRoutineTriggerKindsAndClearing(t *testing.T) {
	for _, kind := range []string{"schedule", "api", "webhook"} {
		t.Run(kind, func(t *testing.T) {
			f := newRoutineFixture(t)
			ctx := context.Background()
			r := NewPaperclipRoutineTriggerResource().(*PaperclipRoutineTriggerResource)
			s := configureTestResource(t, f.server.URL, r)
			m := PaperclipRoutineTriggerModel{RoutineID: types.StringValue("routine"), Kind: types.StringValue(kind), Label: types.StringValue("Daily"), Enabled: types.BoolValue(true)}
			if kind == "schedule" {
				m.CronExpression = types.StringValue("0 9 * * *")
				m.Timezone = types.StringValue("UTC")
			}
			var state tfsdk.State
			if kind == "webhook" {
				created := resource.CreateResponse{State: tfsdk.State{Schema: s}}
				r.Create(ctx, resource.CreateRequest{Plan: routinePlan(t, s, &m)}, &created)
				assertDiagContains(t, created.Diagnostics, "Missing initial webhook delivery", "write-only consumer")
				if len(f.writes) != 0 {
					t.Fatal("webhook creation mutated")
				}
				m.ID = types.StringValue("trigger")
				m.SigningMode = types.StringValue("hmac_sha256")
				m.ReplayWindowSec = types.Int64Value(300)
				f.triggers = []map[string]interface{}{{"id": "trigger", "routineId": "routine", "kind": "webhook", "label": "Daily", "enabled": true, "signingMode": "hmac_sha256", "replayWindowSec": 300}}
				p := routinePlan(t, s, &m)
				state = tfsdk.State{Schema: s, Raw: p.Raw}
			} else {
				created := resource.CreateResponse{State: tfsdk.State{Schema: s}}
				r.Create(ctx, resource.CreateRequest{Plan: routinePlan(t, s, &m)}, &created)
				assertRoutineOK(t, created.Diagnostics)
				state = created.State
			}
			read := resource.ReadResponse{State: state}
			r.Read(ctx, resource.ReadRequest{State: state}, &read)
			assertRoutineOK(t, read.Diagnostics)
			var got PaperclipRoutineTriggerModel
			assertRoutineOK(t, read.State.Get(ctx, &got))
			if got.Kind.ValueString() != kind || got.ID.ValueString() != "trigger" {
				t.Fatal(got)
			}
			got.Label = types.StringNull()
			got.Enabled = types.BoolValue(false)
			if kind == "schedule" {
				got.CronExpression = types.StringValue("30 10 * * *")
				got.Timezone = types.StringValue("Asia/Almaty")
			}
			got.SigningMode = types.StringNull()
			got.ReplayWindowSec = types.Int64Null()
			updated := resource.UpdateResponse{State: read.State}
			r.Update(ctx, resource.UpdateRequest{Plan: routinePlan(t, s, &got), State: read.State}, &updated)
			assertRoutineOK(t, updated.Diagnostics)
			assertRoutineOK(t, updated.State.Get(ctx, &got))
			if !got.Label.IsNull() || got.Enabled.ValueBool() || !got.SigningMode.IsNull() || !got.ReplayWindowSec.IsNull() {
				t.Fatal(got)
			}
			if kind == "schedule" && (got.CronExpression.ValueString() != "30 10 * * *" || got.Timezone.ValueString() != "Asia/Almaty") {
				t.Fatal("schedule update did not roundtrip")
			}
			del := resource.DeleteResponse{}
			r.Delete(ctx, resource.DeleteRequest{State: updated.State}, &del)
			assertRoutineOK(t, del.Diagnostics)
			if len(f.triggers) != 0 || f.methods[len(f.methods)-1] != "DELETE /api/routine-triggers/trigger" {
				t.Fatal(f.methods)
			}
		})
	}
}

func TestRoutineVariableTemplateContract(t *testing.T) {
	for _, tc := range []struct {
		raw, title string
		valid      bool
	}{
		{"", "Daily {{date}} {{timestamp}}", true}, {"[]", "Daily", true}, {`[{"name":"name"}]`, "{{name}} {{name}}", true},
		{`[{"name":"pr_url","type":"textarea","label":null,"defaultValue":null,"required":false,"options":[]}]`, `{{pr\_url}}`, true},
		{"null", "", false}, {"{}", "", false}, {"[1]", "", false}, {"[null]", "{{x}}", false}, {"", "{{x}}", false}, {`[{"name":"y"}]`, "{{x}}", false}, {`[{"name":"x","unexpected":1}]`, "{{x}}", false},
		{`[{"name":"x","label":" leading"}]`, "{{x}}", false}, {`[{"name":"x","options":[" trailing "]}]`, "{{x}}", false},
	} {
		t.Run(tc.raw+tc.title, func(t *testing.T) {
			v, err := routineVariables(tc.raw, tc.title, "")
			if (err == nil) != tc.valid {
				t.Fatalf("%#v %v", v, err)
			}
			if tc.valid && len(v) > 0 {
				if _, ok := v[0]["required"]; !ok {
					t.Fatal("missing native default")
				}
			}
		})
	}
}

func TestRoutineWebhookRotationNeverMutates(t *testing.T) {
	r := NewPaperclipRoutineWebhookSecretEphemeralResource().(*PaperclipRoutineWebhookSecretEphemeralResource)
	var meta ephemeral.MetadataResponse
	r.Metadata(context.Background(), ephemeral.MetadataRequest{ProviderTypeName: "paperclip"}, &meta)
	if meta.TypeName != "paperclip_routine_webhook_secret" {
		t.Fatal(meta)
	}
	resp := ephemeral.OpenResponse{}
	var s ephemeral.SchemaResponse
	r.Schema(context.Background(), ephemeral.SchemaRequest{}, &s)
	secret := s.Schema.Attributes["secret"].(ephemeralschema.StringAttribute)
	if !secret.Computed || !secret.Sensitive {
		t.Fatal("unsafe ephemeral schema")
	}
	m := PaperclipRoutineWebhookSecretModel{RoutineID: types.StringValue("routine"), Name: types.StringValue("hook"), Generation: types.Int64Value(2), Applying: types.BoolValue(true), SigningMode: types.StringValue("bearer"), ReplayWindowSec: types.Int64Value(300)}
	state := tfsdk.State{Schema: s.Schema}
	assertRoutineOK(t, state.Set(context.Background(), &m))
	r.Open(context.Background(), ephemeral.OpenRequest{Config: tfsdk.Config{Schema: s.Schema, Raw: state.Raw}}, &resp)
	assertDiagContains(t, resp.Diagnostics, "Unsupported webhook", "restore the previous credential")
}

func TestRoutinePlanAndConfigValidation(t *testing.T) {
	f := newRoutineFixture(t)
	ctx := context.Background()
	r := NewPaperclipRoutineResource().(*PaperclipRoutineResource)
	s := configureTestResource(t, f.server.URL, r)
	for _, tc := range []struct {
		name    string
		edit    func(*PaperclipRoutineModel)
		invalid bool
	}{
		{"title", func(m *PaperclipRoutineModel) { m.Title = types.StringValue(" ") }, true},
		{"active", func(m *PaperclipRoutineModel) { m.Status = types.StringValue("active") }, true},
		{"template", func(m *PaperclipRoutineModel) { m.Title = types.StringValue("{{missing}}") }, true},
		{"terminated", func(m *PaperclipRoutineModel) { m.AssigneeAgentID = types.StringValue("terminated") }, true},
		{"unknown", func(m *PaperclipRoutineModel) {
			m.CompanyID = types.StringUnknown()
			m.Title = types.StringUnknown()
			m.Description = types.StringUnknown()
			m.Variables = types.StringUnknown()
		}, false},
		{"known", func(m *PaperclipRoutineModel) {
			m.ProjectID = types.StringValue("project")
			m.GoalID = types.StringValue("goal")
			m.ParentIssueID = types.StringValue("parent")
			m.AssigneeAgentID = types.StringUnknown()
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := baseRoutineModel()
			tc.edit(&m)
			p := routinePlan(t, s, &m)
			resp := resource.ModifyPlanResponse{Plan: p}
			r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: p}, &resp)
			if resp.Diagnostics.HasError() != tc.invalid {
				t.Fatal(resp.Diagnostics)
			}
			config := resource.ValidateConfigResponse{}
			r.ValidateConfig(ctx, resource.ValidateConfigRequest{Config: tfsdk.Config{Schema: s, Raw: p.Raw}}, &config)
			if tc.name == "title" && !config.Diagnostics.HasError() {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
	unconfigured := NewPaperclipRoutineResource().(*PaperclipRoutineResource)
	if d := unconfigured.references(ctx, baseRoutineModel()); d.HasError() {
		t.Fatal(d)
	}
	p := routinePlan(t, s, &PaperclipRoutineModel{})
	nullPlan := tfsdk.Plan{Schema: s, Raw: tftypes.NewValue(p.Raw.Type(), nil)}
	resp := resource.ModifyPlanResponse{}
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: nullPlan}, &resp)
	assertRoutineOK(t, resp.Diagnostics)
}

func TestRoutineTriggerRejectsInvalidConfigurationAndTransitions(t *testing.T) {
	f := newRoutineFixture(t)
	ctx := context.Background()
	r := NewPaperclipRoutineTriggerResource().(*PaperclipRoutineTriggerResource)
	s := configureTestResource(t, f.server.URL, r)
	base := PaperclipRoutineTriggerModel{ID: types.StringValue("trigger"), RoutineID: types.StringValue("routine"), Kind: types.StringValue("api"), Enabled: types.BoolValue(true)}
	for _, tc := range []struct {
		name    string
		edit    func(*PaperclipRoutineTriggerModel)
		invalid bool
	}{
		{"label", func(m *PaperclipRoutineTriggerModel) { m.Label = types.StringValue(" leading") }, true},
		{"missing schedule", func(m *PaperclipRoutineTriggerModel) { m.Kind = types.StringValue("schedule") }, true},
		{"irrelevant schedule", func(m *PaperclipRoutineTriggerModel) { m.Timezone = types.StringValue("UTC") }, true},
		{"irrelevant webhook", func(m *PaperclipRoutineTriggerModel) { m.SigningMode = types.StringValue("bearer") }, true},
		{"replay bounds", func(m *PaperclipRoutineTriggerModel) {
			m.Kind = types.StringValue("webhook")
			m.ReplayWindowSec = types.Int64Value(29)
		}, true},
		{"unknown kind", func(m *PaperclipRoutineTriggerModel) {
			m.Kind = types.StringUnknown()
			m.RoutineID = types.StringUnknown()
		}, false},
		{"valid", func(m *PaperclipRoutineTriggerModel) {}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := base
			tc.edit(&m)
			p := routinePlan(t, s, &m)
			config := resource.ValidateConfigResponse{}
			r.ValidateConfig(ctx, resource.ValidateConfigRequest{Config: tfsdk.Config{Schema: s, Raw: p.Raw}}, &config)
			if config.Diagnostics.HasError() != tc.invalid {
				t.Fatal(config.Diagnostics)
			}
		})
	}
	p := routinePlan(t, s, &base)
	state := tfsdk.State{Schema: s, Raw: p.Raw}
	nullState := tfsdk.State{Schema: s, Raw: tftypes.NewValue(p.Raw.Type(), nil)}
	for _, tc := range []struct {
		name          string
		kind, routine types.String
		old           tfsdk.State
		invalid       bool
	}{
		{"create webhook", types.StringValue("webhook"), base.RoutineID, nullState, true},
		{"edit kind", types.StringValue("schedule"), base.RoutineID, state, true},
		{"move routine", base.Kind, types.StringValue("other"), state, true},
		{"same", base.Kind, base.RoutineID, state, false},
		{"unknown", types.StringUnknown(), types.StringUnknown(), state, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := base
			m.Kind = tc.kind
			m.RoutineID = tc.routine
			p := routinePlan(t, s, &m)
			resp := resource.ModifyPlanResponse{Plan: p}
			r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: p, State: tc.old}, &resp)
			if resp.Diagnostics.HasError() != tc.invalid {
				t.Fatal(resp.Diagnostics)
			}
		})
	}
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: tfsdk.Plan{Schema: s, Raw: nullState.Raw}}, &resource.ModifyPlanResponse{})
	f.fail = 403
	planned := resource.ModifyPlanResponse{Plan: p}
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: p, State: state}, &planned)
	assertDiagContains(t, planned.Diagnostics, "Invalid routine reference", "403")
	created := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(ctx, resource.CreateRequest{Plan: p}, &created)
	assertDiagContains(t, created.Diagnostics, "Invalid routine reference", "403")
	f.fail = 0
	f.failWrite = 500
	created = resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(ctx, resource.CreateRequest{Plan: p}, &created)
	assertDiagContains(t, created.Diagnostics, "Error creating", "500")
	m := base
	m.RoutineID = types.StringValue("other")
	updated := resource.UpdateResponse{State: state}
	r.Update(ctx, resource.UpdateRequest{Plan: routinePlan(t, s, &m), State: state}, &updated)
	assertDiagContains(t, updated.Diagnostics, "Unsupported trigger transition", "immutable")
	updated = resource.UpdateResponse{State: state}
	r.Update(ctx, resource.UpdateRequest{Plan: p, State: state}, &updated)
	assertDiagContains(t, updated.Diagnostics, "Invalid routine trigger reference", "resource not found")
	f.triggers = []map[string]interface{}{{"id": "trigger", "routineId": "routine", "kind": "api", "enabled": true}}
	updated = resource.UpdateResponse{State: state}
	r.Update(ctx, resource.UpdateRequest{Plan: p, State: state}, &updated)
	assertDiagContains(t, updated.Diagnostics, "Error updating", "500")
	deleted := resource.DeleteResponse{}
	r.Delete(ctx, resource.DeleteRequest{State: state}, &deleted)
	assertDiagContains(t, deleted.Diagnostics, "Error deleting", "500")
	if len(f.writes) != 0 {
		t.Fatal("rejected operation mutated")
	}
}

func TestRoutineReadsAndImportPreserveIdentity(t *testing.T) {
	f := newRoutineFixture(t)
	ctx := context.Background()
	for _, res := range []resource.Resource{NewPaperclipRoutineResource(), NewPaperclipRoutineTriggerResource()} {
		s := configureTestResource(t, f.server.URL, res.(resource.ResourceWithConfigure))
		importer := res.(resource.ResourceWithImportState)
		id := "routine"
		if _, ok := res.(*PaperclipRoutineTriggerResource); ok {
			id = "routine/trigger"
			for _, bad := range []string{"", "routine", "/trigger", "routine/", "a/b/c"} {
				resp := resource.ImportStateResponse{State: tfsdk.State{Schema: s}}
				importer.ImportState(ctx, resource.ImportStateRequest{ID: bad}, &resp)
				if !resp.Diagnostics.HasError() {
					t.Fatal("invalid import accepted")
				}
			}
		}
		imported := resource.ImportStateResponse{State: tfsdk.State{Schema: s}}
		if _, ok := res.(*PaperclipRoutineResource); ok {
			assertRoutineOK(t, imported.State.Set(ctx, &PaperclipRoutineModel{}))
		} else {
			assertRoutineOK(t, imported.State.Set(ctx, &PaperclipRoutineTriggerModel{}))
		}
		importer.ImportState(ctx, resource.ImportStateRequest{ID: id}, &imported)
		assertRoutineOK(t, imported.Diagnostics)
		var actual types.String
		assertRoutineOK(t, imported.State.GetAttribute(ctx, path.Root("id"), &actual))
		if actual.ValueString() != strings.TrimPrefix(id, "routine/") {
			t.Fatal(actual)
		}
		f.fail = 403
		read := resource.ReadResponse{State: imported.State}
		res.Read(ctx, resource.ReadRequest{State: imported.State}, &read)
		if !read.Diagnostics.HasError() || read.State.Raw.IsNull() {
			t.Fatal("auth error removed identity")
		}
		f.fail = 0
		f.missing = true
		read = resource.ReadResponse{State: imported.State}
		res.Read(ctx, resource.ReadRequest{State: imported.State}, &read)
		assertRoutineOK(t, read.Diagnostics)
		if !read.State.Raw.IsNull() {
			t.Fatal("missing object retained")
		}
		f.missing = false
	}
	r := NewPaperclipRoutineResource().(*PaperclipRoutineResource)
	s := configureTestResource(t, f.server.URL, r)
	m := routineWithIdentity(baseRoutineModel(), "routine", "revision")
	p := routinePlan(t, s, &m)
	read := resource.ReadResponse{State: tfsdk.State{Schema: s, Raw: p.Raw}}
	f.routine["title"] = "Changed {{name}}"
	f.routine["variables"] = []map[string]interface{}{{"name": "name", "type": "text", "label": nil, "defaultValue": nil, "required": true, "options": []interface{}{}}}
	r.Read(ctx, resource.ReadRequest{State: read.State}, &read)
	assertRoutineOK(t, read.Diagnostics)
	assertRoutineOK(t, read.State.Get(ctx, &m))
	if m.Title.ValueString() != "Changed {{name}}" || !strings.Contains(m.Variables.ValueString(), "name") {
		t.Fatal(m)
	}
}

func TestRoutineInvalidStateDoesNotCallAPI(t *testing.T) {
	ctx := context.Background()
	bad := tftypes.NewValue(tftypes.String, "invalid")
	for _, res := range []resource.Resource{NewPaperclipRoutineResource(), NewPaperclipRoutineTriggerResource()} {
		var s resource.SchemaResponse
		res.Schema(ctx, resource.SchemaRequest{}, &s)
		var meta resource.MetadataResponse
		res.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "paperclip"}, &meta)
		if !strings.HasPrefix(meta.TypeName, "paperclip_routine") {
			t.Fatal(meta)
		}
		configured := res.(resource.ResourceWithConfigure)
		var c resource.ConfigureResponse
		configured.Configure(ctx, resource.ConfigureRequest{}, &c)
		assertRoutineOK(t, c.Diagnostics)
		configured.Configure(ctx, resource.ConfigureRequest{ProviderData: "bad"}, &c)
		if !c.Diagnostics.HasError() {
			t.Fatal("invalid client accepted")
		}
		create := resource.CreateResponse{}
		res.Create(ctx, resource.CreateRequest{Plan: tfsdk.Plan{Schema: s.Schema, Raw: bad}}, &create)
		if !create.Diagnostics.HasError() {
			t.Fatal("bad create state accepted")
		}
		read := resource.ReadResponse{}
		res.Read(ctx, resource.ReadRequest{State: tfsdk.State{Schema: s.Schema, Raw: bad}}, &read)
		if !read.Diagnostics.HasError() {
			t.Fatal("bad read state accepted")
		}
		update := resource.UpdateResponse{}
		res.Update(ctx, resource.UpdateRequest{Plan: tfsdk.Plan{Schema: s.Schema, Raw: bad}, State: tfsdk.State{Schema: s.Schema, Raw: bad}}, &update)
		if !update.Diagnostics.HasError() {
			t.Fatal("bad update state accepted")
		}
		del := resource.DeleteResponse{}
		res.Delete(ctx, resource.DeleteRequest{State: tfsdk.State{Schema: s.Schema, Raw: bad}}, &del)
		if !del.Diagnostics.HasError() {
			t.Fatal("bad delete state accepted")
		}
		plan := resource.ModifyPlanResponse{}
		res.(resource.ResourceWithModifyPlan).ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: tfsdk.Plan{Schema: s.Schema, Raw: bad}}, &plan)
		if !plan.Diagnostics.HasError() {
			t.Fatal("bad plan accepted")
		}
		config := resource.ValidateConfigResponse{}
		res.(resource.ResourceWithValidateConfig).ValidateConfig(ctx, resource.ValidateConfigRequest{Config: tfsdk.Config{Schema: s.Schema, Raw: bad}}, &config)
		if !config.Diagnostics.HasError() {
			t.Fatal("bad config accepted")
		}
	}
}
