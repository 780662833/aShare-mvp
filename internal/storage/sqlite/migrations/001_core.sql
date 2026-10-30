CREATE TABLE IF NOT EXISTS stock_basic (
    ts_code       TEXT PRIMARY KEY NOT NULL,
    symbol        TEXT NOT NULL,
    name          TEXT NOT NULL,
    area          TEXT NOT NULL DEFAULT '',
    industry      TEXT NOT NULL DEFAULT '',
    fullname      TEXT NOT NULL DEFAULT '',
    enname        TEXT NOT NULL DEFAULT '',
    cnspell       TEXT NOT NULL DEFAULT '',
    market        TEXT NOT NULL DEFAULT '',
    exchange      TEXT NOT NULL CHECK (exchange IN ('SSE', 'SZSE', 'BSE')),
    curr_type     TEXT NOT NULL DEFAULT '',
    list_status   TEXT NOT NULL,
    list_date     TEXT NOT NULL DEFAULT '',
    delist_date   TEXT NOT NULL DEFAULT '',
    is_hs         TEXT NOT NULL DEFAULT '',
    updated_at    TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_stock_basic_symbol
    ON stock_basic(symbol);
CREATE INDEX IF NOT EXISTS idx_stock_basic_exchange_status
    ON stock_basic(exchange, list_status);

CREATE TABLE IF NOT EXISTS security_daily_state (
    trade_date      TEXT NOT NULL,
    ts_code         TEXT NOT NULL,
    st_type         TEXT NOT NULL CHECK (st_type IN ('NONE', 'ST', 'ST_STAR', 'UNKNOWN')),
    suspend_type    TEXT NOT NULL CHECK (suspend_type IN ('NONE', 'FULL', 'INTRADAY', 'UNKNOWN')),
    suspend_timing  TEXT NOT NULL DEFAULT '',
    source          TEXT NOT NULL,
    fetched_at      TEXT NOT NULL,
    PRIMARY KEY (trade_date, ts_code),
    FOREIGN KEY (ts_code) REFERENCES stock_basic(ts_code) ON UPDATE CASCADE
);

CREATE TABLE IF NOT EXISTS trade_calendar (
    exchange       TEXT NOT NULL CHECK (exchange IN ('SSE', 'SZSE', 'BSE')),
    cal_date       TEXT NOT NULL,
    is_open        INTEGER NOT NULL CHECK (is_open IN (0, 1)),
    pretrade_date  TEXT NOT NULL DEFAULT '',
    source         TEXT NOT NULL CHECK (source IN ('EXCHANGE_NOTICE', 'TUSHARE', 'MANUAL_OVERRIDE')),
    source_url     TEXT NOT NULL DEFAULT '',
    fetched_at     TEXT NOT NULL,
    PRIMARY KEY (exchange, cal_date)
);

CREATE TABLE IF NOT EXISTS stock_kline (
    ts_code       TEXT NOT NULL,
    freq          TEXT NOT NULL CHECK (freq IN ('D1', 'M30', 'M60')),
    bar_time      TEXT NOT NULL,
    open          REAL NOT NULL,
    high          REAL NOT NULL,
    low           REAL NOT NULL,
    close         REAL NOT NULL,
    volume        REAL NOT NULL CHECK (volume >= 0),
    amount        REAL NOT NULL CHECK (amount >= 0),
    adjust_mode   TEXT NOT NULL CHECK (adjust_mode IN ('QFQ', 'HFQ', 'NONE', 'UNKNOWN')),
    volume_unit   TEXT NOT NULL CHECK (volume_unit IN ('SHARE', 'LOT', 'SOURCE_NATIVE', 'UNKNOWN')),
    source        TEXT NOT NULL,
    is_complete   INTEGER NOT NULL CHECK (is_complete IN (0, 1)),
    fetched_at    TEXT NOT NULL,
    PRIMARY KEY (ts_code, freq, adjust_mode, bar_time),
    FOREIGN KEY (ts_code) REFERENCES stock_basic(ts_code) ON UPDATE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_stock_kline_freq_time
    ON stock_kline(freq, bar_time);
