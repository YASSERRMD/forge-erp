package survey

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/YASSERRMD/forge-erp/backend/internal/identity"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the persistence contract for the survey context.
type Store interface {
	CreateSurvey(ctx context.Context, db platform.DBTX, s *Survey) error
	SurveyByID(ctx context.Context, db platform.DBTX, entityID int64, id int64) (Survey, error)
	ListSurveys(ctx context.Context, db platform.DBTX, entityID int64) ([]Survey, error)
	SetSurveyStatus(ctx context.Context, db platform.DBTX, entityID int64, id int64, to SurveyStatus, rowVersion int64) (Survey, error)
	AddQuestion(ctx context.Context, db platform.DBTX, q *Question) error
	QuestionsOf(ctx context.Context, db platform.DBTX, entityID int64, surveyID int64) ([]Question, error)
	AddOption(ctx context.Context, db platform.DBTX, o *Option) error
	OptionsOf(ctx context.Context, db platform.DBTX, entityID int64, questionID int64) ([]Option, error)
	CastVote(ctx context.Context, db platform.DBTX, v *Vote) error
	VotesOf(ctx context.Context, db platform.DBTX, entityID int64, questionID int64) ([]Vote, error)
	Results(ctx context.Context, db platform.DBTX, entityID int64, questionID int64) ([]Tally, error)
}

// PGStore implements Store against PostgreSQL.
type PGStore struct{ pool *pgxpool.Pool }

// NewPGStore wraps a pool.
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

const surveyCols = `id, entity_id, title, description, status, created_by, created_at, updated_at, row_version`

func scanSurvey(row pgx.Row) (Survey, error) {
	var s Survey
	err := row.Scan(&s.ID, &s.EntityID, &s.Title, &s.Description, &s.Status,
		&s.CreatedBy, &s.CreatedAt, &s.UpdatedAt, &s.RowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return Survey{}, identity.ErrNotFound
	}
	return s, err
}

func (s *PGStore) CreateSurvey(ctx context.Context, db platform.DBTX, sv *Survey) error {
	if err := sv.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	return db.QueryRow(ctx, `INSERT INTO ferp_surveys
		(entity_id, title, description, status, created_by)
		VALUES ($1,$2,$3,$4,$5) RETURNING id, row_version`,
		sv.EntityID, sv.Title, sv.Description, sv.Status, sv.CreatedBy,
	).Scan(&sv.ID, &sv.RowVersion)
}

func (s *PGStore) SurveyByID(ctx context.Context, db platform.DBTX, entityID int64, id int64) (Survey, error) {
	return scanSurvey(db.QueryRow(ctx, `SELECT `+surveyCols+` FROM ferp_surveys WHERE id=$1 AND entity_id=$2`, id, entityID))
}

func (s *PGStore) ListSurveys(ctx context.Context, db platform.DBTX, entityID int64) ([]Survey, error) {
	rows, err := db.Query(ctx, `SELECT `+surveyCols+` FROM ferp_surveys WHERE entity_id=$1 ORDER BY id`, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Survey
	for rows.Next() {
		sv, err := scanSurvey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sv)
	}
	return out, rows.Err()
}

func (s *PGStore) SetSurveyStatus(ctx context.Context, db platform.DBTX, entityID int64, id int64, to SurveyStatus, rowVersion int64) (Survey, error) {
	sv, err := s.SurveyByID(ctx, db, entityID, id)
	if err != nil {
		return Survey{}, err
	}
	if sv.RowVersion != rowVersion {
		return Survey{}, identity.ErrVersionConflict
	}
	if !sv.CanTransition(to) {
		return Survey{}, fmt.Errorf("survey: illegal transition: %w", platform.ErrValidation)
	}
	tag, err := db.Exec(ctx, `UPDATE ferp_surveys SET status=$1, updated_at=now(), row_version=row_version+1
		WHERE id=$2 AND entity_id=$4 AND row_version=$3`, to, id, rowVersion, entityID)
	if err != nil {
		return Survey{}, err
	}
	if tag.RowsAffected() == 0 {
		return Survey{}, identity.ErrVersionConflict
	}
	sv.Status = to
	sv.RowVersion++
	return sv, nil
}

func (s *PGStore) AddQuestion(ctx context.Context, db platform.DBTX, q *Question) error {
	if err := q.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	sv, err := s.SurveyByID(ctx, db, q.EntityID, q.SurveyID)
	if err != nil {
		return err
	}
	if sv.Status != SurveyDraft {
		return fmt.Errorf("survey: questions editable on drafts only: %w", platform.ErrValidation)
	}
	return db.QueryRow(ctx, `INSERT INTO ferp_survey_questions
		(entity_id, survey_id, text, multi, position)
		VALUES ($1,$2,$3,$4,$5) RETURNING id`,
		q.EntityID, q.SurveyID, q.Text, q.Multi, q.Position,
	).Scan(&q.ID)
}

func (s *PGStore) QuestionsOf(ctx context.Context, db platform.DBTX, entityID int64, surveyID int64) ([]Question, error) {
	rows, err := db.Query(ctx, `SELECT q.id, q.entity_id, q.survey_id, q.text, q.multi, q.position
		FROM ferp_survey_questions q JOIN ferp_surveys sv ON sv.id=q.survey_id
		WHERE q.survey_id=$1 AND sv.entity_id=$2 ORDER BY q.position, q.id`, surveyID, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Question
	for rows.Next() {
		var q Question
		if err := rows.Scan(&q.ID, &q.EntityID, &q.SurveyID, &q.Text, &q.Multi, &q.Position); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

// questionByID fetches one question within its tenant.
func (s *PGStore) questionByID(ctx context.Context, db platform.DBTX, entityID int64, questionID int64) (Question, error) {
	var q Question
	err := db.QueryRow(ctx, `SELECT id, entity_id, survey_id, text, multi, position
		FROM ferp_survey_questions WHERE id=$1 AND entity_id=$2`, questionID, entityID).Scan(
		&q.ID, &q.EntityID, &q.SurveyID, &q.Text, &q.Multi, &q.Position)
	if errors.Is(err, pgx.ErrNoRows) {
		return Question{}, identity.ErrNotFound
	}
	if err != nil {
		return Question{}, err
	}
	return q, nil
}

func (s *PGStore) AddOption(ctx context.Context, db platform.DBTX, o *Option) error {
	if err := o.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	if _, err := s.questionByID(ctx, db, o.EntityID, o.QuestionID); err != nil {
		return err
	}
	return db.QueryRow(ctx, `INSERT INTO ferp_survey_options
		(entity_id, question_id, label, position)
		VALUES ($1,$2,$3,$4) RETURNING id`,
		o.EntityID, o.QuestionID, o.Label, o.Position,
	).Scan(&o.ID)
}

func (s *PGStore) OptionsOf(ctx context.Context, db platform.DBTX, entityID int64, questionID int64) ([]Option, error) {
	rows, err := db.Query(ctx, `SELECT id, entity_id, question_id, label, position
		FROM ferp_survey_options WHERE question_id=$1 AND entity_id=$2 ORDER BY position, id`, questionID, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Option
	for rows.Next() {
		var o Option
		if err := rows.Scan(&o.ID, &o.EntityID, &o.QuestionID, &o.Label, &o.Position); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// CastVote records or replaces one ballot (one per user per question).
func (s *PGStore) CastVote(ctx context.Context, db platform.DBTX, v *Vote) error {
	if err := v.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	q, err := s.questionByID(ctx, db, v.EntityID, v.QuestionID)
	if err != nil {
		return err
	}
	sv, err := s.SurveyByID(ctx, db, v.EntityID, q.SurveyID)
	if err != nil {
		return err
	}
	if sv.Status != SurveyOpen {
		return fmt.Errorf("survey: voting open on open surveys only: %w", platform.ErrValidation)
	}
	opts, err := s.OptionsOf(ctx, db, v.EntityID, q.ID)
	if err != nil {
		return err
	}
	valid := map[int64]bool{}
	for _, o := range opts {
		valid[o.ID] = true
	}
	if err := CheckBallot(q.Multi, v.OptionIDs, valid); err != nil {
		return err
	}
	_, err = db.Exec(ctx, `INSERT INTO ferp_survey_votes
		(entity_id, question_id, user_login, option_ids)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (entity_id, question_id, user_login)
		DO UPDATE SET option_ids=EXCLUDED.option_ids, created_at=now()`,
		v.EntityID, v.QuestionID, v.UserLogin, v.OptionIDs)
	if err != nil {
		return err
	}
	return db.QueryRow(ctx, `SELECT id FROM ferp_survey_votes
		WHERE entity_id=$1 AND question_id=$2 AND user_login=$3`,
		v.EntityID, v.QuestionID, v.UserLogin).Scan(&v.ID)
}

func (s *PGStore) VotesOf(ctx context.Context, db platform.DBTX, entityID int64, questionID int64) ([]Vote, error) {
	rows, err := db.Query(ctx, `SELECT id, entity_id, question_id, user_login, option_ids, created_at
		FROM ferp_survey_votes WHERE question_id=$1 AND entity_id=$2 ORDER BY id`, questionID, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Vote
	for rows.Next() {
		var v Vote
		if err := rows.Scan(&v.ID, &v.EntityID, &v.QuestionID, &v.UserLogin, &v.OptionIDs, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *PGStore) Results(ctx context.Context, db platform.DBTX, entityID int64, questionID int64) ([]Tally, error) {
	opts, err := s.OptionsOf(ctx, db, entityID, questionID)
	if err != nil {
		return nil, err
	}
	votes, err := s.VotesOf(ctx, db, entityID, questionID)
	if err != nil {
		return nil, err
	}
	return TallyCounts(opts, votes), nil
}

// MemoryStore is the in-process fake for handler tests.
type MemoryStore struct {
	mu        sync.Mutex
	seq       int64
	surveys   map[int64]Survey
	questions map[int64]Question
	options   map[int64]Option
	votes     map[int64]Vote
	byBallot  map[string]int64 // entity/question/user -> vote id
}

// NewMemoryStore builds an empty fake.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		surveys: map[int64]Survey{}, questions: map[int64]Question{},
		options: map[int64]Option{}, votes: map[int64]Vote{}, byBallot: map[string]int64{},
	}
}

func (m *MemoryStore) next() int64 { m.seq++; return m.seq }

func ballotKey(entityID, questionID int64, user string) string {
	return fmt.Sprintf("%d/%d/%s", entityID, questionID, user)
}

func (m *MemoryStore) CreateSurvey(_ context.Context, _ platform.DBTX, s *Survey) error {
	if err := s.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s.ID = m.next()
	s.RowVersion = 1
	m.surveys[s.ID] = *s
	return nil
}

func (m *MemoryStore) SurveyByID(_ context.Context, _ platform.DBTX, entityID int64, id int64) (Survey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.surveys[id]
	if !ok || s.EntityID != entityID {
		return Survey{}, identity.ErrNotFound
	}
	return s, nil
}

func (m *MemoryStore) ListSurveys(_ context.Context, _ platform.DBTX, entityID int64) ([]Survey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Survey
	for _, s := range m.surveys {
		if s.EntityID == entityID {
			out = append(out, s)
		}
	}
	return out, nil
}

func (m *MemoryStore) SetSurveyStatus(_ context.Context, _ platform.DBTX, entityID int64, id int64, to SurveyStatus, rowVersion int64) (Survey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.surveys[id]
	if !ok || s.EntityID != entityID {
		return Survey{}, identity.ErrNotFound
	}
	if s.RowVersion != rowVersion {
		return Survey{}, identity.ErrVersionConflict
	}
	if !s.CanTransition(to) {
		return Survey{}, fmt.Errorf("survey: illegal transition: %w", platform.ErrValidation)
	}
	s.Status = to
	s.RowVersion++
	m.surveys[id] = s
	return s, nil
}

func (m *MemoryStore) AddQuestion(_ context.Context, _ platform.DBTX, q *Question) error {
	if err := q.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	sv, ok := m.surveys[q.SurveyID]
	if !ok || sv.EntityID != q.EntityID {
		return fmt.Errorf("survey: survey not found: %w", platform.ErrNotFound)
	}
	if sv.Status != SurveyDraft {
		return fmt.Errorf("survey: questions editable on drafts only: %w", platform.ErrValidation)
	}
	q.ID = m.next()
	m.questions[q.ID] = *q
	return nil
}

func (m *MemoryStore) QuestionsOf(_ context.Context, _ platform.DBTX, entityID int64, surveyID int64) ([]Question, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sv, ok := m.surveys[surveyID]
	if !ok || sv.EntityID != entityID {
		return nil, nil
	}
	var out []Question
	for _, q := range m.questions {
		if q.SurveyID == surveyID && q.EntityID == entityID {
			out = append(out, q)
		}
	}
	return out, nil
}

func (m *MemoryStore) AddOption(_ context.Context, _ platform.DBTX, o *Option) error {
	if err := o.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	q, ok := m.questions[o.QuestionID]
	if !ok || q.EntityID != o.EntityID {
		return identity.ErrNotFound
	}
	o.ID = m.next()
	m.options[o.ID] = *o
	return nil
}

func (m *MemoryStore) OptionsOf(_ context.Context, _ platform.DBTX, entityID int64, questionID int64) ([]Option, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Option
	for _, o := range m.options {
		if o.QuestionID == questionID && o.EntityID == entityID {
			out = append(out, o)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Position != out[j].Position {
			return out[i].Position < out[j].Position
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (m *MemoryStore) CastVote(_ context.Context, _ platform.DBTX, v *Vote) error {
	if err := v.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	q, ok := m.questions[v.QuestionID]
	if !ok || q.EntityID != v.EntityID {
		return identity.ErrNotFound
	}
	sv, ok := m.surveys[q.SurveyID]
	if !ok || sv.EntityID != v.EntityID || sv.Status != SurveyOpen {
		return fmt.Errorf("survey: voting open on open surveys only: %w", platform.ErrValidation)
	}
	valid := map[int64]bool{}
	for _, o := range m.options {
		if o.QuestionID == q.ID && o.EntityID == v.EntityID {
			valid[o.ID] = true
		}
	}
	if err := CheckBallot(q.Multi, v.OptionIDs, valid); err != nil {
		return err
	}
	if id, ok := m.byBallot[ballotKey(v.EntityID, v.QuestionID, v.UserLogin)]; ok {
		v.ID = id
	} else {
		v.ID = m.next()
	}
	m.votes[v.ID] = *v
	m.byBallot[ballotKey(v.EntityID, v.QuestionID, v.UserLogin)] = v.ID
	return nil
}

func (m *MemoryStore) VotesOf(_ context.Context, _ platform.DBTX, entityID int64, questionID int64) ([]Vote, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Vote
	for _, v := range m.votes {
		if v.QuestionID == questionID && v.EntityID == entityID {
			out = append(out, v)
		}
	}
	return out, nil
}

func (m *MemoryStore) Results(_ context.Context, _ platform.DBTX, entityID int64, questionID int64) ([]Tally, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var opts []Option
	for _, o := range m.options {
		if o.QuestionID == questionID && o.EntityID == entityID {
			opts = append(opts, o)
		}
	}
	var votes []Vote
	for _, v := range m.votes {
		if v.QuestionID == questionID && v.EntityID == entityID {
			votes = append(votes, v)
		}
	}
	return TallyCounts(opts, votes), nil
}
