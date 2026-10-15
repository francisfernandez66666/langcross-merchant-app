// Command api 是 LangCross 商家店端的后端入口：
// 连库 → 自动迁移 → 装配 OCR/翻译实现 → 启动 HTTP 服务。
//
// 启动顺序即依赖顺序，任一关键步骤失败即 fail-fast（log.Fatalf）：
// 配置 → 数据库连接 → 内嵌迁移 → 装配外部能力 → 监听 → 等待退出信号。
//
// 外部能力（OCR / 翻译）的装配结果会打进启动日志，
// 便于一眼确认当前跑的是「真实服务」还是「本地降级实现」。
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

// main 装配并运行 HTTP 服务，直到收到中断 / 终止信号后优雅退出。
func main() {
	cfg := config.Load()

	// 连库：失败即退出，因为本服务没有「无库降级」模式。
	database, err := db.Open(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("数据库连接失败: %v（检查 LC_DATABASE_URL）", err)
	}
	defer func() { _ = database.Close() }()

	// 自动迁移：迁移文件已内嵌进二进制，启动时补齐增量即可。
	// 30s 超时是给「首次建表 + 执行全部迁移」留的余量。
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.Migrate(ctx, database); err != nil {
		log.Fatalf("迁移失败: %v", err)
	}

	// 装配依赖：repo 同时充当翻译记忆（TM）存储，因此直接传给 NewTranslator。
	// OCR / 翻译都按配置选择实现，未配置时自动走降级版本，保证零配置可跑。
	r := repo.New(database)
	ocr := service.NewOCR(cfg.OCRServiceURL)
	tr := service.NewTranslator(cfg.TranslateURL, cfg.MTEmail, r)
	srv := httpapi.NewServer(cfg, r, ocr, tr)

	httpSrv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	// 监听放在 goroutine：主协程要空出来等退出信号。
	go func() {
		// 启动日志把「真实实现 / 降级实现」显式打出来，避免误以为在跑真实 OCR/翻译。
		log.Printf("LangCross 商家店端 API 监听 %s ｜ OCR=%s ｜ 翻译=%s ｜ 门店页基址=%s",
			cfg.Addr,
			modeLabel(ocr.Ready(), "paddleocr", "stub-manual"),
			modeLabel(tr.Ready(), "live", "dict-stub"),
			cfg.PublicBaseURL)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("HTTP 服务失败: %v", err)
		}
	}()

	// 等待 Ctrl-C（SIGINT）或 SIGTERM，然后给在途请求 10s 收尾时间。
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	_ = httpSrv.Shutdown(shutdownCtx)
	log.Println("已优雅退出")
}

// modeLabel 按就绪状态在「真实实现」与「降级实现」两个标签间选择，
// 仅用于启动日志的可读性，不参与任何业务逻辑。
func modeLabel(ready bool, live, fallback string) string {
	if ready {
		return live
	}
	return fallback
}
