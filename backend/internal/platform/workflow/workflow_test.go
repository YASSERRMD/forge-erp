package workflow

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func testEngine(t *testing.T) *Engine {
	t.Helper()
	e, err := New(
		Rule{Context: "members.subscription", From: "0", To: "1",
			Actions: []Action{{Kind: ActionHook, Target: "forgeerp.members.subscription.validated.v1"}}},
		Rule{Context: "members.subscription", From: "1", To: "2",
			Actions: []Action{
				{Kind: ActionHook, Target: "forgeerp.members.subscription.paid.v1"},
				{Kind: ActionFieldSet, Fields: map[string]any{"settled": true}},
			}},
		Rule{Context: "members.subscription", From: "*", To: "-1",
			Actions: []Action{{Kind: ActionHook, Target: "forgeerp.members.subscription.canceled.v1"}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestFireMatches(t *testing.T) {
	e := testEngine(t)
	got := e.Fire(Transition{Context: "members.subscription", EntityID: 1, From: "0", To: "1"})
	if len(got) != 1 || got[0].Target != "forgeerp.members.subscription.validated.v1" {
		t.Fatalf("validate fire=%+v", got)
	}
	got = e.Fire(Transition{Context: "members.subscription", EntityID: 1, From: "1", To: "2"})
	if len(got) != 2 {
		t.Fatalf("pay fire=%+v", got)
	}
	// Wildcard from-side: any → canceled.
	got = e.Fire(Transition{Context: "members.subscription", EntityID: 1, From: "2", To: "-1"})
	if len(got) != 1 || got[0].Target != "forgeerp.members.subscription.canceled.v1" {
		t.Fatalf("cancel fire=%+v", got)
	}
}

func TestFireNoMatch(t *testing.T) {
	e := testEngine(t)
	// Unknown context.
	if got := e.Fire(Transition{Context: "sales.order", EntityID: 1, From: "0", To: "1"}); len(got) != 0 {
		t.Fatalf("foreign context fired=%+v", got)
	}
	// Known context, unmapped edge (paid is terminal — no rule leaves it).
	if got := e.Fire(Transition{Context: "members.subscription", EntityID: 1, From: "2", To: "0"}); len(got) != 0 {
		t.Fatalf("terminal edge fired=%+v", got)
	}
	// Illegal edge the transition table itself forbids.
	if got := e.Fire(Transition{Context: "members.subscription", EntityID: 1, From: "0", To: "2"}); len(got) != 0 {
		t.Fatalf("skipped edge fired=%+v", got)
	}
}

func TestEntityOverrideReplacesCodeTable(t *testing.T) {
	e := testEngine(t)
	custom := []Rule{{
		Context: "members.subscription", From: "0", To: "1",
		Actions: []Action{{Kind: ActionHook, Target: "custom.validated.v1"}},
	}}
	if err := e.SetEntityRules(7, "members.subscription", custom); err != nil {
		t.Fatal(err)
	}
	got := e.Fire(Transition{Context: "members.subscription", EntityID: 7, From: "0", To: "1"})
	if len(got) != 1 || got[0].Target != "custom.validated.v1" {
		t.Fatalf("override fire=%+v", got)
	}
	// Other entities still see the code table.
	got = e.Fire(Transition{Context: "members.subscription", EntityID: 1, From: "0", To: "1"})
	if len(got) != 1 || got[0].Target != "forgeerp.members.subscription.validated.v1" {
		t.Fatalf("code table after override=%+v", got)
	}
	// Clearing restores the code table.
	if err := e.SetEntityRules(7, "members.subscription", nil); err != nil {
		t.Fatal(err)
	}
	got = e.Fire(Transition{Context: "members.subscription", EntityID: 7, From: "0", To: "1"})
	if len(got) != 1 || got[0].Target != "forgeerp.members.subscription.validated.v1" {
		t.Fatalf("after clear=%+v", got)
	}
	// Mismatched context rejected.
	if err := e.SetEntityRules(7, "members.subscription", []Rule{{
		Context: "other", From: "0", To: "1",
		Actions: []Action{{Kind: ActionHook, Target: "x"}},
	}}); err == nil {
		t.Error("context-mismatched override accepted")
	}
}

func TestRuleValidate(t *testing.T) {
	_, err := New(Rule{Context: "c", From: "*", To: "*",
		Actions: []Action{{Kind: ActionHook, Target: "x"}}})
	if err == nil {
		t.Error("double-wildcard rule accepted")
	}
	_, err = New(Rule{Context: "c", From: "0", To: "1"})
	if err == nil {
		t.Error("action-less rule accepted")
	}
	_, err = New(Rule{Context: "c", From: "0", To: "1",
		Actions: []Action{{Kind: ActionWebhook, Target: "http://insecure/x"}}})
	if err == nil {
		t.Error("non-https webhook accepted")
	}
	_, err = New(Rule{Context: "c", From: "0", To: "1",
		Actions: []Action{{Kind: "carrier-pigeon"}}})
	if err == nil {
		t.Error("unknown action kind accepted")
	}
}

func TestApplyFields(t *testing.T) {
	out := ApplyFields(map[string]any{"a": 1}, []Action{
		{Kind: ActionHook, Target: "x"},
		{Kind: ActionFieldSet, Fields: map[string]any{"settled": true, "a": 2}},
	})
	if out["settled"] != true || out["a"] != 2 {
		t.Fatalf("merged=%v", out)
	}
}

func TestWebhookDeliver(t *testing.T) {
	var gotPath string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// httptest serves http, not https — rewrite the target check by
	// delivering through a client test: validation happens at rule build,
	// so construct the action directly for the transport test.
	c := &WebhookClient{HTTP: srv.Client(), Timeout: 5 * time.Second}
	tr := Transition{Context: "members.subscription", EntityID: 1, From: "0", To: "1"}
	if err := c.Deliver(context.Background(), tr, []Action{{Kind: ActionWebhook, Target: srv.URL + "/hook"}}); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/hook" || len(gotBody) == 0 {
		t.Fatalf("delivered path=%q body=%d bytes", gotPath, len(gotBody))
	}
	// Non-2xx is an error.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()
	if err := c.Deliver(context.Background(), tr, []Action{{Kind: ActionWebhook, Target: bad.URL}}); err == nil {
		t.Error("500 delivery accepted")
	}
	// Non-webhook actions are skipped without I/O.
	if err := c.Deliver(context.Background(), tr, []Action{{Kind: ActionHook, Target: "x"}}); err != nil {
		t.Fatal(err)
	}
}
