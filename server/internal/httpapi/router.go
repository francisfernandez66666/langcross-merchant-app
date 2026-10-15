// Package httpapi 是 LangCross 商家端的 HTTP 接口层：路由注册、中间件、统一响应、
// 鉴权与全部业务 handler。
//
// 技术栈刻意保持极简：只用 Go 标准库 net/http（Go 1.22+ 的「方法 + 通配符」路由）
// 与 database/sql，不引入 Web 框架、不引入 ORM。这样「一条业务链路」在少数几个
// 文件里就能从头读到尾，也避免了框架级复杂度。
//
// 文件分工：
//
//	router.go            路由表 + 健康检查（本文件）
//	middleware.go        CORS / 访问日志 / 鉴权（注入 account_id）
//	respond.go           统一 JSON 响应与错误码常量
//	auth.go              无状态 token 的签发与校验（HMAC-SHA256）
//	handlers_auth.go     发送验证码、登录/自动注册、当前账号、埋点
//	handlers_merchant.go 商家端：门店 / 二维码 / 确认稿 / 翻译 / 菜单 / 评价 / 券
//	handlers_public.go   客人端（公开）与工具接口
//
// 统一响应契约：
//
//	成功：{"ok":true, ...业务字段}
//	失败：{"ok":false,"error":{"code":"<语义串>","msg":"<给用户看的人话>"}}
//
// 错误码定义见 respond.go 的 Code* 常量。
//
// 鉴权方式：手机号 + 短信验证码登录，成功后返回 token，客户端在
// Authorization: Bearer <token> 中携带。需登录的路由统一包 s.withAuth，
// 由它校验 token 并把 account_id 注入 request context；业务 handler 通过
// accountID(r) 读取当前账号。
//
// # 路由表
//
// 公开（无需登录）：
//
//	GET   /api/health                            健康检查：服务名 + OCR/翻译引擎的就绪状态与当前模式
//	POST  /api/auth/otp                          发送手机验证码（开发模式回显 dev_code）
//	POST  /api/auth/login                        手机号 + 验证码登录；账号不存在则自动注册
//	GET   /api/public/store/{slug}               客人端门店页：门店 + 菜单 + 券模板（WiFi 为空则不下发该字段）
//	POST  /api/public/store/{slug}/rating        客人提交三维度评价；★ 评价即自动领券
//	GET   /api/public/coupon/{code}              按券码查券（核销页预览 / 客人回看）
//	POST  /api/public/translate-preview          注册前多语预览：不落库、不鉴权
//	GET   /api/translation/langs                 受支持语种、原文语种与当前翻译引擎
//	GET   /api/uploads/{name}                    菜品图片静态文件（魔数已核验过，见 service.upload.go）
//	POST  /api/ocr/extract                       菜单 OCR 识别代理；识别服务不可用时报错让前端手动录入
//	POST  /api/events                            前端埋点上报（失败不阻塞前端）
//
// 需登录（Authorization: Bearer <token>）：
//
//	GET   /api/me                                当前账号 + 其名下门店列表
//	GET   /api/stores                            列出当前账号的门店
//	POST  /api/stores                            建店：★ 自动配券模板 + 生成门口码/桌码链接
//	GET   /api/stores/{id}                       门店详情 + 菜单 + 最新确认稿 + 链接
//	PATCH /api/stores/{id}                       局部更新门店（含档位 service_tier / 点单模式 order_mode）
//	GET   /api/stores/{id}/qr                    生成门口码 / 桌码 PNG（?ref=door|t01）
//	GET   /api/stores/{id}/draft                 取最新原文确认稿
//	POST  /api/stores/{id}/draft                 保存原文确认稿（OCR 后的人工校对结果）
//	POST  /api/stores/{id}/draft/confirm         确认原文；★ 未确认不允许翻译
//	POST  /api/stores/{id}/translate             多语化：一次可翻多个语种，落 dish.names_json
//	GET   /api/stores/{id}/dishes                列出菜单（多语化成品）
//	PATCH /api/stores/{id}/dishes/{dishId}       更新售罄 / 每日剩余（唯一动态字段）
//	POST  /api/stores/{id}/dishes/{dishId}/image 上传菜品图片（multipart，≤5MB，校验魔数）
//	DELETE /api/stores/{id}/dishes/{dishId}/image 删除菜品图片
//	GET   /api/stores/{id}/ratings               列出评价（商家端看中文译文）
//	GET   /api/stores/{id}/insights              经营洞察（口径：只统计用我们工具的外国客数据）
//	GET   /api/stores/{id}/coupon-template       取券模板
//	GET   /api/stores/{id}/coupons               列出本店券（核销台）
//	POST  /api/stores/{id}/coupon/redeem         核销券：★ 必须校验券归属本店，重复核销拒绝
//
// 后台管理（需登录；按角色门禁，见 middleware.go 的 requireRole）：
//
//	GET   /api/admin/tenants                     列出租户（超管）
//	POST  /api/admin/tenants                     新建租户（超管）
//	PATCH /api/admin/tenants/{id}                改租户名 / 状态 / 到期 / 配额（超管）
//	GET   /api/admin/tenants/{id}/stores         某租户的门店（超管）
//	GET   /api/admin/stats                       平台总览：租户数 / 账号数 / 门店数（超管）
//	GET   /api/admin/accounts                    列账号（租户管理员看本租户，超管可用 ?tenant_id=）
//	POST  /api/admin/accounts                    邀请账号：手机号 + 角色（租户管理员及以上）
//	PATCH /api/admin/accounts/{id}/role          改角色（超管任意；租户管理员仅限本租户低等级）
//	PATCH /api/admin/accounts/{id}/status        停用 / 启用账号
//	GET   /api/admin/translate-keys              列出本租户翻译 Key（脱敏）
//	POST  /api/admin/translate-keys              新增翻译 Key（密文落库）
//	DELETE /api/admin/translate-keys/{id}         删除 Key
//	POST  /api/admin/translate-keys/{id}/reveal  查看 Key 明文（一次性返回 + 审计）
//	POST  /api/admin/translate-keys/{id}/rotate  轮换 Key（新密文覆盖，旧的立即失效）
//	POST  /api/admin/translate-keys/{id}/status  启用 / 停用 Key
//	POST  /api/admin/translate-keys/{id}/test    试译：用这把 Key 翻一句，验证可用性与质量
//	GET   /api/admin/audit                       管理操作日志（超管全量；租户管理员看本租户）
//
// 注意：所有 /api/stores/{id}/... 路由进入 handler 后的第一件事都是 requireStore，
// 校验「门店存在 + 你有权访问」（owner，或同租户的租户管理员 / 超管），越权返回 FORBIDDEN。
package httpapi

import (
	"net/http"

	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/assist"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/config"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/repo"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/service"
)

// Server 汇总 HTTP 层依赖。
//
// 所有 handler 都挂在这个结构上，好处是依赖显式：谁需要什么一目了然，
// 也方便测试时替换成假实现（fake repo / stub OCR / 假翻译器）。
type Server struct {
	cfg    config.Config        // 运行期配置：对外基址、密钥、DevMode、翻译链地址等
	repo   *repo.Repo           // 数据访问层（PostgreSQL，无 ORM）
	ocr    service.OCRProvider  // 菜单图片识别：生产转发 PaddleOCR 微服务，无服务时走 stub 手动保底
	tr     service.Translator   // 翻译链：主引擎 + 内置词典兜底 + TM 缓存（见 service.NewTranslator）
	images service.ImageStore   // 菜品图片存储：默认落本地磁盘，可换对象存储（见 service.upload.go 的接缝）
	keys   *service.KeyVault    // 租户翻译 Key 凭据箱：后台维护的密文在此解密（见 service/tenant_translate.go）
	assist *assist.Engine       // AI 经营助手引擎：四道主体 + 出站守卫（见 assist/engine.go）
	aiCred *service.AICredVault // LLM 凭据箱：按租户解析当前该用哪份模型凭据
	secret []byte               // token 签名密钥，由 cfg.Secret 转字节得到
}

// NewServer 构造 HTTP 服务。
//
// 只做依赖注入，不在这里做任何初始化 IO：数据库迁移、翻译链装配等
// 都发生在 cmd/api/main.go，便于测试时直接构造 Server。
// images 传 nil 时兜底成 LocalImageStore；keys 传 nil 时「翻译 Key 管理」类接口
// 会明确返回未启用（而不是 panic），保证老调用点不必逐个改。
// assist / aiCred 传 nil 时「经营助手」类接口返回未启用——但引擎本身零依赖，
// 正常启动路径总会装上（见 main.go 的装配段）。
func NewServer(cfg config.Config, r *repo.Repo, ocr service.OCRProvider, tr service.Translator,
	images service.ImageStore, keys *service.KeyVault) *Server {
	if images == nil {
		images = service.NewLocalImageStore(cfg.UploadDir)
	}
	return &Server{cfg: cfg, repo: r, ocr: ocr, tr: tr, images: images, keys: keys, secret: []byte(cfg.Secret)}
}

// WithAssist 装配 AI 经营助手（引擎 + LLM 凭据箱）。
//
// 单独一个构造方法而不是塞进 NewServer 的第七、第八个参数：
// 助手是「可后加的能力」，与翻译 / 门店 / 券不在同一生命周期上，
// 分开装配让依赖关系更清楚，也让 NewServer 的老调用点不必改。
//
// eng 传 nil 时用默认内存会话存储构造一个；store 传 nil 时退化为内存版
// （单机开发可用，多实例部署请传 repo.NewAssistSessionStore(db)）。
func (s *Server) WithAssist(eng *assist.Engine, vault *service.AICredVault) *Server {
	if eng == nil {
		eng = assist.New()
	}
	s.assist = eng
	s.aiCred = vault
	return s
}

// Routes 注册全部路由（Go 1.22+ 方法+通配符路由）。
//
// 为什么不用 Web 框架：路由形态简单，标准库的「方法 + 通配符」模式已经够用，
// 且避免为「10 个语种 + 一条业务链路」引入框架级复杂度。
//
// 中间件包裹顺序为 withLog(withCORS(mux))：
//   - withLog 在最外层，才能覆盖包括 CORS 预检与鉴权失败在内的所有请求；
//   - withCORS 在内层，负责给真实响应加跨域头。
//
// 完整路由表见本文件顶部的包注释。
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	// ── 健康检查 ──
	// 公开且不鉴权：给探针与前端判断「OCR / 翻译当前是否可用」。
	mux.HandleFunc("GET /api/health", s.handleHealth)

	// ── 认证（手机号 + 验证码） ──
	// 发码与登录必须公开；/api/me 需要登录才能回答「我是谁」。
	mux.HandleFunc("POST /api/auth/otp", s.handleSendOTP)
	mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	mux.HandleFunc("GET /api/me", s.withAuth(s.handleMe))

	// ── 商家端（需登录） ──
	// 这些路由都以门店为作用域，进入 handler 后第一件事是 requireStore
	// 校验「门店存在 + 属于当前账号」，越权返回 FORBIDDEN。
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
	mux.HandleFunc("POST /api/stores/{id}/dishes/{dishId}/image", s.withAuth(s.handleUploadDishImage))
	mux.HandleFunc("DELETE /api/stores/{id}/dishes/{dishId}/image", s.withAuth(s.handleDeleteDishImage))
	mux.HandleFunc("GET /api/stores/{id}/ratings", s.withAuth(s.handleListRatings))
	mux.HandleFunc("GET /api/stores/{id}/insights", s.withAuth(s.handleInsights))
	mux.HandleFunc("GET /api/stores/{id}/coupon-template", s.withAuth(s.handleGetTemplate))
	mux.HandleFunc("GET /api/stores/{id}/coupons", s.withAuth(s.handleListCoupons))
	mux.HandleFunc("POST /api/stores/{id}/coupon/redeem", s.withAuth(s.handleRedeem))

	// ── 后台管理（租户 / 角色 / 翻译 Key） ──
	// 设计对齐 langcross 平台的租户模型：超管管租户，租户管理员管自己人与翻译 Key。
	// 所有路由仍走 withAuth（先认证），角色判定在 handler 内用 requireRole 完成，
	// 这样错误码才能区分「未登录 401」与「等级不够 403」。
	s.registerAdmin(mux)

	// ── AI 经营助手 ──
	// 商家侧对话 + 超管侧 LLM 凭据维护。凭据写操作只归超管（见 handlers_assist_admin.go）。
	s.registerAssist(mux)

	// ── 客人端 AI 点菜助手 + 服务请求 ──
	// 公开不登录（扫桌码即用）：知识库是本店菜品与服务，守卫改为「菜品与价格不许编」。
	// 助手只提议动作按钮，客人点一下才生成服务请求（提议—确认边界）。
	s.registerGuestAssist(mux)

	// ── 客人端（公开，无需登录） ──
	// 客人扫桌码即进入，不能有任何登录门槛；{slug} 是门店对外不可变标识，
	// 它被印在物料上，因此路由形态（/s/{slug}）也长期稳定。
	mux.HandleFunc("GET /api/public/store/{slug}", s.handlePublicStore)
	mux.HandleFunc("POST /api/public/store/{slug}/rating", s.handleSubmitRating)
	mux.HandleFunc("GET /api/public/coupon/{code}", s.handlePublicCoupon)
	// P4 注册前的多语预览：不落库、不需要登录
	mux.HandleFunc("POST /api/public/translate-preview", s.handlePreviewTranslate)

	// ── 工具 ──
	// 语种清单来自 service.SupportedLangs，前端据此渲染语言选择器；
	// 新增语种只需在 service 里登记一行，本文件与其它业务代码零改动。
	// OCR 与埋点同样公开：OCR 在注册前就要能用，埋点则来自客人端页面。
	mux.HandleFunc("GET /api/translation/langs", s.handleLangs)
	mux.HandleFunc("POST /api/ocr/extract", s.handleOCR)
	mux.HandleFunc("POST /api/events", s.handleTrack)

	// 菜品图片静态服务：文件本身不带秘密（文件名是 32 位随机 hex），
	// 但「谁能知道文件名」就等于「谁能看图」，因此不额外加鉴权，
	// 也不在任意接口里返回完整图片清单。ImageStore 的接缝使这里可整体换成 CDN。
	imgHandler := http.HandlerFunc(service.ServeImage(s.cfg.UploadDir).ServeHTTP)
	mux.HandleFunc("GET /api/uploads/", imgHandler)
	mux.HandleFunc("HEAD /api/uploads/", imgHandler)

	return withLog(s.withCORS(mux))
}

// handleHealth 健康检查：对外暴露服务名，以及 OCR / 翻译的就绪状态与当前模式。
//
// 只报「是否可用 + 走哪条实现」，不泄露任何密钥或内部服务地址。
// 前端可据此在页面上提示「当前为手动录入模式 / 词典兜底模式」。
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

// modeName 把「就绪布尔值」翻译成人类可读的实现名，供健康检查展示。
// ready 为真返回 live（实际生效的实现），否则返回 fallback（降级实现）。
func modeName(ready bool, live, fallback string) string {
	if ready {
		return live
	}
	return fallback
}
