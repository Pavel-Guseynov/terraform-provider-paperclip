package provider

import (
	"context"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestToolOwnershipConflictsBeforeMutation(t *testing.T) {
	ctx := context.Background()
	f := newToolFixture(t)
	pr, _, pm, pstate := createToolProfile(t, f)
	policy, _, policyModel, policyState := createToolPolicy(t, f)
	binding := &PaperclipToolProfileBindingResource{}
	bs := configureTestResource(t, f.server.URL, binding)
	bm := bindingTestModel()
	bc := resource.CreateResponse{State: tfsdk.State{Schema: bs}}
	binding.Create(ctx, resource.CreateRequest{Plan: toolPlan(t, bs, &bm)}, &bc)
	requireToolDiagnostics(t, bc.Diagnostics)
	requireToolDiagnostics(t, bc.State.Get(ctx, &bm))
	for _, tc := range []struct {
		r resource.ResourceWithModifyPlan
		m interface{}
		s tfsdk.State
	}{{pr, &pm, pstate}, {policy, &policyModel, policyState}, {binding, &bm, bc.State}} {
		plan := toolPlan(t, tc.s.Schema.(schema.Schema), tc.m)
		resp := resource.ModifyPlanResponse{Plan: plan}
		tc.r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan, State: tc.s}, &resp)
		requireToolDiagnostics(t, resp.Diagnostics)
		bad := plan
		requireToolDiagnostics(t, bad.SetAttribute(ctx, path.Root("owner_id"), types.StringValue("conflicting-owner")))
		conflict := resource.ModifyPlanResponse{Plan: bad}
		tc.r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: bad, State: tc.s}, &conflict)
		if !conflict.Diagnostics.HasError() {
			t.Fatal("duplicate declarative owner accepted")
		}
	}
	pm.OwnerID = types.StringValue("conflicting-owner")
	if err := pr.save(ctx, &pm, true); err == nil {
		t.Fatal("profile mutation accepted duplicate owner")
	}
	policyModel.OwnerID = types.StringValue("conflicting-owner")
	if err := policy.save(ctx, &policyModel, true); err == nil {
		t.Fatal("policy mutation accepted duplicate owner")
	}
	for _, entry := range []struct {
		r resource.Resource
		s tfsdk.State
	}{{pr, pstate}, {policy, policyState}, {binding, bc.State}} {
		state := entry.s
		requireToolDiagnostics(t, state.SetAttribute(ctx, path.Root("owner_id"), types.StringValue("conflicting-owner")))
		read := resource.ReadResponse{State: state}
		entry.r.Read(ctx, resource.ReadRequest{State: state}, &read)
		if !read.Diagnostics.HasError() || !reflect.DeepEqual(read.State.Raw, state.Raw) {
			t.Fatal("conflicting import claimed object or lost state")
		}
	}
	f.profiles["owned"]["bindings"] = []interface{}{}
	bm.OwnerID = types.StringValue("conflicting-owner")
	cc := resource.CreateResponse{State: tfsdk.State{Schema: bs}}
	binding.Create(ctx, resource.CreateRequest{Plan: toolPlan(t, bs, &bm)}, &cc)
	if !cc.Diagnostics.HasError() {
		t.Fatal("binding creation accepted duplicate owner")
	}
	if len(f.writes) != 3 {
		t.Fatalf("ownership conflicts mutated native objects: %d writes", len(f.writes))
	}
}

func TestToolBindingNativeLimitValidation(t *testing.T) {
	f := newToolFixture(t)
	createToolProfile(t, f)
	r := &PaperclipToolProfileBindingResource{}
	s := configureTestResource(t, f.server.URL, r)
	ctx := context.Background()
	m := bindingTestModel()
	for _, change := range []string{"metadata", "priority", "target", "reference", "profile", "finish"} {
		t.Run(change, func(t *testing.T) {
			bad := m
			switch change {
			case "metadata":
				bad.Metadata = types.StringValue(`[]`)
			case "priority":
				bad.Priority = types.Int64Value(-1)
			case "target":
				bad.TargetID = types.StringValue(" ")
			case "reference":
				bad.TargetID = types.StringValue("other-company")
			case "profile":
				bad.ProfileID = types.StringValue("missing")
			case "finish":
				bad.ProfileID = types.StringValue("app")
			}
			plan := toolPlan(t, s, &bad)
			modify := resource.ModifyPlanResponse{Plan: plan}
			r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan, State: tfsdk.State{Schema: s}}, &modify)
			if !modify.Diagnostics.HasError() {
				t.Fatal("invalid binding survived plan")
			}
			create := resource.CreateResponse{State: tfsdk.State{Schema: s}}
			r.Create(ctx, resource.CreateRequest{Plan: plan}, &create)
			if !create.Diagnostics.HasError() {
				t.Fatal("invalid binding accepted a write")
			}
		})
	}
	m.ProfileID = types.StringUnknown()
	if _, err := r.validate(ctx, m); err != nil {
		t.Fatal(err)
	}
	m.CompanyID = types.StringUnknown()
	if _, err := r.validate(ctx, m); err != nil {
		t.Fatal(err)
	}
	m.Metadata = types.StringValue(`[]`)
	if _, err := r.validate(ctx, m); err == nil {
		t.Fatal("malformed deferred binding accepted")
	}
	prior := bindingTestModel()
	next := prior
	next.Priority = types.Int64Value(1)
	plan := toolPlan(t, s, &next)
	st := toolState(t, s, &prior)
	modify := resource.ModifyPlanResponse{Plan: plan}
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan, State: st}, &modify)
	if !modify.Diagnostics.HasError() {
		t.Fatal("insert-only update survived plan")
	}
	update := resource.UpdateResponse{State: st}
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: st}, &update)
	if !update.Diagnostics.HasError() {
		t.Fatal("insert-only update succeeded")
	}
	update = resource.UpdateResponse{State: st}
	r.Update(ctx, resource.UpdateRequest{Plan: toolPlan(t, s, &prior), State: st}, &update)
	requireToolDiagnostics(t, update.Diagnostics)
}

func TestToolImportsAndVerifiedAbsence(t *testing.T) {
	f := newToolFixture(t)
	_, _, pm, _ := createToolProfile(t, f)
	_, _, polm, _ := createToolPolicy(t, f)
	ctx := context.Background()
	binding := &PaperclipToolProfileBindingResource{}
	bs := configureTestResource(t, f.server.URL, binding)
	bm := bindingTestModel()
	bc := resource.CreateResponse{State: tfsdk.State{Schema: bs}}
	binding.Create(ctx, resource.CreateRequest{Plan: toolPlan(t, bs, &bm)}, &bc)
	requireToolDiagnostics(t, bc.Diagnostics)
	for _, kind := range []string{"profile", "binding"} {
		t.Run(kind, func(t *testing.T) {
			var r resource.Resource
			var id string
			if kind == "profile" {
				r = NewPaperclipToolProfileResource()
				id = "comp-1/owned"
			} else {
				r = NewPaperclipToolProfileBindingResource()
				id = "comp-1/owned/binding"
			}
			s := configureTestResource(t, f.server.URL, r.(resource.ResourceWithConfigure))
			imp := resource.ImportStateResponse{State: tfsdk.State{Schema: s}}
			if kind == "profile" {
				requireToolDiagnostics(t, imp.State.Set(ctx, &toolProfileModel{}))
			} else {
				requireToolDiagnostics(t, imp.State.Set(ctx, &toolBindingModel{}))
			}
			r.(resource.ResourceWithImportState).ImportState(ctx, resource.ImportStateRequest{ID: id}, &imp)
			requireToolDiagnostics(t, imp.Diagnostics)
			read := resource.ReadResponse{State: imp.State}
			r.Read(ctx, resource.ReadRequest{State: imp.State}, &read)
			requireToolDiagnostics(t, read.Diagnostics)
			if kind == "profile" {
				var got toolProfileModel
				requireToolDiagnostics(t, read.State.Get(ctx, &got))
				if got.ID.ValueString() != pm.ID.ValueString() || got.Entries.ValueString() == "" || got.Metadata.ValueString() != "{}" || got.ForceDestroy.ValueBool() {
					t.Fatal("profile import lost identity/defaults")
				}
			} else {
				var got toolBindingModel
				requireToolDiagnostics(t, read.State.Get(ctx, &got))
				if got.ID.ValueString() != "binding" {
					t.Fatal("binding import replaced identity")
				}
			}
		})
	}
	delete(f.profiles, "owned")
	delete(f.policies, "owned")
	for _, kind := range []string{"profile", "binding", "policy"} {
		var r resource.Resource
		var model interface{}
		switch kind {
		case "profile":
			r = NewPaperclipToolProfileResource()
			model = &pm
		case "binding":
			r = NewPaperclipToolProfileBindingResource()
			model = &bm
		case "policy":
			r = NewPaperclipPolicyResource()
			model = &polm
		}
		s := configureTestResource(t, f.server.URL, r.(resource.ResourceWithConfigure))
		state := toolState(t, s, model)
		read := resource.ReadResponse{State: state}
		r.Read(ctx, resource.ReadRequest{State: state}, &read)
		requireToolDiagnostics(t, read.Diagnostics)
		if !read.State.Raw.IsNull() {
			t.Fatal("verified absence retained state")
		}
		del := resource.DeleteResponse{}
		r.Delete(ctx, resource.DeleteRequest{State: state}, &del)
		requireToolDiagnostics(t, del.Diagnostics)
	}
}
func TestToolBindingAbsentAndChangedTargetAreDistinct(t *testing.T) {
	f := newToolFixture(t)
	createToolProfile(t, f)
	r := &PaperclipToolProfileBindingResource{}
	s := configureTestResource(t, f.server.URL, r)
	ctx := context.Background()
	m := bindingTestModel()
	m.ID = types.StringValue("binding")
	state := toolState(t, s, &m)
	read := resource.ReadResponse{State: state}
	r.Read(ctx, resource.ReadRequest{State: state}, &read)
	requireToolDiagnostics(t, read.Diagnostics)
	if !read.State.Raw.IsNull() {
		t.Fatal("absent binding retained state")
	}
	del := resource.DeleteResponse{}
	r.Delete(ctx, resource.DeleteRequest{State: state}, &del)
	requireToolDiagnostics(t, del.Diagnostics)
	f.profiles["owned"]["bindings"] = []interface{}{map[string]interface{}{"id": "binding", "targetType": "project", "targetId": "different"}}
	del = resource.DeleteResponse{}
	r.Delete(ctx, resource.DeleteRequest{State: state}, &del)
	if !del.Diagnostics.HasError() {
		t.Fatal("changed target unbound another identity")
	}
}
func TestToolProfilePlanAndReferences(t *testing.T) {
	f := newToolFixture(t)
	r, s, m, state := createToolProfile(t, f)
	ctx := context.Background()
	m.Entries = types.StringValue(`[{"selectorType":"connection","connectionId":"` + toolTestUUID + `","applicationId":"` + toolTestUUID + `","catalogEntryId":"` + toolTestUUID + `"}]`)
	if err := r.validate(ctx, m); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"other", "missing", "app", "owned"} {
		m.ReassignToProfileID = types.StringValue(id)
		err := r.validate(ctx, m)
		if (err == nil) != (id == "other") {
			t.Fatalf("reassignment %s: %v", id, err)
		}
	}
	m.ReassignToProfileID = types.StringNull()
	m.OwnerID = types.StringUnknown()
	m.ProfileKey = types.StringValue("planned")
	plan := toolPlan(t, s, &m)
	resp := resource.ModifyPlanResponse{Plan: plan}
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan, State: state}, &resp)
	requireToolDiagnostics(t, resp.Diagnostics)
	badState := state
	badState.Raw = tftypes.NewValue(tftypes.String, "bad")
	resp = resource.ModifyPlanResponse{Plan: plan}
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan, State: badState}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("malformed prior state accepted")
	}
	if !reflect.DeepEqual(normalizeToolEntries("bad"), "bad") || !reflect.DeepEqual(normalizeToolEntries([]interface{}{true}), []interface{}{true}) {
		t.Fatal("malformed entry shape masked by comparison")
	}
	f.profiles["owned"]["description"] = "retained"
	m = profileTestModel()
	m.ID = types.StringValue("owned")
	m.OwnerID = types.StringValue("owner")
	m.Description = types.StringValue("")
	if err := r.save(ctx, &m, true); err == nil {
		t.Fatal("duplicate owner escaped profile write claim")
	}
}

func TestToolPlanErrorsKeepValidPriorState(t *testing.T) {
	ctx := context.Background()
	f := newToolFixture(t)
	policy, s, m, state := createToolPolicy(t, f)
	m.Conditions = types.StringValue(`{}`)
	plan := toolPlan(t, s, &m)
	modified := resource.ModifyPlanResponse{Plan: plan}
	policy.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan, State: state}, &modified)
	if !modified.Diagnostics.HasError() {
		t.Fatal("unsupported conditions survived planning")
	}
	m.Conditions = types.StringNull()
	m.ID = types.StringValue("missing")
	if err := policy.save(ctx, &m, true); err == nil {
		t.Fatal("update recreated an absent policy")
	}
	profile, _, pm, pstate := createToolProfile(t, f)
	pm.Entries = types.StringValue(`{}`)
	if err := profile.validate(ctx, pm); err == nil {
		t.Fatal("malformed entries survived reference validation")
	}
	f.profiles["owned"]["description"] = ""
	read := resource.ReadResponse{State: pstate}
	profile.Read(ctx, resource.ReadRequest{State: pstate}, &read)
	requireToolDiagnostics(t, read.Diagnostics)
	requireToolDiagnostics(t, read.State.Get(ctx, &pm))
	if pm.Description.IsNull() || pm.Description.ValueString() != "" {
		t.Fatal("empty native description was changed to null")
	}
	binding := &PaperclipToolProfileBindingResource{}
	bs := configureTestResource(t, f.server.URL, binding)
	bm := bindingTestModel()
	bp := toolPlan(t, bs, &bm)
	badState := tfsdk.State{Schema: bs, Raw: tftypes.NewValue(tftypes.String, "malformed prior state")}
	bmresp := resource.ModifyPlanResponse{Plan: bp}
	binding.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: bp, State: badState}, &bmresp)
	if !bmresp.Diagnostics.HasError() {
		t.Fatal("malformed prior binding state survived planning")
	}
}

func TestToolOwnerRemainsUnknownAcrossPlanExpansion(t *testing.T) {
	f := newToolFixture(t)
	createToolProfile(t, f)
	ctx := context.Background()
	profile := profileTestModel()
	profile.ProfileKey = types.StringValue("uncreated")
	profile.OwnerID = types.StringUnknown()
	policy := policyTestModel()
	policy.OwnerID = types.StringUnknown()
	binding := bindingTestModel()
	binding.OwnerID = types.StringUnknown()
	for _, entry := range []struct {
		r     resource.Resource
		model interface{}
	}{
		{NewPaperclipToolProfileResource(), &profile}, {NewPaperclipPolicyResource(), &policy}, {NewPaperclipToolProfileBindingResource(), &binding},
	} {
		s := configureTestResource(t, f.server.URL, entry.r.(resource.ResourceWithConfigure))
		plan := toolPlan(t, s, entry.model)
		for range 2 {
			response := resource.ModifyPlanResponse{Plan: plan}
			entry.r.(resource.ResourceWithModifyPlan).ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan, State: tfsdk.State{Schema: s}}, &response)
			requireToolDiagnostics(t, response.Diagnostics)
			var owner types.String
			requireToolDiagnostics(t, response.Plan.GetAttribute(ctx, path.Root("owner_id"), &owner))
			if !owner.IsUnknown() {
				t.Fatal("planning minted a new owner identity; apply expansion would be inconsistent")
			}
		}
	}
}

func TestToolPolicyRefreshExposesExplicitEmptyJSON(t *testing.T) {
	f := newToolFixture(t)
	r, s, m, _ := createToolPolicy(t, f)
	m.PolicyType = types.StringValue("allow")
	m.Config = types.StringNull()
	m.Selectors = types.StringNull()
	f.policies["owned"]["policyType"] = "allow"
	f.policies["owned"]["config"] = map[string]interface{}{}
	f.policies["owned"]["selectors"] = map[string]interface{}{}
	state := toolState(t, s, &m)
	read := resource.ReadResponse{State: state}
	r.Read(context.Background(), resource.ReadRequest{State: state}, &read)
	requireToolDiagnostics(t, read.Diagnostics)
	requireToolDiagnostics(t, read.State.Get(context.Background(), &m))
	if m.Config.ValueString() != "{}" || m.Selectors.ValueString() != "{}" {
		t.Fatal("native empty objects were hidden as null; matching imports would drift")
	}
}
