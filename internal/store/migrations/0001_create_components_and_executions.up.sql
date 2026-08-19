CREATE TABLE components (
    id              TEXT PRIMARY KEY NOT NULL,
    nome            TEXT NOT NULL,
    descricao       TEXT,
    source          TEXT NOT NULL,
    build           TEXT,
    resources       TEXT NOT NULL,
    runtime         TEXT NOT NULL,
    hooks           TEXT,
    target_context  TEXT NOT NULL,
    lifecycle       TEXT,
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE executions (
    id              TEXT PRIMARY KEY NOT NULL,
    component_id    TEXT NOT NULL REFERENCES components(id) ON DELETE RESTRICT,
    phase           TEXT NOT NULL DEFAULT 'Pending'
                        CHECK (phase IN ('Pending', 'Running', 'Succeeded', 'Failed')),
    started_at      TEXT,
    completed_at    TEXT,
    image_digest    TEXT,
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX idx_executions_component_id ON executions(component_id);
