-- Persist securities rejected by the scan eligibility gate without treating them as evaluated items.
ALTER TABLE scan_runs
    ADD COLUMN excluded_count INTEGER NOT NULL DEFAULT 0 CHECK (excluded_count >= 0);

CREATE TABLE IF NOT EXISTS scan_run_exclusions (
    scan_id       TEXT NOT NULL,
    ts_code       TEXT NOT NULL,
    security_name TEXT NOT NULL,
    exchange      TEXT NOT NULL CHECK (exchange IN ('SSE', 'SZSE', 'BSE')),
    trade_date    TEXT NOT NULL,
    reason_codes  TEXT NOT NULL,
    PRIMARY KEY (scan_id, ts_code),
    FOREIGN KEY (scan_id) REFERENCES scan_runs(scan_id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_scan_run_exclusions_reason
    ON scan_run_exclusions(scan_id, trade_date, ts_code);
