package httpapi

import (
	"net/http"

	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/config"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/repo"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/service"
)

// Server 汇总 HTTP 层依赖。
type Server struct {
	cfg    config.Config
	repo   *repo.Repo
	ocr    service.OCRProvider
	tr     service.Translator
	secret []byte
}

// NewServer 构造 HTTP 服务。
func NewServer(cfg config.Config, r *repo.Repo, ocr service.OCRProvider, tr service.Translator) *Server {
	return &Server{cfg: cfg, repo: r, ocr: ocr, tr: tr, secret: []byte(cfg.Secret)}
}

// Routes 注册全部路由（Go 1.22+ 方法+通配符路由）。
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	// ── 健康检查 ──
	mux.HandleFunc("GET /api/health", s.handleHealth)

	// ── 认证（手机号 + 验证码） ──
	mux.HandleFunc("POST /api/auth/otp", s.handleSendOTP)
	mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	mux.HandleFunc("GET /api/me", s.withAuth(s.handleMe))

	// ── 商家端（需登录） ──
	mux.HandleFunc("GET /api/stores", s.withAuth(s.handleListStores))
	mux.HandleFunc("POST /api/stores", s.withAuth(s.handleCreateStore))
	mux.HandleFunc("GET /api/stores/{id}", s.withAuth(s.handleGetStore))
	mux.HandleFunc("PATCH /api/stores/{id}", s.withAuth(s.handlePatchStore))
	mux.HandleFunc("GET /api/stores/{id}/qr", s.withAuth(s.handleQR))
	mux.HandleFunc("GET /api/stores/{id}/draft", s.withAuth(s.handleGetDraft))
	mux.HandleFunc("POST /api/stores/{id}/draft", s.withAuth(s.handleSaveDraft))
	mux.HandleFunc("POST /api/stores/{id}/draft/confirm", s.withAuth(s.handleConfirmDraft))
	mux.HandleFunc("POST /api/stores/{id}/translate", s.withAuth(s.handleTranslate))
	mux.HandleFunc("GET /api/stores/{id}/dishes", s.withAuth(s.handleListDishes))
	mux.HandleFunc("PATCH /api/stores/{id}/dishes/{dishId}", s.withAuth(s.handlePatchDish))
	mux.HandleFunc("GET /api/stores/{id}/ratings", s.withAuth(s.handleListRatings))
	mux.HandleFunc("GET /api/stores/{id}/insights", s.withAuth(s.handleInsights))
	mux.HandleFunc("GET /api/stores/{id}/coupon-template", s.withAuth(s.handleGetTemplate))
	mux.HandleFunc("GET /api/stores/{id}/coupons", s.withAuth(s.handleListCoupons))
	mux.HandleFunc("POST /api/stores/{id}/coupon/redeem", s.withAuth(s.handleRedeem))

	// ── 客人端（公开，无需登录） ──
	mux.HandleFunc("GET /api/public/store/{slug}", s.handlePublicStore)
	mux.HandleFunc("POST /api/public/store/{slug}/rating", s.handleSubmitRating)
	mux.HandleFunc("GET /api/public/coupon/{code}", s.handlePublicCoupon)
	// P4 注册前的多语预览：不落库、不需要登录
	mux.HandleFunc("POST /api/public/translate-preview", s.handlePreviewTranslate)

	// ── 工具 ──
	mux.HandleFunc("POST /api/ocr/extract", s.handleOCR)
	mux.HandleFunc("POST /api/events", s.handleTrack)

	return withLog(s.withCORS(mux))
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	ok(w, map[string]any{
		"ok":              true,
		"service":         "langcross-merchant-app",
		"ocr_ready":       s.ocr.Ready(),
		"ocr_mode":        modeName(s.ocr.Ready(), "paddleocr", "stub-manual"),
		"translate_ready": s.tr.Ready(),
		"translate_mode":  modeName(s.tr.Ready(), "live", "dict-stub"),
	})
}

func modeName(ready bool, live, fallback string) string {
	if ready {
		return live
	}
	return fallback
}
