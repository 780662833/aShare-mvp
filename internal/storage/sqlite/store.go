// Package sqlite 提供 V1 最小 SQLite 仓储。业务模型仍由领域包定义。
package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/780662833/aShare-mvp/internal/market/calendar"
	"github.com/780662833/aShare-mvp/internal/market/universe"
	"github.com/780662833/aShare-mvp/internal/marketdata"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

type Store struct {
	db *sql.DB
}

func Open(ctx context.Context, dbPath string) (*Store, error) {
	if dbPath == "" {
		return nil, fmt.Errorf("数据库路径不能为空")
	}
	absPath, err := filepath.Abs(dbPath)
	if err != nil {
		return nil, fmt.Errorf("解析数据库路径失败: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return nil, fmt.Errorf("创建数据库目录失败: %w", err)
	}

	// Windows 绝对路径需要保留盘符但避免被 URL parser 解释成 authority。
	dsnPath := filepath.ToSlash(absPath)
	if runtime.GOOS == "windows" {
		dsnPath = "/" + dsnPath
	}
	u := url.URL{Scheme: "file", Path: dsnPath}
	dsn := u.String() + "?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开 SQLite 失败: %w", err)
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(0)

	store := &Store{db: db}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("连接 SQLite 失败: %w", err)
	}
	if err := store.Migrate(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("执行 SQLite migration 失败: %w", err)
	}
	return store, nil
}

func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		name TEXT PRIMARY KEY NOT NULL,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return err
	}

	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		var exists int
		err := s.db.QueryRowContext(ctx, "SELECT 1 FROM schema_migrations WHERE name = ?", entry.Name()).Scan(&exists)
		if err == nil {
			continue
		}
		if err != sql.ErrNoRows {
			return err
		}
		content, err := migrationFiles.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return err
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		for _, statement := range strings.Split(string(content), ";") {
			statement = strings.TrimSpace(statement)
			if statement == "" {
				continue
			}
			if _, err = tx.ExecContext(ctx, statement); err != nil {
				break
			}
		}
		if err == nil {
			_, err = tx.ExecContext(ctx,
				"INSERT INTO schema_migrations(name, applied_at) VALUES(?, ?)",
				entry.Name(), time.Now().In(marketdata.ShanghaiLocation()).Format(time.RFC3339))
		}
		if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %s: %w", entry.Name(), err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("提交 migration %s: %w", entry.Name(), err)
		}
	}
	return nil
}

func (s *Store) UpsertStockBasics(ctx context.Context, items []universe.StockBasic) error {
	if len(items) == 0 {
		return fmt.Errorf("证券资料批次不能为空")
	}
	for i, item := range items {
		if err := item.Validate(); err != nil {
			return fmt.Errorf("stock_basic[%d]: %w", i, err)
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	stmt := `INSERT INTO stock_basic (
		ts_code, symbol, name, area, industry, fullname, enname, cnspell,
		market, exchange, curr_type, list_status, list_date, delist_date, is_hs, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(ts_code) DO UPDATE SET
		symbol=CASE WHEN excluded.symbol='' THEN stock_basic.symbol ELSE excluded.symbol END,
		name=CASE WHEN excluded.name='' THEN stock_basic.name ELSE excluded.name END,
		area=CASE WHEN excluded.area='' THEN stock_basic.area ELSE excluded.area END,
		industry=CASE WHEN excluded.industry='' THEN stock_basic.industry ELSE excluded.industry END,
		fullname=CASE WHEN excluded.fullname='' THEN stock_basic.fullname ELSE excluded.fullname END,
		enname=CASE WHEN excluded.enname='' THEN stock_basic.enname ELSE excluded.enname END,
		cnspell=CASE WHEN excluded.cnspell='' THEN stock_basic.cnspell ELSE excluded.cnspell END,
		market=CASE WHEN excluded.market='' THEN stock_basic.market ELSE excluded.market END,
		exchange=excluded.exchange,
		curr_type=CASE WHEN excluded.curr_type='' THEN stock_basic.curr_type ELSE excluded.curr_type END,
		list_status=CASE WHEN excluded.list_status='UNKNOWN' AND stock_basic.list_status<>'UNKNOWN'
			THEN stock_basic.list_status ELSE excluded.list_status END,
		list_date=CASE WHEN excluded.list_date='' THEN stock_basic.list_date ELSE excluded.list_date END,
		delist_date=CASE WHEN excluded.delist_date='' THEN stock_basic.delist_date ELSE excluded.delist_date END,
		is_hs=CASE WHEN excluded.is_hs='' THEN stock_basic.is_hs ELSE excluded.is_hs END,
		updated_at=excluded.updated_at`
	for _, item := range items {
		_, err = tx.ExecContext(ctx, stmt,
			item.TsCode, item.Symbol, item.Name, item.Area, item.Industry, item.Fullname,
			item.Ename, item.Cnspell, item.Market, string(item.Exchange), item.CurrType,
			string(item.ListStatus), item.ListDate, item.DelistDate, item.IsHS,
			time.Now().In(marketdata.ShanghaiLocation()).Format(time.RFC3339))
		if err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) UpsertDailyStates(ctx context.Context, items []universe.SecurityDailyState) error {
	if len(items) == 0 {
		return fmt.Errorf("证券每日状态批次不能为空")
	}
	for i, item := range items {
		if err := item.Validate(); err != nil {
			return fmt.Errorf("security_daily_state[%d]: %w", i, err)
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	stmt := `INSERT INTO security_daily_state (
		trade_date, ts_code, st_type, suspend_type, suspend_timing, source, fetched_at
	) VALUES (?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(trade_date, ts_code) DO UPDATE SET
		st_type=excluded.st_type, suspend_type=excluded.suspend_type,
		suspend_timing=excluded.suspend_timing, source=excluded.source, fetched_at=excluded.fetched_at`
	for _, item := range items {
		_, err = tx.ExecContext(ctx, stmt, item.TradeDate, item.TsCode, string(item.STType),
			string(item.SuspendType), item.SuspendTiming, item.Source,
			item.FetchedAt.In(marketdata.ShanghaiLocation()).Format(time.RFC3339))
		if err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) UpsertTradeCalendar(ctx context.Context, items []calendar.TradeDay) error {
	if len(items) == 0 {
		return fmt.Errorf("交易日历批次不能为空")
	}
	for i, item := range items {
		if err := item.Validate(); err != nil {
			return fmt.Errorf("trade_calendar[%d]: %w", i, err)
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	stmt := `INSERT INTO trade_calendar (
		exchange, cal_date, is_open, pretrade_date, source, source_url, fetched_at
	) VALUES (?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(exchange, cal_date) DO UPDATE SET
		is_open=excluded.is_open, pretrade_date=excluded.pretrade_date,
		source=excluded.source, source_url=excluded.source_url, fetched_at=excluded.fetched_at`
	for _, item := range items {
		var currentSource string
		err := tx.QueryRowContext(ctx,
			"SELECT source FROM trade_calendar WHERE exchange = ? AND cal_date = ?",
			string(item.Exchange), item.CalDate,
		).Scan(&currentSource)
		if err != nil && err != sql.ErrNoRows {
			_ = tx.Rollback()
			return fmt.Errorf("读取现有交易日历来源失败: %w", err)
		}
		if err == nil && !calendar.IsAuthoritative(item.Source, calendar.Source(currentSource)) {
			continue
		}

		isOpen := 0
		if item.IsOpen {
			isOpen = 1
		}
		_, err = tx.ExecContext(ctx, stmt, string(item.Exchange), item.CalDate, isOpen,
			item.PretradeDate, string(item.Source), item.SourceURL,
			item.FetchedAt.In(marketdata.ShanghaiLocation()).Format(time.RFC3339))
		if err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) UpsertBars(ctx context.Context, bars []marketdata.Bar) error {
	if err := marketdata.ValidateBatch(bars); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	stmt := `INSERT INTO stock_kline (
		ts_code, freq, bar_time, open, high, low, close, volume, amount,
		adjust_mode, volume_unit, source, is_complete, fetched_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(ts_code, freq, adjust_mode, bar_time) DO UPDATE SET
		open=excluded.open, high=excluded.high, low=excluded.low, close=excluded.close,
		volume=excluded.volume, amount=excluded.amount, volume_unit=excluded.volume_unit,
		source=excluded.source, is_complete=excluded.is_complete, fetched_at=excluded.fetched_at`
	first := bars[0]
	sourceRows, err := tx.QueryContext(ctx, `SELECT DISTINCT source FROM stock_kline
		WHERE ts_code = ? AND freq = ? AND adjust_mode = ?`,
		first.TsCode, string(first.Frequency), string(first.AdjustMode))
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("读取现有 K 线来源失败: %w", err)
	}
	for sourceRows.Next() {
		var currentSource string
		if err := sourceRows.Scan(&currentSource); err != nil {
			_ = sourceRows.Close()
			_ = tx.Rollback()
			return fmt.Errorf("读取现有 K 线来源失败: %w", err)
		}
		if currentSource != first.Source {
			_ = sourceRows.Close()
			_ = tx.Rollback()
			return fmt.Errorf("拒绝混合 K 线数据源：%s %s %s 已有来源 %q，新批次来源 %q；需先单独核对重叠区间并执行显式迁移",
				first.TsCode, first.Frequency, first.AdjustMode, currentSource, first.Source)
		}
	}
	if err := sourceRows.Err(); err != nil {
		_ = sourceRows.Close()
		_ = tx.Rollback()
		return fmt.Errorf("遍历现有 K 线来源失败: %w", err)
	}
	if err := sourceRows.Close(); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("关闭 K 线来源查询失败: %w", err)
	}

	for _, bar := range bars {
		barTime := bar.BarTime.Format(time.RFC3339Nano)
		isComplete := 0
		if bar.IsComplete {
			isComplete = 1
		}
		_, err = tx.ExecContext(ctx, stmt,
			bar.TsCode, string(bar.Frequency), barTime,
			bar.Open, bar.High, bar.Low, bar.Close, bar.Volume, bar.Amount,
			string(bar.AdjustMode), string(bar.VolumeUnit), bar.Source, isComplete,
			bar.FetchedAt.Format(time.RFC3339Nano))
		if err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ListBars(ctx context.Context, tsCode string, freq marketdata.Frequency, adjust marketdata.AdjustMode) ([]marketdata.Bar, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT
		ts_code, freq, bar_time, open, high, low, close, volume, amount,
		adjust_mode, volume_unit, source, is_complete, fetched_at
		FROM stock_kline
		WHERE ts_code = ? AND freq = ? AND adjust_mode = ?
		ORDER BY bar_time ASC`, tsCode, string(freq), string(adjust))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]marketdata.Bar, 0)
	for rows.Next() {
		var bar marketdata.Bar
		var freqValue, adjustValue, volumeUnitValue, barTimeValue, fetchedAtValue string
		var isComplete int
		if err := rows.Scan(&bar.TsCode, &freqValue, &barTimeValue, &bar.Open, &bar.High,
			&bar.Low, &bar.Close, &bar.Volume, &bar.Amount, &adjustValue,
			&volumeUnitValue, &bar.Source, &isComplete, &fetchedAtValue); err != nil {
			return nil, err
		}
		bar.Frequency = marketdata.Frequency(freqValue)
		bar.AdjustMode = marketdata.AdjustMode(adjustValue)
		bar.VolumeUnit = marketdata.VolumeUnit(volumeUnitValue)
		bar.BarTime, err = time.Parse(time.RFC3339Nano, barTimeValue)
		if err != nil {
			return nil, err
		}
		bar.FetchedAt, err = time.Parse(time.RFC3339Nano, fetchedAtValue)
		if err != nil {
			return nil, err
		}
		bar.IsComplete = isComplete == 1
		result = append(result, bar)
	}
	return result, rows.Err()
}
