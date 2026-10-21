package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func stdioToolsPlan(t *testing.T, endpoint string, tools types.String) (*PaperclipStdioTemplateResource, tfsdk.Plan) {
	t.Helper()
	ctx := context.Background()
	r := NewPaperclipStdioTemplateResource().(*PaperclipStdioTemplateResource)
	s := configureTestResource(t, endpoint, r)
	plan := tfsdk.Plan{Schema: s}
	model := PaperclipStdioTemplateResourceModel{
		CompanyID: types.StringValue("company"), TemplateID: types.StringValue("fixture.tools.v1"),
		Name: types.StringValue("Fixture tools"), Command: types.StringValue("fixture-mcp"),
		Args: types.ListNull(types.StringType), EnvKeys: types.ListNull(types.StringType),
	}
	if d := plan.Set(ctx, &model); d.HasError() {
		t.Fatal(d)
	}
	if d := plan.SetAttribute(ctx, path.Root("tools"), tools); d.HasError() {
		t.Fatalf("producer tool descriptors cannot be declared: %v", d)
	}
	return r, plan
}

func TestStdioTemplatePreservesProducerToolsThroughCreateAndRefresh(t *testing.T) {
	ctx := context.Background()
	data, err := os.ReadFile("../fixtures/stdio-tools.json")
	if err != nil {
		t.Fatal(err)
	}
	descriptors := string(data)
	var expected []map[string]interface{}
	if err := json.Unmarshal([]byte(descriptors), &expected); err != nil {
		t.Fatal(err)
	}
	var live map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch req.Method + " " + req.URL.Path {
		case "POST /api/companies/company/tools/stdio-templates":
			if err := json.NewDecoder(req.Body).Decode(&live); err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			var actual []map[string]interface{}
			encoded, _ := json.Marshal(live["tools"])
			if err := json.Unmarshal(encoded, &actual); err != nil || !reflect.DeepEqual(actual, expected) {
				t.Errorf("producer descriptors changed in the native request: %s", encoded)
			}
			live["id"], live["companyId"], live["status"] = "template-uuid", "company", "active"
			_ = json.NewEncoder(w).Encode(live)
		case "GET /api/companies/company/tools/stdio-templates":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"templates": []interface{}{live}})
		default:
			t.Errorf("unexpected native request: %s %s", req.Method, req.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	r, plan := stdioToolsPlan(t, server.URL, types.StringValue(descriptors))
	created := resource.CreateResponse{State: tfsdk.State{Schema: plan.Schema}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &created)
	if created.Diagnostics.HasError() {
		t.Fatal(created.Diagnostics)
	}
	var actual types.String
	if d := created.State.GetAttribute(ctx, path.Root("tools"), &actual); d.HasError() || actual.ValueString() != descriptors {
		t.Fatalf("created descriptor state changed: %s %v", actual, d)
	}
	expected[0]["annotations"].(map[string]interface{})["readOnlyHint"] = false
	live["tools"] = expected
	read := resource.ReadResponse{State: created.State}
	r.Read(ctx, resource.ReadRequest{State: created.State}, &read)
	if read.Diagnostics.HasError() {
		t.Fatal(read.Diagnostics)
	}
	if d := read.State.GetAttribute(ctx, path.Root("tools"), &actual); d.HasError() || !semanticJSONEqual(actual.ValueString(), marshalJSON(expected)) {
		t.Fatalf("native descriptor drift was masked: %s %v", actual, d)
	}
}

func TestStdioTemplateToolsRejectMalformedDescriptors(t *testing.T) {
	ctx := context.Background()
	var resourceSchema resource.SchemaResponse
	NewPaperclipStdioTemplateResource().Schema(ctx, resource.SchemaRequest{}, &resourceSchema)
	attribute := resourceSchema.Schema.Attributes["tools"].(schema.StringAttribute)
	for _, raw := range []string{
		``, `{`, `null`, `{}`, `[1]`, `[null]`, `[{}]`, `[{"name":false}]`,
		`[{"name":" "}]`, `[{"name":"same"},{"name":" same "}]`,
		`[{"name":"read","title":false}]`, `[{"name":"read","description":[]}]`,
		`[{"name":"read","inputSchema":[]}]`, `[{"name":"read","annotations":true}]`,
		`[{"name":"read","outputSchema":{}}]`,
	} {
		t.Run(raw, func(t *testing.T) {
			response := validator.StringResponse{}
			attribute.Validators[0].ValidateString(ctx, validator.StringRequest{Path: path.Root("tools"), ConfigValue: types.StringValue(raw)}, &response)
			if !response.Diagnostics.HasError() {
				t.Fatalf("malformed native descriptors were accepted: %s", raw)
			}
		})
	}
	var tooMany []map[string]interface{}
	for index := 0; index < 501; index++ {
		tooMany = append(tooMany, map[string]interface{}{"name": fmt.Sprintf("fixture_%d", index)})
	}
	if _, err := parseStdioTemplateTools(types.StringValue(marshalJSON(tooMany))); err == nil {
		t.Fatal("more than 500 native descriptors were accepted")
	}
	for _, raw := range []types.String{
		types.StringNull(), types.StringUnknown(), types.StringValue(`[]`),
		types.StringValue(`[{"name":"read","title":null,"description":null,"inputSchema":null,"annotations":null}]`),
		types.StringValue(marshalJSON(tooMany[:500])),
	} {
		response := validator.StringResponse{}
		attribute.Validators[0].ValidateString(ctx, validator.StringRequest{Path: path.Root("tools"), ConfigValue: raw}, &response)
		if response.Diagnostics.HasError() {
			t.Fatalf("valid native descriptors were rejected: %v", response.Diagnostics)
		}
	}
	if !strings.Contains(attribute.Validators[0].Description(ctx), "producer") || attribute.Validators[0].MarkdownDescription(ctx) == "" {
		t.Fatal("the public validation contract omits descriptor provenance")
	}
}

func TestStdioTemplateToolsCompareOnlyNativeDescriptorDefaults(t *testing.T) {
	ctx := context.Background()
	var resourceSchema resource.SchemaResponse
	NewPaperclipStdioTemplateResource().Schema(ctx, resource.SchemaRequest{}, &resourceSchema)
	attribute := resourceSchema.Schema.Attributes["tools"].(schema.StringAttribute)
	planned := types.StringValue(`[{"name":" read ","title":" Label "}]`)
	native := types.StringValue(`[{"name":"read","title":"Label","description":null,"inputSchema":{"type":"object","properties":{}},"annotations":{}}]`)
	response := planmodifier.StringResponse{PlanValue: planned}
	attribute.PlanModifiers[0].PlanModifyString(ctx, planmodifier.StringRequest{PlanValue: planned, StateValue: native}, &response)
	if !response.PlanValue.Equal(native) {
		t.Fatal("native descriptor defaults caused a replacement-only formatting diff")
	}
	if semanticJSONEqualNormalized(`[{"name":"read","annotations":{"readOnlyHint":false}}]`, `[{"name":"read","annotations":{"readOnlyHint":true}}]`, normalizeStdioTemplateTools) {
		t.Fatal("different native approval annotations were treated as equal")
	}
	if normalizeStdioTemplateTools("malformed state") != "malformed state" || !reflect.DeepEqual(normalizeStdioTemplateTools([]interface{}{nil}), []interface{}{nil}) {
		t.Fatal("malformed descriptor state was synthesized into native tools")
	}
}

func TestStdioTemplateRejectsMalformedToolsBeforeMutation(t *testing.T) {
	mutations := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mutations++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	r, plan := stdioToolsPlan(t, server.URL, types.StringValue(`[{"name":"read","annotations":[]}]`))
	response := resource.CreateResponse{State: tfsdk.State{Schema: plan.Schema}}
	r.Create(context.Background(), resource.CreateRequest{Plan: plan}, &response)
	if !response.Diagnostics.HasError() || mutations != 0 {
		t.Fatalf("invalid producer descriptors reached Paperclip: %d %v", mutations, response.Diagnostics)
	}
}

func TestStdioTemplateRetainsCreatedIdentityWhenNativeDescriptorsAreLost(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id": "created-template", "companyId": "company", "templateId": "fixture.tools.v1",
			"name": "Fixture tools", "command": "fixture-mcp", "tools": []interface{}{},
		})
	}))
	t.Cleanup(server.Close)
	r, plan := stdioToolsPlan(t, server.URL, types.StringValue(`[{"name":"fixture_read"}]`))
	response := resource.CreateResponse{State: tfsdk.State{Schema: plan.Schema}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &response)
	var id types.String
	if d := response.State.GetAttribute(ctx, path.Root("id"), &id); d.HasError() || id.ValueString() != "created-template" || !response.Diagnostics.HasError() {
		t.Fatalf("lost descriptors did not fail while retaining the created identity: %s %v %v", id, d, response.Diagnostics)
	}
}
