package portal

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/documents"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/YASSERRMD/forge-erp/backend/internal/sales"
	"github.com/YASSERRMD/forge-erp/backend/internal/services"
)

// Service owns the customer self-service boundary: every read/write is
// scoped to the token's org (cross-customer isolation), enforced here — not
// left to handlers — so both HTTP and future callers share the guarantee.
type Service struct {
	Store    Store
	Sales    Sales
	Services Services
	Bus      platform.Bus
	DB       platform.DBTX
	Now      func() time.Time
	Logger   *slog.Logger
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now().UTC()
}

func (s *Service) log(ctx context.Context) *slog.Logger {
	base := s.Logger
	if base == nil {
		base = platform.Default()
	}
	return platform.ReqCtxLogger(ctx, base)
}

func (s *Service) published(ctx context.Context, entityID, id int64, subject, entity string) {
	if s.Bus == nil {
		return
	}
	_ = s.Bus.Publish(ctx, platform.Event{Subject: subject, Entity: entity, EntityID: entityID, ID: id})
}

// MintToken creates a customer credential bound to an org (staff-minted via
// the handler's RBAC endpoint). Returns the row plus the bearer credential
// ("<salt>.<secret>", salt non-secret per crypt/MCF practice), shown once —
// only the salted hash is persisted.
func (s *Service) MintToken(ctx context.Context, entityID, orgID int64, contactID *int64, ttl time.Duration) (PortalToken, string, error) {
	if entityID <= 0 || orgID <= 0 {
		return PortalToken{}, "", fmt.Errorf("portal: entity and org required: %w", platform.ErrValidation)
	}
	if ttl <= 0 {
		ttl = TokenTTL
	}
	raw, err := MintToken()
	if err != nil {
		return PortalToken{}, "", err
	}
	salt, err := MintSalt()
	if err != nil {
		return PortalToken{}, "", err
	}
	t := PortalToken{EntityID: entityID, OrgID: orgID, ContactID: contactID,
		Salt: salt, TokenHash: HashToken(salt, raw), ExpiresAt: s.now().Add(ttl)}
	if err := s.Store.CreateToken(ctx, s.DB, &t); err != nil {
		return PortalToken{}, "", err
	}
	s.log(ctx).Info("portal: token minted", "entity", entityID, "org", orgID, "token_id", t.ID)
	return t, salt + "." + raw, nil
}

// RevokeToken kills a credential (staff side; customers cannot revoke peers'
// tokens because revocation needs the row id + entity, never just a bearer).
func (s *Service) RevokeToken(ctx context.Context, entityID, id int64) error {
	return s.Store.RevokeToken(ctx, s.DB, entityID, id)
}

// ownDoc loads a sales document and enforces customer ownership: wrong org
// (or wrong entity) is ErrNotFound, never a distinct "forbidden" —
// cross-customer IDs must be undiscoverable.
func (s *Service) ownDoc(ctx context.Context, id Identity, docID int64) (sales.Document, error) {
	d, err := s.Sales.DocByID(ctx, s.DB, id.EntityID, docID)
	if err != nil {
		return sales.Document{}, err
	}
	if d.OrgID != id.OrgID {
		return sales.Document{}, fmt.Errorf("portal: document not found: %w", platform.ErrNotFound)
	}
	return d, nil
}

// ListInvoices returns the customer's own invoices (newest paging via the seam).
func (s *Service) ListInvoices(ctx context.Context, id Identity, limit, offset int) ([]sales.Document, error) {
	all, err := s.Sales.ListDocs(ctx, s.DB, id.EntityID, documents.TypeInvoice, limit, offset)
	if err != nil {
		return nil, err
	}
	mine := all[:0]
	for _, d := range all {
		if d.OrgID == id.OrgID {
			mine = append(mine, d)
		}
	}
	return mine, nil
}

// GetInvoice returns one own invoice.
func (s *Service) GetInvoice(ctx context.Context, id Identity, docID int64) (sales.Document, error) {
	d, err := s.ownDoc(ctx, id, docID)
	if err != nil {
		return sales.Document{}, err
	}
	if d.Type != documents.TypeInvoice {
		return sales.Document{}, fmt.Errorf("portal: not an invoice: %w", platform.ErrValidation)
	}
	return d, nil
}

// ListQuotes returns the customer's own proposals.
func (s *Service) ListQuotes(ctx context.Context, id Identity, limit, offset int) ([]sales.Document, error) {
	all, err := s.Sales.ListDocs(ctx, s.DB, id.EntityID, documents.TypeProposal, limit, offset)
	if err != nil {
		return nil, err
	}
	mine := all[:0]
	for _, d := range all {
		if d.OrgID == id.OrgID {
			mine = append(mine, d)
		}
	}
	return mine, nil
}

// AcceptQuote signs the customer's own validated proposal (proposal→signed
// through the sales seam's kernel transition table; drafts must be validated
// by staff first — the seam rejects anything else with ErrValidation).
func (s *Service) AcceptQuote(ctx context.Context, id Identity, docID int64) (sales.Document, error) {
	d, err := s.ownDoc(ctx, id, docID)
	if err != nil {
		return sales.Document{}, err
	}
	if d.Type != documents.TypeProposal {
		return sales.Document{}, fmt.Errorf("portal: not a quote: %w", platform.ErrValidation)
	}
	if d.Status != sales.ProposalValidated {
		return sales.Document{}, fmt.Errorf("portal: only validated quotes can be accepted: %w", platform.ErrValidation)
	}
	out, err := s.Sales.SetStatus(ctx, s.DB, id.EntityID, docID, sales.ProposalSigned)
	if err != nil {
		return sales.Document{}, err
	}
	s.log(ctx).Info("portal: quote accepted", "entity", id.EntityID, "org", id.OrgID, "doc", docID)
	s.published(ctx, id.EntityID, out.ID, "forgeerp.portal.quote.accepted.v1", "proposal")
	return out, nil
}

// OpenTicket files a support ticket as the customer's org. The ref is
// server-assigned (TICK-PORTAL-<nanosuffix> is unique per entity call; the
// seam rejects duplicates with ErrConflict, surfaced as-is).
func (s *Service) OpenTicket(ctx context.Context, id Identity, subject string, priority int16) (services.Ticket, error) {
	if subject == "" {
		return services.Ticket{}, fmt.Errorf("portal: subject required: %w", platform.ErrValidation)
	}
	if priority < 1 || priority > 4 {
		priority = 2
	}
	org := id.OrgID
	t := &services.Ticket{EntityID: id.EntityID, OrgID: &org, Subject: subject,
		Priority: priority, Status: services.TicketOpen,
		Ref: fmt.Sprintf("TICK-PORTAL-%d", s.now().UnixNano())}
	if err := s.Services.CreateTicket(ctx, s.DB, t); err != nil {
		return services.Ticket{}, err
	}
	s.log(ctx).Info("portal: ticket opened", "entity", id.EntityID, "org", id.OrgID, "ticket", t.ID)
	s.published(ctx, id.EntityID, t.ID, "forgeerp.portal.ticket.opened.v1", "ticket")
	return *t, nil
}

// ListTickets returns the customer's own tickets.
func (s *Service) ListTickets(ctx context.Context, id Identity, limit, offset int) ([]services.Ticket, error) {
	all, err := s.Services.ListTickets(ctx, s.DB, id.EntityID, limit, offset)
	if err != nil {
		return nil, err
	}
	mine := all[:0]
	for _, t := range all {
		if t.OrgID != nil && *t.OrgID == id.OrgID {
			mine = append(mine, t)
		}
	}
	return mine, nil
}

// GetTicket returns one own ticket.
func (s *Service) GetTicket(ctx context.Context, id Identity, ticketID int64) (services.Ticket, error) {
	t, err := s.Services.TicketByID(ctx, s.DB, id.EntityID, ticketID)
	if err != nil {
		return services.Ticket{}, err
	}
	if t.OrgID == nil || *t.OrgID != id.OrgID {
		return services.Ticket{}, fmt.Errorf("portal: ticket not found: %w", platform.ErrNotFound)
	}
	return t, nil
}
