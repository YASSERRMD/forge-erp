// Package survey implements opinion surveys (Dolibarr opensurvey): surveys
// with questions and options, single/multi-choice voting with one ballot per
// user per question, and tallied results. Surveys open and close explicitly;
// closed surveys reject votes.
package survey

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Survey status.
type SurveyStatus int16

const (
	SurveyDraft  SurveyStatus = 0
	SurveyOpen   SurveyStatus = 1
	SurveyClosed SurveyStatus = 2
)

// Survey is one questionnaire.
type Survey struct {
	ID          int64        `json:"id"`
	EntityID    int64        `json:"entity_id"`
	Title       string       `json:"title"`
	Description string       `json:"description"`
	Status      SurveyStatus `json:"status"`
	CreatedBy   string       `json:"created_by"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
	RowVersion  int64        `json:"row_version"`
}

// Validate checks survey invariants.
func (s Survey) Validate() error {
	if s.EntityID <= 0 {
		return errors.New("survey: entity_id required")
	}
	if strings.TrimSpace(s.Title) == "" {
		return errors.New("survey: title required")
	}
	return nil
}

// CanTransition reports whether a survey status change is legal.
func (s Survey) CanTransition(to SurveyStatus) bool {
	switch s.Status {
	case SurveyDraft:
		return to == SurveyOpen
	case SurveyOpen:
		return to == SurveyClosed
	default:
		return false
	}
}

// Question is one survey question.
type Question struct {
	ID       int64  `json:"id"`
	EntityID int64  `json:"entity_id"`
	SurveyID int64  `json:"survey_id"`
	Text     string `json:"text"`
	Multi    bool   `json:"multi"` // true = several options per ballot
	Position int32  `json:"position"`
}

// Validate checks question invariants.
func (q Question) Validate() error {
	if q.EntityID <= 0 || q.SurveyID <= 0 {
		return errors.New("survey: entity_id and survey_id required")
	}
	if strings.TrimSpace(q.Text) == "" {
		return errors.New("survey: text required")
	}
	return nil
}

// Option is one answer choice.
type Option struct {
	ID         int64  `json:"id"`
	EntityID   int64  `json:"entity_id"`
	QuestionID int64  `json:"question_id"`
	Label      string `json:"label"`
	Position   int32  `json:"position"`
}

// Validate checks option invariants.
func (o Option) Validate() error {
	if o.EntityID <= 0 || o.QuestionID <= 0 {
		return errors.New("survey: entity_id and question_id required")
	}
	if strings.TrimSpace(o.Label) == "" {
		return errors.New("survey: label required")
	}
	return nil
}

// Vote is one ballot: a user picks options for a question. Single-choice
// questions accept exactly one option; multi-choice one or more. One ballot
// per user per question (re-vote replaces).
type Vote struct {
	ID         int64     `json:"id"`
	EntityID   int64     `json:"entity_id"`
	QuestionID int64     `json:"question_id"`
	UserLogin  string    `json:"user_login"`
	OptionIDs  []int64   `json:"option_ids"`
	CreatedAt  time.Time `json:"created_at"`
}

// Validate checks vote shape (ballot rules enforced by the store).
func (v Vote) Validate() error {
	if v.EntityID <= 0 || v.QuestionID <= 0 {
		return errors.New("survey: entity_id and question_id required")
	}
	if strings.TrimSpace(v.UserLogin) == "" {
		return errors.New("survey: user_login required")
	}
	if len(v.OptionIDs) == 0 {
		return errors.New("survey: at least one option required")
	}
	return nil
}

// Tally is one option's vote count.
type Tally struct {
	OptionID int64  `json:"option_id"`
	Label    string `json:"label"`
	Votes    int64  `json:"votes"`
}

// TallyCounts counts ballots per option.
func TallyCounts(options []Option, votes []Vote) []Tally {
	counts := map[int64]int64{}
	for _, v := range votes {
		seen := map[int64]bool{}
		for _, id := range v.OptionIDs {
			if !seen[id] {
				seen[id] = true
				counts[id]++
			}
		}
	}
	out := make([]Tally, 0, len(options))
	for _, o := range options {
		out = append(out, Tally{OptionID: o.ID, Label: o.Label, Votes: counts[o.ID]})
	}
	return out
}

// CheckBallot enforces single/multi rules for one ballot.
func CheckBallot(multi bool, optionIDs []int64, valid map[int64]bool) error {
	if !multi && len(optionIDs) != 1 {
		return fmt.Errorf("survey: single-choice needs exactly one option, got %d", len(optionIDs))
	}
	for _, id := range optionIDs {
		if !valid[id] {
			return fmt.Errorf("survey: option %d not part of the question", id)
		}
	}
	return nil
}
