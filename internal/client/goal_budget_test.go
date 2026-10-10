package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGoalClientMethods(t *testing.T) {
	ctx := context.Background()

	goalsDB := map[string]PaperclipGoal{
		"goal-1": {
			ID:          "goal-1",
			CompanyID:   "comp-1",
			Title:       "Goal 1",
			Description: "Desc 1",
			Level:       "company",
			Status:      "active",
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/companies/comp-1/goals":
			var g PaperclipGoal
			_ = json.NewDecoder(r.Body).Decode(&g)
			g.ID = "goal-new"
			g.CompanyID = "comp-1"
			_ = json.NewEncoder(w).Encode(g)
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-1/goals":
			var list []PaperclipGoal
			for _, g := range goalsDB {
				list = append(list, g)
			}
			_ = json.NewEncoder(w).Encode(list)
		case r.Method == http.MethodGet && r.URL.Path == "/api/goals/goal-1":
			_ = json.NewEncoder(w).Encode(goalsDB["goal-1"])
		case r.Method == http.MethodGet && r.URL.Path == "/api/goals/goal-404":
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "not found"})
		case r.Method == http.MethodPatch && r.URL.Path == "/api/goals/goal-1":
			var patch map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&patch)
			g := goalsDB["goal-1"]
			if t, ok := patch["title"].(string); ok {
				g.Title = t
			}
			_ = json.NewEncoder(w).Encode(g)
		case r.Method == http.MethodDelete && r.URL.Path == "/api/goals/goal-1":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete && r.URL.Path == "/api/goals/goal-err":
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "failed to delete"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cli, err := NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}

	// 1. CreateGoal with all fields
	fullGoal := PaperclipGoal{
		Title:        "Full Goal",
		Description:  "Full Desc",
		Level:        "team",
		Status:       "planned",
		ParentID:     "goal-parent",
		OwnerAgentID: "agent-1",
	}
	created, err := cli.CreateGoal(ctx, "comp-1", fullGoal)
	if err != nil {
		t.Fatalf("CreateGoal failed: %v", err)
	}
	if created.ID != "goal-new" || created.CompanyID != "comp-1" {
		t.Fatalf("unexpected created goal: %+v", created)
	}

	// CreateGoal error
	_, err = cli.CreateGoal(ctx, "comp-err", fullGoal)
	if err == nil {
		t.Fatal("expected error on invalid company")
	}

	// 2. GetGoal
	g, err := cli.GetGoal(ctx, "goal-1")
	if err != nil {
		t.Fatalf("GetGoal failed: %v", err)
	}
	if g.Title != "Goal 1" {
		t.Fatalf("unexpected goal title: %s", g.Title)
	}

	// GetGoal not found
	_, err = cli.GetGoal(ctx, "goal-404")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	// 3. UpdateGoal
	updated, err := cli.UpdateGoal(ctx, "goal-1", map[string]interface{}{"title": "Goal Updated"})
	if err != nil {
		t.Fatalf("UpdateGoal failed: %v", err)
	}
	if updated.Title != "Goal Updated" {
		t.Fatalf("unexpected updated title: %s", updated.Title)
	}

	// UpdateGoal error
	_, err = cli.UpdateGoal(ctx, "goal-404", map[string]interface{}{"title": "Fail"})
	if err == nil {
		t.Fatal("expected error updating missing goal")
	}

	// 4. DeleteGoal
	if err := cli.DeleteGoal(ctx, "goal-1"); err != nil {
		t.Fatalf("DeleteGoal failed: %v", err)
	}
	if err := cli.DeleteGoal(ctx, "goal-err"); err == nil {
		t.Fatal("expected error on failed DeleteGoal")
	}

	// 5. ListGoals
	list, err := cli.ListGoals(ctx, "comp-1")
	if err != nil {
		t.Fatalf("ListGoals failed: %v", err)
	}
	if len(list) == 0 {
		t.Fatal("expected non-empty list of goals")
	}

	// ListGoals error
	_, err = cli.ListGoals(ctx, "comp-err")
	if err == nil {
		t.Fatal("expected error on missing company ListGoals")
	}
}

func TestBudgetPolicyClientMethods(t *testing.T) {
	ctx := context.Background()

	policies := []PaperclipBudgetPolicySummary{
		{
			PolicyID:        "pol-1",
			CompanyID:       "comp-1",
			ScopeType:       "company",
			ScopeID:         "comp-1",
			Metric:          "billed_cents",
			WindowKind:      "calendar_month_utc",
			Amount:          10000,
			WarnPercent:     80,
			HardStopEnabled: true,
			NotifyEnabled:   true,
			IsActive:        true,
		},
		{
			PolicyID:        "pol-2",
			CompanyID:       "comp-1",
			ScopeType:       "agent",
			ScopeID:         "agent-1",
			Metric:          "billed_cents",
			WindowKind:      "lifetime",
			Amount:          5000,
			WarnPercent:     90,
			HardStopEnabled: false,
			NotifyEnabled:   true,
			IsActive:        true,
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/companies/comp-1/budgets/policies":
			var p map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&p)
			amt, _ := p["amount"].(float64)
			wp, _ := p["warnPercent"].(float64)
			hardStop, _ := p["hardStopEnabled"].(bool)
			notify, _ := p["notifyEnabled"].(bool)
			active, _ := p["isActive"].(bool)
			_ = json.NewEncoder(w).Encode(PaperclipBudgetPolicySummary{
				PolicyID:        "pol-upserted",
				CompanyID:       "comp-1",
				ScopeType:       p["scopeType"].(string),
				ScopeID:         p["scopeId"].(string),
				Metric:          p["metric"].(string),
				WindowKind:      p["windowKind"].(string),
				Amount:          int64(amt),
				WarnPercent:     int64(wp),
				HardStopEnabled: hardStop,
				NotifyEnabled:   notify,
				IsActive:        active,
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-1/budgets/overview":
			_ = json.NewEncoder(w).Encode(PaperclipBudgetOverview{
				Policies: policies,
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-2/budgets/overview":
			_ = json.NewEncoder(w).Encode(PaperclipBudgetOverview{
				Policies: []PaperclipBudgetPolicySummary{
					{
						PolicyID:   "pol-3",
						CompanyID:  "comp-2",
						ScopeType:  "company",
						ScopeID:    "comp-2",
						WindowKind: "calendar_month_utc",
						IsActive:   true,
					},
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies":
			_ = json.NewEncoder(w).Encode([]PaperclipCompany{
				{ID: "comp-1", Name: "Company 1"},
				{ID: "comp-2", Name: "Company 2"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cli, err := NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}

	// 1. UpsertBudgetPolicy with defaults (empty metric, empty windowKind)
	pDefaults := PaperclipBudgetPolicy{
		ScopeType:       "company",
		ScopeID:         "comp-1",
		Amount:          20000,
		WarnPercent:     85,
		HardStopEnabled: true,
		NotifyEnabled:   true,
		IsActive:        true,
	}
	res, err := cli.UpsertBudgetPolicy(ctx, "comp-1", pDefaults)
	if err != nil {
		t.Fatalf("UpsertBudgetPolicy failed: %v", err)
	}
	if res.Metric != "billed_cents" || res.WindowKind != "calendar_month_utc" {
		t.Fatalf("expected default metric and windowKind, got: %+v", res)
	}

	// Upsert with explicit metric and windowKind
	pExplicit := PaperclipBudgetPolicy{
		ScopeType:       "agent",
		ScopeID:         "agent-1",
		Metric:          "billed_cents",
		WindowKind:      "lifetime",
		Amount:          15000,
		WarnPercent:     90,
		HardStopEnabled: false,
		NotifyEnabled:   true,
		IsActive:        true,
	}
	res2, err := cli.UpsertBudgetPolicy(ctx, "comp-1", pExplicit)
	if err != nil {
		t.Fatalf("UpsertBudgetPolicy explicit failed: %v", err)
	}
	if res2.WindowKind != "lifetime" {
		t.Fatalf("expected lifetime windowKind, got: %+v", res2)
	}

	// Upsert error
	_, err = cli.UpsertBudgetPolicy(ctx, "comp-err", pDefaults)
	if err == nil {
		t.Fatal("expected error on comp-err")
	}

	// 2. GetBudgetOverview
	overview, err := cli.GetBudgetOverview(ctx, "comp-1")
	if err != nil {
		t.Fatalf("GetBudgetOverview failed: %v", err)
	}
	if len(overview.Policies) != 2 {
		t.Fatalf("expected 2 policies, got %d", len(overview.Policies))
	}

	// GetBudgetOverview error
	_, err = cli.GetBudgetOverview(ctx, "comp-err")
	if err == nil {
		t.Fatal("expected error on comp-err GetBudgetOverview")
	}

	// 3. ListBudgetPolicies
	pList, err := cli.ListBudgetPolicies(ctx, "comp-1")
	if err != nil {
		t.Fatalf("ListBudgetPolicies failed: %v", err)
	}
	if len(pList) != 2 {
		t.Fatalf("expected 2 policies, got %d", len(pList))
	}

	// ListBudgetPolicies error
	_, err = cli.ListBudgetPolicies(ctx, "comp-err")
	if err == nil {
		t.Fatal("expected error on comp-err ListBudgetPolicies")
	}

	// 4. GetBudgetPolicy
	pol, err := cli.GetBudgetPolicy(ctx, "comp-1", "pol-1")
	if err != nil {
		t.Fatalf("GetBudgetPolicy pol-1 failed: %v", err)
	}
	if pol.PolicyID != "pol-1" {
		t.Fatalf("unexpected policy ID: %s", pol.PolicyID)
	}

	// GetBudgetPolicy not found
	_, err = cli.GetBudgetPolicy(ctx, "comp-1", "pol-missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	// GetBudgetPolicy error
	_, err = cli.GetBudgetPolicy(ctx, "comp-err", "pol-1")
	if err == nil {
		t.Fatal("expected error on comp-err GetBudgetPolicy")
	}

	// 5. FindBudgetPolicy
	fPol, err := cli.FindBudgetPolicy(ctx, "comp-1", "agent", "agent-1", "lifetime")
	if err != nil {
		t.Fatalf("FindBudgetPolicy failed: %v", err)
	}
	if fPol.PolicyID != "pol-2" {
		t.Fatalf("unexpected found policy ID: %s", fPol.PolicyID)
	}

	// FindBudgetPolicy not found
	_, err = cli.FindBudgetPolicy(ctx, "comp-1", "agent", "agent-missing", "lifetime")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	// FindBudgetPolicy error
	_, err = cli.FindBudgetPolicy(ctx, "comp-err", "agent", "agent-1", "lifetime")
	if err == nil {
		t.Fatal("expected error on comp-err FindBudgetPolicy")
	}

	// 6. FindBudgetPolicyByID
	fbPol1, err := cli.FindBudgetPolicyByID(ctx, "pol-1")
	if err != nil {
		t.Fatalf("FindBudgetPolicyByID pol-1 failed: %v", err)
	}
	if fbPol1.PolicyID != "pol-1" {
		t.Fatalf("unexpected fbPol1 ID: %s", fbPol1.PolicyID)
	}

	fbPol3, err := cli.FindBudgetPolicyByID(ctx, "pol-3")
	if err != nil {
		t.Fatalf("FindBudgetPolicyByID pol-3 failed: %v", err)
	}
	if fbPol3.PolicyID != "pol-3" {
		t.Fatalf("unexpected fbPol3 ID: %s", fbPol3.PolicyID)
	}

	// FindBudgetPolicyByID not found
	_, err = cli.FindBudgetPolicyByID(ctx, "pol-missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for missing policy, got %v", err)
	}

	// FindBudgetPolicyByID when ListCompanies fails
	errServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "server error", http.StatusInternalServerError)
	}))
	defer errServer.Close()
	errCli, _ := NewClient(errServer.URL, "token")
	_, err = errCli.FindBudgetPolicyByID(ctx, "pol-1")
	if err == nil {
		t.Fatal("expected error when ListCompanies fails")
	}
}
