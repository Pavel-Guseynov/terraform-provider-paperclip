package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/Pavel-Guseynov/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type PaperclipToolProfileResource struct{ client *client.Client }
type toolProfileModel struct {
	ID                  types.String `tfsdk:"id"`
	CompanyID           types.String `tfsdk:"company_id"`
	OwnerID             types.String `tfsdk:"owner_id"`
	ProfileKey          types.String `tfsdk:"profile_key"`
	Name                types.String `tfsdk:"name"`
	Description         types.String `tfsdk:"description"`
	Status              types.String `tfsdk:"status"`
	DefaultAction       types.String `tfsdk:"default_action"`
	Metadata            types.String `tfsdk:"metadata"`
	Entries             types.String `tfsdk:"entries"`
	ForceDestroy        types.Bool   `tfsdk:"force_destroy"`
	ReassignToProfileID types.String `tfsdk:"reassign_to_profile_id"`
}

func NewPaperclipToolProfileResource() resource.Resource { return &PaperclipToolProfileResource{} }
func (r *PaperclipToolProfileResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_tool_profile"
}
func toolIdentityAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"id":         schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
		"company_id": schema.StringAttribute{Required: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
		"owner_id":   schema.StringAttribute{Computed: true, Description: "Single resource ownership identity within this provider configuration. Independent states must coordinate ownership externally; the API has no persistent owner/CAS field.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
	}
}
func toolJSONAttribute(description string) schema.StringAttribute {
	return schema.StringAttribute{Optional: true, Description: description, Validators: []validator.String{applicationMetadataValidator{}}, PlanModifiers: []planmodifier.String{SemanticJSONPlanModifier()}}
}
func (r *PaperclipToolProfileResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	a := toolIdentityAttributes()
	a["profile_key"] = schema.StringAttribute{Required: true, Description: "Company-unique profile key. The app: namespace is reserved for app_permission."}
	a["name"] = schema.StringAttribute{Required: true}
	a["description"] = schema.StringAttribute{Optional: true, Description: "Native description. A non-null value cannot be cleared to null; empty string is supported."}
	a["status"] = schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("active"), Validators: []validator.String{nativeEnumValidator{values: []string{"draft", "active", "disabled", "archived"}}}}
	a["default_action"] = schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("deny"), Validators: []validator.String{nativeEnumValidator{values: []string{"deny", "allow"}}}}
	a["metadata"] = toolJSONAttribute("Native metadata object. Use {} to clear; non-null metadata cannot become null. source=app_gallery_finish is reserved.")
	metadataAttribute := a["metadata"].(schema.StringAttribute)
	metadataAttribute.Computed = true
	metadataAttribute.Default = stringdefault.StaticString("{}")
	a["metadata"] = metadataAttribute
	a["entries"] = schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("[]"), Description: "Complete JSON array of up to 250 native entries (camelCase fields). Replaced as one set; omitted entries are removed only from this profile. Catalog schemas and producer annotations are never written.", Validators: []validator.String{toolEntriesValidator{}}, PlanModifiers: []planmodifier.String{semanticJSONPlanModifier{normalize: normalizeToolEntries}}}
	a["force_destroy"] = schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false), Description: "Explicitly permit deletion of the company default profile."}
	a["reassign_to_profile_id"] = schema.StringAttribute{Optional: true, Description: "Explicit native deletion target for existing bindings; must be a different active profile in this company, outside app_permission ownership."}
	resp.Schema = schema.Schema{Description: "Single aggregate writer of one native tool profile and its complete entries set. Bindings have separate resources. Finish-route profiles cannot be claimed.", Attributes: a}
}
func (r *PaperclipToolProfileResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Resource Configure Type", fmt.Sprintf("Expected *client.Client, got: %T", req.ProviderData))
		return
	}
	r.client = c
}

func normalizeToolEntries(value interface{}) interface{} {
	items, ok := value.([]interface{})
	if !ok {
		return value
	}
	keys := make([]string, 0, len(items))
	for _, item := range items {
		entry, ok := item.(map[string]interface{})
		if !ok {
			return value
		}
		clean := map[string]interface{}{"effect": "include"}
		for _, key := range []string{"selectorType", "effect", "applicationId", "connectionId", "catalogEntryId", "toolName", "riskLevel", "conditions"} {
			if v := entry[key]; v != nil {
				clean[key] = v
			}
		}
		b, _ := json.Marshal(clean)
		keys = append(keys, string(b))
	}
	sort.Strings(keys)
	return keys
}

func parseToolEntries(value types.String) ([]map[string]interface{}, error) {
	if value.IsUnknown() {
		return nil, nil
	}
	var entries []map[string]interface{}
	if err := json.Unmarshal([]byte(value.ValueString()), &entries); err != nil || entries == nil {
		return nil, fmt.Errorf("entries must be a JSON array; use [] for no entries")
	}
	if len(entries) > 250 {
		return nil, fmt.Errorf("entries exceeds the native limit of 250")
	}
	selectors := map[string]string{"application": "applicationId", "connection": "connectionId", "catalog_entry": "catalogEntryId", "tool_name": "toolName", "risk_level": "riskLevel"}
	for _, entry := range entries {
		selector, _ := entry["selectorType"].(string)
		field := selectors[selector]
		if field == "" || entry[field] == nil || entry[field] == "" {
			return nil, fmt.Errorf("each entry requires a supported selectorType and its %s selector value", field)
		}
		for key, v := range entry {
			if v == nil {
				continue
			}
			s, stringValue := v.(string)
			switch key {
			case "selectorType":
			case "effect":
				if !stringValue || (s != "include" && s != "exclude") {
					return nil, fmt.Errorf("entry effect must be include or exclude")
				}
			case "applicationId", "connectionId", "catalogEntryId":
				if !stringValue || !toolUUID.MatchString(s) {
					return nil, fmt.Errorf("entry %s must be a UUID", key)
				}
			case "toolName":
				if !stringValue || strings.TrimSpace(s) != s || len(s) == 0 || len([]rune(s)) > 240 {
					return nil, fmt.Errorf("entry toolName must contain 1-240 trimmed characters")
				}
			case "riskLevel":
				if !stringValue || !toolRiskLevels[s] {
					return nil, fmt.Errorf("entry riskLevel is unsupported")
				}
			case "conditions":
				if _, ok := v.(map[string]interface{}); !ok {
					return nil, fmt.Errorf("entry conditions must be an object")
				}
			default:
				return nil, fmt.Errorf("unknown entry field %s; catalog/schema/annotation writes are not supported", key)
			}
		}
	}
	return entries, nil
}

var toolUUID = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var toolRiskLevels = map[string]bool{"low": true, "medium": true, "high": true, "critical": true, "read": true, "write": true, "destructive": true}

type toolEntriesValidator struct{}

func (toolEntriesValidator) Description(context.Context) string {
	return "Complete native profile entry array."
}
func (v toolEntriesValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}
func (toolEntriesValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() {
		return
	}
	if _, err := parseToolEntries(req.ConfigValue); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid tool entries", err.Error())
	}
}

func (m toolProfileModel) payload() (map[string]interface{}, error) {
	for key, value := range map[string]types.String{"profile_key": m.ProfileKey, "name": m.Name, "description": m.Description} {
		limit := 160
		if key == "description" {
			limit = 4000
		}
		if !value.IsUnknown() && !value.IsNull() && (len([]rune(value.ValueString())) > limit || (key != "description" && (strings.TrimSpace(value.ValueString()) != value.ValueString() || value.ValueString() == ""))) {
			return nil, fmt.Errorf("%s must contain valid native text of at most %d characters", key, limit)
		}
	}
	if !m.ProfileKey.IsUnknown() && !regexp.MustCompile(`(?i)^[a-z0-9][a-z0-9._:-]*$`).MatchString(m.ProfileKey.ValueString()) {
		return nil, fmt.Errorf("profile_key must be a native safe key")
	}
	metadata, err := decodeApplicationMetadata(m.Metadata)
	if err != nil {
		return nil, err
	}
	profile := client.ToolProfile{ProfileKey: m.ProfileKey.ValueString(), Metadata: metadata}
	if err := profile.AssertDeclarativeOwnership(); err != nil {
		return nil, err
	}
	entries, err := parseToolEntries(m.Entries)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"profileKey": m.ProfileKey.ValueString(), "name": m.Name.ValueString(), "description": nativeStringPointer(m.Description), "status": m.Status.ValueString(), "defaultAction": m.DefaultAction.ValueString(), "metadata": metadata, "entries": entries}, nil
}
func (r *PaperclipToolProfileResource) validate(ctx context.Context, m toolProfileModel) error {
	if _, err := m.payload(); err != nil {
		return err
	}
	if r.client == nil || m.CompanyID.IsUnknown() {
		return nil
	}
	if targetID := m.ReassignToProfileID.ValueString(); targetID != "" {
		target, err := r.client.GetToolProfile(ctx, m.CompanyID.ValueString(), targetID)
		if err != nil {
			return err
		}
		if err := target.AssertDeclarativeOwnership(); err != nil {
			return err
		}
		if target.ID == m.ID.ValueString() || target.Status != "active" {
			return fmt.Errorf("reassign_to_profile_id must be a different active profile")
		}
	}
	entries, _ := parseToolEntries(m.Entries)
	seen := map[string]bool{}
	for _, entry := range entries {
		for field, kind := range map[string]string{"applicationId": "application", "connectionId": "connection", "catalogEntryId": "catalog_entry"} {
			if id, ok := entry[field].(string); ok && !seen[kind+id] {
				if err := r.client.AssertToolTarget(ctx, m.CompanyID.ValueString(), kind, id); err != nil {
					return err
				}
				seen[kind+id] = true
			}
		}
	}
	return nil
}
func profileTransition(m toolProfileModel, p *client.ToolProfile) error {
	if err := p.AssertDeclarativeOwnership(); err != nil {
		return err
	}
	if m.Description.IsNull() && p.Description != nil {
		return fmt.Errorf("native profile description cannot be cleared to null; retain it or use an empty string; no mutation was sent")
	}
	if m.Metadata.IsNull() && p.Metadata != nil {
		return fmt.Errorf("native profile metadata cannot be cleared to null; retain it or use {}; no mutation was sent")
	}
	return nil
}
func (r *PaperclipToolProfileResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}
	var m toolProfileModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.validate(ctx, m); err != nil {
		resp.Diagnostics.AddError("Invalid tool profile", err.Error())
		return
	}
	if !req.State.Raw.IsNull() {
		var prior toolProfileModel
		resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
		if resp.Diagnostics.HasError() {
			return
		}
		p := client.ToolProfile{Description: nativeStringPointer(prior.Description)}
		p.Metadata, _ = decodeApplicationMetadata(prior.Metadata)
		if err := profileTransition(m, &p); err != nil {
			resp.Diagnostics.AddError("Unsupported profile transition", err.Error())
			return
		}
	}
	if r.client != nil && !m.OwnerID.IsNull() && !m.OwnerID.IsUnknown() && !m.CompanyID.IsUnknown() && !m.ProfileKey.IsUnknown() {
		if err := claimAggregate(r.client, "tool_profile", m.CompanyID.ValueString()+"/"+m.ProfileKey.ValueString(), &m.OwnerID); err != nil {
			resp.Diagnostics.AddError("Tool profile ownership conflict", err.Error())
			return
		}
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("owner_id"), m.OwnerID)...)
	}
}
func (m *toolProfileModel) refresh(p *client.ToolProfile) {
	m.ID = types.StringValue(p.ID)
	m.CompanyID = types.StringValue(p.CompanyID)
	m.ProfileKey = types.StringValue(p.ProfileKey)
	m.Name = types.StringValue(p.Name)
	m.Description = types.StringNull()
	if p.Description != nil {
		m.Description = types.StringValue(*p.Description)
	}
	m.Status = types.StringValue(p.Status)
	m.DefaultAction = types.StringValue(p.DefaultAction)
	if m.Metadata.IsNull() && p.Metadata != nil {
		m.Metadata = types.StringUnknown()
	}
	m.Metadata = refreshSemanticJSON(m.Metadata, p.Metadata)
	entries := []interface{}{}
	for _, entry := range p.Entries {
		entries = append(entries, entry)
	}
	if semanticJSONEqualNormalized(m.Entries.ValueString(), marshalJSON(entries), normalizeToolEntries) {
		return
	}
	clean := []map[string]interface{}{}
	for _, entry := range p.Entries {
		item := map[string]interface{}{}
		for _, key := range []string{"selectorType", "effect", "applicationId", "connectionId", "catalogEntryId", "toolName", "riskLevel", "conditions"} {
			if entry[key] != nil {
				item[key] = entry[key]
			}
		}
		clean = append(clean, item)
	}
	m.Entries = types.StringValue(marshalJSON(clean))
}
func marshalJSON(v interface{}) string { b, _ := json.Marshal(v); return string(b) }
func (r *PaperclipToolProfileResource) save(ctx context.Context, m *toolProfileModel, update bool) error {
	if err := r.validate(ctx, *m); err != nil {
		return err
	}
	if err := claimAggregate(r.client, "tool_profile", m.CompanyID.ValueString()+"/"+m.ProfileKey.ValueString(), &m.OwnerID); err != nil {
		return err
	}
	id := ""
	if update {
		id = m.ID.ValueString()
		p, err := r.client.GetToolProfile(ctx, m.CompanyID.ValueString(), id)
		if err != nil {
			return err
		}
		if err := profileTransition(*m, p); err != nil {
			return err
		}
	}
	payload, _ := m.payload()
	p, err := r.client.WriteToolProfile(ctx, m.CompanyID.ValueString(), id, payload)
	if err != nil {
		return err
	}
	if p.ID == "" || p.CompanyID != m.CompanyID.ValueString() || (update && p.ID != id) {
		return fmt.Errorf("profile write returned an invalid identity")
	}
	m.refresh(p)
	return nil
}
func (r *PaperclipToolProfileResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m toolProfileModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.save(ctx, &m, false); err != nil {
		resp.Diagnostics.AddError("Error creating tool profile", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (r *PaperclipToolProfileResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m toolProfileModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.save(ctx, &m, true); err != nil {
		resp.Diagnostics.AddError("Error updating tool profile", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (r *PaperclipToolProfileResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m toolProfileModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p, err := r.client.GetToolProfile(ctx, m.CompanyID.ValueString(), m.ID.ValueString())
	if errors.Is(err, client.ErrNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err == nil {
		err = p.AssertDeclarativeOwnership()
	}
	if err == nil {
		err = claimAggregate(r.client, "tool_profile", m.CompanyID.ValueString()+"/"+p.ProfileKey, &m.OwnerID)
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading tool profile", err.Error())
		return
	}
	m.refresh(p)
	if m.ForceDestroy.IsNull() {
		m.ForceDestroy = types.BoolValue(false)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (r *PaperclipToolProfileResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m toolProfileModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p, err := r.client.GetToolProfile(ctx, m.CompanyID.ValueString(), m.ID.ValueString())
	if errors.Is(err, client.ErrNotFound) {
		return
	}
	if err == nil {
		err = p.AssertDeclarativeOwnership()
	}
	if err != nil {
		resp.Diagnostics.AddError("Error deleting tool profile", err.Error())
		return
	}
	reassign := m.ReassignToProfileID.ValueString()
	if p.Summary.IsCompanyDefault && !m.ForceDestroy.ValueBool() && reassign == "" {
		resp.Diagnostics.AddError("Default profile deletion requires consent", "Set force_destroy=true or reassign_to_profile_id to a different active profile; no mutation was sent")
		return
	}
	if reassign != "" {
		target, e := r.client.GetToolProfile(ctx, m.CompanyID.ValueString(), reassign)
		if e == nil {
			e = target.AssertDeclarativeOwnership()
		}
		if e == nil && (target.ID == p.ID || target.Status != "active") {
			e = fmt.Errorf("reassign_to_profile_id must be a different active profile")
		}
		if e != nil {
			resp.Diagnostics.AddError("Invalid profile reassignment", e.Error())
			return
		}
	}
	if err := r.client.DeleteToolProfile(ctx, p.ID, m.ForceDestroy.ValueBool(), reassign); err != nil {
		resp.Diagnostics.AddError("Error deleting tool profile", err.Error())
	}
}
func (r *PaperclipToolProfileResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(req.ID, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		resp.Diagnostics.AddError("Invalid profile import ID", "Expected company_id/profile_id")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("company_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
}
