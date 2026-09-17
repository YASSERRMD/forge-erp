package mailing

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/notify"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Service owns the campaign boundary: audience expansion, queueing, sending
// through the notify package (library use only), and unsubscribes.
type Service struct {
	Store  Store
	Sender notify.Sender // production: notify SMTP sender; tests: fake
	DB     platform.DBTX
	Now    func() time.Time
	Mint   func() string
	Bus    platform.Bus
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now().UTC()
}

func (s *Service) mint() string {
	if s.Mint != nil {
		return s.Mint()
	}
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (s *Service) published(ctx context.Context, entityID, id int64, subject, entity string) {
	if s.Bus == nil {
		return
	}
	_ = s.Bus.Publish(ctx, platform.Event{Subject: subject, Entity: entity, EntityID: entityID, ID: id})
}

// ExpandAndQueue resolves the audience (members/orgs mirrors + extras,
// minus suppressions) and queues one recipient per address with a fresh
// unsubscribe token. The campaign must be draft or queued.
func (s *Service) ExpandAndQueue(ctx context.Context, entityID, campaignID int64, aud Audience, members []MemberMirror, orgs []OrgMirror) (int, error) {
	c, err := s.Store.CampaignByID(ctx, s.DB, entityID, campaignID)
	if err != nil {
		return 0, err
	}
	if c.Status != CampaignDraft && c.Status != CampaignQueued {
		return 0, fmt.Errorf("mailing: campaign not queueable: %w", platform.ErrValidation)
	}
	supp, err := s.Store.SuppressedMap(ctx, s.DB, entityID)
	if err != nil {
		return 0, err
	}
	emails := ExpandAudience(aud, members, orgs, supp)
	queued := 0
	for _, e := range emails {
		r := &Recipient{EntityID: entityID, CampaignID: campaignID, Email: e,
			Token: s.mint(), Status: RecipientQueued}
		if err := s.Store.AddRecipient(ctx, s.DB, r); err != nil {
			// Re-queueing the same audience is idempotent: duplicates skip.
			continue
		}
		queued++
	}
	if _, err := s.Store.SetCampaignStatus(ctx, s.DB, entityID, campaignID, CampaignQueued, c.RowVersion); err != nil {
		return queued, err
	}
	s.published(ctx, entityID, campaignID, "forgeerp.mailing.campaign.queued.v1", "campaign")
	return queued, nil
}

// SendAll delivers every queued recipient via the notify Sender (through
// notify.Dispatcher for retry accounting) and marks the campaign done.
// Per-recipient status ends queued→sent or queued→failed (error recorded).
func (s *Service) SendAll(ctx context.Context, entityID, campaignID int64) (sent, failed int64, err error) {
	c, err := s.Store.CampaignByID(ctx, s.DB, entityID, campaignID)
	if err != nil {
		return 0, 0, err
	}
	if c.Status != CampaignQueued && c.Status != CampaignSending {
		return 0, 0, fmt.Errorf("mailing: campaign not sendable: %w", platform.ErrValidation)
	}
	if s.Sender == nil {
		return 0, 0, fmt.Errorf("mailing: no sender configured: %w", platform.ErrValidation)
	}
	c, err = s.Store.SetCampaignStatus(ctx, s.DB, entityID, campaignID, CampaignSending, c.RowVersion)
	if err != nil {
		return 0, 0, err
	}
	queued, err := s.Store.QueuedOf(ctx, s.DB, entityID, campaignID)
	if err != nil {
		return 0, 0, err
	}
	d := notify.Dispatcher{Sender: s.Sender, Now: s.now}
	for _, r := range queued {
		item := notify.OutboxItem{EntityID: entityID, Channel: "email",
			Recipient: r.Email, Subject: c.Subject, Body: c.Body}
		if d.Dispatch(ctx, &item) {
			_ = s.Store.SetRecipientStatus(ctx, s.DB, r.ID, RecipientSent, "")
			sent++
		} else {
			_ = s.Store.SetRecipientStatus(ctx, s.DB, r.ID, RecipientFailed, item.Error)
			failed++
		}
	}
	if _, err := s.Store.SetCampaignStatus(ctx, s.DB, entityID, campaignID, CampaignDone, c.RowVersion); err != nil {
		return sent, failed, err
	}
	s.published(ctx, entityID, campaignID, "forgeerp.mailing.campaign.sent.v1", "campaign")
	return sent, failed, nil
}

// Unsubscribe records an entity-scoped suppression for the address behind
// the token (future expansions skip it) and fails the token's queued rows.
func (s *Service) Unsubscribe(ctx context.Context, token string) error {
	r, err := s.Store.RecipientByToken(ctx, s.DB, token)
	if err != nil {
		return err
	}
	if err := s.Store.SuppressEmail(ctx, s.DB, r.EntityID, r.Email); err != nil {
		return err
	}
	if r.Status == RecipientQueued {
		_ = s.Store.SetRecipientStatus(ctx, s.DB, r.ID, RecipientFailed, "unsubscribed")
	}
	return nil
}
