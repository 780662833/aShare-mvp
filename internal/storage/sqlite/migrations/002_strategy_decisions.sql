CREATE TABLE IF NOT EXISTS strategy_decisions (
    decision_id       TEXT PRIMARY KEY NOT NULL,
    ts_code           TEXT NOT NULL,
    strategy_id       TEXT NOT NULL,
    strategy_version  TEXT NOT NULL,
    config_version    TEXT NOT NULL,
    as_of             TEXT NOT NULL,
    confirmed_at      TEXT NOT NULL DEFAULT '',
    status            TEXT NOT NULL,
    data_version      TEXT NOT NULL,
    payload           TEXT NOT NULL,
    saved_at          TEXT NOT NULL,
    FOREIGN KEY (ts_code) REFERENCES stock_basic(ts_code) ON UPDATE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_strategy_decisions_latest
    ON strategy_decisions(ts_code, strategy_id, as_of DESC);
