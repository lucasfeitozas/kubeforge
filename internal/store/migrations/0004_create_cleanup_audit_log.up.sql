CREATE TABLE cleanup_audit_log (
    id            TEXT PRIMARY KEY NOT NULL,
    resource_kind TEXT NOT NULL,
    resource_name TEXT NOT NULL,
    namespace     TEXT NOT NULL,
    removed_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
