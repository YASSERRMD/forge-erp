// Subscription workflow adoption (Phase 2 proof): the fixed subscription
// transition table (Subscription.CanTransition) stays the guard; this engine
// adds configurable automatic actions on each legal move. Hook actions are
// published on the platform bus by the handler; field-set actions merge
// into the published payload.
package members

import (
	"strconv"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform/workflow"
)

// SubscriptionWorkflowContext is the workflow context adopted as proof.
const SubscriptionWorkflowContext = "members.subscription"

func subStatusString(s SubscriptionStatus) string { return strconv.Itoa(int(s)) }

// SubscriptionWorkflow returns the code rule table for subscriptions:
// draft→validated and validated→paid announce hooks (paid additionally
// stamps the payload settled), any→canceled announces cancellation.
func SubscriptionWorkflow() *workflow.Engine {
	e, err := workflow.New(
		workflow.Rule{Context: SubscriptionWorkflowContext, From: "0", To: "1",
			Actions: []workflow.Action{
				{Kind: workflow.ActionHook, Target: "forgeerp.members.subscription.validated.v1"},
			}},
		workflow.Rule{Context: SubscriptionWorkflowContext, From: "1", To: "2",
			Actions: []workflow.Action{
				{Kind: workflow.ActionHook, Target: "forgeerp.members.subscription.paid.v1"},
				{Kind: workflow.ActionFieldSet, Fields: map[string]any{"settled": true}},
			}},
		workflow.Rule{Context: SubscriptionWorkflowContext, From: "*", To: "-1",
			Actions: []workflow.Action{
				{Kind: workflow.ActionHook, Target: "forgeerp.members.subscription.canceled.v1"},
			}},
	)
	if err != nil {
		panic(err) // static table, validated at development time
	}
	return e
}

// fireSubscription runs the engine for a subscription move and returns the
// fired actions (nil-safe engine: nil behaves as "no automatic actions").
func fireSubscription(e *workflow.Engine, entityID, id int64, from, to SubscriptionStatus, base map[string]any) ([]workflow.Action, map[string]any) {
	if e == nil {
		e = SubscriptionWorkflow()
	}
	actions := e.Fire(workflow.Transition{
		Context: SubscriptionWorkflowContext, EntityID: entityID, ID: id,
		From: subStatusString(from), To: subStatusString(to), Payload: base,
	})
	return actions, workflow.ApplyFields(base, actions)
}
