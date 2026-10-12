BEGIN;

CREATE TABLE package_types (
    id SERIAL PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    name_ru TEXT NOT NULL
);

INSERT INTO package_types (name, name_ru)
VALUES
    ('clothing', 'Одежда'),
    ('electronics', 'Электроника'),
    ('other', 'Разное');

CREATE TABLE packages (
    package_id UUID PRIMARY KEY,
    session_id UUID NOT NULL,

    name TEXT NOT NULL,

    weight_kg NUMERIC(10, 3) NOT NULL
        CHECK (weight_kg > 0),

    package_type_id INTEGER NOT NULL
        REFERENCES package_types(id),

    content_value_usd NUMERIC(12, 2) NOT NULL
        CHECK (content_value_usd >= 0),

    delivery_cost_rub NUMERIC(18, 2),

    status TEXT NOT NULL DEFAULT 'calculation_pending'
        CHECK (status IN ('calculation_pending', 'calculated')),

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_packages_session_id
    ON packages(session_id);

CREATE INDEX idx_packages_status
    ON packages(status);

CREATE INDEX idx_packages_session_type
    ON packages(session_id, package_type_id);

COMMIT;