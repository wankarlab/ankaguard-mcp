-- Standalone local auth: agent tokens issued by the operator (internal/localauth).
-- Only the metadata is stored. The token value (a signed JWT) is shown once at
-- creation and is never persisted. Revocation sets revoked_at; the validator
-- keeps an in-memory set of revoked jti reloaded every 2 seconds.
CREATE TABLE IF NOT EXISTS agent_tokens (
    jti          TEXT      PRIMARY KEY,
    name         TEXT      NOT NULL,
    profile      TEXT      NOT NULL CHECK (profile IN ('read', 'paper', 'live')),
    created_at   TIMESTAMP NOT NULL,
    expires_at   TIMESTAMP NOT NULL,
    last_used_at TIMESTAMP,
    revoked_at   TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_agent_tokens_revoked
    ON agent_tokens (revoked_at);
