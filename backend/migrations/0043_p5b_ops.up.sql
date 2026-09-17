-- 0043_p5b_ops: Phase 5 PORT batch B (partnership, mailing, datapolicy, label).
-- All money in minor units (int64); commission rates in basis points (0..10000).

CREATE TABLE IF NOT EXISTS ferp_partner_programs (
    id          BIGSERIAL PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id) ON DELETE CASCADE,
    code        TEXT NOT NULL,
    name        TEXT NOT NULL,
    tiers       JSONB NOT NULL DEFAULT '[]',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    row_version BIGINT NOT NULL DEFAULT 1,
    UNIQUE (entity_id, code)
);
CREATE INDEX IF NOT EXISTS ix_partner_programs_entity ON ferp_partner_programs(entity_id);

CREATE TABLE IF NOT EXISTS ferp_referrals (
    id              BIGSERIAL PRIMARY KEY,
    entity_id       BIGINT NOT NULL REFERENCES ferp_entities(id) ON DELETE CASCADE,
    program_id      BIGINT NOT NULL REFERENCES ferp_partner_programs(id) ON DELETE CASCADE,
    referrer_org_id BIGINT NOT NULL,
    referred_org_id BIGINT NOT NULL,
    code            TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'active',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    row_version     BIGINT NOT NULL DEFAULT 1,
    UNIQUE (entity_id, code)
);
CREATE INDEX IF NOT EXISTS ix_referrals_program ON ferp_referrals(program_id);
CREATE INDEX IF NOT EXISTS ix_referrals_entity ON ferp_referrals(entity_id);

CREATE TABLE IF NOT EXISTS ferp_commission_accruals (
    id          BIGSERIAL PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id) ON DELETE CASCADE,
    referral_id BIGINT NOT NULL REFERENCES ferp_referrals(id) ON DELETE CASCADE,
    sale_total  BIGINT NOT NULL CHECK (sale_total > 0),
    rate_bps    BIGINT NOT NULL CHECK (rate_bps >= 0 AND rate_bps <= 10000),
    amount      BIGINT NOT NULL CHECK (amount >= 0),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS ix_accruals_referral ON ferp_commission_accruals(referral_id);

CREATE TABLE IF NOT EXISTS ferp_mailing_campaigns (
    id          BIGSERIAL PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id) ON DELETE CASCADE,
    subject     TEXT NOT NULL,
    body        TEXT NOT NULL DEFAULT '',
    status      SMALLINT NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    row_version BIGINT NOT NULL DEFAULT 1
);
CREATE INDEX IF NOT EXISTS ix_mailing_campaigns_entity ON ferp_mailing_campaigns(entity_id);

CREATE TABLE IF NOT EXISTS ferp_mailing_recipients (
    id           BIGSERIAL PRIMARY KEY,
    entity_id    BIGINT NOT NULL REFERENCES ferp_entities(id) ON DELETE CASCADE,
    campaign_id  BIGINT NOT NULL REFERENCES ferp_mailing_campaigns(id) ON DELETE CASCADE,
    email        TEXT NOT NULL,
    token        TEXT NOT NULL,
    status       TEXT NOT NULL DEFAULT 'queued',
    error        TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (campaign_id, email),
    UNIQUE (token)
);
CREATE INDEX IF NOT EXISTS ix_mailing_recipients_campaign ON ferp_mailing_recipients(campaign_id);

CREATE TABLE IF NOT EXISTS ferp_mailing_suppressions (
    entity_id  BIGINT NOT NULL REFERENCES ferp_entities(id) ON DELETE CASCADE,
    email      TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (entity_id, email)
);

CREATE TABLE IF NOT EXISTS ferp_retention_rules (
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id) ON DELETE CASCADE,
    scope       TEXT NOT NULL,
    retain_days BIGINT NOT NULL CHECK (retain_days >= 0),
    action      TEXT NOT NULL DEFAULT 'anonymize',
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (entity_id, scope)
);

CREATE TABLE IF NOT EXISTS ferp_erasure_requests (
    id          BIGSERIAL PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id) ON DELETE CASCADE,
    scope       TEXT NOT NULL,
    subject_id  BIGINT NOT NULL,
    reason      TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'pending',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS ix_erasures_entity ON ferp_erasure_requests(entity_id);

CREATE TABLE IF NOT EXISTS ferp_label_sheets (
    id             BIGSERIAL PRIMARY KEY,
    entity_id      BIGINT NOT NULL REFERENCES ferp_entities(id) ON DELETE CASCADE,
    code           TEXT NOT NULL,
    name           TEXT NOT NULL,
    rows           BIGINT NOT NULL CHECK (rows BETWEEN 1 AND 20),
    cols           BIGINT NOT NULL CHECK (cols BETWEEN 1 AND 20),
    label_w_mm     DOUBLE PRECISION NOT NULL CHECK (label_w_mm > 0),
    label_h_mm     DOUBLE PRECISION NOT NULL CHECK (label_h_mm > 0),
    margin_top_mm  DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (margin_top_mm >= 0),
    margin_left_mm DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (margin_left_mm >= 0),
    gap_x_mm       DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (gap_x_mm >= 0),
    gap_y_mm       DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (gap_y_mm >= 0),
    fields         JSONB NOT NULL DEFAULT '[]',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    row_version    BIGINT NOT NULL DEFAULT 1,
    UNIQUE (entity_id, code)
);
CREATE INDEX IF NOT EXISTS ix_label_sheets_entity ON ferp_label_sheets(entity_id);
