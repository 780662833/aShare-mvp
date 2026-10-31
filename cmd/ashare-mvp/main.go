package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	tdxtypes "github.com/bensema/gotdx/types"

	appstrategy "github.com/780662833/aShare-mvp/internal/app/strategy"
	"github.com/780662833/aShare-mvp/internal/httpapi"
	"github.com/780662833/aShare-mvp/internal/czscclient"
	gotdxadapter "github.com/780662833/aShare-mvp/internal/datahub/gotdx"
	"github.com/780662833/aShare-mvp/internal/market/calendar"
	"github.com/780662833/aShare-mvp/internal/market/universe"
	"github.com/780662833/aShare-mvp/internal/marketdata"
	"github.com/780662833/aShare-mvp/internal/scanner"
	sqlitestore "github.com/780662833/aShare-mvp/internal/storage/sqlite"
	"github.com/780662833/aShare-mvp/internal/strategy/contract"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 {
		printUsage()
		return errors.New("缺少命令")
	}
	switch os.Args[1] {
	case "help", "-h", "--help":
		printUsage()
		return nil
	case "init":
		return runInit(os.Args[2:])
	case "sync-universe":
		return runSyncUniverse(os.Args[2:])
	case "sync-bars":
		return runSyncBars(os.Args[2:])
	case "import-reference":
		return runImportReference(os.Args[2:])
	case "evaluate":
		return runEvaluate(os.Args[2:])
	case "scan":
		return runScan(os.Args[2:])
	case "scan-result":
		return runScanResult(os.Args[2:])
	case "history":
		return runHistory(os.Args[2:])
	case "serve":
		return runServe(os.Args[2:])
	default:
		printUsage()
		return fmt.Errorf("未知命令 %q", os.Args[1])
	}
}

func printUsage() {
	fmt.Println(`aShare-mvp CLI

所有命令均支持 --db，默认 data/ashare.db。

  init
      初始化 SQLite 和 migrations
  sync-universe
      从 gotdx 获取证券名单并写入（上市状态保留 UNKNOWN）
  sync-bars --ts-code 600519.SH [--count 250] [--freq all|D1|M60|M30]
      获取并写入指定证券的前复权 K 线
  import-reference --file reference.json
      导入经核验的证券主数据、每日状态和交易日历
  evaluate --ts-code 600519.SH --config runtime.json [--as-of RFC3339]
      执行单股双策略评估并分别持久化决策
  scan --config runtime.json [--as-of RFC3339]
      扫描数据库中明确标记为上市的普通 A 股股票池
  scan-result --scan-id scan-...
      读取扫描运行、逐股结果和原始策略决策
  history [--ts-code ...] [--strategy-id ...] [--status ...]
          [--from RFC3339] [--to RFC3339] [--limit 50] [--offset 0]
      查询历史策略决策

CZSC Python 服务需单独启动。策略参数必须通过 runtime.json 显式提供；
程序不会把测试夹具参数当作实盘默认值。`)
}

func withStore(name string, args []string, configure func(*flag.FlagSet) error, action func(context.Context, *sqlitestore.Store) error) error {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	dbPath := fs.String("db", filepath.Join("data", "ashare.db"), "SQLite 数据库路径")
	timeout := fs.Duration("timeout", 0, "命令超时，例如 15m；0 表示不设置超时")
	if err := configure(fs); err != nil { return err }
	if err := fs.Parse(args); err != nil { return err }
	if *timeout < 0 { return errors.New("--timeout 不能为负数") }
	if fs.NArg() != 0 { return fmt.Errorf("存在未识别的位置参数: %v", fs.Args()) }

	baseCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx := baseCtx
	if *timeout > 0 { var cancel context.CancelFunc; ctx, cancel = context.WithTimeout(baseCtx, *timeout); defer cancel() }
	if err := os.MkdirAll(filepath.Dir(*dbPath), 0o755); err != nil {
		return fmt.Errorf("创建数据库目录失败: %w", err)
	}
	store, err := sqlitestore.Open(ctx, *dbPath)
	if err != nil { return err }
	defer store.Close()
	return action(ctx, store)
}

func runInit(args []string) error {
	return withStore("init", args, func(*flag.FlagSet) error { return nil },
		func(_ context.Context, _ *sqlitestore.Store) error {
			fmt.Println("SQLite 已初始化，migrations 已应用。")
			return nil
		})
}

func runSyncUniverse(args []string) error {
	return withStore("sync-universe", args, func(*flag.FlagSet) error { return nil },
		func(ctx context.Context, store *sqlitestore.Store) error {
			adapter := gotdxadapter.New(gotdxadapter.Config{})
			defer adapter.Close()
			result, err := adapter.FetchUniverse(ctx)
			if err != nil {
				_ = writeJSON(os.Stdout, result)
				return err
			}
			if !result.Complete() { return errors.New("gotdx 证券名单不完整，拒绝写入") }
			if err := store.UpsertStockBasics(ctx, result.Securities); err != nil {
				return fmt.Errorf("写入证券主数据失败: %w", err)
			}
			return writeJSON(os.Stdout, result)
		})
}

func runSyncBars(args []string) error {
	var code, freq string
	var count int
	var all bool
	return withStore("sync-bars", args, func(fs *flag.FlagSet) error {
		fs.StringVar(&code, "ts-code", "", "证券代码，例如 600519.SH")
		fs.BoolVar(&all, "all", false, "同步当前明确标记为 ACTIVE 的全部普通 A 股")
		fs.IntVar(&count, "count", 250, "每个周期获取 K 线数量（1-5000）")
		fs.StringVar(&freq, "freq", "all", "周期：all、D1、M60 或 M30")
		return nil
	}, func(ctx context.Context, store *sqlitestore.Store) error {
		code = strings.TrimSpace(code)
		if all == (code != "") { return errors.New("必须且只能指定 --ts-code 或 --all") }
		if count < 1 || count > 5000 { return errors.New("--count 必须在 1-5000") }
		frequencies := []marketdata.Frequency{marketdata.FrequencyD1, marketdata.FrequencyM60, marketdata.FrequencyM30}
		if freq != "all" {
			f := marketdata.Frequency(freq)
			if !f.Valid() { return fmt.Errorf("不支持的周期 %q", freq) }
			frequencies = []marketdata.Frequency{f}
		}
		targets := []string{code}
		if all {
			securities, err := store.ListActiveStockBasics(ctx)
			if err != nil { return fmt.Errorf("读取 ACTIVE 证券池失败: %w", err) }
			targets = make([]string, 0, len(securities))
			for _, security := range securities {
				if tdxtypes.IsStock(security.TsCode) && !tdxtypes.IsETF(security.TsCode) && !tdxtypes.IsIndex(security.TsCode) {
					targets = append(targets, security.TsCode)
				}
			}
			if len(targets) == 0 { return errors.New("ACTIVE 普通 A 股股票池为空；请先导入经核验的证券主数据") }
		}
		adapter := gotdxadapter.New(gotdxadapter.Config{})
		defer adapter.Close()
		type symbolResult struct {
			TsCode string "json:\"ts_code\""
			Counts map[marketdata.Frequency]int "json:\"counts,omitempty\""
			Error string "json:\"error,omitempty\""
		}
		type syncSummary struct {
			Requested int "json:\"requested\""
			Succeeded int "json:\"succeeded\""
			Failed int "json:\"failed\""
			Results []symbolResult "json:\"results\""
		}
		summary := syncSummary{Requested: len(targets), Results: make([]symbolResult, 0, len(targets))}
		for _, target := range targets {
			if err := ctx.Err(); err != nil { break }
			result := symbolResult{TsCode: target, Counts: make(map[marketdata.Frequency]int)}
			batches := make([][]marketdata.Bar, 0, len(frequencies))
			for _, frequency := range frequencies {
				bars, err := adapter.FetchBars(ctx, target, frequency, uint16(count))
				if err != nil { result.Error = fmt.Sprintf("获取 %s K 线失败: %v", frequency, err); break }
				if len(bars) == 0 { result.Error = fmt.Sprintf("%s 未返回 %s K 线", target, frequency); break }
				batches = append(batches, bars)
				result.Counts[frequency] = len(bars)
			}
			if result.Error == "" {
				for _, bars := range batches {
					if err := store.UpsertBars(ctx, bars); err != nil { result.Error = fmt.Sprintf("写入 %s %s K 线失败: %v", target, bars[0].Frequency, err); break }
				}
			}
			if result.Error == "" { summary.Succeeded++ } else { summary.Failed++ }
			summary.Results = append(summary.Results, result)
		}
		if err := writeJSON(os.Stdout, summary); err != nil { return err }
		if err := ctx.Err(); err != nil { return err }
		if summary.Failed > 0 { return fmt.Errorf("sync-bars 完成但有 %d/%d 只证券失败；请根据 results 检查并重试", summary.Failed, summary.Requested) }
		return nil
	})
}

type referenceImport struct {
	Securities []universe.StockBasic `json:"securities"`
	DailyStates []universe.SecurityDailyState `json:"daily_states"`
	TradeCalendar []calendar.TradeDay `json:"trade_calendar"`
}

func runImportReference(args []string) error {
	var path string
	return withStore("import-reference", args, func(fs *flag.FlagSet) error {
		fs.StringVar(&path, "file", "", "reference JSON 文件路径")
		return nil
	}, func(ctx context.Context, store *sqlitestore.Store) error {
		if strings.TrimSpace(path) == "" { return errors.New("--file 必填") }
		body, err := os.ReadFile(path)
		if err != nil { return err }
		var input referenceImport
		if err := json.Unmarshal(body, &input); err != nil {
			return fmt.Errorf("解析 reference JSON 失败: %w", err)
		}
		if len(input.Securities)+len(input.DailyStates)+len(input.TradeCalendar) == 0 {
			return errors.New("reference JSON 至少包含一类非空数据")
		}
		for i, item := range input.Securities {
			if err := item.Validate(); err != nil { return fmt.Errorf("securities[%d]: %w", i, err) }
		}
		for i, item := range input.DailyStates {
			if err := item.Validate(); err != nil { return fmt.Errorf("daily_states[%d]: %w", i, err) }
		}
		for i, item := range input.TradeCalendar {
			if err := item.Validate(); err != nil { return fmt.Errorf("trade_calendar[%d]: %w", i, err) }
		}
		// 每类数据复用已有仓储的独立事务和来源保护规则。
		if len(input.Securities) > 0 {
			if err := store.UpsertStockBasics(ctx, input.Securities); err != nil { return err }
		}
		if len(input.DailyStates) > 0 {
			if err := store.UpsertDailyStates(ctx, input.DailyStates); err != nil { return err }
		}
		if len(input.TradeCalendar) > 0 {
			if err := store.UpsertTradeCalendar(ctx, input.TradeCalendar); err != nil { return err }
		}
		return writeJSON(os.Stdout, map[string]int{
			"securities": len(input.Securities),
			"daily_states": len(input.DailyStates),
			"trade_calendar": len(input.TradeCalendar),
		})
	})
}

func runEvaluate(args []string) error {
	var code, asOfText, configPath string
	return withStore("evaluate", args, func(fs *flag.FlagSet) error {
		fs.StringVar(&code, "ts-code", "", "证券代码")
		fs.StringVar(&asOfText, "as-of", "", "决策时点 RFC3339（必须带时区）")
		fs.StringVar(&configPath, "config", "", "策略配置 JSON")
		return nil
	}, func(ctx context.Context, store *sqlitestore.Store) error {
		if strings.TrimSpace(code) == "" || strings.TrimSpace(configPath) == "" {
			return errors.New("--ts-code 和 --config 必填")
		}
		asOf, err := parseAsOf(asOfText); if err != nil { return err }
		evaluator, err := newEvaluator(store, configPath); if err != nil { return err }
		result, err := evaluator.EvaluateSymbol(ctx, code, asOf); if err != nil { return err }
		return writeJSON(os.Stdout, result)
	})
}

func runScan(args []string) error {
	var asOfText, configPath string
	return withStore("scan", args, func(fs *flag.FlagSet) error {
		fs.StringVar(&asOfText, "as-of", "", "扫描时点 RFC3339（必须带时区）")
		fs.StringVar(&configPath, "config", "", "策略配置 JSON")
		return nil
	}, func(ctx context.Context, store *sqlitestore.Store) error {
		if strings.TrimSpace(configPath) == "" { return errors.New("--config 必填") }
		asOf, err := parseAsOf(asOfText); if err != nil { return err }
		evaluator, err := newEvaluator(store, configPath); if err != nil { return err }
		s, err := scanner.New(store, evaluator); if err != nil { return err }
		run, runErr := s.Run(ctx, asOf)
		if err := writeJSON(os.Stdout, run); err != nil { return err }
		return runErr
	})
}

func runScanResult(args []string) error {
	var scanID string
	return withStore("scan-result", args, func(fs *flag.FlagSet) error {
		fs.StringVar(&scanID, "scan-id", "", "扫描 ID")
		return nil
	}, func(ctx context.Context, store *sqlitestore.Store) error {
		if strings.TrimSpace(scanID) == "" { return errors.New("--scan-id 必填") }
		result, err := store.GetScanResult(ctx, scanID); if err != nil { return err }
		return writeJSON(os.Stdout, result)
	})
}

func runHistory(args []string) error {
	var tsCode, strategyID, status, fromText, toText string
	var limit, offset int
	return withStore("history", args, func(fs *flag.FlagSet) error {
		fs.StringVar(&tsCode, "ts-code", "", "证券代码")
		fs.StringVar(&strategyID, "strategy-id", "", "策略 ID")
		fs.StringVar(&status, "status", "", "决策状态")
		fs.StringVar(&fromText, "from", "", "起始时间 RFC3339")
		fs.StringVar(&toText, "to", "", "结束时间 RFC3339")
		fs.IntVar(&limit, "limit", 50, "每页数量 1-200")
		fs.IntVar(&offset, "offset", 0, "分页偏移")
		return nil
	}, func(ctx context.Context, store *sqlitestore.Store) error {
		query := sqlitestore.StrategyDecisionQuery{
			TsCode: tsCode, StrategyID: strategyID,
			Status: contract.DecisionStatus(status), Limit: limit, Offset: offset,
		}
		if fromText != "" {
			v, err := parseAsOf(fromText); if err != nil { return err }; query.FromAsOf = &v
		}
		if toText != "" {
			v, err := parseAsOf(toText); if err != nil { return err }; query.ToAsOf = &v
		}
		page, err := store.ListStrategyDecisions(ctx, query); if err != nil { return err }
		return writeJSON(os.Stdout, page)
	})
}

func parseAsOf(value string) (time.Time, error) {
	if strings.TrimSpace(value) == "" { return time.Now().In(marketdata.ShanghaiLocation()), nil }
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("时间 %q 必须使用带时区的 RFC3339 格式: %w", value, err)
	}
	return t.In(marketdata.ShanghaiLocation()), nil
}

func newEvaluator(store *sqlitestore.Store, configPath string) (*appstrategy.Service, error) {
	body, err := os.ReadFile(configPath); if err != nil { return nil, err }
	var cfg runtimeConfig
	if err := json.Unmarshal(body, &cfg); err != nil { return nil, fmt.Errorf("解析策略配置失败: %w", err) }
	if token := os.Getenv("ASHARE_CZSC_TOKEN"); token != "" { cfg.CZSCToken = token }
	client, err := czscclient.New(cfg.CZSCURL, time.Duration(cfg.CZSCTimeoutSeconds)*time.Second, cfg.CZSCToken)
	if err != nil { return nil, err }
	return appstrategy.New(store, client, cfg.Strategy)
}

type runtimeConfig struct {
	CZSCURL string `json:"czsc_url"`
	CZSCTimeoutSeconds int `json:"czsc_timeout_seconds"`
	CZSCToken string `json:"czsc_token,omitempty"`
	Strategy appstrategy.Config `json:"strategy"`
}

func writeJSON(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
