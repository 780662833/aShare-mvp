package main

import (
	"context"
	"flag"
	"log"
	"os"
	"path/filepath"

	sqlitestore "github.com/780662833/aShare-mvp/internal/storage/sqlite"
)

func main() {
	dbPath := flag.String("db", filepath.Join("data", "ashare.db"), "SQLite 数据库文件路径")
	flag.Parse()

	if err := os.MkdirAll(filepath.Dir(*dbPath), 0o755); err != nil {
		log.Fatalf("创建数据目录失败: %v", err)
	}

	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, *dbPath)
	if err != nil {
		log.Fatalf("初始化 SQLite 失败: %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			log.Printf("关闭 SQLite 失败: %v", err)
		}
	}()

	log.Printf("aShare-mvp 数据库已就绪: %s", *dbPath)
}
