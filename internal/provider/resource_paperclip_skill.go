package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"git.guseynov.org/guseynov/paperclip/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func int64UseStateForUnknown() planmodifier.Int64 {
	return int64UseStateForUnknownModifier{}
}

type int64UseStateForUnknownModifier struct{}

func (m int64UseStateForUnknownModifier) Description(_ context.Context) string {
	return "Once set, the value of this attribute in state will not change."
}

func (m int64UseStateForUnknownModifier) MarkdownDescription(_ context.Context) string {
	return "Once set, the value of this attribute in state will not change."
}

func (m int64UseStateForUnknownModifier) PlanModifyInt64(_ context.Context, req planmodifier.Int64Request, resp *planmodifier.Int64Response) {
	if req.State.Raw.IsNull() {
		return
	}
	if !req.PlanValue.IsUnknown() {
		return
	}
	if req.ConfigValue.IsUnknown() {
		return
	}
	resp.PlanValue = req.StateValue
}

var fileInventoryObjectType = types.ObjectType{
	AttrTypes: map[string]attr.Type{
		"path": types.StringType,
		"kind": types.StringType,
	},
}

var (
	_ resource.Resource                 = &PaperclipSkillResource{}
	_ resource.ResourceWithConfigure    = &PaperclipSkillResource{}
	_ resource.ResourceWithImportState  = &PaperclipSkillResource{}
	_ resource.ResourceWithUpgradeState = &PaperclipSkillResource{}
)

func NewPaperclipSkillResource() resource.Resource {
	return &PaperclipSkillResource{}
}

type PaperclipSkillResource struct {
	client *client.Client
}

type PaperclipSkillFileInventoryModel struct {
	Path types.String `tfsdk:"path"`
	Kind types.String `tfsdk:"kind"`
}

type PaperclipSkillResourceModel struct {
	ID               types.String `tfsdk:"id"`
	CompanyID        types.String `tfsdk:"company_id"`
	Key              types.String `tfsdk:"key"`
	Name             types.String `tfsdk:"name"`
	Slug             types.String `tfsdk:"slug"`
	Description      types.String `tfsdk:"description"`
	Content          types.String `tfsdk:"content"`
	SchemaVersion    types.Int64  `tfsdk:"schema_version"`
	Metadata         types.String `tfsdk:"metadata"`
	IconURL          types.String `tfsdk:"icon_url"`
	Color            types.String `tfsdk:"color"`
	Tagline          types.String `tfsdk:"tagline"`
	AuthorName       types.String `tfsdk:"author_name"`
	HomepageURL      types.String `tfsdk:"homepage_url"`
	Categories       types.List   `tfsdk:"categories"`
	SharingScope     types.String `tfsdk:"sharing_scope"`
	Files            types.Map    `tfsdk:"files"`
	FileInventory    types.List   `tfsdk:"file_inventory"`
	CurrentVersionID types.String `tfsdk:"current_version_id"`
	VersionSnapshot  types.String `tfsdk:"version_snapshot"`
	SourceType       types.String `tfsdk:"source_type"`
	SourceLocator    types.String `tfsdk:"source_locator"`
	FolderID         types.String `tfsdk:"folder_id"`
	ForkedFromID     types.String `tfsdk:"forked_from_id"`
	ImportSource     types.String `tfsdk:"import_source"`
	CatalogSkillID   types.String `tfsdk:"catalog_skill_id"`
}

func (r *PaperclipSkillResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_skill"
}

func (r *PaperclipSkillResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Version:     1,
		Description: "Manages a Paperclip skill in a company's catalog with multi-file, versioning, and lifecycle management.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Unique identifier for the skill.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"company_id": schema.StringAttribute{
				Description: "The Paperclip company ID.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"name": schema.StringAttribute{
				Description: "Human-readable skill name.",
				Required:    true,
			},
			"slug": schema.StringAttribute{
				Description: "Unique skill slug identifier.",
				Required:    true,
			},
			"key": schema.StringAttribute{
				Description: "Canonical skill reference key (e.g. 'company/<companyId>/<slug>').",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"description": schema.StringAttribute{
				Description: "Optional skill description.",
				Optional:    true,
			},
			"content": schema.StringAttribute{
				Description: "Markdown instructions and skill content (SKILL.md).",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"schema_version": schema.Int64Attribute{
				Description: "Skill schema version (default: 1).",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.Int64{
					int64UseStateForUnknown(),
				},
			},
			"metadata": schema.StringAttribute{
				Description: "Paperclip-owned skill metadata as a computed JSON object.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					SemanticJSONPlanModifier(),
				},
			},
			"icon_url": schema.StringAttribute{
				Description: "Icon URL for the skill.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"color": schema.StringAttribute{
				Description: "Badge or UI accent color for the skill.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"tagline": schema.StringAttribute{
				Description: "Short summary tagline for catalog display.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"author_name": schema.StringAttribute{
				Description: "Display author name.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"homepage_url": schema.StringAttribute{
				Description: "Documentation or homepage URL.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"categories": schema.ListAttribute{
				Description: "List of skill categorization tags.",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
				PlanModifiers: []planmodifier.List{
					listplanmodifier.UseStateForUnknown(),
				},
			},
			"sharing_scope": schema.StringAttribute{
				Description: "Sharing scope of the skill (e.g. 'company').",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"files": schema.MapAttribute{
				Description: "Multi-file map of file relative paths to file contents.",
				Optional:    true,
				ElementType: types.StringType,
			},
			"file_inventory": schema.ListNestedAttribute{
				Description: "Catalog file inventory of all files associated with this skill.",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"path": schema.StringAttribute{
							Description: "Relative file path.",
							Computed:    true,
						},
						"kind": schema.StringAttribute{
							Description: "Kind of file (e.g. 'skill_md', 'script', 'asset').",
							Computed:    true,
						},
					},
				},
			},
			"current_version_id": schema.StringAttribute{
				Description: "ID of the current active version snapshot.",
				Computed:    true,
			},
			"version_snapshot": schema.StringAttribute{
				Description: "When set or changed, creates an immutable skill version snapshot with this label.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"source_type": schema.StringAttribute{
				Description: "Provenance source type.",
				Computed:    true,
			},
			"source_locator": schema.StringAttribute{
				Description: "Provenance source locator URI or path.",
				Computed:    true,
			},
			"folder_id": schema.StringAttribute{
				Description: "Folder ID containing this skill.",
				Computed:    true,
			},
			"forked_from_id": schema.StringAttribute{
				Description: "Original skill ID if forked.",
				Computed:    true,
			},
			"import_source": schema.StringAttribute{
				Description: "Source locator or local path to import on creation.",
				Optional:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"catalog_skill_id": schema.StringAttribute{
				Description: "Catalog skill ID to install on creation.",
				Optional:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
		},
	}
}

func (r *PaperclipSkillResource) UpgradeState(ctx context.Context) map[int64]resource.StateUpgrader {
	return map[int64]resource.StateUpgrader{
		0: {
			PriorSchema: &schema.Schema{
				Attributes: map[string]schema.Attribute{
					"id":             schema.StringAttribute{Computed: true},
					"company_id":     schema.StringAttribute{Required: true},
					"name":           schema.StringAttribute{Required: true},
					"slug":           schema.StringAttribute{Required: true},
					"description":    schema.StringAttribute{Optional: true},
					"content":        schema.StringAttribute{Required: true},
					"schema_version": schema.Int64Attribute{Optional: true, Computed: true},
					"metadata":       schema.StringAttribute{Computed: true},
				},
			},
			StateUpgrader: func(ctx context.Context, req resource.UpgradeStateRequest, resp *resource.UpgradeStateResponse) {
				var prior struct {
					ID            types.String `tfsdk:"id"`
					CompanyID     types.String `tfsdk:"company_id"`
					Name          types.String `tfsdk:"name"`
					Slug          types.String `tfsdk:"slug"`
					Description   types.String `tfsdk:"description"`
					Content       types.String `tfsdk:"content"`
					SchemaVersion types.Int64  `tfsdk:"schema_version"`
					Metadata      types.String `tfsdk:"metadata"`
				}
				resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
				if resp.Diagnostics.HasError() {
					return
				}
				upgraded := PaperclipSkillResourceModel{
					ID:            prior.ID,
					CompanyID:     prior.CompanyID,
					Key:           types.StringValue(fmt.Sprintf("company/%s/%s", prior.CompanyID.ValueString(), prior.Slug.ValueString())),
					Name:          prior.Name,
					Slug:          prior.Slug,
					Description:   prior.Description,
					Content:       prior.Content,
					SchemaVersion: prior.SchemaVersion,
					Metadata:      prior.Metadata,
					Categories:    types.ListNull(types.StringType),
					Files:         types.MapNull(types.StringType),
					FileInventory: types.ListNull(fileInventoryObjectType),
					SharingScope:  types.StringValue("company"),
				}
				resp.Diagnostics.Append(resp.State.Set(ctx, &upgraded)...)
			},
		},
	}
}

func (r *PaperclipSkillResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *client.Client, got: %T", req.ProviderData),
		)
		return
	}
	r.client = c
}

func populateSkillModel(ctx context.Context, m *PaperclipSkillResourceModel, skill *client.PaperclipSkill, planFiles types.Map) {
	m.ID = types.StringValue(skill.ID)
	if skill.CompanyID != "" {
		m.CompanyID = types.StringValue(skill.CompanyID)
	}
	canonicalKey := skill.Key
	if canonicalKey == "" {
		cid := skill.CompanyID
		if cid == "" && !m.CompanyID.IsNull() && !m.CompanyID.IsUnknown() {
			cid = m.CompanyID.ValueString()
		}
		if cid != "" && skill.Slug != "" {
			canonicalKey = fmt.Sprintf("company/%s/%s", cid, skill.Slug)
		}
	}
	m.Key = stringOrNull(canonicalKey)
	m.Name = types.StringValue(skill.Name)
	m.Slug = types.StringValue(skill.Slug)
	m.Description = stringOrNull(skill.Description)
	if skill.Content != "" || (!m.Content.IsNull() && !m.Content.IsUnknown()) {
		m.Content = types.StringValue(skill.Content)
	} else {
		m.Content = types.StringNull()
	}
	if skill.SchemaVersion > 0 {
		m.SchemaVersion = types.Int64Value(skill.SchemaVersion)
	} else {
		m.SchemaVersion = types.Int64Value(1)
	}
	m.Metadata = refreshSemanticJSON(m.Metadata, skill.Metadata)
	m.IconURL = stringOrNull(skill.IconURL)
	m.Color = stringOrNull(skill.Color)
	m.Tagline = stringOrNull(skill.Tagline)
	m.AuthorName = stringOrNull(skill.AuthorName)
	m.HomepageURL = stringOrNull(skill.HomepageURL)
	m.SharingScope = stringOrNull(skill.SharingScope)
	if len(skill.Categories) > 0 {
		cats, _ := types.ListValueFrom(ctx, types.StringType, skill.Categories)
		m.Categories = cats
	} else {
		m.Categories = types.ListNull(types.StringType)
	}
	m.CurrentVersionID = stringOrNull(skill.CurrentVersionID)
	if skill.CurrentVersion != nil && skill.CurrentVersion.Label != "" {
		m.VersionSnapshot = types.StringValue(skill.CurrentVersion.Label)
	} else if m.VersionSnapshot.IsUnknown() {
		m.VersionSnapshot = types.StringNull()
	}
	m.SourceType = stringOrNull(skill.SourceType)
	m.SourceLocator = stringOrNull(skill.SourceLocator)
	m.FolderID = stringOrNull(skill.FolderID)
	m.ForkedFromID = stringOrNull(skill.ForkedFromID)

	// File inventory
	if len(skill.FileInventory) > 0 {
		inv := make([]PaperclipSkillFileInventoryModel, len(skill.FileInventory))
		for i, entry := range skill.FileInventory {
			inv[i] = PaperclipSkillFileInventoryModel{
				Path: types.StringValue(entry.Path),
				Kind: stringOrNull(entry.Kind),
			}
		}
		invList, _ := types.ListValueFrom(ctx, fileInventoryObjectType, inv)
		m.FileInventory = invList
	} else {
		m.FileInventory = types.ListNull(fileInventoryObjectType)
	}

	if planFiles.IsNull() || planFiles.IsUnknown() {
		m.Files = types.MapNull(types.StringType)
	} else {
		m.Files = planFiles
	}
}

func (r *PaperclipSkillResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan PaperclipSkillResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	companyID := plan.CompanyID.ValueString()
	schemaVer := int64(1)
	if !plan.SchemaVersion.IsNull() && !plan.SchemaVersion.IsUnknown() && plan.SchemaVersion.ValueInt64() > 0 {
		schemaVer = plan.SchemaVersion.ValueInt64()
	}

	var filesMap map[string]string
	if !plan.Files.IsNull() && !plan.Files.IsUnknown() {
		resp.Diagnostics.Append(plan.Files.ElementsAs(ctx, &filesMap, false)...)
	}

	content := plan.Content.ValueString()
	if content == "" && filesMap != nil {
		if c, ok := filesMap["SKILL.md"]; ok {
			content = c
		}
	}

	var categories []string
	if !plan.Categories.IsNull() && !plan.Categories.IsUnknown() {
		resp.Diagnostics.Append(plan.Categories.ElementsAs(ctx, &categories, false)...)
	}

	var created *client.PaperclipSkill
	var err error

	if !plan.ImportSource.IsNull() && !plan.ImportSource.IsUnknown() && plan.ImportSource.ValueString() != "" {
		created, err = r.client.ImportSkill(ctx, companyID, plan.ImportSource.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Error importing Paperclip skill", err.Error())
			return
		}
		// If custom name or slug was specified and differs from imported, rename
		if (plan.Name.ValueString() != "" && plan.Name.ValueString() != created.Name) ||
			(plan.Slug.ValueString() != "" && plan.Slug.ValueString() != created.Slug) {
			renamed, rerr := r.client.RenameSkill(ctx, companyID, created.ID, plan.Name.ValueString(), plan.Slug.ValueString())
			if rerr != nil {
				resp.Diagnostics.AddError("Error renaming imported Paperclip skill", rerr.Error())
				return
			}
			created = renamed
		}
	} else if !plan.CatalogSkillID.IsNull() && !plan.CatalogSkillID.IsUnknown() && plan.CatalogSkillID.ValueString() != "" {
		created, err = r.client.InstallCatalogSkill(ctx, companyID, plan.CatalogSkillID.ValueString(), plan.Slug.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Error installing catalog skill", err.Error())
			return
		}
		if plan.Name.ValueString() != "" && plan.Name.ValueString() != created.Name {
			renamed, rerr := r.client.RenameSkill(ctx, companyID, created.ID, plan.Name.ValueString(), plan.Slug.ValueString())
			if rerr != nil {
				resp.Diagnostics.AddError("Error renaming installed catalog skill", rerr.Error())
				return
			}
			created = renamed
		}
	} else {
		skill := client.PaperclipSkill{
			CompanyID:     companyID,
			Name:          plan.Name.ValueString(),
			Slug:          plan.Slug.ValueString(),
			Description:   plan.Description.ValueString(),
			Content:       content,
			SchemaVersion: schemaVer,
			IconURL:       plan.IconURL.ValueString(),
			Color:         plan.Color.ValueString(),
			Tagline:       plan.Tagline.ValueString(),
			AuthorName:    plan.AuthorName.ValueString(),
			HomepageURL:   plan.HomepageURL.ValueString(),
			Categories:    categories,
			SharingScope:  plan.SharingScope.ValueString(),
		}
		created, err = r.client.CreateSkill(ctx, companyID, skill)
		if err != nil {
			resp.Diagnostics.AddError("Error creating Paperclip skill", err.Error())
			return
		}
	}

	// Update auxiliary files if declared
	if len(filesMap) > 0 {
		for filePath, fileContent := range filesMap {
			if filePath == "SKILL.md" {
				continue
			}
			if _, ferr := r.client.UpdateSkillFile(ctx, companyID, created.ID, filePath, fileContent); ferr != nil {
				resp.Diagnostics.AddError("Error uploading skill auxiliary file", ferr.Error())
				return
			}
		}
	}

	// Create version snapshot if requested
	if !plan.VersionSnapshot.IsNull() && !plan.VersionSnapshot.IsUnknown() && plan.VersionSnapshot.ValueString() != "" {
		v, verr := r.client.CreateSkillVersion(ctx, companyID, created.ID, plan.VersionSnapshot.ValueString())
		if verr != nil {
			resp.Diagnostics.AddError("Error creating skill version snapshot", verr.Error())
			return
		}
		if v != nil {
			created.CurrentVersionID = v.ID
		}
	}

	fresh, err := r.client.GetSkill(ctx, companyID, created.ID)
	if err != nil {
		resp.Diagnostics.AddError("Error fetching created Paperclip skill", err.Error())
		return
	}
	created = fresh

	populateSkillModel(ctx, &plan, created, plan.Files)
	if !plan.Files.IsNull() && !plan.Files.IsUnknown() && len(filesMap) > 0 {
		plan.Files, _ = types.MapValueFrom(ctx, types.StringType, filesMap)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipSkillResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state PaperclipSkillResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	companyID := state.CompanyID.ValueString()
	skillID := state.ID.ValueString()

	skill, err := r.client.GetSkill(ctx, companyID, skillID)
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading Paperclip skill", err.Error())
		return
	}

	populateSkillModel(ctx, &state, skill, state.Files)

	// If files are tracked in state, refresh their contents
	if !state.Files.IsNull() && !state.Files.IsUnknown() {
		var stateFiles map[string]string
		resp.Diagnostics.Append(state.Files.ElementsAs(ctx, &stateFiles, false)...)
		if stateFiles != nil {
			refreshed := make(map[string]string, len(stateFiles))
			for p := range stateFiles {
				detail, ferr := r.client.GetSkillFile(ctx, companyID, skillID, p)
				if ferr != nil {
					if errors.Is(ferr, client.ErrNotFound) {
						// File was removed remotely (drift); omit from refreshed map
						continue
					}
					resp.Diagnostics.AddError(
						"Error reading skill file",
						fmt.Sprintf("Failed to read file %q from skill %s: %s", p, skillID, ferr.Error()),
					)
					return
				}
				if detail != nil {
					refreshed[p] = detail.Content
				}
			}
			if len(refreshed) > 0 {
				refreshedMap, _ := types.MapValueFrom(ctx, types.StringType, refreshed)
				state.Files = refreshedMap
			} else {
				state.Files = types.MapNull(types.StringType)
			}
		}
	} else if len(skill.FileInventory) > 0 {
		// Populate files on import if auxiliary files exist in fileInventory
		auxFiles := make(map[string]string)
		for _, entry := range skill.FileInventory {
			if entry.Path == "SKILL.md" {
				continue
			}
			detail, ferr := r.client.GetSkillFile(ctx, companyID, skillID, entry.Path)
			if ferr == nil && detail != nil {
				auxFiles[entry.Path] = detail.Content
			}
		}
		if len(auxFiles) > 0 {
			state.Files, _ = types.MapValueFrom(ctx, types.StringType, auxFiles)
		} else {
			state.Files = types.MapNull(types.StringType)
		}
	} else {
		state.Files = types.MapNull(types.StringType)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *PaperclipSkillResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan PaperclipSkillResourceModel
	var state PaperclipSkillResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	companyID := state.CompanyID.ValueString()
	skillID := state.ID.ValueString()

	// 1. Rename via dedicated endpoint if name or slug changed
	nameChanged := !plan.Name.IsNull() && !plan.Name.IsUnknown() && plan.Name.ValueString() != "" && plan.Name.ValueString() != state.Name.ValueString()
	slugChanged := !plan.Slug.IsNull() && !plan.Slug.IsUnknown() && plan.Slug.ValueString() != "" && plan.Slug.ValueString() != state.Slug.ValueString()
	if nameChanged || slugChanged {
		targetName := state.Name.ValueString()
		if !plan.Name.IsNull() && !plan.Name.IsUnknown() && plan.Name.ValueString() != "" {
			targetName = plan.Name.ValueString()
		}
		targetSlug := state.Slug.ValueString()
		if !plan.Slug.IsNull() && !plan.Slug.IsUnknown() && plan.Slug.ValueString() != "" {
			targetSlug = plan.Slug.ValueString()
		}
		if _, err := r.client.RenameSkill(ctx, companyID, skillID, targetName, targetSlug); err != nil {
			resp.Diagnostics.AddError("Error renaming Paperclip skill", err.Error())
			return
		}
	}

	// 2. Metadata & SKILL.md update
	schemaVer := int64(1)
	if !plan.SchemaVersion.IsNull() && !plan.SchemaVersion.IsUnknown() && plan.SchemaVersion.ValueInt64() > 0 {
		schemaVer = plan.SchemaVersion.ValueInt64()
	}

	var metaMap map[string]interface{}
	if !plan.Metadata.IsNull() && !plan.Metadata.IsUnknown() && plan.Metadata.ValueString() != "" {
		if err := json.Unmarshal([]byte(plan.Metadata.ValueString()), &metaMap); err != nil {
			resp.Diagnostics.AddError("Invalid Metadata JSON", fmt.Sprintf("Failed to parse metadata JSON: %s", err))
			return
		}
	}

	var planFiles map[string]string
	if !plan.Files.IsNull() && !plan.Files.IsUnknown() {
		resp.Diagnostics.Append(plan.Files.ElementsAs(ctx, &planFiles, false)...)
	}

	content := plan.Content.ValueString()
	if content == "" && planFiles != nil {
		if c, ok := planFiles["SKILL.md"]; ok {
			content = c
		}
	}

	var categories []string
	if !plan.Categories.IsNull() && !plan.Categories.IsUnknown() {
		resp.Diagnostics.Append(plan.Categories.ElementsAs(ctx, &categories, false)...)
	}

	skillPayload := client.PaperclipSkill{
		CompanyID:     companyID,
		Name:          plan.Name.ValueString(),
		Slug:          plan.Slug.ValueString(),
		Description:   plan.Description.ValueString(),
		Content:       content,
		SchemaVersion: schemaVer,
		Metadata:      metaMap,
		IconURL:       plan.IconURL.ValueString(),
		Color:         plan.Color.ValueString(),
		Tagline:       plan.Tagline.ValueString(),
		AuthorName:    plan.AuthorName.ValueString(),
		HomepageURL:   plan.HomepageURL.ValueString(),
		Categories:    categories,
		SharingScope:  plan.SharingScope.ValueString(),
	}

	updated, err := r.client.UpdateSkill(ctx, companyID, skillID, skillPayload)
	if err != nil {
		resp.Diagnostics.AddError("Error updating Paperclip skill", err.Error())
		return
	}

	// 3. Multi-file reconciliation (add, patch, delete)
	var stateFiles map[string]string
	if !state.Files.IsNull() && !state.Files.IsUnknown() {
		resp.Diagnostics.Append(state.Files.ElementsAs(ctx, &stateFiles, false)...)
	}

	// Delete obsolete files (excluding SKILL.md)
	for oldPath := range stateFiles {
		if oldPath == "SKILL.md" {
			continue
		}
		if _, stillPresent := planFiles[oldPath]; !stillPresent {
			if ferr := r.client.DeleteSkillFile(ctx, companyID, skillID, oldPath); ferr != nil {
				resp.Diagnostics.AddError("Error deleting skill auxiliary file", ferr.Error())
				return
			}
		}
	}

	// Update or add auxiliary files
	for newPath, newContent := range planFiles {
		if newPath == "SKILL.md" {
			continue
		}
		oldContent, hadOld := stateFiles[newPath]
		if !hadOld || oldContent != newContent {
			if _, ferr := r.client.UpdateSkillFile(ctx, companyID, skillID, newPath, newContent); ferr != nil {
				resp.Diagnostics.AddError("Error updating skill auxiliary file", ferr.Error())
				return
			}
		}
	}

	// 4. Version snapshot
	if !plan.VersionSnapshot.IsNull() && !plan.VersionSnapshot.IsUnknown() && plan.VersionSnapshot.ValueString() != "" &&
		plan.VersionSnapshot.ValueString() != state.VersionSnapshot.ValueString() {
		v, verr := r.client.CreateSkillVersion(ctx, companyID, skillID, plan.VersionSnapshot.ValueString())
		if verr != nil {
			resp.Diagnostics.AddError("Error creating skill version snapshot", verr.Error())
			return
		}
		if v != nil {
			updated.CurrentVersionID = v.ID
		}
	}

	fresh, err := r.client.GetSkill(ctx, companyID, skillID)
	if err != nil {
		resp.Diagnostics.AddError("Error fetching updated Paperclip skill", err.Error())
		return
	}
	updated = fresh

	populateSkillModel(ctx, &plan, updated, plan.Files)
	if !plan.Files.IsNull() && !plan.Files.IsUnknown() && len(planFiles) > 0 {
		plan.Files, _ = types.MapValueFrom(ctx, types.StringType, planFiles)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipSkillResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state PaperclipSkillResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.client.DeleteSkill(ctx, state.CompanyID.ValueString(), state.ID.ValueString()); err != nil {
		if !errors.Is(err, client.ErrNotFound) {
			resp.Diagnostics.AddError(
				"Error deleting Paperclip skill",
				fmt.Sprintf("Failed to delete skill %s: %s (ensure any dependent agent skill assignments are removed before retiring skills)", state.ID.ValueString(), err.Error()),
			)
			return
		}
	}
}

func (r *PaperclipSkillResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id := req.ID
	if strings.Contains(id, "/") {
		parts := strings.SplitN(id, "/", 2)
		if parts[0] != "" && parts[1] != "" {
			resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("company_id"), parts[0])...)
			resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
			return
		}
	}
	resp.Diagnostics.AddError(
		"Invalid Skill Import ID",
		fmt.Sprintf("Expected <company_id>/<skill_id>, got %q", id),
	)
}
