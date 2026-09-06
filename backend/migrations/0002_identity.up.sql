-- 0002_identity: users, groups, rights matrix, sessions.
-- Dolibarr equivalents: llx_user, llx_usergroup, llx_user_rights,
-- llx_usergroup_user, llx_usergroup_rights, llx_rights_def, llx_session.

CREATE TABLE ferp_users (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id       BIGINT NOT NULL REFERENCES ferp_entities(id),
    login           TEXT NOT NULL,
    email           TEXT NOT NULL,
    first_name      TEXT NOT NULL DEFAULT '',
    last_name       TEXT NOT NULL DEFAULT '',
    status          SMALLINT NOT NULL DEFAULT 1,     -- 1 active, 2 locked, 0 disabled
    password_hash   TEXT NOT NULL DEFAULT '',        -- empty = SSO-only account
    is_admin        BOOLEAN NOT NULL DEFAULT FALSE,
    failed_attempts INTEGER NOT NULL DEFAULT 0,
    locked_until    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by      BIGINT REFERENCES ferp_users(id),
    updated_by      BIGINT REFERENCES ferp_users(id),
    row_version     BIGINT NOT NULL DEFAULT 1,
    UNIQUE (entity_id, login)
);

CREATE TABLE ferp_groups (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    code        TEXT NOT NULL,
    label       TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (entity_id, code)
);

CREATE TABLE ferp_group_members (
    group_id    BIGINT NOT NULL REFERENCES ferp_groups(id) ON DELETE CASCADE,
    user_id     BIGINT NOT NULL REFERENCES ferp_users(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (group_id, user_id)
);

-- Rights grants, user-direct or group-inherited (Dolibarr: llx_user_rights / llx_usergroup_rights).
-- Exactly one of user_id / group_id is set (enforced by CHECK).
CREATE TABLE ferp_rights (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    user_id     BIGINT REFERENCES ferp_users(id) ON DELETE CASCADE,
    group_id    BIGINT REFERENCES ferp_groups(id) ON DELETE CASCADE,
    module      TEXT NOT NULL,                        -- e.g. 'partners'
    entity      TEXT NOT NULL,                        -- e.g. 'organization' or '*'
    action      TEXT NOT NULL,                        -- e.g. 'read'
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((user_id IS NULL) <> (group_id IS NULL)),
    UNIQUE (entity_id, user_id, group_id, module, entity, action)
);

CREATE TABLE ferp_sessions (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id     BIGINT NOT NULL REFERENCES ferp_users(id) ON DELETE CASCADE,
    token_hash  TEXT NOT NULL UNIQUE,                 -- SHA-256 of refresh token
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL,
    revoked_at  TIMESTAMPTZ
);
CREATE INDEX ferp_sessions_user_idx ON ferp_sessions (user_id) WHERE revoked_at IS NULL;
