-- 0015_survey: surveys, questions, options, ballots.
-- Dolibarr equivalent: opensurvey (polls). One ballot per user per question;
-- re-vote replaces the previous ballot.

CREATE TABLE ferp_surveys (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    title       TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    status      SMALLINT NOT NULL DEFAULT 0,    -- 0 draft, 1 open, 2 closed
    created_by  TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    row_version BIGINT NOT NULL DEFAULT 1
);
CREATE INDEX ferp_surveys_entity_idx ON ferp_surveys (entity_id, status);

CREATE TABLE ferp_survey_questions (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    survey_id   BIGINT NOT NULL REFERENCES ferp_surveys(id) ON DELETE CASCADE,
    text        TEXT NOT NULL,
    multi       BOOLEAN NOT NULL DEFAULT FALSE,
    position    INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX ferp_survey_questions_survey_idx ON ferp_survey_questions (survey_id, position);

CREATE TABLE ferp_survey_options (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    question_id BIGINT NOT NULL REFERENCES ferp_survey_questions(id) ON DELETE CASCADE,
    label       TEXT NOT NULL,
    position    INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX ferp_survey_options_question_idx ON ferp_survey_options (question_id, position);

CREATE TABLE ferp_survey_votes (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    question_id BIGINT NOT NULL REFERENCES ferp_survey_questions(id) ON DELETE CASCADE,
    user_login  TEXT NOT NULL,
    option_ids  BIGINT[] NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (entity_id, question_id, user_login)
);
CREATE INDEX ferp_survey_votes_question_idx ON ferp_survey_votes (question_id);
