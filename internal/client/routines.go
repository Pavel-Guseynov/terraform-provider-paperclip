package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// Routine reads use the native detail endpoint, including its trigger list.
type PaperclipRoutine struct {
	ID                 string                    `json:"id"`
	CompanyID          string                    `json:"companyId"`
	ProjectID          *string                   `json:"projectId"`
	GoalID             *string                   `json:"goalId"`
	ParentIssueID      *string                   `json:"parentIssueId"`
	Title              string                    `json:"title"`
	Description        *string                   `json:"description"`
	AssigneeAgentID    *string                   `json:"assigneeAgentId"`
	Priority           string                    `json:"priority"`
	Status             string                    `json:"status"`
	ConcurrencyPolicy  string                    `json:"concurrencyPolicy"`
	CatchUpPolicy      string                    `json:"catchUpPolicy"`
	ActivityGatePolicy string                    `json:"activityGatePolicy"`
	ActivityGateScope  string                    `json:"activityGateScope"`
	Variables          []map[string]interface{}  `json:"variables"`
	Env                map[string]interface{}    `json:"env"`
	LatestRevisionID   string                    `json:"latestRevisionId"`
	Triggers           []PaperclipRoutineTrigger `json:"triggers"`
}

type PaperclipRoutineTrigger struct {
	ID              string  `json:"id"`
	RoutineID       string  `json:"routineId"`
	CompanyID       string  `json:"companyId"`
	Kind            string  `json:"kind"`
	Label           *string `json:"label"`
	Enabled         bool    `json:"enabled"`
	CronExpression  *string `json:"cronExpression"`
	Timezone        *string `json:"timezone"`
	SigningMode     *string `json:"signingMode"`
	ReplayWindowSec *int64  `json:"replayWindowSec"`
	SetupPending    bool    `json:"setupPending"`
}

func (c *Client) GetRoutine(ctx context.Context, id string) (*PaperclipRoutine, error) {
	var result PaperclipRoutine
	err := c.paperclipRequest(ctx, http.MethodGet, "/api/routines/"+url.PathEscape(id), nil, &result)
	if err == nil && (result.ID != id || result.CompanyID == "" || result.LatestRevisionID == "") {
		return nil, fmt.Errorf("native routine detail returned an inconsistent identity or no revision; refusing to adopt or update it")
	}
	return &result, err
}

func (c *Client) CreateRoutine(ctx context.Context, companyID string, input map[string]interface{}) (*PaperclipRoutine, error) {
	var result PaperclipRoutine
	err := c.paperclipRequest(ctx, http.MethodPost, "/api/companies/"+url.PathEscape(companyID)+"/routines", input, &result)
	if err == nil && (result.ID == "" || result.CompanyID != companyID || result.LatestRevisionID == "") {
		return nil, fmt.Errorf("native routine creation returned an inconsistent identity or no revision")
	}
	return &result, err
}

func (c *Client) UpdateRoutine(ctx context.Context, id string, input map[string]interface{}) (*PaperclipRoutine, error) {
	if revision, ok := input["baseRevisionId"].(string); !ok || revision == "" {
		return nil, fmt.Errorf("routine updates require the revision captured by refresh; refresh and plan again")
	}
	var result PaperclipRoutine
	err := c.paperclipRequest(ctx, http.MethodPatch, "/api/routines/"+url.PathEscape(id), input, &result)
	if err == nil && (result.ID != id || result.LatestRevisionID == "") {
		return nil, fmt.Errorf("native routine update returned an inconsistent identity or no revision")
	}
	return &result, err
}

func (c *Client) CreateRoutineTrigger(ctx context.Context, routineID string, input map[string]interface{}) (*PaperclipRoutineTrigger, error) {
	// Native webhook creation returns an unrecoverable one-time secret. Never
	// discard that secret or publish a trigger claiming delivery succeeded.
	if input["kind"] == "webhook" {
		return nil, fmt.Errorf("webhook creation requires the apply-only ephemeral producer, pending setup and confirmed write-only delivery")
	}
	var result struct {
		Trigger PaperclipRoutineTrigger `json:"trigger"`
	}
	err := c.paperclipRequest(ctx, http.MethodPost, "/api/routines/"+url.PathEscape(routineID)+"/triggers", input, &result)
	if err == nil && (result.Trigger.ID == "" || result.Trigger.RoutineID != routineID || result.Trigger.Kind != input["kind"]) {
		return nil, fmt.Errorf("native trigger creation returned an inconsistent identity, kind or routine")
	}
	return &result.Trigger, err
}

func (c *Client) UpdateRoutineTrigger(ctx context.Context, id string, input map[string]interface{}) (*PaperclipRoutineTrigger, error) {
	var result PaperclipRoutineTrigger
	err := c.paperclipRequest(ctx, http.MethodPatch, "/api/routine-triggers/"+url.PathEscape(id), input, &result)
	if err == nil && result.ID != id {
		return nil, fmt.Errorf("native trigger update returned a different identity")
	}
	return &result, err
}

func (c *Client) DeleteRoutineTrigger(ctx context.Context, id string) error {
	return c.paperclipRequest(ctx, http.MethodDelete, "/api/routine-triggers/"+url.PathEscape(id), nil, nil)
}

// There is no GET /routine-triggers/:id. Resolve exact identity from the
// owning routine detail; never interpret an unknown route as object absence.
func (c *Client) GetRoutineTrigger(ctx context.Context, routineID, id string) (*PaperclipRoutineTrigger, error) {
	routine, err := c.GetRoutine(ctx, routineID)
	if err != nil {
		return nil, err
	}
	for _, trigger := range routine.Triggers {
		if trigger.ID == id {
			return &trigger, nil
		}
	}
	return nil, ErrNotFound
}

func (c *Client) GetRoutineParentCompany(ctx context.Context, issueID string) (string, error) {
	var result struct {
		CompanyID string `json:"companyId"`
	}
	err := c.paperclipRequest(ctx, http.MethodGet, "/api/issues/"+url.PathEscape(issueID), nil, &result)
	return result.CompanyID, err
}

const RoutineWebhookSecretLimitation = "The native rotate-secret API immediately replaces the active secret and returns the new value once. It cannot stage a replacement generation, preserve the predecessor until confirmed write-only delivery, or restore the previous credential after failed delivery. Webhook rotation is unsupported under the credential lifecycle contract. Keep the delivered generation unchanged. No mutation was sent."

func routineWebhookOwner(routineID string) string { return "routine:" + routineID }

// Webhooks share the credential registry, acknowledgement and graceful-exit
// cleanup with keys, while retaining their native trigger deletion operation.
func (c *Client) RegisterRoutineWebhook(id, secret, name, routineID string) {
	c.credMu.Lock()
	defer c.credMu.Unlock()
	if secret != "" {
		c.mintedTokens[id] = secret
		delete(c.deliveredTokens, id)
		c.acknowledgedDeliveries[id] = false
	}
	c.mintedKeys[id] = mintedKeyRecord{keyID: id, token: secret, name: name, routineID: routineID}
	c.mintedKeysByName[lookupKey(name, false, routineWebhookOwner(routineID))] = id
}
func (c *Client) RoutineWebhookForName(name, routineID string) (string, string, bool) {
	return c.GetMintedKeyForName(name, false, routineWebhookOwner(routineID))
}
func (c *Client) RoutineWebhookDelivered(name, routineID string, generation int64) bool {
	return c.IsGenerationDelivered(name, false, routineWebhookOwner(routineID), generation)
}
func (c *Client) ConfirmRoutineWebhook(name, routineID, id string, generation int64) {
	c.RecordDeliveredGeneration(name, false, routineWebhookOwner(routineID), generation, id)
}
func (c *Client) RoutineWebhookIdentity(id string) (string, string, bool) {
	c.credMu.RLock()
	defer c.credMu.RUnlock()
	record, ok := c.mintedKeys[id]
	return record.name, record.routineID, ok && record.routineID != ""
}

func (c *Client) MintRoutineWebhook(ctx context.Context, routineID, name, signingMode string, replayWindow int64) (*PaperclipRoutineTrigger, string, error) {
	var result struct {
		Trigger        PaperclipRoutineTrigger `json:"trigger"`
		SecretMaterial struct {
			Secret string `json:"webhookSecret"`
		} `json:"secretMaterial"`
	}
	input := map[string]interface{}{"kind": "webhook", "setupPending": true, "enabled": true, "signingMode": signingMode, "replayWindowSec": replayWindow}
	if err := c.paperclipRequest(ctx, http.MethodPost, "/api/routines/"+url.PathEscape(routineID)+"/triggers", input, &result); err != nil {
		return nil, "", err
	}
	if result.Trigger.ID == "" || result.Trigger.RoutineID != routineID || result.Trigger.Kind != "webhook" || !result.Trigger.SetupPending || result.SecretMaterial.Secret == "" {
		if result.Trigger.ID != "" && result.Trigger.RoutineID == routineID && result.Trigger.Kind == "webhook" {
			c.RegisterRoutineWebhook(result.Trigger.ID, result.SecretMaterial.Secret, name, routineID)
			if err := c.DeleteRoutineTrigger(ctx, result.Trigger.ID); err != nil {
				return nil, "", fmt.Errorf("invalid one-time webhook response; compensating trigger deletion failed: %w", err)
			}
			c.ClearMintedToken(result.Trigger.ID)
		}
		return nil, "", fmt.Errorf("native webhook creation did not return a pending trigger identity and one-time secret; inspect the routine for unconfirmed setup before retrying")
	}
	c.RegisterRoutineWebhook(result.Trigger.ID, result.SecretMaterial.Secret, name, routineID)
	return &result.Trigger, result.SecretMaterial.Secret, nil
}
