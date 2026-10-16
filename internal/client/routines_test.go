package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRoutineNativeRequests(t *testing.T) {
	ctx := context.Background()
	type request struct {
		method, path string
		body         map[string]interface{}
	}
	requests := []request{}
	status := 200
	malformed := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&body)
		}
		requests = append(requests, request{r.Method, r.URL.Path, body})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if status != 200 {
			_, _ = w.Write([]byte(`{"error":"Routine was updated by someone else"}`))
			return
		}
		if malformed {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		switch {
		case r.URL.Path == "/api/issues/parent":
			_, _ = w.Write([]byte(`{"companyId":"company"}`))
		case r.URL.Path == "/api/routines/routine/triggers":
			_, _ = w.Write([]byte(`{"trigger":{"id":"trigger","routineId":"routine","kind":"api"},"secretMaterial":null}`))
		case r.URL.Path == "/api/routine-triggers/trigger":
			_, _ = w.Write([]byte(`{"id":"trigger","kind":"api"}`))
		default:
			_, _ = w.Write([]byte(`{"id":"routine","companyId":"company","latestRevisionId":"revision","triggers":[{"id":"other"},{"id":"trigger","kind":"webhook"}]}`))
		}
	}))
	defer server.Close()
	c, _ := NewClient(server.URL, "")
	r, err := c.CreateRoutine(ctx, "company", map[string]interface{}{"title": "Daily"})
	if err != nil || r.ID != "routine" {
		t.Fatalf("create: %#v %v", r, err)
	}
	if got := requests[len(requests)-1]; got.method != "POST" || got.path != "/api/companies/company/routines" || got.body["title"] != "Daily" {
		t.Fatal(got)
	}
	r, err = c.GetRoutine(ctx, "routine")
	if err != nil || r.LatestRevisionID != "revision" {
		t.Fatalf("read: %#v %v", r, err)
	}
	_, err = c.UpdateRoutine(ctx, "routine", map[string]interface{}{"baseRevisionId": "revision", "description": nil})
	if err != nil {
		t.Fatal(err)
	}
	if got := requests[len(requests)-1]; got.method != "PATCH" || got.path != "/api/routines/routine" || got.body["baseRevisionId"] != "revision" || got.body["description"] != nil {
		t.Fatal(got)
	}
	count := len(requests)
	for _, patch := range []map[string]interface{}{{}, {"baseRevisionId": ""}, {"baseRevisionId": nil}} {
		if _, err := c.UpdateRoutine(ctx, "routine", patch); err == nil {
			t.Fatal("unguarded update accepted")
		}
	}
	if _, err := c.CreateRoutineTrigger(ctx, "routine", map[string]interface{}{"kind": "webhook"}); err == nil || !strings.Contains(err.Error(), "ephemeral producer") {
		t.Fatal(err)
	}
	if len(requests) != count {
		t.Fatal("unsupported operation mutated")
	}
	trigger, err := c.CreateRoutineTrigger(ctx, "routine", map[string]interface{}{"kind": "api"})
	if err != nil || trigger.ID != "trigger" {
		t.Fatalf("trigger: %#v %v", trigger, err)
	}
	trigger, err = c.GetRoutineTrigger(ctx, "routine", "trigger")
	if err != nil || trigger.Kind != "webhook" {
		t.Fatalf("read trigger: %#v %v", trigger, err)
	}
	if _, err = c.GetRoutineTrigger(ctx, "routine", "absent"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err = c.UpdateRoutineTrigger(ctx, "trigger", map[string]interface{}{"label": nil}); err != nil {
		t.Fatal(err)
	}
	if got := requests[len(requests)-1]; got.method != "PATCH" || got.path != "/api/routine-triggers/trigger" || got.body["label"] != nil {
		t.Fatal(got)
	}
	if err = c.DeleteRoutineTrigger(ctx, "trigger"); err != nil {
		t.Fatal(err)
	}
	if got := requests[len(requests)-1]; got.method != "DELETE" || got.path != "/api/routine-triggers/trigger" {
		t.Fatal(got)
	}
	company, err := c.GetRoutineParentCompany(ctx, "parent")
	if err != nil || company != "company" {
		t.Fatalf("parent: %s %v", company, err)
	}
	status = 409
	if _, err = c.UpdateRoutine(ctx, "routine", map[string]interface{}{"baseRevisionId": "stale"}); err == nil || !strings.Contains(err.Error(), "409") {
		t.Fatal(err)
	}
	status = 403
	if _, err = c.GetRoutineTrigger(ctx, "routine", "trigger"); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	status = 200
	malformed = true
	for _, call := range []func() error{
		func() error { _, e := c.GetRoutine(ctx, "routine"); return e },
		func() error { _, e := c.CreateRoutine(ctx, "company", map[string]interface{}{}); return e },
		func() error {
			_, e := c.UpdateRoutine(ctx, "routine", map[string]interface{}{"baseRevisionId": "revision"})
			return e
		},
		func() error {
			_, e := c.CreateRoutineTrigger(ctx, "routine", map[string]interface{}{"kind": "api"})
			return e
		},
		func() error { _, e := c.UpdateRoutineTrigger(ctx, "trigger", map[string]interface{}{}); return e },
	} {
		if err := call(); err == nil || !strings.Contains(err.Error(), "identity") {
			t.Fatal(err)
		}
	}
}

func TestRoutineWebhookStagingAndCredentialCleanup(t *testing.T) {
	ctx := context.Background()
	failure := 0
	deleteFailure := 0
	malformed := ""
	deletes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "DELETE" {
			status := failure
			if deleteFailure != 0 {
				status = deleteFailure
			}
			if status != 0 {
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"error":"native deletion failed"}`))
				return
			}
			deletes++
			w.WriteHeader(204)
			return
		}
		if failure != 0 {
			w.WriteHeader(failure)
			_, _ = w.Write([]byte(`{"error":"native mint failed"}`))
			return
		}
		var input map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&input)
		if r.Method != "POST" || r.URL.Path != "/api/routines/routine/triggers" || input["setupPending"] != true || input["enabled"] != true || input["signingMode"] != "hmac_sha256" || input["replayWindowSec"] != float64(300) {
			t.Errorf("unexpected mint: %s %s %#v", r.Method, r.URL.Path, input)
		}
		if malformed != "" {
			_, _ = w.Write([]byte(malformed))
			return
		}
		_, _ = w.Write([]byte(`{"trigger":{"id":"trigger","routineId":"routine","kind":"webhook","setupPending":true},"secretMaterial":{"webhookSecret":"native-one-time-secret"}}`))
	}))
	defer server.Close()
	c, _ := NewClient(server.URL, "")
	trigger, secret, err := c.MintRoutineWebhook(ctx, "routine", "hook", "hmac_sha256", 300)
	if err != nil || trigger.ID != "trigger" || secret != "native-one-time-secret" {
		t.Fatalf("%#v %q %v", trigger, secret, err)
	}
	id, cached, ok := c.RoutineWebhookForName("hook", "routine")
	if !ok || id != "trigger" || cached != secret || c.RoutineWebhookDelivered("hook", "routine", 1) {
		t.Fatal("unconfirmed identity/cache incorrect")
	}
	c.ConfirmRoutineWebhook("hook", "routine", id, 1)
	if !c.RoutineWebhookDelivered("hook", "routine", 1) {
		t.Fatal("delivery was not recorded")
	}
	if err := c.CleanupUnconfirmedKeys(ctx); err != nil || deletes != 0 {
		t.Fatal("confirmed trigger deleted")
	}
	c.ClearMintedToken(id)
	c.RegisterRoutineWebhook("older", "", "same", "routine")
	c.RegisterRoutineWebhook("newer", "value", "same", "routine")
	c.ClearMintedToken("newer")
	id, _, ok = c.RoutineWebhookForName("same", "routine")
	if !ok || id != "older" {
		t.Fatal("credential registry lost remaining producing identity")
	}
	c.ClearMintedToken("older")
	c.RegisterRoutineWebhook("unconfirmed", "value", "pending", "routine")
	failure = 500
	if err := c.CleanupUnconfirmedKeys(ctx); err == nil {
		t.Fatal("deletion failure hidden")
	}
	if _, ok := c.GetMintedToken("unconfirmed"); !ok {
		t.Fatal("failed cleanup discarded recovery identity")
	}
	failure = 0
	if err := c.CleanupUnconfirmedKeys(ctx); err != nil {
		t.Fatal(err)
	}
	failure = 403
	if _, _, err := c.MintRoutineWebhook(ctx, "routine", "failure", "hmac_sha256", 300); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatal(err)
	}
	failure = 0
	for _, body := range []string{`{}`, `{"trigger":{"id":"bad","routineId":"routine","kind":"webhook","setupPending":true},"secretMaterial":null}`} {
		malformed = body
		if _, _, err := c.MintRoutineWebhook(ctx, "routine", "malformed", "hmac_sha256", 300); err == nil || !strings.Contains(err.Error(), "one-time") {
			t.Fatal(err)
		}
	}
	// Retain identity on failed compensation, then recover through the same exit cleanup.
	malformed = `{"trigger":{"id":"bad","routineId":"routine","kind":"webhook","setupPending":true},"secretMaterial":null}`
	deleteFailure = 500
	if _, _, err := c.MintRoutineWebhook(ctx, "routine", "malformed", "hmac_sha256", 300); err == nil || !strings.Contains(err.Error(), "compensating") {
		t.Fatal(err)
	}
	if err := c.CleanupUnconfirmedKeys(ctx); err == nil {
		t.Fatal("compensation failure hidden")
	}
	deleteFailure = 0
	assertCleanup := c.CleanupUnconfirmedKeys(ctx)
	if assertCleanup != nil {
		t.Fatal(assertCleanup)
	}
	c.RegisterRoutineWebhook("already-deleted", "", "absent", "routine")
	failure = 404
	if err := c.CleanupUnconfirmedKeys(ctx); err != nil {
		t.Fatal("already deleted webhook failed cleanup", err)
	}
}
