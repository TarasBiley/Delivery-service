CREATE TABLE package_idempotency (
    session_id UUID NOT NULL,
    idempotency_key VARCHAR(128) NOT NULL CHECK (length(idempotency_key) > 0),
    request_hash BYTEA NOT NULL CHECK (octet_length(request_hash) = 32),
    package_id UUID NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (session_id, idempotency_key)
);
-- No FK to packages: reservation precedes asynchronous worker creation.
