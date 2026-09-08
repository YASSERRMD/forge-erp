package survey

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

func passthrough(_, _, _ string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler { return next }
}

func TestBallotRulesAndTally(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()
	sv := &Survey{EntityID: 1, Title: "Lunch poll"}
	if err := m.CreateSurvey(ctx, sv); err != nil {
		t.Fatalf("survey: %v", err)
	}
	q := &Question{EntityID: 1, SurveyID: sv.ID, Text: "Where?", Multi: false}
	if err := m.AddQuestion(ctx, q); err != nil {
		t.Fatalf("question: %v", err)
	}
	var optIDs []int64
	for _, label := range []string{"Tacos", "Sushi"} {
		o := &Option{EntityID: 1, QuestionID: q.ID, Label: label}
		if err := m.AddOption(ctx, o); err != nil {
			t.Fatalf("option: %v", err)
		}
		optIDs = append(optIDs, o.ID)
	}
	// Draft survey rejects votes.
	if err := m.CastVote(ctx, &Vote{EntityID: 1, QuestionID: q.ID,
		UserLogin: "ada", OptionIDs: []int64{optIDs[0]}}); err == nil {
		t.Error("vote on draft accepted")
	}
	upd, err := m.SetSurveyStatus(ctx, sv.ID, SurveyOpen, sv.RowVersion)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_ = upd
	// Single-choice with two options rejected.
	if err := m.CastVote(ctx, &Vote{EntityID: 1, QuestionID: q.ID,
		UserLogin: "ada", OptionIDs: optIDs}); err == nil {
		t.Error("multi ballot on single-choice accepted")
	}
	// Unknown option rejected.
	if err := m.CastVote(ctx, &Vote{EntityID: 1, QuestionID: q.ID,
		UserLogin: "ada", OptionIDs: []int64{999}}); err == nil {
		t.Error("unknown option accepted")
	}
	// Two valid ballots, then ada re-votes (replaces).
	for _, v := range []Vote{
		{EntityID: 1, QuestionID: q.ID, UserLogin: "ada", OptionIDs: []int64{optIDs[0]}},
		{EntityID: 1, QuestionID: q.ID, UserLogin: "bob", OptionIDs: []int64{optIDs[1]}},
		{EntityID: 1, QuestionID: q.ID, UserLogin: "ada", OptionIDs: []int64{optIDs[1]}},
	} {
		v := v
		if err := m.CastVote(ctx, &v); err != nil {
			t.Fatalf("vote: %v", err)
		}
	}
	tally, err := m.Results(ctx, q.ID)
	if err != nil {
		t.Fatalf("results: %v", err)
	}
	byLabel := map[string]int64{}
	for _, tly := range tally {
		byLabel[tly.Label] = tly.Votes
	}
	if len(tally) != 2 || byLabel["Tacos"] != 0 || byLabel["Sushi"] != 2 {
		t.Fatalf("tally=%+v want Tacos 0 / Sushi 2", tally)
	}
	// Closed survey rejects votes.
	upd, _ = m.SetSurveyStatus(ctx, sv.ID, SurveyClosed, upd.RowVersion)
	_ = upd
	if err := m.CastVote(ctx, &Vote{EntityID: 1, QuestionID: q.ID,
		UserLogin: "cid", OptionIDs: []int64{optIDs[0]}}); err == nil {
		t.Error("vote on closed accepted")
	}
}

func TestSurveyAPI(t *testing.T) {
	st := NewMemoryStore()
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		Routes(r, Deps{Store: st}, passthrough)
	})
	post := func(path string, body any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	rec := post("/api/v1/surveys", map[string]any{"title": "Lunch"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("survey: code=%d", rec.Code)
	}
	var sv Survey
	_ = json.NewDecoder(rec.Body).Decode(&sv)
	rec = post(fmt.Sprintf("/api/v1/surveys/%d/questions", sv.ID),
		map[string]any{"text": "Where?", "multi": false})
	if rec.Code != http.StatusCreated {
		t.Fatalf("question: code=%d", rec.Code)
	}
	var q Question
	_ = json.NewDecoder(rec.Body).Decode(&q)
	var optID int64
	for _, label := range []string{"Tacos", "Sushi"} {
		rec = post(fmt.Sprintf("/api/v1/questions/%d/options", q.ID),
			map[string]any{"label": label})
		if rec.Code != http.StatusCreated {
			t.Fatalf("option: code=%d", rec.Code)
		}
		var o Option
		_ = json.NewDecoder(rec.Body).Decode(&o)
		optID = o.ID
	}
	// Vote while draft → 422.
	rec = post(fmt.Sprintf("/api/v1/questions/%d/votes", q.ID),
		map[string]any{"user_login": "ada", "option_ids": []int64{optID}})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("draft vote: code=%d want 422", rec.Code)
	}
	// Open, vote, results.
	rec = post(fmt.Sprintf("/api/v1/surveys/%d/status", sv.ID),
		map[string]any{"status": 1, "row_version": sv.RowVersion})
	if rec.Code != http.StatusOK {
		t.Fatalf("open: code=%d", rec.Code)
	}
	rec = post(fmt.Sprintf("/api/v1/questions/%d/votes", q.ID),
		map[string]any{"user_login": "ada", "option_ids": []int64{optID}})
	if rec.Code != http.StatusCreated {
		t.Fatalf("vote: code=%d body=%s", rec.Code, rec.Body.String())
	}
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/questions/%d/results", q.ID), nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var tally []Tally
	_ = json.NewDecoder(rec.Body).Decode(&tally)
	if len(tally) != 2 || tally[0].Votes+tally[1].Votes != 1 {
		t.Fatalf("tally=%+v", tally)
	}
}
