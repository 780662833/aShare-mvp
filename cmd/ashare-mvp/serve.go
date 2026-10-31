package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/780662833/aShare-mvp/internal/httpapi"
	sqlitestore "github.com/780662833/aShare-mvp/internal/storage/sqlite"
)

func runServe(args []string) error {
	var configPath, addr string
	var scanTimeout time.Duration
	return withStore("serve", args, func(fs *flag.FlagSet) error {
		fs.StringVar(&configPath, "config", "", "策略配置 JSON")
		fs.StringVar(&addr, "addr", "127.0.0.1:8080", "HTTP 监听地址（仅允许 loopback）")
		fs.DurationVar(&scanTimeout, "scan-timeout", 30*time.Minute, "后台扫描任务最大运行时间")
		return nil
	}, func(ctx context.Context, store *sqlitestore.Store) error {
		if strings.TrimSpace(configPath) == "" { return errors.New("--config 必填") }
		if scanTimeout <= 0 { return errors.New("--scan-timeout 必须大于 0") }
		host, _, err := net.SplitHostPort(addr)
		if err != nil { return fmt.Errorf("--addr 无效: %w", err) }
		if host != "localhost" {
			ip := net.ParseIP(host)
			if ip == nil || !ip.IsLoopback() { return errors.New("V1 API 仅允许监听 localhost/loopback 地址") }
		}
		evaluator, err := newEvaluator(store, configPath)
		if err != nil { return err }
		api, err := httpapi.New(store, evaluator, scanTimeout)
		if err != nil { return err }
		defer api.Close()
		httpServer := &http.Server{Addr: addr, Handler: api.Handler(), ReadHeaderTimeout: 5*time.Second, IdleTimeout: 60*time.Second}
		serverErr := make(chan error, 1)
		go func() { serverErr <- httpServer.ListenAndServe() }()
		fmt.Printf("aShare-mvp API 已启动，监听地址：%s\n", addr)
		select {
		case err := <-serverErr:
			if errors.Is(err, http.ErrServerClosed) { return nil }
			return err
		case <-ctx.Done():
			api.Close()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := httpServer.Shutdown(shutdownCtx); err != nil { _ = httpServer.Close(); return err }
			if errors.Is(ctx.Err(), context.Canceled) { return nil }
			return ctx.Err()
		}
	})
}
