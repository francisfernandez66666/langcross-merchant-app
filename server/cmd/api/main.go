// Command api 是 LangCross 商家店端的后端入口：
// 连库 → 自动迁移 → 装配 OCR/翻译实现 → 启动 HTTP 服务。
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/config"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/db"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/httpapi"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/repo"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/service"
)

func main() {
	cfg := config.Load()

	database, err := db.Open(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("数据库连接失败: %v（检查 LC_DATABASE_URL）", err)
	}
	defer func() { _ = database.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.Migrate(ctx, database); err != nil {
		log.Fatalf("迁移失败: %v", err)
	}

	r := repo.New(database)
	ocr := service.NewOCR(cfg.OCRServiceURL)
	tr := service.NewTranslator(cfg.TranslateURL, cfg.MTEmail, r)
	srv := httpapi.NewServer(cfg, r, ocr, tr)

	httpSrv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("LangCross 商家店端 API 监听 %s ｜ OCR=%s ｜ 翻译=%s ｜ 门店页基址=%s",
			cfg.Addr,
			modeLabel(ocr.Ready(), "paddleocr", "stub-manual"),
			modeLabel(tr.Ready(), "live", "dict-stub"),
			cfg.PublicBaseURL)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("HTTP 服务失败: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	_ = httpSrv.Shutdown(shutdownCtx)
	log.Println("已优雅退出")
}

func modeLabel(ready bool, live, fallback string) string {
	if ready {
		return live
	}
	return fallback
}
