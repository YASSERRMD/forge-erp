-- 0044_p5c_lite: Phase 5 PORT-LITE tables (bookmarks, quick memos, collab
-- comments, AI run log). ModuleBuilder persists in ferp_modules (migration 0025),
-- Website reads published ferp_articles (migration 0018), LDAP is stateless.

CREATE TABLE ferp_bookmarks (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    user_login  TEXT NOT NULL,
    scope       TEXT NOT NULL,
    object_type TEXT NOT NULL,
    object_id   BIGINT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    row_version BIGINT NOT NULL DEFAULT 1,
    UNIQUE (entity_id, user_login, scope, object_type, object_id)
);
CREATE INDEX ferp_bookmarks_user_idx ON ferp_bookmarks (entity_id, user_login);

CREATE TABLE ferp_quickmemos (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    user_login  TEXT NOT NULL,
    title       TEXT NOT NULL,
    body        TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    row_version BIGINT NOT NULL DEFAULT 1
);
CREATE INDEX ferp_quickmemos_user_idx ON ferp_quickmemos (entity_id, user_login);

CREATE TABLE ferp_collab_comments (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    scope       TEXT NOT NULL,
    object_type TEXT NOT NULL,
    object_id   BIGINT NOT NULL,
    thread      TEXT NOT NULL DEFAULT '',
    author      TEXT NOT NULL,
    body        TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    row_version BIGINT NOT NULL DEFAULT 1
);
CREATE INDEX ferp_collab_comments_object_idx
    ON ferp_collab_comments (entity_id, scope, object_type, object_id, thread, id);

CREATE TABLE ferp_ai_runs (
    id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id      BIGINT NOT NULL REFERENCES ferp_entities(id),
    model          TEXT NOT NULL DEFAULT '',
    prompt_excerpt TEXT NOT NULL DEFAULT '',
    output_excerpt TEXT NOT NULL DEFAULT '',
    duration_ms    BIGINT NOT NULL DEFAULT 0,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ferp_ai_runs_entity_idx ON ferp_ai_runs (entity_id, created_at);
