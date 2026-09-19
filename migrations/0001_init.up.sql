-- DevLens 初始表结构

CREATE TYPE severity_t AS ENUM ('LOW', 'MEDIUM', 'HIGH', 'CRITICAL');
CREATE TYPE status_t   AS ENUM ('ANALYZING', 'OPEN', 'INVESTIGATING', 'RESOLVED', 'FAILED');

CREATE TABLE users (
    id            BIGSERIAL PRIMARY KEY,
    email         TEXT UNIQUE NOT NULL,
    password_hash TEXT,
    name          TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE incidents (
    id            BIGSERIAL PRIMARY KEY,
    title         TEXT NOT NULL DEFAULT '',
    raw_log       TEXT NOT NULL,
    normalized    TEXT NOT NULL,
    fingerprint   BYTEA NOT NULL,
    severity      severity_t,
    category      TEXT,
    status        status_t NOT NULL DEFAULT 'ANALYZING',
    is_recurring  BOOLEAN NOT NULL DEFAULT false,
    created_by    BIGINT REFERENCES users(id),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE incident_analysis (
    incident_id       BIGINT PRIMARY KEY REFERENCES incidents(id) ON DELETE CASCADE,
    summary           TEXT NOT NULL,
    possible_causes   JSONB NOT NULL DEFAULT '[]',
    evidence          JSONB NOT NULL DEFAULT '[]',
    suggested_actions JSONB NOT NULL DEFAULT '[]',
    model             TEXT NOT NULL,
    prompt_version    TEXT NOT NULL,
    confidence        REAL NOT NULL CHECK (confidence >= 0 AND confidence <= 1),
    raw_response      TEXT NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE incident_events (
    id          BIGSERIAL PRIMARY KEY,
    incident_id BIGINT NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
    event_type  TEXT NOT NULL,
    from_status status_t,
    to_status   status_t,
    payload     JSONB NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE comments (
    id          BIGSERIAL PRIMARY KEY,
    incident_id BIGINT NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
    author_id   BIGINT REFERENCES users(id),
    body        TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 找同类的热路径。FAILED 的记录没有有效分析，不参与匹配。
CREATE INDEX idx_incidents_fingerprint
    ON incidents (fingerprint)
    WHERE status <> 'FAILED';

CREATE INDEX idx_incidents_status_created
    ON incidents (status, created_at DESC);

CREATE INDEX idx_incidents_created_by
    ON incidents (created_by, created_at DESC);

CREATE INDEX idx_events_incident
    ON incident_events (incident_id, created_at);

-- demo 用户。Demo 免登录，代码路径固定用它。
INSERT INTO users (email, name)
VALUES ('demo@devlens.local', 'Demo User');