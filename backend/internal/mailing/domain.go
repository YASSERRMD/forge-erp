// Package mailing implements a campaign engine on top of notify (read-only
// library use — this package never modifies notify).
//
// Campaigns carry an audience query over members/orgs; the member/org rows
// themselves are mirrored locally (MemberMirror/OrgMirror seams — members and
// partners packages are NOT modified). Sending goes through the notify
// package's Sender/Dispatcher as a library; per-recipient status
// (queued/sent/failed) is tracked here, with per-recipient unsubscribe
// tokens and an entity-scoped suppression list.
package mailing

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Campaign statuses.
const (
	CampaignDraft   int16 = 0
	CampaignQueued  int16 = 1
	CampaignSending int16 = 2
	CampaignDone    int16 = 3
)

// Recipient statuses.
const (
	RecipientQueued = "queued"
	RecipientSent   = "sent"
	RecipientFailed = "failed"
)

// Campaign is one mail shot.
type Campaign struct {
	ID         int64  `json:"id"`
	EntityID   int64  `json:"entity_id"`
	Subject    string `json:"subject"`
	Body       string `json:"body"`
	Status     int16  `json:"status"`
	RowVersion int64  `json:"row_version"`
}

// Validate checks campaign invariants.
func (c Campaign) Validate() error {
	if c.EntityID <= 0 {
		return errors.New("mailing: entity_id required")
	}
	if strings.TrimSpace(c.Subject) == "" {
		return errors.New("mailing: subject required")
	}
	return nil
}

// Recipient is one addressed delivery with its unsubscribe token.
type Recipient struct {
	ID         int64  `json:"id"`
	EntityID   int64  `json:"entity_id"`
	CampaignID int64  `json:"campaign_id"`
	Email      string `json:"email"`
	Token      string `json:"token"`
	Status     string `json:"status"`
	Error      string `json:"error"`
}

// Validate checks recipient invariants.
func (r Recipient) Validate() error {
	if r.EntityID <= 0 || r.CampaignID <= 0 {
		return errors.New("mailing: entity_id and campaign_id required")
	}
	if !validEmail(r.Email) {
		return fmt.Errorf("mailing: bad email %q", r.Email)
	}
	if strings.TrimSpace(r.Token) == "" {
		return errors.New("mailing: unsubscribe token required")
	}
	return nil
}

func validEmail(e string) bool {
	e = strings.TrimSpace(e)
	return strings.Contains(e, "@") && strings.Contains(e, ".")
}

// MemberMirror is the local seam mirror of a member contact (members
// package untouched — callers project rows into this shape).
type MemberMirror struct {
	Email        string `json:"email"`
	Unsubscribed bool   `json:"unsubscribed"`
	TypeCode     string `json:"type_code"`
}

// OrgMirror is the local seam mirror of an organization contact (partners
// package untouched — callers project rows into this shape).
type OrgMirror struct {
	Email        string `json:"email"`
	Unsubscribed bool   `json:"unsubscribed"`
	Customer     bool   `json:"customer"`
}

// Audience selects recipients: members filtered by TypeCode (empty = all),
// customer orgs when CustomersOnly, plus explicit extra emails.
type Audience struct {
	MemberType    string   `json:"member_type"`
	CustomersOnly bool     `json:"customers_only"`
	ExtraEmails   []string `json:"extra_emails"`
}

// ExpandAudience resolves an audience into a deduplicated, sorted recipient
// list: unsubscribed mirrors and suppressed emails are skipped, invalid
// addresses dropped. Pure — fully testable with fakes.
func ExpandAudience(a Audience, members []MemberMirror, orgs []OrgMirror, suppressed map[string]bool) []string {
	set := map[string]bool{}
	add := func(email string) {
		e := strings.ToLower(strings.TrimSpace(email))
		if !validEmail(e) || set[e] || suppressed[e] {
			return
		}
		set[e] = true
	}
	for _, m := range members {
		if m.Unsubscribed {
			continue
		}
		if a.MemberType != "" && m.TypeCode != a.MemberType {
			continue
		}
		add(m.Email)
	}
	if a.CustomersOnly {
		for _, o := range orgs {
			if !o.Customer || o.Unsubscribed {
				continue
			}
			add(o.Email)
		}
	}
	for _, e := range a.ExtraEmails {
		add(e)
	}
	out := make([]string, 0, len(set))
	for e := range set {
		out = append(out, e)
	}
	sort.Strings(out)
	return out
}
