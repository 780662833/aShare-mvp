CREATE TABLE IF NOT EXISTS scan_runs (
    scan_id                 TEXT PRIMARY KEY NOT NULL,
    as_of                   TEXT NOT NULL,
    started_at              TEXT NOT NULL,
    completed_at            TEXT NOT NULL DEFAULT '',
    status                  TEXT NOT NULL CHECK (status IN ('RUNNING', 'COMPLETED', 'PARTIAL_SUCCESS', 'FAILED', 'CANCELLED')),
    total_count             INTEGER NOT NULL CHECK (total_count >= 0),
    success_count           INTEGER NOT NULL DEFAULT 0 CHECK (success_count >= 0),
    data_incomplete_count   INTEGER NOT NULL DEFAULT 0 CHECK (data_incomplete_count >= 0),
    analysis_error_count    INTEGER NOT NULL DEFAULT 0 CHECK (analysis_error_count >= 0),
    failed_count            INTEGER NOT NULL DEFAULT 0 CHECK (failed_count >= 0),
    error_message           TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_scan_runs_started_at
    ON scan_runs(started_at DESC);

CREATE TABLE IF NOT EXISTS scan_run_items (
    scan_id          TEXT NOT NULL,
    ts_code          TEXT NOT NULL,
    security_name    TEXT NOT NULL,
    exchange         TEXT NOT NULL CHECK (exchange IN ('SSE', 'SZSE', 'BSE')),
    list_date        TEXT NOT NULL DEFAULT '',
    delist_date      TEXT NOT NULL DEFAULT '',
    status           TEXT NOT NULL CHECK (status IN ('PENDING', 'SUCCESS', 'DATA_INCOMPLETE', 'ANALYSIS_ERROR', 'FAILED')),
    data_version     TEXT NOT NULL DEFAULT '',
    decision_ids     TEXT NOT NULL DEFAULT '[]',
    reason_codes     TEXT NOT NULL DEFAULT '[]',
    error_message    TEXT NOT NULL DEFAULT '',
    started_at       TEXT NOT NULL DEFAULT '',
    completed_at     TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (scan_id, ts_code),
    FOREIGN KEY (scan_id) REFERENCES scan_runs(scan_id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_scan_run_items_status
    ON scan_run_items(scan_id, status, ts_code);
