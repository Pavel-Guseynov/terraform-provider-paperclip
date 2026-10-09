package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

type PaperclipCompanyInvite struct {
	ID               string                 `json:"id"`
	CompanyID        string                 `json:"companyId"`
	InviteType       string                 `json:"inviteType"`
	AllowedJoinTypes string                 `json:"allowedJoinTypes"`
	HumanRole        *string                `json:"humanRole"`
	InviteMessage    *string                `json:"inviteMessage"`
	DefaultsPayload  map[string]interface{} `json:"defaultsPayload"`
	State            string                 `json:"state"`
	ExpiresAt        string                 `json:"expiresAt"`
	CreatedAt        string                 `json:"createdAt"`
	RevokedAt        *string                `json:"revokedAt"`
	AcceptedAt       *string                `json:"acceptedAt"`
}

type CreateCompanyInviteInput struct {
	AllowedJoinTypes string                 `json:"allowedJoinTypes"`
	HumanRole        *string                `json:"humanRole,omitempty"`
	DefaultsPayload  map[string]interface{} `json:"defaultsPayload,omitempty"`
	AgentMessage     *string                `json:"agentMessage,omitempty"`
}

type ListCompanyInvitesOptions struct {
	State  string
	Limit  int
	Offset int
}

type ListCompanyInvitesResponse struct {
	Invites    []PaperclipCompanyInvite `json:"invites"`
	NextOffset *int                     `json:"nextOffset"`
}

type PaperclipUserProfile struct {
	ID    string  `json:"id"`
	Email *string `json:"email"`
	Name  *string `json:"name"`
	Image *string `json:"image"`
}

type PaperclipMemberGrant struct {
	ID            string                 `json:"id"`
	CompanyID     string                 `json:"companyId"`
	PrincipalType string                 `json:"principalType"`
	PrincipalID   string                 `json:"principalId"`
	PermissionKey string                 `json:"permissionKey"`
	Scope         map[string]interface{} `json:"scope"`
}

type PaperclipMemberGrantInput struct {
	PermissionKey string                 `json:"permissionKey"`
	Scope         map[string]interface{} `json:"scope,omitempty"`
}

type PaperclipCompanyMember struct {
	ID             string                 `json:"id"`
	CompanyID      string                 `json:"companyId"`
	PrincipalType  string                 `json:"principalType"`
	PrincipalID    string                 `json:"principalId"`
	MembershipRole *string                `json:"membershipRole"`
	Status         string                 `json:"status"`
	User           *PaperclipUserProfile  `json:"user"`
	Grants         []PaperclipMemberGrant `json:"grants"`
}

type UpdateCompanyMemberInput struct {
	MembershipRole *string `json:"membershipRole,omitempty"`
	Status         *string `json:"status,omitempty"`
}

type MemberReassignment struct {
	AssigneeAgentID *string `json:"assigneeAgentId,omitempty"`
	AssigneeUserID  *string `json:"assigneeUserId,omitempty"`
}

type ArchiveCompanyMemberInput struct {
	Reassignment *MemberReassignment `json:"reassignment,omitempty"`
}

type PaperclipDirectoryUser struct {
	PrincipalID string                `json:"principalId"`
	Status      string                `json:"status"`
	User        *PaperclipUserProfile `json:"user"`
}

type PaperclipAdminUser struct {
	ID                           string  `json:"id"`
	Email                        *string `json:"email"`
	Name                         *string `json:"name"`
	Image                        *string `json:"image"`
	IsInstanceAdmin              bool    `json:"isInstanceAdmin"`
	ActiveCompanyMembershipCount int     `json:"activeCompanyMembershipCount"`
}

type UserCompanyAccessItem struct {
	ID             string  `json:"id"`
	CompanyID      string  `json:"companyId"`
	CompanyName    *string `json:"companyName"`
	CompanyStatus  *string `json:"companyStatus"`
	MembershipRole *string `json:"membershipRole"`
	Status         string  `json:"status"`
}

type UserCompanyAccessResponse struct {
	User          *PaperclipAdminUser     `json:"user"`
	CompanyAccess []UserCompanyAccessItem `json:"companyAccess"`
}

func checkInstanceAccessError(err error) error {
	if err == nil {
		return nil
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		if apiErr.StatusCode == http.StatusForbidden &&
			(strings.Contains(apiErr.Body, "settings_operator_managed") ||
				strings.Contains(apiErr.Body, "instance.access") ||
				strings.Contains(apiErr.Body, "hosting operator")) {
			return fmt.Errorf("instance user administration is managed by the hosting operator on this instance (instance.access hidden): %w", err)
		}
	}
	errStr := strings.ToLower(err.Error())
	if (strings.Contains(errStr, "403") || strings.Contains(errStr, "forbidden")) &&
		(strings.Contains(errStr, "settings_operator_managed") ||
			strings.Contains(errStr, "instance.access") ||
			strings.Contains(errStr, "hosting operator")) {
		return fmt.Errorf("instance user administration is managed by the hosting operator on this instance (instance.access hidden): %w", err)
	}
	return err
}

func (c *Client) CreateCompanyInvite(ctx context.Context, companyID string, input CreateCompanyInviteInput) (*PaperclipCompanyInvite, error) {
	body := map[string]interface{}{}
	if input.AllowedJoinTypes != "" {
		body["allowedJoinTypes"] = input.AllowedJoinTypes
	} else {
		body["allowedJoinTypes"] = "both"
	}
	if input.HumanRole != nil {
		body["humanRole"] = *input.HumanRole
	}
	if input.DefaultsPayload != nil {
		body["defaultsPayload"] = input.DefaultsPayload
	}
	if input.AgentMessage != nil {
		body["agentMessage"] = *input.AgentMessage
	}

	var res PaperclipCompanyInvite
	path := fmt.Sprintf("/api/companies/%s/invites", url.PathEscape(companyID))
	if err := c.paperclipRequest(ctx, http.MethodPost, path, body, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

func (c *Client) ListCompanyInvites(ctx context.Context, companyID string, options ListCompanyInvitesOptions) (*ListCompanyInvitesResponse, error) {
	v := url.Values{}
	if options.State != "" {
		v.Set("state", options.State)
	}
	if options.Limit > 0 {
		v.Set("limit", fmt.Sprintf("%d", options.Limit))
	}
	if options.Offset > 0 {
		v.Set("offset", fmt.Sprintf("%d", options.Offset))
	}

	path := fmt.Sprintf("/api/companies/%s/invites", url.PathEscape(companyID))
	if q := v.Encode(); q != "" {
		path += "?" + q
	}

	var res ListCompanyInvitesResponse
	if err := c.paperclipRequest(ctx, http.MethodGet, path, nil, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

func (c *Client) GetCompanyInvite(ctx context.Context, companyID, inviteID string) (*PaperclipCompanyInvite, error) {
	offset := 0
	limit := 100
	for {
		resp, err := c.ListCompanyInvites(ctx, companyID, ListCompanyInvitesOptions{
			Limit:  limit,
			Offset: offset,
		})
		if err != nil {
			return nil, err
		}
		for i := range resp.Invites {
			if resp.Invites[i].ID == inviteID {
				return &resp.Invites[i], nil
			}
		}
		if resp.NextOffset == nil || *resp.NextOffset <= offset {
			break
		}
		offset = *resp.NextOffset
	}
	return nil, ErrNotFound
}

func (c *Client) RevokeCompanyInvite(ctx context.Context, inviteID string) (*PaperclipCompanyInvite, error) {
	path := fmt.Sprintf("/api/invites/%s/revoke", url.PathEscape(inviteID))
	var res PaperclipCompanyInvite
	if err := c.paperclipRequest(ctx, http.MethodPost, path, nil, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

func (c *Client) ListCompanyMembers(ctx context.Context, companyID string) ([]PaperclipCompanyMember, error) {
	path := fmt.Sprintf("/api/companies/%s/members", url.PathEscape(companyID))
	var res struct {
		Members []PaperclipCompanyMember `json:"members"`
	}
	if err := c.paperclipRequest(ctx, http.MethodGet, path, nil, &res); err != nil {
		return nil, err
	}
	return res.Members, nil
}

func (c *Client) GetCompanyMember(ctx context.Context, companyID, memberOrUserID string) (*PaperclipCompanyMember, error) {
	members, err := c.ListCompanyMembers(ctx, companyID)
	if err != nil {
		return nil, err
	}
	for i := range members {
		if members[i].ID == memberOrUserID || members[i].PrincipalID == memberOrUserID {
			return &members[i], nil
		}
	}
	return nil, ErrNotFound
}

func (c *Client) UpdateCompanyMember(ctx context.Context, companyID, memberID string, input UpdateCompanyMemberInput) (*PaperclipCompanyMember, error) {
	body := map[string]interface{}{}
	if input.MembershipRole != nil {
		body["membershipRole"] = *input.MembershipRole
	}
	if input.Status != nil {
		body["status"] = *input.Status
	}
	path := fmt.Sprintf("/api/companies/%s/members/%s", url.PathEscape(companyID), url.PathEscape(memberID))
	var res PaperclipCompanyMember
	if err := c.paperclipRequest(ctx, http.MethodPatch, path, body, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

func (c *Client) ArchiveCompanyMember(ctx context.Context, companyID, memberID string, reassignment *MemberReassignment) error {
	body := ArchiveCompanyMemberInput{
		Reassignment: reassignment,
	}
	path := fmt.Sprintf("/api/companies/%s/members/%s/archive", url.PathEscape(companyID), url.PathEscape(memberID))
	return c.paperclipRequest(ctx, http.MethodPost, path, body, nil)
}

func (c *Client) SetMemberPermissions(ctx context.Context, companyID, memberID string, grants []PaperclipMemberGrantInput) (*PaperclipCompanyMember, error) {
	body := map[string]interface{}{
		"grants": grants,
	}
	path := fmt.Sprintf("/api/companies/%s/members/%s/permissions", url.PathEscape(companyID), url.PathEscape(memberID))
	var res PaperclipCompanyMember
	if err := c.paperclipRequest(ctx, http.MethodPatch, path, body, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

func (c *Client) GetCompanyUserDirectory(ctx context.Context, companyID string) ([]PaperclipDirectoryUser, error) {
	path := fmt.Sprintf("/api/companies/%s/user-directory", url.PathEscape(companyID))
	var res struct {
		Users []PaperclipDirectoryUser `json:"users"`
	}
	if err := c.paperclipRequest(ctx, http.MethodGet, path, nil, &res); err != nil {
		return nil, err
	}
	return res.Users, nil
}

func (c *Client) ListAdminUsers(ctx context.Context, query string) ([]PaperclipAdminUser, error) {
	path := "/api/admin/users"
	if query != "" {
		path += "?query=" + url.QueryEscape(query)
	}
	var res []PaperclipAdminUser
	if err := c.paperclipRequest(ctx, http.MethodGet, path, nil, &res); err != nil {
		return nil, checkInstanceAccessError(err)
	}
	return res, nil
}

func (c *Client) PromoteInstanceAdmin(ctx context.Context, userID string) error {
	path := fmt.Sprintf("/api/admin/users/%s/promote-instance-admin", url.PathEscape(userID))
	if err := c.paperclipRequest(ctx, http.MethodPost, path, nil, nil); err != nil {
		return checkInstanceAccessError(err)
	}
	return nil
}

func (c *Client) DemoteInstanceAdmin(ctx context.Context, userID string) error {
	path := fmt.Sprintf("/api/admin/users/%s/demote-instance-admin", url.PathEscape(userID))
	if err := c.paperclipRequest(ctx, http.MethodPost, path, nil, nil); err != nil {
		return checkInstanceAccessError(err)
	}
	return nil
}

func (c *Client) GetUserCompanyAccess(ctx context.Context, userID string) (*UserCompanyAccessResponse, error) {
	path := fmt.Sprintf("/api/admin/users/%s/company-access", url.PathEscape(userID))
	var res UserCompanyAccessResponse
	if err := c.paperclipRequest(ctx, http.MethodGet, path, nil, &res); err != nil {
		return nil, checkInstanceAccessError(err)
	}
	return &res, nil
}

func (c *Client) SetUserCompanyAccess(ctx context.Context, userID string, companyIDs []string) (*UserCompanyAccessResponse, error) {
	body := map[string]interface{}{
		"companyIds": companyIDs,
	}
	path := fmt.Sprintf("/api/admin/users/%s/company-access", url.PathEscape(userID))
	var res UserCompanyAccessResponse
	if err := c.paperclipRequest(ctx, http.MethodPut, path, body, &res); err != nil {
		return nil, checkInstanceAccessError(err)
	}
	return &res, nil
}
