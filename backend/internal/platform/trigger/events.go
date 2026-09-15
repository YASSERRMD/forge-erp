package trigger

// Event is one catalogue occurrence: a Go type per event carrying its
// catalogue name, definition, tenant, object identity and payload.
type Event interface {
	// EventName is the catalogue name, e.g. BILL_VALIDATE.
	EventName() string
	// Def is the catalogue definition for this event.
	Def() Definition
	// EntityID is the tenant scope (always > 0).
	EntityID() int64
	// ObjectID is the business row id carried as payload "id" (always > 0).
	ObjectID() int64
	// Payload is the JSON-able event body (money stays int64 minor units).
	Payload() map[string]any
}

// Definitions — one var per catalogue event so typed events bind without a
// registry lookup. Subjects for pre-existing ad-hoc events reuse the exact
// strings already on the bus (notably EXPENSE_PAID).

var (
	DefProposalValidate       = Definition{Name: "PROPOSAL_VALIDATE", Subject: "forgeerp.sales.proposal.validated.v1", Entity: "proposal", Version: 1, Schema: map[string]string{"entity_id": "int64", "id": "int64", "ref": "string", "org_id": "int64", "total_minor": "int64", "currency": "string"}}
	DefProposalClose          = Definition{Name: "PROPOSAL_CLOSE", Subject: "forgeerp.sales.proposal.closed.v1", Entity: "proposal", Version: 1, Schema: map[string]string{"entity_id": "int64", "id": "int64", "ref": "string", "org_id": "int64", "status": "string"}}
	DefOrderValidate          = Definition{Name: "ORDER_VALIDATE", Subject: "forgeerp.sales.order.validated.v1", Entity: "order", Version: 1, Schema: map[string]string{"entity_id": "int64", "id": "int64", "ref": "string", "org_id": "int64", "total_minor": "int64", "currency": "string"}}
	DefOrderClose             = Definition{Name: "ORDER_CLOSE", Subject: "forgeerp.sales.order.closed.v1", Entity: "order", Version: 1, Schema: map[string]string{"entity_id": "int64", "id": "int64", "ref": "string", "org_id": "int64", "status": "string"}}
	DefBillValidate           = Definition{Name: "BILL_VALIDATE", Subject: "forgeerp.sales.invoice.validated.v1", Entity: "invoice", Version: 1, Schema: map[string]string{"entity_id": "int64", "id": "int64", "ref": "string", "org_id": "int64", "total_minor": "int64", "currency": "string"}}
	DefBillPayed              = Definition{Name: "BILL_PAYED", Subject: "forgeerp.sales.invoice.paid.v1", Entity: "invoice", Version: 1, Schema: map[string]string{"entity_id": "int64", "id": "int64", "ref": "string", "org_id": "int64", "amount_minor": "int64", "currency": "string"}}
	DefPaymentCreated         = Definition{Name: "PAYMENT_CREATED", Subject: "forgeerp.sales.payment.created.v1", Entity: "payment", Version: 1, Schema: map[string]string{"entity_id": "int64", "id": "int64", "org_id": "int64", "amount_minor": "int64", "currency": "string", "method": "string"}}
	DefLeaveSubmitted         = Definition{Name: "LEAVE_SUBMITTED", Subject: "forgeerp.hr.leave.submitted.v1", Entity: "leave", Version: 1, Schema: map[string]string{"entity_id": "int64", "id": "int64", "user_login": "string", "type": "string", "days": "int64"}}
	DefLeaveApproved          = Definition{Name: "LEAVE_APPROVED", Subject: "forgeerp.hr.leave.approved.v1", Entity: "leave", Version: 1, Schema: map[string]string{"entity_id": "int64", "id": "int64", "user_login": "string", "type": "string", "days": "int64"}}
	DefExpenseReportSubmitted = Definition{Name: "EXPENSE_REPORT_SUBMITTED", Subject: "forgeerp.hr.expense.submitted.v1", Entity: "expense", Version: 1, Schema: map[string]string{"entity_id": "int64", "id": "int64", "ref": "string", "user_login": "string", "total_minor": "int64"}}
	DefExpensePaid            = Definition{Name: "EXPENSE_PAID", Subject: "forgeerp.hr.expense.paid.v1", Entity: "expense", Version: 1, Schema: map[string]string{"entity_id": "int64", "id": "int64", "ref": "string", "user_login": "string", "total_minor": "int64"}}
	DefSalaryValidated        = Definition{Name: "SALARY_VALIDATED", Subject: "forgeerp.hr.salary.validated.v1", Entity: "salary", Version: 1, Schema: map[string]string{"entity_id": "int64", "id": "int64", "user_login": "string", "period": "string", "net_minor": "int64"}}
	DefMemberValidate         = Definition{Name: "MEMBER_VALIDATE", Subject: "forgeerp.members.validated.v1", Entity: "member", Version: 1, Schema: map[string]string{"entity_id": "int64", "id": "int64", "ref": "string", "label": "string"}}
	DefMemberSubscription     = Definition{Name: "MEMBER_SUBSCRIPTION", Subject: "forgeerp.members.subscription.v1", Entity: "subscription", Version: 1, Schema: map[string]string{"entity_id": "int64", "id": "int64", "member_id": "int64", "period": "string", "amount_minor": "int64"}}
	DefDonationCreated        = Definition{Name: "DONATION_CREATED", Subject: "forgeerp.members.donation.created.v1", Entity: "donation", Version: 1, Schema: map[string]string{"entity_id": "int64", "id": "int64", "donor": "string", "amount_minor": "int64"}}
	DefPOSSaleCompleted       = Definition{Name: "POS_SALE_COMPLETED", Subject: "forgeerp.pos.sale.completed.v1", Entity: "sale", Version: 1, Schema: map[string]string{"entity_id": "int64", "id": "int64", "ref": "string", "total_minor": "int64", "terminal_id": "int64"}}
	DefPOSSaleReturned        = Definition{Name: "POS_SALE_RETURNED", Subject: "forgeerp.pos.sale.returned.v1", Entity: "sale", Version: 1, Schema: map[string]string{"entity_id": "int64", "id": "int64", "ref": "string", "total_minor": "int64", "terminal_id": "int64"}}
	DefSupplierOrderValidate  = Definition{Name: "SUPPLIER_ORDER_VALIDATE", Subject: "forgeerp.procurement.order.validated.v1", Entity: "order", Version: 1, Schema: map[string]string{"entity_id": "int64", "id": "int64", "ref": "string", "org_id": "int64", "total_minor": "int64"}}
	DefSupplierOrderReceived  = Definition{Name: "SUPPLIER_ORDER_RECEIVED", Subject: "forgeerp.procurement.order.received.v1", Entity: "order", Version: 1, Schema: map[string]string{"entity_id": "int64", "id": "int64", "ref": "string", "org_id": "int64"}}
	DefReceptionReceived      = Definition{Name: "RECEPTION_RECEIVED", Subject: "forgeerp.procurement.reception.received.v1", Entity: "reception", Version: 1, Schema: map[string]string{"entity_id": "int64", "id": "int64", "ref": "string", "order_id": "int64"}}
)

// Typed events. Amounts stay int64 minor units; every payload carries "id".

// ProposalValidate is PROPOSAL_VALIDATE.
type ProposalValidate struct {
	Entity, ProposalID, OrgID int64
	Ref, Currency             string
	Total                     int64
}

func (ProposalValidate) EventName() string { return "PROPOSAL_VALIDATE" }
func (ProposalValidate) Def() Definition   { return DefProposalValidate }
func (e ProposalValidate) EntityID() int64 { return e.Entity }
func (e ProposalValidate) ObjectID() int64 { return e.ProposalID }
func (e ProposalValidate) Payload() map[string]any {
	return map[string]any{"id": e.ProposalID, "ref": e.Ref, "org_id": e.OrgID, "total_minor": e.Total, "currency": e.Currency}
}

// ProposalClose is PROPOSAL_CLOSE.
type ProposalClose struct {
	Entity, ProposalID, OrgID int64
	Ref, Status               string
}

func (ProposalClose) EventName() string { return "PROPOSAL_CLOSE" }
func (ProposalClose) Def() Definition   { return DefProposalClose }
func (e ProposalClose) EntityID() int64 { return e.Entity }
func (e ProposalClose) ObjectID() int64 { return e.ProposalID }
func (e ProposalClose) Payload() map[string]any {
	return map[string]any{"id": e.ProposalID, "ref": e.Ref, "org_id": e.OrgID, "status": e.Status}
}

// OrderValidate is ORDER_VALIDATE.
type OrderValidate struct {
	Entity, OrderID, OrgID int64
	Ref, Currency          string
	Total                  int64
}

func (OrderValidate) EventName() string { return "ORDER_VALIDATE" }
func (OrderValidate) Def() Definition   { return DefOrderValidate }
func (e OrderValidate) EntityID() int64 { return e.Entity }
func (e OrderValidate) ObjectID() int64 { return e.OrderID }
func (e OrderValidate) Payload() map[string]any {
	return map[string]any{"id": e.OrderID, "ref": e.Ref, "org_id": e.OrgID, "total_minor": e.Total, "currency": e.Currency}
}

// OrderClose is ORDER_CLOSE.
type OrderClose struct {
	Entity, OrderID, OrgID int64
	Ref, Status            string
}

func (OrderClose) EventName() string { return "ORDER_CLOSE" }
func (OrderClose) Def() Definition   { return DefOrderClose }
func (e OrderClose) EntityID() int64 { return e.Entity }
func (e OrderClose) ObjectID() int64 { return e.OrderID }
func (e OrderClose) Payload() map[string]any {
	return map[string]any{"id": e.OrderID, "ref": e.Ref, "org_id": e.OrgID, "status": e.Status}
}

// BillValidate is BILL_VALIDATE.
type BillValidate struct {
	Entity, InvoiceID, OrgID int64
	Ref, Currency            string
	Total                    int64
}

func (BillValidate) EventName() string { return "BILL_VALIDATE" }
func (BillValidate) Def() Definition   { return DefBillValidate }
func (e BillValidate) EntityID() int64 { return e.Entity }
func (e BillValidate) ObjectID() int64 { return e.InvoiceID }
func (e BillValidate) Payload() map[string]any {
	return map[string]any{"id": e.InvoiceID, "ref": e.Ref, "org_id": e.OrgID, "total_minor": e.Total, "currency": e.Currency}
}

// BillPayed is BILL_PAYED (Dolibarr spelling).
type BillPayed struct {
	Entity, InvoiceID, OrgID int64
	Ref, Currency            string
	Amount                   int64
}

func (BillPayed) EventName() string { return "BILL_PAYED" }
func (BillPayed) Def() Definition   { return DefBillPayed }
func (e BillPayed) EntityID() int64 { return e.Entity }
func (e BillPayed) ObjectID() int64 { return e.InvoiceID }
func (e BillPayed) Payload() map[string]any {
	return map[string]any{"id": e.InvoiceID, "ref": e.Ref, "org_id": e.OrgID, "amount_minor": e.Amount, "currency": e.Currency}
}

// PaymentCreated is PAYMENT_CREATED.
type PaymentCreated struct {
	Entity, PaymentID, OrgID int64
	Amount                   int64
	Currency, Method         string
}

func (PaymentCreated) EventName() string { return "PAYMENT_CREATED" }
func (PaymentCreated) Def() Definition   { return DefPaymentCreated }
func (e PaymentCreated) EntityID() int64 { return e.Entity }
func (e PaymentCreated) ObjectID() int64 { return e.PaymentID }
func (e PaymentCreated) Payload() map[string]any {
	return map[string]any{"id": e.PaymentID, "org_id": e.OrgID, "amount_minor": e.Amount, "currency": e.Currency, "method": e.Method}
}

// LeaveSubmitted is LEAVE_SUBMITTED.
type LeaveSubmitted struct {
	Entity, LeaveID int64
	UserLogin       string
	Type            string
	Days            int64
}

func (LeaveSubmitted) EventName() string { return "LEAVE_SUBMITTED" }
func (LeaveSubmitted) Def() Definition   { return DefLeaveSubmitted }
func (e LeaveSubmitted) EntityID() int64 { return e.Entity }
func (e LeaveSubmitted) ObjectID() int64 { return e.LeaveID }
func (e LeaveSubmitted) Payload() map[string]any {
	return map[string]any{"id": e.LeaveID, "user_login": e.UserLogin, "type": e.Type, "days": e.Days}
}

// LeaveApproved is LEAVE_APPROVED.
type LeaveApproved struct {
	Entity, LeaveID int64
	UserLogin       string
	Type            string
	Days            int64
}

func (LeaveApproved) EventName() string { return "LEAVE_APPROVED" }
func (LeaveApproved) Def() Definition   { return DefLeaveApproved }
func (e LeaveApproved) EntityID() int64 { return e.Entity }
func (e LeaveApproved) ObjectID() int64 { return e.LeaveID }
func (e LeaveApproved) Payload() map[string]any {
	return map[string]any{"id": e.LeaveID, "user_login": e.UserLogin, "type": e.Type, "days": e.Days}
}

// ExpenseReportSubmitted is EXPENSE_REPORT_SUBMITTED.
type ExpenseReportSubmitted struct {
	Entity, ExpenseID int64
	Ref, UserLogin    string
	Total             int64
}

func (ExpenseReportSubmitted) EventName() string { return "EXPENSE_REPORT_SUBMITTED" }
func (ExpenseReportSubmitted) Def() Definition   { return DefExpenseReportSubmitted }
func (e ExpenseReportSubmitted) EntityID() int64 { return e.Entity }
func (e ExpenseReportSubmitted) ObjectID() int64 { return e.ExpenseID }
func (e ExpenseReportSubmitted) Payload() map[string]any {
	return map[string]any{"id": e.ExpenseID, "ref": e.Ref, "user_login": e.UserLogin, "total_minor": e.Total}
}

// ExpensePaid is EXPENSE_PAID (subject-compatible with the legacy
// forgeerp.hr.expense.paid.v1 publish from hr.Service.Pay).
type ExpensePaid struct {
	Entity, ExpenseID int64
	Ref, UserLogin    string
	Total             int64
}

func (ExpensePaid) EventName() string { return "EXPENSE_PAID" }
func (ExpensePaid) Def() Definition   { return DefExpensePaid }
func (e ExpensePaid) EntityID() int64 { return e.Entity }
func (e ExpensePaid) ObjectID() int64 { return e.ExpenseID }
func (e ExpensePaid) Payload() map[string]any {
	return map[string]any{"id": e.ExpenseID, "ref": e.Ref, "user_login": e.UserLogin, "total_minor": e.Total}
}

// SalaryValidated is SALARY_VALIDATED.
type SalaryValidated struct {
	Entity, SalaryID int64
	UserLogin        string
	Period           string
	Net              int64
}

func (SalaryValidated) EventName() string { return "SALARY_VALIDATED" }
func (SalaryValidated) Def() Definition   { return DefSalaryValidated }
func (e SalaryValidated) EntityID() int64 { return e.Entity }
func (e SalaryValidated) ObjectID() int64 { return e.SalaryID }
func (e SalaryValidated) Payload() map[string]any {
	return map[string]any{"id": e.SalaryID, "user_login": e.UserLogin, "period": e.Period, "net_minor": e.Net}
}

// MemberValidate is MEMBER_VALIDATE.
type MemberValidate struct {
	Entity, MemberID int64
	Ref, Label       string
}

func (MemberValidate) EventName() string { return "MEMBER_VALIDATE" }
func (MemberValidate) Def() Definition   { return DefMemberValidate }
func (e MemberValidate) EntityID() int64 { return e.Entity }
func (e MemberValidate) ObjectID() int64 { return e.MemberID }
func (e MemberValidate) Payload() map[string]any {
	return map[string]any{"id": e.MemberID, "ref": e.Ref, "label": e.Label}
}

// MemberSubscription is MEMBER_SUBSCRIPTION.
type MemberSubscription struct {
	Entity, SubscriptionID, MemberID int64
	Period                           string
	Amount                           int64
}

func (MemberSubscription) EventName() string { return "MEMBER_SUBSCRIPTION" }
func (MemberSubscription) Def() Definition   { return DefMemberSubscription }
func (e MemberSubscription) EntityID() int64 { return e.Entity }
func (e MemberSubscription) ObjectID() int64 { return e.SubscriptionID }
func (e MemberSubscription) Payload() map[string]any {
	return map[string]any{"id": e.SubscriptionID, "member_id": e.MemberID, "period": e.Period, "amount_minor": e.Amount}
}

// DonationCreated is DONATION_CREATED.
type DonationCreated struct {
	Entity, DonationID int64
	Donor              string
	Amount             int64
}

func (DonationCreated) EventName() string { return "DONATION_CREATED" }
func (DonationCreated) Def() Definition   { return DefDonationCreated }
func (e DonationCreated) EntityID() int64 { return e.Entity }
func (e DonationCreated) ObjectID() int64 { return e.DonationID }
func (e DonationCreated) Payload() map[string]any {
	return map[string]any{"id": e.DonationID, "donor": e.Donor, "amount_minor": e.Amount}
}

// POSSaleCompleted is POS_SALE_COMPLETED.
type POSSaleCompleted struct {
	Entity, SaleID, TerminalID int64
	Ref                        string
	Total                      int64
}

func (POSSaleCompleted) EventName() string { return "POS_SALE_COMPLETED" }
func (POSSaleCompleted) Def() Definition   { return DefPOSSaleCompleted }
func (e POSSaleCompleted) EntityID() int64 { return e.Entity }
func (e POSSaleCompleted) ObjectID() int64 { return e.SaleID }
func (e POSSaleCompleted) Payload() map[string]any {
	return map[string]any{"id": e.SaleID, "ref": e.Ref, "total_minor": e.Total, "terminal_id": e.TerminalID}
}

// POSSaleReturned is POS_SALE_RETURNED.
type POSSaleReturned struct {
	Entity, SaleID, TerminalID int64
	Ref                        string
	Total                      int64
}

func (POSSaleReturned) EventName() string { return "POS_SALE_RETURNED" }
func (POSSaleReturned) Def() Definition   { return DefPOSSaleReturned }
func (e POSSaleReturned) EntityID() int64 { return e.Entity }
func (e POSSaleReturned) ObjectID() int64 { return e.SaleID }
func (e POSSaleReturned) Payload() map[string]any {
	return map[string]any{"id": e.SaleID, "ref": e.Ref, "total_minor": e.Total, "terminal_id": e.TerminalID}
}

// SupplierOrderValidate is SUPPLIER_ORDER_VALIDATE.
type SupplierOrderValidate struct {
	Entity, OrderID, OrgID int64
	Ref                    string
	Total                  int64
}

func (SupplierOrderValidate) EventName() string { return "SUPPLIER_ORDER_VALIDATE" }
func (SupplierOrderValidate) Def() Definition   { return DefSupplierOrderValidate }
func (e SupplierOrderValidate) EntityID() int64 { return e.Entity }
func (e SupplierOrderValidate) ObjectID() int64 { return e.OrderID }
func (e SupplierOrderValidate) Payload() map[string]any {
	return map[string]any{"id": e.OrderID, "ref": e.Ref, "org_id": e.OrgID, "total_minor": e.Total}
}

// SupplierOrderReceived is SUPPLIER_ORDER_RECEIVED.
type SupplierOrderReceived struct {
	Entity, OrderID, OrgID int64
	Ref                    string
}

func (SupplierOrderReceived) EventName() string { return "SUPPLIER_ORDER_RECEIVED" }
func (SupplierOrderReceived) Def() Definition   { return DefSupplierOrderReceived }
func (e SupplierOrderReceived) EntityID() int64 { return e.Entity }
func (e SupplierOrderReceived) ObjectID() int64 { return e.OrderID }
func (e SupplierOrderReceived) Payload() map[string]any {
	return map[string]any{"id": e.OrderID, "ref": e.Ref, "org_id": e.OrgID}
}

// ReceptionReceived is RECEPTION_RECEIVED.
type ReceptionReceived struct {
	Entity, ReceptionID, OrderID int64
	Ref                          string
}

func (ReceptionReceived) EventName() string { return "RECEPTION_RECEIVED" }
func (ReceptionReceived) Def() Definition   { return DefReceptionReceived }
func (e ReceptionReceived) EntityID() int64 { return e.Entity }
func (e ReceptionReceived) ObjectID() int64 { return e.ReceptionID }
func (e ReceptionReceived) Payload() map[string]any {
	return map[string]any{"id": e.ReceptionID, "ref": e.Ref, "order_id": e.OrderID}
}

// Compile-time proof that every typed event implements Event.
var (
	_ Event = ProposalValidate{}
	_ Event = ProposalClose{}
	_ Event = OrderValidate{}
	_ Event = OrderClose{}
	_ Event = BillValidate{}
	_ Event = BillPayed{}
	_ Event = PaymentCreated{}
	_ Event = LeaveSubmitted{}
	_ Event = LeaveApproved{}
	_ Event = ExpenseReportSubmitted{}
	_ Event = ExpensePaid{}
	_ Event = SalaryValidated{}
	_ Event = MemberValidate{}
	_ Event = MemberSubscription{}
	_ Event = DonationCreated{}
	_ Event = POSSaleCompleted{}
	_ Event = POSSaleReturned{}
	_ Event = SupplierOrderValidate{}
	_ Event = SupplierOrderReceived{}
	_ Event = ReceptionReceived{}
)

// Samples returns one instance per catalogue event (docs, tests, fixtures).
func Samples() []Event {
	return []Event{
		ProposalValidate{Entity: 1, ProposalID: 1, OrgID: 1, Ref: "P-1", Currency: "USD", Total: 100},
		ProposalClose{Entity: 1, ProposalID: 1, OrgID: 1, Ref: "P-1", Status: "signed"},
		OrderValidate{Entity: 1, OrderID: 1, OrgID: 1, Ref: "O-1", Currency: "USD", Total: 100},
		OrderClose{Entity: 1, OrderID: 1, OrgID: 1, Ref: "O-1", Status: "closed"},
		BillValidate{Entity: 1, InvoiceID: 1, OrgID: 1, Ref: "I-1", Currency: "USD", Total: 100},
		BillPayed{Entity: 1, InvoiceID: 1, OrgID: 1, Ref: "I-1", Currency: "USD", Amount: 100},
		PaymentCreated{Entity: 1, PaymentID: 1, OrgID: 1, Amount: 100, Currency: "USD", Method: "transfer"},
		LeaveSubmitted{Entity: 1, LeaveID: 1, UserLogin: "ada", Type: "paid", Days: 2},
		LeaveApproved{Entity: 1, LeaveID: 1, UserLogin: "ada", Type: "paid", Days: 2},
		ExpenseReportSubmitted{Entity: 1, ExpenseID: 1, Ref: "EXP-1", UserLogin: "ada", Total: 100},
		ExpensePaid{Entity: 1, ExpenseID: 1, Ref: "EXP-1", UserLogin: "ada", Total: 100},
		SalaryValidated{Entity: 1, SalaryID: 1, UserLogin: "ada", Period: "2026-09", Net: 800},
		MemberValidate{Entity: 1, MemberID: 1, Ref: "M-1", Label: "Ada"},
		MemberSubscription{Entity: 1, SubscriptionID: 1, MemberID: 1, Period: "2026", Amount: 5000},
		DonationCreated{Entity: 1, DonationID: 1, Donor: "Ada", Amount: 5000},
		POSSaleCompleted{Entity: 1, SaleID: 1, TerminalID: 1, Ref: "S-1", Total: 100},
		POSSaleReturned{Entity: 1, SaleID: 1, TerminalID: 1, Ref: "S-1", Total: 100},
		SupplierOrderValidate{Entity: 1, OrderID: 1, OrgID: 1, Ref: "SO-1", Total: 100},
		SupplierOrderReceived{Entity: 1, OrderID: 1, OrgID: 1, Ref: "SO-1"},
		ReceptionReceived{Entity: 1, ReceptionID: 1, OrderID: 1, Ref: "R-1"},
	}
}
