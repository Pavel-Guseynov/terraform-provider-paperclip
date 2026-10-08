package client

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"sort"
	"strings"
)

// PaperclipToolSelection carries the registry's names and native approval policy.
// A nil EnabledTools selects the catalog; an empty slice selects no tools.
type PaperclipToolSelection struct {
	EnabledTools      []string          `json:"enabled_tools"`
	ApprovalMode      string            `json:"approval_mode"`
	ToolApprovalModes map[string]string `json:"tool_approval_modes,omitempty"`
}

func SelectAppPermissions(catalog []map[string]interface{}, policyJSON string, agents []string) (PaperclipAppFinish, error) {
	finish := PaperclipAppFinish{
		EnabledCatalogEntryIDs: []string{}, AskFirstCatalogEntryIDs: []string{}, ReviewedCatalogEntryIDs: []string{},
	}
	if len(agents) > 0 {
		finish.Access = map[string]interface{}{"agentIds": append([]string{}, agents...)}
	}
	var selection PaperclipToolSelection
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(policyJSON), &fields); err != nil {
		return finish, fmt.Errorf("invalid tool selection: %w", err)
	}
	for key := range fields {
		if key != "enabled_tools" && key != "approval_mode" && key != "tool_approval_modes" {
			return finish, fmt.Errorf("unknown tool selection field %q", key)
		}
	}
	if err := json.Unmarshal([]byte(policyJSON), &selection); err != nil {
		return finish, fmt.Errorf("invalid tool selection: %w", err)
	}
	if selection.ApprovalMode != "writes" {
		return finish, fmt.Errorf("tool selection requires the writes approval policy")
	}
	if len(catalog) == 0 {
		return finish, fmt.Errorf("connection catalog is empty; discover its tools before granting access")
	}
	names := map[string]bool{}
	ids := map[string]bool{}
	wanted := map[string]bool{}
	for _, name := range selection.EnabledTools {
		if name == "" || wanted[name] {
			return finish, fmt.Errorf("enabled tool names must be nonempty and unique")
		}
		wanted[name] = true
	}
	for name, mode := range selection.ToolApprovalModes {
		if name == "" || (mode != "auto" && mode != "approve" && mode != "prompt" && mode != "writes") {
			return finish, fmt.Errorf("invalid tool approval override for %q", name)
		}
	}
	for _, entry := range catalog {
		id, idOK := entry["id"].(string)
		name, nameOK := entry["toolName"].(string)
		if !idOK || !nameOK || id == "" || name == "" || ids[id] || names[name] {
			return finish, fmt.Errorf("connection catalog must have unique tool names and IDs")
		}
		ids[id], names[name] = true, true
		if entry["status"] == "quarantined" {
			finish.ReviewedCatalogEntryIDs = append(finish.ReviewedCatalogEntryIDs, id)
		}
		if selection.EnabledTools != nil && !wanted[name] {
			continue
		}
		if entry["status"] == "disabled" {
			return finish, fmt.Errorf("selected tool %q is disabled", name)
		}
		finish.EnabledCatalogEntryIDs = append(finish.EnabledCatalogEntryIDs, id)
		readOnly := entry["isReadOnly"] == true && entry["isWrite"] != true && entry["isDestructive"] != true
		mode := selection.ToolApprovalModes[name]
		askFirst := !readOnly
		if mode != "" {
			askFirst = mode == "prompt" || mode == "writes"
		}
		if askFirst {
			finish.AskFirstCatalogEntryIDs = append(finish.AskFirstCatalogEntryIDs, id)
		}
	}
	for name := range wanted {
		if !names[name] {
			return finish, fmt.Errorf("selected tool %q is absent from the connection catalog", name)
		}
	}
	for name := range selection.ToolApprovalModes {
		if !names[name] {
			return finish, fmt.Errorf("approval override tool %q is absent from the connection catalog", name)
		}
	}
	sort.Strings(finish.EnabledCatalogEntryIDs)
	sort.Strings(finish.AskFirstCatalogEntryIDs)
	sort.Strings(finish.ReviewedCatalogEntryIDs)
	return finish, nil
}

func (c *Client) RefreshConnectionCatalog(ctx context.Context, connectionID string) error {
	return c.paperclipRequest(ctx, http.MethodPost, fmt.Sprintf("/api/tool-connections/%s/catalog/refresh", url.PathEscape(connectionID)), map[string]interface{}{}, nil)
}

type PaperclipAppPermissionState struct {
	EnabledIDs  []string
	AskFirstIDs []string
	// StrayPolicyIDs are enabled approval policies on the connection's catalog
	// entries other than its one scoped ask-first policy.
	StrayPolicyIDs []string
	AgentIDs       []string
	AccessType     string
	// Finished reports that the connection has a native App profile.
	Finished bool
}

// askFirstPolicyPriority orders ask-first policies where Paperclip's App finish
// orders its own, ahead of default-priority (100) policies.
const askFirstPolicyPriority = 50

// askFirstPolicy finds the connection's one managed ask-first policy, with the
// catalog entries it gates, and lists every other enabled approval policy on the
// connection's entries as stray. Paperclip loads every enabled policy for each
// tool decision, so one policy per connection replaces one per entry. Paperclip
// evaluates policies before profile access, so a policy selecting beyond the
// granted scope offers its tools to agents never granted the app: the managed
// policy carries no config and selects only this connection's entries plus the
// agent scope, or only the entries when access is company-wide.
func askFirstPolicy(policies []PaperclipToolPolicy, catalog []map[string]interface{}, companyWide bool, agentIDs []string) (string, []string, []string) {
	entries := map[string]bool{}
	for _, entry := range catalog {
		if id, ok := entry["id"].(string); ok {
			entries[id] = true
		}
	}
	managedID, managedIDs, stray := "", []string{}, []string{}
	for _, policy := range policies {
		if policy.PolicyType != "require_approval" || policy.Enabled == nil || !*policy.Enabled {
			continue
		}
		single, _ := policy.Selectors["catalogEntryId"].(string)
		selected := extractStringSlice(policy.Selectors["catalogEntryIds"])
		touches, own := entries[single], len(selected) > 0 && single == ""
		seen := map[string]bool{}
		for _, id := range selected {
			touches = touches || entries[id]
			own = own && entries[id] && !seen[id]
			seen[id] = true
		}
		if !touches {
			continue
		}
		scoped := len(policy.Selectors) == 1
		if !companyWide {
			scoped = len(agentIDs) > 0 && len(policy.Selectors) == 2 && stringSliceEqual(extractStringSlice(policy.Selectors["agentIds"]), agentIDs)
		}
		if own && scoped && len(policy.Config) == 0 && managedID == "" {
			managedID, managedIDs = policy.ID, selected
		} else {
			stray = append(stray, policy.ID)
		}
	}
	sort.Strings(managedIDs)
	sort.Strings(stray)
	return managedID, managedIDs, stray
}

// appPermissionProfiles lists the connection's native App profiles.
func (c *Client) appPermissionProfiles(ctx context.Context, companyID, connectionID string) ([]map[string]interface{}, error) {
	var raw interface{}
	if err := c.paperclipRequest(ctx, http.MethodGet, fmt.Sprintf("/api/companies/%s/tools/profiles", url.PathEscape(companyID)), nil, &raw); err != nil {
		return nil, err
	}
	profiles := []map[string]interface{}{}
	for _, item := range extractItems(raw, "profiles") {
		if item["profileKey"] == "app:"+connectionID {
			profiles = append(profiles, item)
		}
	}
	return profiles, nil
}

// readAppProfile projects the native App profile's tools and agent scope.
func readAppProfile(state *PaperclipAppPermissionState, profile map[string]interface{}, companyID, connectionID string) error {
	if profile["defaultAction"] != "deny" {
		return fmt.Errorf("native application profile must deny unselected tools")
	}
	if profile["status"] == "active" {
		for _, entry := range extractItems(profile["entries"], "entries") {
			if entry["selectorType"] == "connection" {
				return ErrNotFound
			}
			id, ok := entry["catalogEntryId"].(string)
			if !ok || id == "" || entry["selectorType"] != "catalog_entry" || entry["effect"] != "include" || entry["connectionId"] != connectionID {
				return fmt.Errorf("native application profile has a non-catalog permission entry")
			}
			state.EnabledIDs = append(state.EnabledIDs, id)
		}
	}
	for _, binding := range extractItems(profile["bindings"], "bindings") {
		id, ok := binding["targetId"].(string)
		if !ok || id == "" {
			return fmt.Errorf("native application permission binding has no target")
		}
		switch binding["targetType"] {
		case "company":
			if id != companyID {
				return fmt.Errorf("native application permission has an invalid company scope")
			}
			state.AccessType = "all_agents"
		case "agent":
			state.AgentIDs = append(state.AgentIDs, id)
		case "gateway":
			// Paperclip binds its per-run MCP gateways to the native app
			// profile. Those runtime bindings are not desired agent access.
			continue
		default:
			return fmt.Errorf("native application permission has an unsupported binding scope")
		}
	}
	return nil
}

// Read the native state owned by the finish API and the ask-first policies on
// the connection's catalog entries.
func (c *Client) GetAppPermissionState(ctx context.Context, companyID, connectionID string) (*PaperclipAppPermissionState, error) {
	profiles, err := c.appPermissionProfiles(ctx, companyID, connectionID)
	if err != nil {
		return nil, err
	}
	if len(profiles) > 1 {
		return nil, fmt.Errorf("duplicate native application permission profile")
	}
	state := &PaperclipAppPermissionState{EnabledIDs: []string{}, AskFirstIDs: []string{}, AgentIDs: []string{}, AccessType: "agents", Finished: len(profiles) == 1}
	if state.Finished {
		if err := readAppProfile(state, profiles[0], companyID, connectionID); err != nil {
			return nil, err
		}
	}
	catalog, err := c.GetConnectionCatalog(ctx, connectionID)
	if err != nil {
		return nil, err
	}
	policies, err := c.ListToolPolicies(ctx, companyID)
	if err != nil {
		return nil, err
	}
	_, state.AskFirstIDs, state.StrayPolicyIDs = askFirstPolicy(policies, catalog, state.AccessType == "all_agents", state.AgentIDs)
	sort.Strings(state.EnabledIDs)
	sort.Strings(state.AgentIDs)
	return state, nil
}

func stringSliceEqual(left, right []string) bool {
	a, b := append([]string{}, left...), append([]string{}, right...)
	sort.Strings(a)
	sort.Strings(b)
	return reflect.DeepEqual(a, b)
}

func extractStringSlice(v interface{}) []string {
	switch slice := v.(type) {
	case []string:
		res := make([]string, len(slice))
		copy(res, slice)
		return res
	case []interface{}:
		var res []string
		for _, item := range slice {
			if s, ok := item.(string); ok && s != "" {
				res = append(res, s)
			}
		}
		return res
	default:
		return nil
	}
}

type PaperclipCompany struct {
	ID                           string `json:"id,omitempty"`
	Name                         string `json:"name"`
	Description                  string `json:"description,omitempty"`
	RequireTaskStartConfirmation bool   `json:"requireTaskStartConfirmation"`
}

type PaperclipProject struct {
	ID                       string                 `json:"id,omitempty"`
	CompanyID                string                 `json:"companyId,omitempty"`
	Name                     string                 `json:"name"`
	Description              string                 `json:"description,omitempty"`
	LeadAgentID              string                 `json:"leadAgentId,omitempty"`
	ExecutionWorkspacePolicy map[string]interface{} `json:"executionWorkspacePolicy,omitempty"`
}

// PaperclipWorkspace carries the workspace fields Paperclip's project
// workspace API accepts and echoes; the declared branch travels as both refs.
type PaperclipWorkspace struct {
	ID         string `json:"id,omitempty"`
	ProjectID  string `json:"projectId,omitempty"`
	CompanyID  string `json:"companyId,omitempty"`
	Name       string `json:"name"`
	RepoURL    string `json:"repoUrl"`
	RepoRef    string `json:"repoRef,omitempty"`
	DefaultRef string `json:"defaultRef,omitempty"`
	IsPrimary  bool   `json:"isPrimary"`
	SourceType string `json:"sourceType,omitempty"`
}

type PaperclipSkillFileEntry struct {
	Path string `json:"path"`
	Kind string `json:"kind,omitempty"`
}

type PaperclipSkillFileDetail struct {
	SkillID  string `json:"skillId,omitempty"`
	Path     string `json:"path"`
	Kind     string `json:"kind,omitempty"`
	Content  string `json:"content"`
	Language string `json:"language,omitempty"`
	Markdown bool   `json:"markdown,omitempty"`
	Editable bool   `json:"editable,omitempty"`
}

type PaperclipSkillVersion struct {
	ID             string `json:"id"`
	CompanyID      string `json:"companyId"`
	CompanySkillID string `json:"companySkillId"`
	RevisionNumber int64  `json:"revisionNumber,omitempty"`
	Label          string `json:"label,omitempty"`
	CreatedAt      string `json:"createdAt,omitempty"`
}

type PaperclipSkill struct {
	ID               string                    `json:"id,omitempty"`
	CompanyID        string                    `json:"companyId,omitempty"`
	Name             string                    `json:"name"`
	Slug             string                    `json:"slug"`
	Description      string                    `json:"description,omitempty"`
	Content          string                    `json:"content,omitempty"`
	Markdown         string                    `json:"markdown,omitempty"`
	IconURL          string                    `json:"iconUrl,omitempty"`
	Color            string                    `json:"color,omitempty"`
	Tagline          string                    `json:"tagline,omitempty"`
	AuthorName       string                    `json:"authorName,omitempty"`
	HomepageURL      string                    `json:"homepageUrl,omitempty"`
	Categories       []string                  `json:"categories,omitempty"`
	SharingScope     string                    `json:"sharingScope,omitempty"`
	FolderID         string                    `json:"folderId,omitempty"`
	ForkedFromID     string                    `json:"forkedFromSkillId,omitempty"`
	SourceType       string                    `json:"sourceType,omitempty"`
	SourceLocator    string                    `json:"sourceLocator,omitempty"`
	CurrentVersionID string                    `json:"currentVersionId,omitempty"`
	FileInventory    []PaperclipSkillFileEntry `json:"fileInventory,omitempty"`
	SchemaVersion    int64                     `json:"schemaVersion,omitempty"`
	Metadata         map[string]interface{}    `json:"metadata,omitempty"`
}

type PaperclipDesiredSkillEntry struct {
	Key       string  `json:"key"`
	VersionID *string `json:"versionId,omitempty"`
}

type PaperclipAgentSkillSnapshot struct {
	AdapterType         string                       `json:"adapterType"`
	Supported           bool                         `json:"supported"`
	Mode                string                       `json:"mode"`
	DesiredSkills       []string                     `json:"desiredSkills"`
	DesiredSkillEntries []PaperclipDesiredSkillEntry `json:"desiredSkillEntries"`
	Entries             []map[string]interface{}     `json:"entries"`
	Warnings            []string                     `json:"warnings"`
}

type PaperclipSkillPolicy struct {
	SchemaVersion int64                    `json:"schemaVersion"`
	Revision      int64                    `json:"revision"`
	DefaultEffect string                   `json:"defaultEffect"`
	Rules         []map[string]interface{} `json:"rules"`
	Materialized  bool                     `json:"materialized"`
}

type PaperclipSecret struct {
	ID          string `json:"id,omitempty"`
	CompanyID   string `json:"companyId,omitempty"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Value       string `json:"value,omitempty"`
}

type PaperclipAgent struct {
	ID            string                 `json:"id,omitempty"`
	CompanyID     string                 `json:"companyId,omitempty"`
	Name          string                 `json:"name"`
	Role          string                 `json:"role,omitempty"`
	Title         string                 `json:"title,omitempty"`
	AdapterType   string                 `json:"adapterType,omitempty"`
	Status        string                 `json:"status,omitempty"`
	RuntimeConfig map[string]interface{} `json:"runtimeConfig,omitempty"`
	AdapterConfig        map[string]interface{} `json:"adapterConfig,omitempty"`
	ReplaceAdapterConfig bool                   `json:"replaceAdapterConfig,omitempty"`
	Metadata             map[string]interface{} `json:"metadata,omitempty"`
	// Permissions is board-reported on a single-agent read and never sent back
	// on an agent write; the permissions route owns it.
	Permissions *PaperclipAgentPermissions `json:"permissions,omitempty"`
}

// PaperclipAgentPermissions is the agent's own permission flags and the body of
// the permissions route, which replaces every flag at once, so a write carries
// the unchanged flags back. Writing CanAssignTasks false also withdraws the
// explicit tasks:assign grant Paperclip attaches to every agent it creates.
type PaperclipAgentPermissions struct {
	CanCreateAgents bool `json:"canCreateAgents"`
	CanCreateSkills bool `json:"canCreateSkills"`
	CanAssignTasks  bool `json:"canAssignTasks"`
}

type PaperclipAgentKey struct {
	ID        string `json:"id,omitempty"`
	AgentID   string `json:"agentId,omitempty"`
	Name      string `json:"name"`
	Token     string `json:"token,omitempty"`
	CreatedAt string `json:"createdAt,omitempty"`
}

type PaperclipBoardKey struct {
	ID    string `json:"id,omitempty"`
	Name  string `json:"name"`
	Token string `json:"token,omitempty"`
}

type paperclipCLIAuthChallenge struct {
	ID            string `json:"id"`
	Token         string `json:"token"`
	BoardAPIToken string `json:"boardApiToken"`
}

type paperclipCLIAuthApproval struct {
	Approved bool   `json:"approved"`
	Status   string `json:"status"`
	KeyID    string `json:"keyId"`
}

type paperclipCLIAuthIdentity struct {
	Source string `json:"source"`
	KeyID  string `json:"keyId"`
}

type PaperclipToolApp struct {
	ID             string                 `json:"id,omitempty"`
	CompanyID      string                 `json:"companyId,omitempty"`
	Name           string                 `json:"name"`
	Type           string                 `json:"type"`
	ApplicationKey string                 `json:"applicationKey,omitempty"`
	Description    string                 `json:"description,omitempty"`
	Status         string                 `json:"status,omitempty"`
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
}

// ToolAppArchivedStatus is the retirement state Paperclip accepts for an
// application; connection rows keep referencing their application, so a hard
// delete is refused with a conflict for as long as any connection ever existed.
const ToolAppArchivedStatus = "archived"

type PaperclipToolConnection struct {
	ID                   string                   `json:"id,omitempty"`
	CompanyID            string                   `json:"companyId,omitempty"`
	AppID                string                   `json:"applicationId,omitempty"`
	Name                 string                   `json:"name"`
	Transport            string                   `json:"transport,omitempty"`
	AuthKind             string                   `json:"authKind,omitempty"`
	Config               map[string]interface{}   `json:"config,omitempty"`
	CredentialRefs       []map[string]interface{} `json:"credentialRefs,omitempty"`
	CredentialSecretRefs []map[string]interface{} `json:"credentialSecretRefs"`
	CredentialPolicy     string                   `json:"credentialPolicy,omitempty"`
	Status               string                   `json:"status,omitempty"`
}

// ToolConnectionArchivedStatus is the state Paperclip's connection deletion
// leaves behind; finish refuses an archived connection.
const ToolConnectionArchivedStatus = "archived"

// AwaitingAuthorization reports whether an OAuth connection has no usable user
// authorization: it stays a draft until its first callback succeeds, and a
// failed refresh returns it to draft. Its catalog cannot be read until then.
func (conn PaperclipToolConnection) AwaitingAuthorization() bool {
	return conn.AuthKind == "oauth" && conn.Status == "draft"
}

type paperclipToolConnectionUpdate struct {
	Name                 string                   `json:"name"`
	Transport            string                   `json:"transport"`
	AuthKind             string                   `json:"authKind"`
	Config               map[string]interface{}   `json:"config"`
	CredentialRefs       []map[string]interface{} `json:"credentialRefs"`
	CredentialSecretRefs []map[string]interface{} `json:"credentialSecretRefs"`
}

// Execution grants refer to the same protected secrets as the connection header
// projection. This is derived from credentialRefs, never configured separately.
func ConnectionCredentialSecretRefs(refs []map[string]interface{}) ([]map[string]interface{}, error) {
	result := []map[string]interface{}{}
	paths := map[string]bool{}
	for _, ref := range refs {
		name, nameOK := ref["name"].(string)
		id, idOK := ref["secretId"].(string)
		configPath := name
		if !strings.HasPrefix(name, "credentials.") {
			configPath = "credentials." + name
		}
		if !nameOK || !idOK || name == "" || id == "" || paths[configPath] {
			return nil, fmt.Errorf("connection credentials require unique native paths and protected secret IDs")
		}
		paths[configPath] = true
		version := ref["version"]
		if version == nil {
			version = "latest"
		}
		result = append(result, map[string]interface{}{"configPath": configPath, "secretId": id, "versionSelector": version})
	}
	return result, nil
}

type PaperclipConnectionGrant struct {
	ID                   string                   `json:"id"`
	Kind                 string                   `json:"kind"`
	Status               string                   `json:"status"`
	IsDefault            bool                     `json:"isDefault"`
	CredentialSecretRefs []map[string]interface{} `json:"credentialSecretRefs"`
	SupersededIDs        []string                 `json:"-"`
}

func (c *Client) GetDefaultConnectionGrant(ctx context.Context, connectionID string) (*PaperclipConnectionGrant, error) {
	var response struct {
		Grants []PaperclipConnectionGrant `json:"grants"`
	}
	if err := c.paperclipRequest(ctx, http.MethodGet, "/api/tool-connections/"+url.PathEscape(connectionID)+"/grants", nil, &response); err != nil {
		return nil, err
	}
	var found *PaperclipConnectionGrant
	var superseded []string
	for _, grant := range response.Grants {
		if (grant.Kind == "workspace" || grant.Kind == "organization") && grant.IsDefault {
			if found != nil {
				return nil, fmt.Errorf("connection has multiple default organization grants")
			}
			copy := grant
			found = &copy
		}
		if (grant.Kind == "workspace" || grant.Kind == "organization") && !grant.IsDefault && grant.Status == "active" {
			superseded = append(superseded, grant.ID)
		}
	}
	if found != nil {
		found.SupersededIDs = superseded
	}
	return found, nil
}

func ConnectionGrantMatches(grant *PaperclipConnectionGrant, refs []map[string]interface{}) bool {
	return connectionGrantCredentialsMatch(grant, refs) && len(grant.SupersededIDs) == 0
}

func connectionGrantCredentialsMatch(grant *PaperclipConnectionGrant, refs []map[string]interface{}) bool {
	return grant != nil && grant.Status == "active" && grant.IsDefault && reflect.DeepEqual(grant.CredentialSecretRefs, refs)
}

func (c *Client) EnsureConnectionCredentialGrant(ctx context.Context, connectionID string, refs []map[string]interface{}) error {
	prior, err := c.GetDefaultConnectionGrant(ctx, connectionID)
	if err != nil {
		return err
	}
	endpoint := "/api/tool-connections/" + url.PathEscape(connectionID) + "/grants"
	actual := prior
	if !connectionGrantCredentialsMatch(prior, refs) {
		var rawCreated map[string]interface{}
		if err := c.paperclipRequest(ctx, http.MethodPost, endpoint+"/installations", map[string]interface{}{"credentialSecretRefs": refs, "isDefault": true}, &rawCreated); err != nil {
			return err
		}
		actual, err = c.GetDefaultConnectionGrant(ctx, connectionID)
		if err != nil {
			return err
		}
		createdID, _ := rawCreated["id"].(string)
		if grantObj, ok := rawCreated["grant"].(map[string]interface{}); ok && createdID == "" {
			createdID, _ = grantObj["id"].(string)
		}
		if instObj, ok := rawCreated["installation"].(map[string]interface{}); ok && createdID == "" {
			createdID, _ = instObj["id"].(string)
		}
		if createdID == "" || actual == nil || actual.ID != createdID || !connectionGrantCredentialsMatch(actual, refs) {
			rawActual, _ := json.Marshal(actual)
			createdBytes, _ := json.Marshal(rawCreated)
			expectedBytes, _ := json.Marshal(refs)
			sanitizedActual := sanitizeResponseBody(rawActual)
			sanitizedCreated := sanitizeResponseBody(createdBytes)
			sanitizedExpected := sanitizeResponseBody(expectedBytes)
			return fmt.Errorf("native default credential grant differs after installation: created=%s actual=%s expected_refs=%s", sanitizedCreated, sanitizedActual, sanitizedExpected)
		}
	}
	for _, id := range actual.SupersededIDs {
		if err := c.paperclipRequest(ctx, http.MethodDelete, endpoint+"/"+url.PathEscape(id), nil, nil); err != nil {
			return err
		}
	}
	verified, err := c.GetDefaultConnectionGrant(ctx, connectionID)
	if err != nil {
		return err
	}
	if !ConnectionGrantMatches(verified, refs) {
		return fmt.Errorf("native credential grant did not converge after reconciliation")
	}
	return nil
}

type PaperclipToolPolicy struct {
	ID          string                 `json:"id,omitempty"`
	CompanyID   string                 `json:"companyId,omitempty"`
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	PolicyType  string                 `json:"policyType,omitempty"`
	Priority    int                    `json:"priority,omitempty"`
	Enabled     *bool                  `json:"enabled,omitempty"`
	Selectors   map[string]interface{} `json:"selectors,omitempty"`
	Config      map[string]interface{} `json:"config,omitempty"`
	Rules       map[string]interface{} `json:"rules,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

// PaperclipStdioTemplate carries the approved stdio template fields Paperclip
// stores; templates are immutable and identified by their company-unique
// template key, and environment values belong to connections, never templates.
type PaperclipStdioTemplate struct {
	ID          string                   `json:"id,omitempty"`
	CompanyID   string                   `json:"companyId,omitempty"`
	TemplateID  string                   `json:"templateId"`
	Name        string                   `json:"name"`
	Description string                   `json:"description,omitempty"`
	Command     string                   `json:"command"`
	Args        []string                 `json:"args,omitempty"`
	EnvKeys     []string                 `json:"envKeys,omitempty"`
	Tools       []map[string]interface{} `json:"tools,omitempty"`
	Status      string                   `json:"status,omitempty"`
}

// StdioTemplateDisabledStatus is the only retirement state Paperclip offers
// for an approved stdio template; disabled rows keep their template key.
const StdioTemplateDisabledStatus = "disabled"

type PaperclipAppFinish struct {
	EnabledCatalogEntryIDs  []string    `json:"enabledCatalogEntryIds"`
	AskFirstCatalogEntryIDs []string    `json:"askFirstCatalogEntryIds"`
	ReviewedCatalogEntryIDs []string    `json:"reviewedCatalogEntryIds"`
	Access                  interface{} `json:"access,omitempty"`
}

func (c *Client) paperclipRequestWithToken(ctx context.Context, method, path string, body interface{}, result interface{}, token string) error {
	return c.paperclipRequestWithErrorBody(ctx, method, path, body, result, token, true)
}

var sensitiveKeyPattern = regexp.MustCompile(`(?i)^(value|token|secret|password|boardapitoken|board_token|apikey|api_key|credential|credentials|authorization|privatekey|private_key)$`)
var sensitiveRegexPattern = regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9\-_\.]+|((?:token|secret|password|key|value)\s*[:=]\s*["']?)[^"'\s,;]+(["']?)`)

func isSensitiveKey(key string) bool {
	if sensitiveKeyPattern.MatchString(key) {
		return true
	}
	lower := strings.ToLower(key)
	return strings.Contains(lower, "token") ||
		strings.Contains(lower, "secret") ||
		strings.Contains(lower, "password") ||
		strings.Contains(lower, "apikey") ||
		strings.Contains(lower, "api_key") ||
		strings.Contains(lower, "credential") ||
		strings.Contains(lower, "private_key")
}

func extractRequestSecrets(v interface{}) []string {
	var secrets []string
	var walk func(val interface{}, parentKey string)
	walk = func(val interface{}, parentKey string) {
		switch node := val.(type) {
		case map[string]interface{}:
			for k, child := range node {
				isSensitive := isSensitiveKey(k) || strings.EqualFold(parentKey, "env") || strings.EqualFold(parentKey, "envvars")
				if str, ok := child.(string); ok && isSensitive && strings.TrimSpace(str) != "" {
					secrets = append(secrets, strings.TrimSpace(str))
				}
				walk(child, k)
			}
		case []interface{}:
			for _, item := range node {
				walk(item, parentKey)
			}
		}
	}
	walk(v, "")
	return secrets
}

func sanitizeJSONValue(v interface{}) interface{} {
	switch val := v.(type) {
	case map[string]interface{}:
		sanitized := make(map[string]interface{}, len(val))
		for k, child := range val {
			if isSensitiveKey(k) {
				sanitized[k] = "[REDACTED]"
			} else {
				sanitized[k] = sanitizeJSONValue(child)
			}
		}
		return sanitized
	case []interface{}:
		sanitized := make([]interface{}, len(val))
		for i, child := range val {
			sanitized[i] = sanitizeJSONValue(child)
		}
		return sanitized
	default:
		return v
	}
}

func sanitizeResponseBody(body []byte, extraSecrets ...string) string {
	if len(body) == 0 {
		return ""
	}
	var raw interface{}
	var result string
	if err := json.Unmarshal(body, &raw); err == nil {
		sanitized := sanitizeJSONValue(raw)
		bytesData, _ := json.Marshal(sanitized)
		result = string(bytesData)
	} else {
		result = string(body)
	}

	result = sensitiveRegexPattern.ReplaceAllStringFunc(result, func(m string) string {
		lower := strings.ToLower(m)
		if strings.HasPrefix(lower, "bearer ") {
			return "Bearer [REDACTED]"
		}
		if idx := strings.Index(m, "="); idx != -1 {
			return m[:idx+1] + "[REDACTED]"
		}
		parts := strings.SplitN(m, ":", 2)
		return parts[0] + ": [REDACTED]"
	})

	for _, secret := range extraSecrets {
		trimmed := strings.TrimSpace(secret)
		if len(trimmed) > 0 && strings.Contains(result, trimmed) {
			result = strings.ReplaceAll(result, trimmed, "[REDACTED]")
		}
	}

	if len(result) > 2048 {
		result = result[:2048] + "..."
	}
	return strings.TrimSpace(result)
}

func extractCorrelationID(resp *http.Response, body []byte) string {
	if resp != nil {
		for _, header := range []string{
			"X-Correlation-Id",
			"X-Correlation-ID",
			"X-Request-Id",
			"X-Request-ID",
			"X-Paperclip-Run-Id",
			"X-Client-Request-Id",
			"Traceparent",
		} {
			if val := strings.TrimSpace(resp.Header.Get(header)); val != "" {
				return val
			}
		}
	}
	if len(body) > 0 {
		var m map[string]interface{}
		if err := json.Unmarshal(body, &m); err == nil {
			for _, k := range []string{"correlationId", "correlation_id", "requestId", "request_id"} {
				if val, ok := m[k].(string); ok && strings.TrimSpace(val) != "" {
					return strings.TrimSpace(val)
				}
			}
		}
	}
	return "none"
}

func isUnknownRoute(statusCode int, body string) bool {
	if statusCode != http.StatusNotFound {
		return false
	}
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	return strings.Contains(lower, "api route not found") ||
		strings.Contains(lower, "route not found") ||
		strings.Contains(lower, "cannot get ") ||
		strings.Contains(lower, "cannot post ") ||
		strings.Contains(lower, "cannot patch ") ||
		strings.Contains(lower, "cannot delete ") ||
		strings.Contains(lower, "cannot put ") ||
		strings.Contains(lower, "<!doctype html") ||
		strings.Contains(lower, "<html")
}

func (c *Client) paperclipRequestWithErrorBody(ctx context.Context, method, path string, body interface{}, result interface{}, token string, includeErrorBody bool) error {
	reqURL := fmt.Sprintf("%s%s", c.Endpoint, path)
	var bodyReader io.Reader
	var sensitiveSecrets []string
	if c.Token != "" {
		sensitiveSecrets = append(sensitiveSecrets, c.Token)
	}
	if token != "" && token != c.Token {
		sensitiveSecrets = append(sensitiveSecrets, token)
	}

	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal paperclip request: %w", err)
		}
		var rawBody interface{}
		if err := json.Unmarshal(data, &rawBody); err == nil {
			sensitiveSecrets = append(sensitiveSecrets, extractRequestSecrets(rawBody)...)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, reqURL, bodyReader)
	if err != nil {
		return fmt.Errorf("create paperclip request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute paperclip request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read paperclip response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		unknownRoute := isUnknownRoute(resp.StatusCode, string(respBody))
		correlationID := extractCorrelationID(resp, respBody)
		sanitizedBody := sanitizeResponseBody(respBody, sensitiveSecrets...)
		if !includeErrorBody {
			sanitizedBody = "[REDACTED]"
		}
		return &APIError{
			Method:         method,
			Path:           path,
			StatusCode:     resp.StatusCode,
			CorrelationID:  correlationID,
			Body:           sanitizedBody,
			IsUnknownRoute: unknownRoute,
		}
	}

	if result != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("unmarshal paperclip response: %w", err)
		}
	}
	return nil
}

func (c *Client) paperclipRequest(ctx context.Context, method, path string, body interface{}, result interface{}) error {
	return c.paperclipRequestWithToken(ctx, method, path, body, result, c.Token)
}

func (c *Client) paperclipSecretRequest(ctx context.Context, method, path string, body interface{}, result interface{}) error {
	return c.paperclipRequestWithErrorBody(ctx, method, path, body, result, c.Token, false)
}

// CreateBoardKey uses Paperclip's official local CLI authorization flow. Both
// challenge calls intentionally omit any existing bearer so a stale key cannot
// shadow the local-trusted board actor. The returned durable board token is
// verified against /api/cli-auth/me before it leaves the client.
func (c *Client) CreateBoardKey(ctx context.Context, name string) (*PaperclipBoardKey, error) {
	var challenge paperclipCLIAuthChallenge
	if err := c.paperclipRequestWithToken(ctx, http.MethodPost, "/api/cli-auth/challenges", map[string]interface{}{
		"command":         "harness paperclip-sync",
		"clientName":      name,
		"requestedAccess": "board",
	}, &challenge, ""); err != nil {
		return nil, err
	}
	if challenge.ID == "" || challenge.Token == "" || challenge.BoardAPIToken == "" {
		return nil, errors.New("paperclip CLI auth challenge omitted required credentials")
	}

	var approval paperclipCLIAuthApproval
	approvalPath := fmt.Sprintf("/api/cli-auth/challenges/%s/approve", url.PathEscape(challenge.ID))
	if err := c.paperclipRequestWithToken(ctx, http.MethodPost, approvalPath, map[string]string{
		"token": challenge.Token,
	}, &approval, ""); err != nil {
		return nil, err
	}
	if !approval.Approved || approval.Status != "approved" || approval.KeyID == "" {
		return nil, errors.New("paperclip CLI auth challenge was not approved")
	}

	var identity paperclipCLIAuthIdentity
	if err := c.paperclipRequestWithToken(ctx, http.MethodGet, "/api/cli-auth/me", nil, &identity, challenge.BoardAPIToken); err != nil {
		return nil, err
	}
	if identity.Source != "board_key" || identity.KeyID != approval.KeyID {
		return nil, errors.New("paperclip board key identity verification failed")
	}
	return &PaperclipBoardKey{
		ID:    approval.KeyID,
		Name:  name,
		Token: challenge.BoardAPIToken,
	}, nil
}

func extractItems(raw interface{}, key string) []map[string]interface{} {
	var result []map[string]interface{}
	switch v := raw.(type) {
	case []interface{}:
		for _, item := range v {
			if m, ok := item.(map[string]interface{}); ok {
				result = append(result, m)
			}
		}
	case map[string]interface{}:
		if list, ok := v[key].([]interface{}); ok {
			for _, item := range list {
				if m, ok := item.(map[string]interface{}); ok {
					result = append(result, m)
				}
			}
		} else if items, ok := v["items"].([]interface{}); ok {
			for _, item := range items {
				if m, ok := item.(map[string]interface{}); ok {
					result = append(result, m)
				}
			}
		} else if d, ok := v["data"].([]interface{}); ok {
			for _, item := range d {
				if m, ok := item.(map[string]interface{}); ok {
					result = append(result, m)
				}
			}
		}
	}
	return result
}

// Company CRUD
func (c *Client) CreateCompany(ctx context.Context, name, description string, requireTaskStartConfirmation bool) (*PaperclipCompany, error) {
	payload := map[string]interface{}{
		"name":                         name,
		"requireTaskStartConfirmation": requireTaskStartConfirmation,
	}
	if description != "" {
		payload["description"] = description
	}
	var resp PaperclipCompany
	if err := c.paperclipRequest(ctx, http.MethodPost, "/api/companies", payload, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) GetCompany(ctx context.Context, companyID string) (*PaperclipCompany, error) {
	if companyID == "" {
		return nil, ErrNotFound
	}
	var direct PaperclipCompany
	if err := c.paperclipRequest(ctx, http.MethodGet, fmt.Sprintf("/api/companies/%s", url.PathEscape(companyID)), nil, &direct); err != nil {
		return nil, err
	}
	return &direct, nil
}

func (c *Client) FindCompanyByName(ctx context.Context, name string) (*PaperclipCompany, error) {
	if name == "" {
		return nil, ErrNotFound
	}
	companies, err := c.ListCompanies(ctx)
	if err != nil {
		return nil, err
	}
	var matches []PaperclipCompany
	for _, comp := range companies {
		if strings.EqualFold(comp.Name, name) {
			matches = append(matches, comp)
		}
	}
	if len(matches) == 0 {
		return nil, ErrNotFound
	}
	if len(matches) > 1 {
		var ids []string
		for _, m := range matches {
			ids = append(ids, m.ID)
		}
		return nil, fmt.Errorf("multiple companies found with name %q: candidate IDs: %s", name, strings.Join(ids, ", "))
	}
	return &matches[0], nil
}

func (c *Client) UpdateCompany(ctx context.Context, id, name, description string, requireTaskStartConfirmation bool) (*PaperclipCompany, error) {
	payload := map[string]interface{}{
		"name":                         name,
		"description":                  description,
		"requireTaskStartConfirmation": requireTaskStartConfirmation,
	}
	var resp PaperclipCompany
	if err := c.paperclipRequest(ctx, http.MethodPatch, fmt.Sprintf("/api/companies/%s", url.PathEscape(id)), payload, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) ArchiveCompany(ctx context.Context, id string) error {
	return c.paperclipRequest(ctx, http.MethodPost, fmt.Sprintf("/api/companies/%s/archive", url.PathEscape(id)), map[string]interface{}{}, nil)
}

func (c *Client) ListCompanies(ctx context.Context) ([]PaperclipCompany, error) {
	var raw interface{}
	if err := c.paperclipRequest(ctx, http.MethodGet, "/api/companies", nil, &raw); err != nil {
		return nil, err
	}
	items := extractItems(raw, "companies")
	var result []PaperclipCompany
	for _, item := range items {
		var comp PaperclipCompany
		bytesData, _ := json.Marshal(item)
		if err := json.Unmarshal(bytesData, &comp); err == nil {
			result = append(result, comp)
		}
	}
	return result, nil
}

// Project CRUD
func (c *Client) CreateProject(ctx context.Context, companyID, name, description string, executionWorkspacePolicy map[string]interface{}) (*PaperclipProject, error) {
	payload := map[string]interface{}{"name": name}
	if description != "" {
		payload["description"] = description
	}
	if executionWorkspacePolicy != nil {
		payload["executionWorkspacePolicy"] = executionWorkspacePolicy
	}
	var resp PaperclipProject
	if err := c.paperclipRequest(ctx, http.MethodPost, fmt.Sprintf("/api/companies/%s/projects", url.PathEscape(companyID)), payload, &resp); err != nil {
		return nil, err
	}
	if resp.CompanyID == "" {
		resp.CompanyID = companyID
	}
	return &resp, nil
}

func (c *Client) GetProject(ctx context.Context, projectID string) (*PaperclipProject, error) {
	var resp PaperclipProject
	if err := c.paperclipRequest(ctx, http.MethodGet, fmt.Sprintf("/api/projects/%s", url.PathEscape(projectID)), nil, &resp); err != nil {
		return nil, err
	}
	if resp.ID != projectID {
		return nil, fmt.Errorf("Paperclip returned a different project identity")
	}
	return &resp, nil
}

func (c *Client) UpdateProject(ctx context.Context, projectID, name, description string, executionWorkspacePolicy map[string]interface{}) (*PaperclipProject, error) {
	payload := map[string]interface{}{
		"name":                     name,
		"description":              description,
		"executionWorkspacePolicy": executionWorkspacePolicy,
	}
	var resp PaperclipProject
	if err := c.paperclipRequest(ctx, http.MethodPatch, fmt.Sprintf("/api/projects/%s", url.PathEscape(projectID)), payload, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) DeleteProject(ctx context.Context, projectID string) error {
	return c.paperclipRequest(ctx, http.MethodDelete, fmt.Sprintf("/api/projects/%s", url.PathEscape(projectID)), nil, nil)
}

// SetProjectLead owns only the native lead field; an empty agent clears it.
func (c *Client) SetProjectLead(ctx context.Context, projectID, agentID string) (*PaperclipProject, error) {
	var lead interface{}
	if agentID != "" {
		lead = agentID
	}
	if err := c.paperclipRequest(ctx, http.MethodPatch, fmt.Sprintf("/api/projects/%s", url.PathEscape(projectID)), map[string]interface{}{"leadAgentId": lead}, nil); err != nil {
		return nil, err
	}
	project, err := c.GetProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if project.LeadAgentID != agentID {
		return nil, fmt.Errorf("Paperclip project lead readback does not match the requested assignment")
	}
	return project, nil
}

func (c *Client) ListProjects(ctx context.Context, companyID string) ([]PaperclipProject, error) {
	var raw interface{}
	if err := c.paperclipRequest(ctx, http.MethodGet, fmt.Sprintf("/api/companies/%s/projects", url.PathEscape(companyID)), nil, &raw); err != nil {
		return nil, err
	}
	items := extractItems(raw, "projects")
	var result []PaperclipProject
	for _, item := range items {
		var proj PaperclipProject
		bytesData, _ := json.Marshal(item)
		if err := json.Unmarshal(bytesData, &proj); err == nil {
			if proj.CompanyID == "" {
				proj.CompanyID = companyID
			}
			result = append(result, proj)
		}
	}
	return result, nil
}

// Workspace CRUD
func (c *Client) CreateWorkspace(ctx context.Context, projectID string, ws PaperclipWorkspace) (*PaperclipWorkspace, error) {
	var resp PaperclipWorkspace
	if err := c.paperclipRequest(ctx, http.MethodPost, fmt.Sprintf("/api/projects/%s/workspaces", url.PathEscape(projectID)), ws, &resp); err != nil {
		return nil, err
	}
	if resp.ProjectID == "" {
		resp.ProjectID = projectID
	}
	return &resp, nil
}

// GetWorkspaceInProject resolves a workspace through Paperclip's project-scoped
// workspace API.
func (c *Client) GetWorkspaceInProject(ctx context.Context, projectID, workspaceID string) (*PaperclipWorkspace, error) {
	if projectID == "" || workspaceID == "" {
		return nil, ErrNotFound
	}
	workspaces, err := c.ListWorkspaces(ctx, projectID)
	if err != nil {
		return nil, err
	}
	for _, ws := range workspaces {
		if ws.ID == workspaceID {
			return &ws, nil
		}
	}
	return nil, ErrNotFound
}

func (c *Client) UpdateWorkspace(ctx context.Context, projectID, workspaceID string, ws PaperclipWorkspace) (*PaperclipWorkspace, error) {
	var resp PaperclipWorkspace
	path := fmt.Sprintf(
		"/api/projects/%s/workspaces/%s",
		url.PathEscape(projectID),
		url.PathEscape(workspaceID),
	)
	if err := c.paperclipRequest(ctx, http.MethodPatch, path, ws, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) DeleteWorkspace(ctx context.Context, projectID, workspaceID string) error {
	path := fmt.Sprintf(
		"/api/projects/%s/workspaces/%s",
		url.PathEscape(projectID),
		url.PathEscape(workspaceID),
	)
	return c.paperclipRequest(ctx, http.MethodDelete, path, nil, nil)
}

func (c *Client) ListWorkspaces(ctx context.Context, projectID string) ([]PaperclipWorkspace, error) {
	var raw interface{}
	if err := c.paperclipRequest(ctx, http.MethodGet, fmt.Sprintf("/api/projects/%s/workspaces", url.PathEscape(projectID)), nil, &raw); err != nil {
		return nil, err
	}
	items := extractItems(raw, "workspaces")
	var result []PaperclipWorkspace
	for _, item := range items {
		var ws PaperclipWorkspace
		bytesData, _ := json.Marshal(item)
		if err := json.Unmarshal(bytesData, &ws); err == nil {
			if ws.ProjectID == "" {
				ws.ProjectID = projectID
			}
			result = append(result, ws)
		}
	}
	return result, nil
}

// Skill CRUD
func (c *Client) CreateSkill(ctx context.Context, companyID string, skill PaperclipSkill) (*PaperclipSkill, error) {
	payload := map[string]interface{}{
		"name":        skill.Name,
		"slug":        skill.Slug,
		"description": skill.Description,
		"markdown":    skill.Content,
		"content":     skill.Content,
	}
	if skill.IconURL != "" {
		payload["iconUrl"] = skill.IconURL
	}
	if skill.Color != "" {
		payload["color"] = skill.Color
	}
	if skill.Tagline != "" {
		payload["tagline"] = skill.Tagline
	}
	if skill.AuthorName != "" {
		payload["authorName"] = skill.AuthorName
	}
	if skill.HomepageURL != "" {
		payload["homepageUrl"] = skill.HomepageURL
	}
	if skill.Categories != nil {
		payload["categories"] = skill.Categories
	}
	if skill.SharingScope != "" {
		payload["sharingScope"] = skill.SharingScope
	}
	if skill.FolderID != "" {
		payload["folderId"] = skill.FolderID
	}
	if skill.ForkedFromID != "" {
		payload["forkedFromSkillId"] = skill.ForkedFromID
	}
	if skill.Metadata != nil {
		payload["metadata"] = skill.Metadata
	}
	var resp PaperclipSkill
	if err := c.paperclipRequest(ctx, http.MethodPost, fmt.Sprintf("/api/companies/%s/skills", url.PathEscape(companyID)), payload, &resp); err != nil {
		return nil, err
	}
	if resp.Content == "" && resp.Markdown != "" {
		resp.Content = resp.Markdown
	}
	if resp.CompanyID == "" {
		resp.CompanyID = companyID
	}
	return &resp, nil
}

func (c *Client) GetSkill(ctx context.Context, companyID, skillID string) (*PaperclipSkill, error) {
	if companyID == "" || skillID == "" {
		return nil, ErrNotFound
	}
	var resp PaperclipSkill
	if err := c.paperclipRequest(ctx, http.MethodGet, fmt.Sprintf("/api/companies/%s/skills/%s", url.PathEscape(companyID), url.PathEscape(skillID)), nil, &resp); err != nil {
		return nil, err
	}
	if resp.Content == "" && resp.Markdown != "" {
		resp.Content = resp.Markdown
	}
	if resp.CompanyID == "" {
		resp.CompanyID = companyID
	}
	return &resp, nil
}

func (c *Client) RenameSkill(ctx context.Context, companyID, skillID, name, slug string) (*PaperclipSkill, error) {
	payload := map[string]interface{}{
		"name": name,
	}
	if slug != "" {
		payload["slug"] = slug
	}
	var resp struct {
		Skill PaperclipSkill `json:"skill"`
	}
	err := c.paperclipRequest(ctx, http.MethodPost, fmt.Sprintf("/api/companies/%s/skills/%s/rename", url.PathEscape(companyID), url.PathEscape(skillID)), payload, &resp)
	if err != nil {
		return nil, err
	}
	if resp.Skill.ID != "" {
		if resp.Skill.Content == "" && resp.Skill.Markdown != "" {
			resp.Skill.Content = resp.Skill.Markdown
		}
		if resp.Skill.CompanyID == "" {
			resp.Skill.CompanyID = companyID
		}
		return &resp.Skill, nil
	}
	return c.GetSkill(ctx, companyID, skillID)
}

func (c *Client) PatchSkillMetadata(ctx context.Context, companyID, skillID string, metadata map[string]interface{}) (*PaperclipSkill, error) {
	var resp PaperclipSkill
	if err := c.paperclipRequest(ctx, http.MethodPatch, fmt.Sprintf("/api/companies/%s/skills/%s", url.PathEscape(companyID), url.PathEscape(skillID)), metadata, &resp); err != nil {
		return nil, err
	}
	if resp.Content == "" && resp.Markdown != "" {
		resp.Content = resp.Markdown
	}
	if resp.CompanyID == "" {
		resp.CompanyID = companyID
	}
	return &resp, nil
}

func (c *Client) UpdateSkillFile(ctx context.Context, companyID, skillID, filePath, content string) (*PaperclipSkillFileDetail, error) {
	payload := map[string]interface{}{
		"path":    filePath,
		"content": content,
	}
	var resp PaperclipSkillFileDetail
	if err := c.paperclipRequest(ctx, http.MethodPatch, fmt.Sprintf("/api/companies/%s/skills/%s/files", url.PathEscape(companyID), url.PathEscape(skillID)), payload, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) DeleteSkillFile(ctx context.Context, companyID, skillID, filePath string) error {
	payload := map[string]interface{}{
		"path":   filePath,
		"target": "file",
	}
	return c.paperclipRequest(ctx, http.MethodDelete, fmt.Sprintf("/api/companies/%s/skills/%s/files", url.PathEscape(companyID), url.PathEscape(skillID)), payload, nil)
}

func (c *Client) GetSkillFile(ctx context.Context, companyID, skillID, filePath string) (*PaperclipSkillFileDetail, error) {
	var resp PaperclipSkillFileDetail
	endpoint := fmt.Sprintf("/api/companies/%s/skills/%s/files?path=%s", url.PathEscape(companyID), url.PathEscape(skillID), url.QueryEscape(filePath))
	if err := c.paperclipRequest(ctx, http.MethodGet, endpoint, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) CreateSkillVersion(ctx context.Context, companyID, skillID, label string) (*PaperclipSkillVersion, error) {
	payload := map[string]interface{}{}
	if label != "" {
		payload["label"] = label
	}
	var resp PaperclipSkillVersion
	if err := c.paperclipRequest(ctx, http.MethodPost, fmt.Sprintf("/api/companies/%s/skills/%s/versions", url.PathEscape(companyID), url.PathEscape(skillID)), payload, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) ListSkillVersions(ctx context.Context, companyID, skillID string) ([]PaperclipSkillVersion, error) {
	var raw []PaperclipSkillVersion
	if err := c.paperclipRequest(ctx, http.MethodGet, fmt.Sprintf("/api/companies/%s/skills/%s/versions", url.PathEscape(companyID), url.PathEscape(skillID)), nil, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func (c *Client) ImportSkill(ctx context.Context, companyID, source string) (*PaperclipSkill, error) {
	payload := map[string]interface{}{
		"source": source,
	}
	var resp struct {
		Imported []PaperclipSkill `json:"imported"`
		Warnings []string         `json:"warnings"`
	}
	if err := c.paperclipRequest(ctx, http.MethodPost, fmt.Sprintf("/api/companies/%s/skills/import", url.PathEscape(companyID)), payload, &resp); err != nil {
		return nil, err
	}
	if len(resp.Imported) == 0 {
		return nil, fmt.Errorf("skill import produced no skills")
	}
	skill := resp.Imported[0]
	if skill.Content == "" && skill.Markdown != "" {
		skill.Content = skill.Markdown
	}
	if skill.CompanyID == "" {
		skill.CompanyID = companyID
	}
	return &skill, nil
}

func (c *Client) InstallCatalogSkill(ctx context.Context, companyID, catalogSkillID, slug string) (*PaperclipSkill, error) {
	payload := map[string]interface{}{
		"catalogSkillId": catalogSkillID,
	}
	if slug != "" {
		payload["slug"] = slug
	}
	var resp struct {
		Action   string         `json:"action"`
		Skill    PaperclipSkill `json:"skill"`
		Warnings []string       `json:"warnings"`
	}
	if err := c.paperclipRequest(ctx, http.MethodPost, fmt.Sprintf("/api/companies/%s/skills/install-catalog", url.PathEscape(companyID)), payload, &resp); err != nil {
		return nil, err
	}
	skill := resp.Skill
	if skill.Content == "" && skill.Markdown != "" {
		skill.Content = skill.Markdown
	}
	if skill.CompanyID == "" {
		skill.CompanyID = companyID
	}
	return &skill, nil
}

func (c *Client) UpdateSkill(ctx context.Context, companyID, skillID string, skill PaperclipSkill) (*PaperclipSkill, error) {
	skillPath := fmt.Sprintf("/api/companies/%s/skills/%s", url.PathEscape(companyID), url.PathEscape(skillID))
	metadata := map[string]interface{}{
		"description":  skill.Description,
		"metadata":     skill.Metadata,
		"iconUrl":      skill.IconURL,
		"color":        skill.Color,
		"tagline":      skill.Tagline,
		"authorName":   skill.AuthorName,
		"homepageUrl":  skill.HomepageURL,
		"categories":   skill.Categories,
		"sharingScope": skill.SharingScope,
	}
	if skill.Name != "" {
		metadata["name"] = skill.Name
	}
	if skill.Slug != "" {
		metadata["slug"] = skill.Slug
	}
	if err := c.paperclipRequest(ctx, http.MethodPatch, skillPath, metadata, nil); err != nil {
		return nil, err
	}

	if _, err := c.UpdateSkillFile(ctx, companyID, skillID, "SKILL.md", skill.Content); err != nil {
		return nil, err
	}
	return c.GetSkill(ctx, companyID, skillID)
}

func (c *Client) DeleteSkill(ctx context.Context, companyID, skillID string) error {
	return c.paperclipRequest(ctx, http.MethodDelete, fmt.Sprintf("/api/companies/%s/skills/%s", url.PathEscape(companyID), url.PathEscape(skillID)), nil, nil)
}

func (c *Client) ListSkills(ctx context.Context, companyID string) ([]PaperclipSkill, error) {
	var raw interface{}
	if err := c.paperclipRequest(ctx, http.MethodGet, fmt.Sprintf("/api/companies/%s/skills", url.PathEscape(companyID)), nil, &raw); err != nil {
		return nil, err
	}
	items := extractItems(raw, "skills")
	var result []PaperclipSkill
	for _, item := range items {
		var s PaperclipSkill
		bytesData, _ := json.Marshal(item)
		if err := json.Unmarshal(bytesData, &s); err == nil {
			if s.Content == "" && s.Markdown != "" {
				s.Content = s.Markdown
			}
			if s.CompanyID == "" {
				s.CompanyID = companyID
			}
			result = append(result, s)
		}
	}
	return result, nil
}

// Agent Skill Assignments
func (c *Client) GetAgentSkills(ctx context.Context, agentID string) (*PaperclipAgentSkillSnapshot, error) {
	if agentID == "" {
		return nil, ErrNotFound
	}
	var resp PaperclipAgentSkillSnapshot
	if err := c.paperclipRequest(ctx, http.MethodGet, fmt.Sprintf("/api/agents/%s/skills", url.PathEscape(agentID)), nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) SyncAgentSkills(ctx context.Context, agentID, mode string, desiredSkills []PaperclipDesiredSkillEntry) (*PaperclipAgentSkillSnapshot, error) {
	if agentID == "" {
		return nil, ErrNotFound
	}
	if desiredSkills == nil {
		desiredSkills = []PaperclipDesiredSkillEntry{}
	}
	payload := map[string]interface{}{
		"mode":          mode,
		"desiredSkills": desiredSkills,
	}
	var resp PaperclipAgentSkillSnapshot
	if err := c.paperclipRequest(ctx, http.MethodPost, fmt.Sprintf("/api/agents/%s/skills/sync", url.PathEscape(agentID)), payload, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// Company Skill Policy
func (c *Client) GetSkillPolicy(ctx context.Context, companyID string) (*PaperclipSkillPolicy, error) {
	if companyID == "" {
		return nil, ErrNotFound
	}
	var resp PaperclipSkillPolicy
	if err := c.paperclipRequest(ctx, http.MethodGet, fmt.Sprintf("/api/companies/%s/skill-policy", url.PathEscape(companyID)), nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) ReplaceSkillPolicy(ctx context.Context, companyID string, expectedRevision int64, defaultEffect string, rules []map[string]interface{}) (*PaperclipSkillPolicy, error) {
	if companyID == "" {
		return nil, ErrNotFound
	}
	if rules == nil {
		rules = []map[string]interface{}{}
	}
	payload := map[string]interface{}{
		"schemaVersion":    1,
		"expectedRevision": expectedRevision,
		"defaultEffect":    defaultEffect,
		"rules":            rules,
	}
	var resp PaperclipSkillPolicy
	if err := c.paperclipRequest(ctx, http.MethodPut, fmt.Sprintf("/api/companies/%s/skill-policy", url.PathEscape(companyID)), payload, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) ResetSkillPolicy(ctx context.Context, companyID string) (*PaperclipSkillPolicy, error) {
	if companyID == "" {
		return nil, ErrNotFound
	}
	var resp PaperclipSkillPolicy
	if err := c.paperclipRequest(ctx, http.MethodDelete, fmt.Sprintf("/api/companies/%s/skill-policy", url.PathEscape(companyID)), nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// Secret CRUD
func (c *Client) CreateSecret(ctx context.Context, companyID, name, description, value string) (*PaperclipSecret, error) {
	payload := map[string]interface{}{
		"name":        name,
		"description": description,
		"value":       value,
	}
	var resp PaperclipSecret
	if err := c.paperclipSecretRequest(ctx, http.MethodPost, fmt.Sprintf("/api/companies/%s/secrets", url.PathEscape(companyID)), payload, &resp); err != nil {
		return nil, err
	}
	if resp.CompanyID == "" {
		resp.CompanyID = companyID
	}
	resp.Value = value
	return &resp, nil
}

func (c *Client) GetSecret(ctx context.Context, companyID, secretID string) (*PaperclipSecret, error) {
	if companyID == "" || secretID == "" {
		return nil, ErrNotFound
	}
	secrets, err := c.ListSecrets(ctx, companyID)
	if err != nil {
		return nil, err
	}
	for _, s := range secrets {
		if s.ID == secretID {
			return &s, nil
		}
	}
	return nil, ErrNotFound
}

func (c *Client) UpdateSecret(ctx context.Context, secretID, name, description string) (*PaperclipSecret, error) {
	payload := map[string]interface{}{
		"name":        name,
		"description": description,
	}
	var resp PaperclipSecret
	if err := c.paperclipSecretRequest(ctx, http.MethodPatch, fmt.Sprintf("/api/secrets/%s", url.PathEscape(secretID)), payload, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) RotateSecret(ctx context.Context, secretID, value string) (*PaperclipSecret, error) {
	payload := map[string]interface{}{"value": value}
	var resp PaperclipSecret
	if err := c.paperclipSecretRequest(ctx, http.MethodPost, fmt.Sprintf("/api/secrets/%s/rotate", url.PathEscape(secretID)), payload, &resp); err != nil {
		return nil, err
	}
	resp.Value = value
	return &resp, nil
}

func (c *Client) DeleteSecret(ctx context.Context, secretID string) error {
	return c.paperclipRequest(ctx, http.MethodDelete, fmt.Sprintf("/api/secrets/%s", url.PathEscape(secretID)), nil, nil)
}

func (c *Client) ListSecrets(ctx context.Context, companyID string) ([]PaperclipSecret, error) {
	var raw interface{}
	if err := c.paperclipRequest(ctx, http.MethodGet, fmt.Sprintf("/api/companies/%s/secrets", url.PathEscape(companyID)), nil, &raw); err != nil {
		return nil, err
	}
	items := extractItems(raw, "secrets")
	var result []PaperclipSecret
	for _, item := range items {
		var s PaperclipSecret
		bytesData, _ := json.Marshal(item)
		if err := json.Unmarshal(bytesData, &s); err == nil {
			if s.CompanyID == "" {
				s.CompanyID = companyID
			}
			result = append(result, s)
		}
	}
	return result, nil
}

// Agent CRUD
func (c *Client) CreateAgent(ctx context.Context, companyID string, agent PaperclipAgent) (*PaperclipAgent, error) {
	var resp PaperclipAgent
	if err := c.paperclipRequest(ctx, http.MethodPost, fmt.Sprintf("/api/companies/%s/agents", url.PathEscape(companyID)), agent, &resp); err != nil {
		return nil, err
	}
	if resp.CompanyID == "" {
		resp.CompanyID = companyID
	}
	return &resp, nil
}

func (c *Client) GetAgent(ctx context.Context, agentID string) (*PaperclipAgent, error) {
	if agentID == "" {
		return nil, ErrNotFound
	}
	var resp PaperclipAgent
	if err := c.paperclipRequest(ctx, http.MethodGet, fmt.Sprintf("/api/agents/%s", url.PathEscape(agentID)), nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) UpdateAgent(ctx context.Context, agentID string, agent PaperclipAgent) (*PaperclipAgent, error) {
	payload := map[string]interface{}{
		"name":        agent.Name,
		"role":        agent.Role,
		"title":       agent.Title,
		"adapterType": agent.AdapterType,
	}
	if agent.RuntimeConfig != nil {
		payload["runtimeConfig"] = agent.RuntimeConfig
	} else {
		payload["runtimeConfig"] = map[string]interface{}{}
	}
	if agent.AdapterConfig != nil {
		payload["adapterConfig"] = agent.AdapterConfig
	} else if agent.ReplaceAdapterConfig {
		payload["adapterConfig"] = map[string]interface{}{}
	}
	if agent.ReplaceAdapterConfig {
		payload["replaceAdapterConfig"] = true
	}
	if agent.Metadata != nil {
		payload["metadata"] = agent.Metadata
	} else {
		payload["metadata"] = map[string]interface{}{}
	}
	var resp PaperclipAgent
	if err := c.paperclipRequest(ctx, http.MethodPatch, fmt.Sprintf("/api/agents/%s", url.PathEscape(agentID)), payload, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) DeleteAgent(ctx context.Context, agentID string) error {
	return c.paperclipRequest(ctx, http.MethodDelete, fmt.Sprintf("/api/agents/%s", url.PathEscape(agentID)), nil, nil)
}

// UpdateAgentPermissions replaces the agent's board permission flags. Paperclip
// grants tasks:assign to every agent it creates, so the declared value is
// reconciled after creation rather than sent with it.
func (c *Client) UpdateAgentPermissions(ctx context.Context, agentID string, permissions PaperclipAgentPermissions) error {
	return c.paperclipRequest(ctx, http.MethodPatch, fmt.Sprintf("/api/agents/%s/permissions", url.PathEscape(agentID)), permissions, nil)
}

func (c *Client) ListAgents(ctx context.Context, companyID string) ([]PaperclipAgent, error) {
	var raw interface{}
	if err := c.paperclipRequest(ctx, http.MethodGet, fmt.Sprintf("/api/companies/%s/agents", url.PathEscape(companyID)), nil, &raw); err != nil {
		return nil, err
	}
	items := extractItems(raw, "agents")
	var result []PaperclipAgent
	for _, item := range items {
		var a PaperclipAgent
		bytesData, _ := json.Marshal(item)
		if err := json.Unmarshal(bytesData, &a); err == nil {
			if a.CompanyID == "" {
				a.CompanyID = companyID
			}
			result = append(result, a)
		}
	}
	return result, nil
}

func (c *Client) GetAgentInstructionsFile(ctx context.Context, agentID, filePath string) (string, error) {
	var resp map[string]interface{}
	reqPath := fmt.Sprintf("/api/agents/%s/instructions-bundle/file?path=%s", url.PathEscape(agentID), url.QueryEscape(filePath))
	if err := c.paperclipRequest(ctx, http.MethodGet, reqPath, nil, &resp); err != nil {
		return "", err
	}
	if content, ok := resp["content"].(string); ok {
		return content, nil
	}
	return "", nil
}

func (c *Client) UpdateAgentInstructionsFile(ctx context.Context, agentID, filePath, content string) error {
	payload := map[string]interface{}{
		"path":    filePath,
		"content": content,
	}
	reqPath := fmt.Sprintf("/api/agents/%s/instructions-bundle/file", url.PathEscape(agentID))
	return c.paperclipRequest(ctx, http.MethodPut, reqPath, payload, nil)
}

// Agent Keys
func (c *Client) CreateAgentKey(ctx context.Context, agentID, name string) (*PaperclipAgentKey, error) {
	payload := map[string]interface{}{"name": name}
	var resp PaperclipAgentKey
	if err := c.paperclipRequest(ctx, http.MethodPost, fmt.Sprintf("/api/agents/%s/keys", url.PathEscape(agentID)), payload, &resp); err != nil {
		return nil, err
	}
	resp.AgentID = agentID
	return &resp, nil
}

// Tool Applications

// ToolAppTypes is the authoritative allowed-value set for the Paperclip
// application type. Every validation site (client and provider schema) must
// consult it through ValidateToolAppType rather than restating the literals.
var ToolAppTypes = []string{"mcp_http", "mcp_stdio"}

func ValidateToolAppType(appType string) error {
	if strings.TrimSpace(appType) == "" {
		return errors.New("application type is required")
	}
	for _, allowed := range ToolAppTypes {
		if appType == allowed {
			return nil
		}
	}
	return fmt.Errorf("invalid application type %q (allowed: %s)", appType, strings.Join(ToolAppTypes, ", "))
}

func (c *Client) CreateToolApp(ctx context.Context, companyID string, app PaperclipToolApp) (*PaperclipToolApp, error) {
	if err := ValidateToolAppType(app.Type); err != nil {
		return nil, err
	}
	var resp PaperclipToolApp
	if err := c.paperclipRequest(ctx, http.MethodPost, fmt.Sprintf("/api/companies/%s/tools/applications", url.PathEscape(companyID)), app, &resp); err != nil {
		return nil, err
	}
	if resp.CompanyID == "" {
		resp.CompanyID = companyID
	}
	if resp.Type == "" {
		resp.Type = app.Type
	}
	return &resp, nil
}

func (c *Client) GetToolApp(ctx context.Context, appID string) (*PaperclipToolApp, error) {
	if appID == "" {
		return nil, ErrNotFound
	}
	var resp PaperclipToolApp
	if err := c.paperclipRequest(ctx, http.MethodGet, fmt.Sprintf("/api/tool-applications/%s", url.PathEscape(appID)), nil, &resp); err != nil {
		return nil, err
	}
	if resp.Type == "" && resp.CompanyID != "" {
		apps, err := c.ListToolApps(ctx, resp.CompanyID)
		if err == nil {
			for _, a := range apps {
				if a.ID == appID {
					resp.Type = a.Type
					break
				}
			}
		}
	}
	return &resp, nil
}

func (c *Client) UpdateToolApp(ctx context.Context, appID string, app PaperclipToolApp) (*PaperclipToolApp, error) {
	if err := ValidateToolAppType(app.Type); err != nil {
		return nil, err
	}
	var resp PaperclipToolApp
	payload := map[string]interface{}{
		"name":           app.Name,
		"type":           app.Type,
		"description":    app.Description,
		"applicationKey": app.ApplicationKey,
		"metadata":       app.Metadata,
	}
	if app.Metadata == nil {
		payload["metadata"] = map[string]interface{}{}
	}
	if err := c.paperclipRequest(ctx, http.MethodPatch, fmt.Sprintf("/api/tool-applications/%s", url.PathEscape(appID)), payload, &resp); err != nil {
		return nil, err
	}
	if resp.Type == "" {
		resp.Type = app.Type
	}
	return &resp, nil
}

// ArchiveToolApp retires an application through the status transition the
// Paperclip API accepts; the hard DELETE route conflicts whenever a connection
// row, even an archived one, still references the application.
func (c *Client) ArchiveToolApp(ctx context.Context, appID string) error {
	return c.paperclipRequest(ctx, http.MethodPatch, fmt.Sprintf("/api/tool-applications/%s", url.PathEscape(appID)), map[string]string{"status": ToolAppArchivedStatus}, nil)
}

func (c *Client) ListToolApps(ctx context.Context, companyID string) ([]PaperclipToolApp, error) {
	var raw interface{}
	if err := c.paperclipRequest(ctx, http.MethodGet, fmt.Sprintf("/api/companies/%s/tools/applications", url.PathEscape(companyID)), nil, &raw); err != nil {
		return nil, err
	}
	items := extractItems(raw, "applications")
	var result []PaperclipToolApp
	for _, item := range items {
		var a PaperclipToolApp
		bytesData, _ := json.Marshal(item)
		if err := json.Unmarshal(bytesData, &a); err == nil {
			if a.CompanyID == "" {
				a.CompanyID = companyID
			}
			result = append(result, a)
		}
	}
	return result, nil
}

// Tool Connections
func (c *Client) CreateToolConnection(ctx context.Context, companyID string, conn PaperclipToolConnection) (*PaperclipToolConnection, error) {
	if conn.CredentialSecretRefs == nil {
		conn.CredentialSecretRefs = []map[string]interface{}{}
	}
	var resp PaperclipToolConnection
	if err := c.paperclipRequest(ctx, http.MethodPost, fmt.Sprintf("/api/companies/%s/tools/connections", url.PathEscape(companyID)), conn, &resp); err != nil {
		return nil, err
	}
	if resp.CompanyID == "" {
		resp.CompanyID = companyID
	}
	return &resp, nil
}

// ConnectGalleryApp connects one of Paperclip's included connectors through its
// app gallery definition. Paperclip creates the application, names the
// connection, records config.sourceTemplateKey, and gives it a shared
// credential; an OAuth connector stays a draft until a user authorizes it.
func (c *Client) ConnectGalleryApp(ctx context.Context, companyID, galleryKey string) (*PaperclipToolConnection, error) {
	var resp struct {
		ConnectionID string                  `json:"connectionId"`
		Connection   PaperclipToolConnection `json:"connection"`
	}
	body := map[string]string{"galleryKey": galleryKey}
	if err := c.paperclipRequest(ctx, http.MethodPost, fmt.Sprintf("/api/companies/%s/tools/apps/connect", url.PathEscape(companyID)), body, &resp); err != nil {
		return nil, err
	}
	conn := resp.Connection
	if conn.ID == "" {
		conn.ID = resp.ConnectionID
	}
	if conn.ID == "" {
		return nil, fmt.Errorf("Paperclip gallery connect for %s returned no connection", galleryKey)
	}
	if conn.CompanyID == "" {
		conn.CompanyID = companyID
	}
	return &conn, nil
}

func (c *Client) GetToolConnection(ctx context.Context, connID string) (*PaperclipToolConnection, error) {
	if connID == "" {
		return nil, ErrNotFound
	}
	var resp PaperclipToolConnection
	if err := c.paperclipRequest(ctx, http.MethodGet, fmt.Sprintf("/api/tool-connections/%s", url.PathEscape(connID)), nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) UpdateToolConnection(ctx context.Context, connID string, conn PaperclipToolConnection) (*PaperclipToolConnection, error) {
	var resp PaperclipToolConnection
	config := conn.Config
	if config == nil {
		config = map[string]interface{}{}
	}
	credRefs := conn.CredentialRefs
	if credRefs == nil {
		credRefs = []map[string]interface{}{}
	}
	payload := paperclipToolConnectionUpdate{
		Name:                 conn.Name,
		Transport:            conn.Transport,
		AuthKind:             conn.AuthKind,
		Config:               config,
		CredentialRefs:       credRefs,
		CredentialSecretRefs: []map[string]interface{}{},
	}
	if err := c.paperclipRequest(ctx, http.MethodPatch, fmt.Sprintf("/api/tool-connections/%s", url.PathEscape(connID)), payload, &resp); err != nil {
		return nil, err
	}
	if resp.AppID == "" {
		resp.AppID = conn.AppID
	}
	return &resp, nil
}

func (c *Client) DeleteToolConnection(ctx context.Context, connID string) error {
	return c.paperclipRequest(ctx, http.MethodDelete, fmt.Sprintf("/api/tool-connections/%s", url.PathEscape(connID)), nil, nil)
}

func (c *Client) ListToolConnections(ctx context.Context, companyID string) ([]PaperclipToolConnection, error) {
	var raw interface{}
	if err := c.paperclipRequest(ctx, http.MethodGet, fmt.Sprintf("/api/companies/%s/tools/connections", url.PathEscape(companyID)), nil, &raw); err != nil {
		return nil, err
	}
	items := extractItems(raw, "connections")
	var result []PaperclipToolConnection
	for _, item := range items {
		var conn PaperclipToolConnection
		bytesData, _ := json.Marshal(item)
		if err := json.Unmarshal(bytesData, &conn); err == nil {
			if conn.CompanyID == "" {
				conn.CompanyID = companyID
			}
			result = append(result, conn)
		}
	}
	return result, nil
}

// Stdio Templates
func (c *Client) CreateStdioTemplate(ctx context.Context, companyID string, tpl PaperclipStdioTemplate) (*PaperclipStdioTemplate, error) {
	var resp PaperclipStdioTemplate
	if err := c.paperclipRequest(ctx, http.MethodPost, fmt.Sprintf("/api/companies/%s/tools/stdio-templates", url.PathEscape(companyID)), tpl, &resp); err != nil {
		return nil, err
	}
	if resp.CompanyID == "" {
		resp.CompanyID = companyID
	}
	if resp.TemplateID == "" {
		resp.TemplateID = tpl.TemplateID
	}
	return &resp, nil
}

func (c *Client) GetStdioTemplate(ctx context.Context, companyID, idOrTemplateID string) (*PaperclipStdioTemplate, error) {
	if companyID == "" || idOrTemplateID == "" {
		return nil, ErrNotFound
	}
	templates, err := c.ListStdioTemplates(ctx, companyID)
	if err != nil {
		return nil, err
	}
	for _, tpl := range templates {
		if tpl.ID == idOrTemplateID || tpl.TemplateID == idOrTemplateID {
			return &tpl, nil
		}
	}
	return nil, ErrNotFound
}

func (c *Client) ListStdioTemplates(ctx context.Context, companyID string) ([]PaperclipStdioTemplate, error) {
	var raw interface{}
	if err := c.paperclipRequest(ctx, http.MethodGet, fmt.Sprintf("/api/companies/%s/tools/stdio-templates", url.PathEscape(companyID)), nil, &raw); err != nil {
		return nil, err
	}
	items := extractItems(raw, "templates")
	var result []PaperclipStdioTemplate
	for _, item := range items {
		var tpl PaperclipStdioTemplate
		bytesData, _ := json.Marshal(item)
		if err := json.Unmarshal(bytesData, &tpl); err == nil {
			if tpl.CompanyID == "" {
				tpl.CompanyID = companyID
			}
			result = append(result, tpl)
		}
	}
	return result, nil
}

func (c *Client) DisableStdioTemplate(ctx context.Context, companyID, templateID string) error {
	return c.paperclipRequest(ctx, http.MethodPost, fmt.Sprintf("/api/companies/%s/tools/stdio-templates/%s/disable", url.PathEscape(companyID), url.PathEscape(templateID)), map[string]interface{}{}, nil)
}

// Connection Installs
func (c *Client) GetConnectionInstalls(ctx context.Context, connectionID string) ([]map[string]interface{}, error) {
	var raw map[string]interface{}
	if err := c.paperclipRequest(ctx, http.MethodGet, fmt.Sprintf("/api/tool-connections/%s/installs", url.PathEscape(connectionID)), nil, &raw); err != nil {
		return nil, err
	}
	if installs, ok := raw["installs"].([]interface{}); ok {
		var result []map[string]interface{}
		for _, inst := range installs {
			if m, ok := inst.(map[string]interface{}); ok {
				result = append(result, m)
			}
		}
		return result, nil
	}
	return nil, nil
}

func (c *Client) UpdateConnectionInstalls(ctx context.Context, connectionID string, installs []map[string]interface{}) ([]map[string]interface{}, error) {
	if installs == nil {
		installs = make([]map[string]interface{}, 0)
	}
	payload := map[string]interface{}{
		"installs": installs,
	}
	var raw map[string]interface{}
	if err := c.paperclipRequest(ctx, http.MethodPut, fmt.Sprintf("/api/tool-connections/%s/installs", url.PathEscape(connectionID)), payload, &raw); err != nil {
		return nil, err
	}
	if insts, ok := raw["installs"].([]interface{}); ok {
		var result []map[string]interface{}
		for _, inst := range insts {
			if m, ok := inst.(map[string]interface{}); ok {
				result = append(result, m)
			}
		}
		return result, nil
	}
	return nil, nil
}

// Connection Catalog and App Finish Permissions
func (c *Client) GetConnectionCatalog(ctx context.Context, connectionID string) ([]map[string]interface{}, error) {
	var raw interface{}
	if err := c.paperclipRequest(ctx, http.MethodGet, fmt.Sprintf("/api/tool-connections/%s/catalog", url.PathEscape(connectionID)), nil, &raw); err != nil {
		return nil, err
	}
	return extractItems(raw, "catalog"), nil
}

func (c *Client) FinishAppPermissions(ctx context.Context, companyID, connectionID string, finish PaperclipAppFinish) (map[string]interface{}, error) {
	var resp map[string]interface{}
	if err := c.paperclipRequest(ctx, http.MethodPost, fmt.Sprintf("/api/companies/%s/tools/apps/%s/finish", url.PathEscape(companyID), url.PathEscape(connectionID)), finish, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// Tool Policies
func (c *Client) CreateToolPolicy(ctx context.Context, companyID string, pol PaperclipToolPolicy) (*PaperclipToolPolicy, error) {
	var resp PaperclipToolPolicy
	if err := c.paperclipRequest(ctx, http.MethodPost, fmt.Sprintf("/api/companies/%s/tools/policies", url.PathEscape(companyID)), pol, &resp); err != nil {
		return nil, err
	}
	if resp.CompanyID == "" {
		resp.CompanyID = companyID
	}
	return &resp, nil
}

func (c *Client) GetToolPolicy(ctx context.Context, companyID, polID string) (*PaperclipToolPolicy, error) {
	if companyID == "" || polID == "" {
		return nil, fmt.Errorf("policy reads require company and policy IDs")
	}
	policies, err := c.ListToolPolicies(ctx, companyID)
	if err != nil {
		return nil, err
	}
	for _, p := range policies {
		if p.ID == polID {
			if p.CompanyID != companyID {
				return nil, fmt.Errorf("policy company does not match its state identity")
			}
			return &p, nil
		}
	}
	return nil, ErrNotFound
}

func (c *Client) UpdateToolPolicy(ctx context.Context, companyID, polID string, pol PaperclipToolPolicy) (*PaperclipToolPolicy, error) {
	var resp PaperclipToolPolicy
	reqPath := fmt.Sprintf("/api/companies/%s/tools/policies/%s", url.PathEscape(companyID), url.PathEscape(polID))
	if companyID == "" {
		reqPath = fmt.Sprintf("/api/tool-policies/%s", url.PathEscape(polID))
	}
	payload := map[string]interface{}{
		"name":       pol.Name,
		"policyType": pol.PolicyType,
		"rules":      pol.Rules,
		"metadata":   pol.Metadata,
	}
	if pol.Rules == nil {
		payload["rules"] = map[string]interface{}{}
	}
	if pol.Metadata == nil {
		payload["metadata"] = map[string]interface{}{}
	}
	if err := c.paperclipRequest(ctx, http.MethodPatch, reqPath, payload, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) DeleteToolPolicy(ctx context.Context, companyID, polID string) error {
	reqPath := fmt.Sprintf("/api/companies/%s/tools/policies/%s", url.PathEscape(companyID), url.PathEscape(polID))
	if companyID == "" {
		reqPath = fmt.Sprintf("/api/tool-policies/%s", url.PathEscape(polID))
	}
	return c.paperclipRequest(ctx, http.MethodDelete, reqPath, nil, nil)
}

func (c *Client) ListToolPolicies(ctx context.Context, companyID string) ([]PaperclipToolPolicy, error) {
	var raw interface{}
	if err := c.paperclipRequest(ctx, http.MethodGet, fmt.Sprintf("/api/companies/%s/tools/policies", url.PathEscape(companyID)), nil, &raw); err != nil {
		return nil, err
	}
	items := extractItems(raw, "policies")
	var result []PaperclipToolPolicy
	for _, item := range items {
		var pol PaperclipToolPolicy
		bytesData, _ := json.Marshal(item)
		if err := json.Unmarshal(bytesData, &pol); err == nil {
			if pol.CompanyID == "" {
				pol.CompanyID = companyID
			}
			result = append(result, pol)
		}
	}
	return result, nil
}

// RevokeAppPermissionProfile removes every agent and company binding from the
// connection's native App profile, then every tool entry. Finish requires at
// least one agent, and Paperclip refuses to delete a profile its runtime
// gateways reference, so an empty profile is the native zero-access state.
func (c *Client) RevokeAppPermissionProfile(ctx context.Context, companyID, connectionID string) error {
	profiles, err := c.appPermissionProfiles(ctx, companyID, connectionID)
	if err != nil {
		return err
	}
	for _, profile := range profiles {
		id, _ := profile["id"].(string)
		for _, binding := range extractItems(profile["bindings"], "bindings") {
			if binding["targetType"] != "agent" && binding["targetType"] != "company" {
				continue
			}
			target := map[string]interface{}{"targetType": binding["targetType"], "targetId": binding["targetId"]}
			if err := c.paperclipRequest(ctx, http.MethodPost, fmt.Sprintf("/api/companies/%s/tools/profiles/%s/unbind", url.PathEscape(companyID), url.PathEscape(id)), target, nil); err != nil {
				return err
			}
		}
		for _, entry := range extractItems(profile["entries"], "entries") {
			entryID, _ := entry["id"].(string)
			if err := c.paperclipRequest(ctx, http.MethodDelete, fmt.Sprintf("/api/tool-profile-entries/%s", url.PathEscape(entryID)), nil, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

// askFirstPolicyNameLimit is Paperclip's policy name length limit.
const askFirstPolicyNameLimit = 160

// ReconcileAskFirstPolicies leaves the connection one enabled approval policy
// selecting every ask-first catalog entry and agentIDs, and deletes every other
// enabled approval policy on the connection's entries. Paperclip's App finish
// writes its ask-first policies without agent scope and resets them on every
// finish, so the provider writes this one through the generic policy API. A
// changed selection gets a new policy before the old one is deleted, so no
// granted tool is left ungated between the two writes.
func (c *Client) ReconcileAskFirstPolicies(ctx context.Context, companyID, connectionName string, catalog []map[string]interface{}, askFirstIDs, agentIDs []string) error {
	policies, err := c.ListToolPolicies(ctx, companyID)
	if err != nil {
		return err
	}
	managedID, managedIDs, stray := askFirstPolicy(policies, catalog, false, agentIDs)
	current := managedID != "" && stringSliceEqual(managedIDs, askFirstIDs)
	if managedID != "" && !current {
		stray = append(stray, managedID)
	}
	if len(askFirstIDs) > 0 && !current {
		ids, agents := append([]string{}, askFirstIDs...), append([]string{}, agentIDs...)
		sort.Strings(ids)
		sort.Strings(agents)
		// Policy names are unique per company, so the name carries a digest of
		// the selection: the replacement never collides with the policy it
		// replaces.
		digest := sha256.Sum256([]byte(strings.Join(ids, ",") + "|" + strings.Join(agents, ",")))
		suffix := fmt.Sprintf(" (%x)", digest[:6])
		label := []rune(connectionName)
		if limit := askFirstPolicyNameLimit - len("Ask first: ") - len(suffix); len(label) > limit {
			label = label[:limit]
		}
		policy := PaperclipToolPolicy{
			Name:        "Ask first: " + string(label) + suffix,
			Description: fmt.Sprintf("Ask first before running any of the %d ask-first tools of %s.", len(ids), connectionName),
			PolicyType:  "require_approval",
			Priority:    askFirstPolicyPriority,
			Selectors:   map[string]interface{}{"catalogEntryIds": ids, "agentIds": agents},
		}
		if _, err := c.CreateToolPolicy(ctx, companyID, policy); err != nil {
			return err
		}
	}
	sort.Strings(stray)
	for _, policyID := range stray {
		if err := c.DeleteToolPolicy(ctx, companyID, policyID); err != nil {
			return err
		}
	}
	return nil
}
