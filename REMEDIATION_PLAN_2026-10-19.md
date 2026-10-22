# 缺陷验证与修改文档（LangCross 商家店端）

- **日期**：2026-10-19
- **上游文档**：《AUDIT_FULL_BYTELEVEL_UAT_2026-10-19.md》（字节级 UAT 审计报告）
- **本文目的**：对审计报告提出的每条缺陷/缺失，回到真实代码与产品文档逐条验证真伪，区分「真缺陷 / 已知权衡 / 路线图规划 / 文档漂移 / 待产品决策」，并给出可执行的修改方案（文件:行 + 改法 + 验收标准）。
- **验证原则**：代码结论以 `file:line` 为证；产品意图以 `商家店端方案/*.md` 与代码注释为证；否定结论用 python 扫描复核（本机 grep 为 toybox，不可信）。

---

## 1. 验证结论总表

| # | 审计报告条目 | 判定 | 关键证据 |
|---|---|---|---|
| P0-1 | 客人 AI 助手 LLM 装配断链 | **真缺陷（且注释与行为相反）→ 已修（FIX-7，见 §8）** | `handlers_guest_assist.go:178` 传 `nil`；`handlers_assist.go:174-176` `acc == nil` 返回零值；产品文档《商业测算与缺口清单.md:20》把"客人提问自动回答的 LLM 成本"列为待测算项——文档以为已接线 |
| P0-2 | 全仓无 panic recovery | **真缺陷** | python 扫描非测试 Go 代码 `recover()` = 0 处；`router.go` 无 recovery 中间件 |
| P0-3 | render-check 在 main 上红色 | **真缺陷（过程缺口）** | git：`98e5bf3` 加 AI 按钮未同步测试；实测 5 失败+崩溃；产品行为经独立探针 15/15 复验正确 |
| P0-4 | LC_DEV 默认 1 + 默认密钥 | **真缺陷（加固缺口）** | `config.go:61-62`；注释自述"生产必须替换"但无强制 |
| P1-1 | 无 RLS、tenant_id 无 FK | **真缺陷** | 6 个迁移 RLS/POLICY=0；`tenant_id TEXT` 无 REFERENCES；实测超管可建孤儿账号 |
| P1-2 | 测试金字塔倒置 | **真缺陷** | 总覆盖 20.2%，httpapi 2.9%、repo 0% |
| P1-3 | AI 凭据 repo 缺租户 WHERE | **真缺陷（且文件注释自称已做）→ 已修（FIX-8，见 §8）** | `repo/ai_credential.go:16-19` 注释称"归属校验一律写进 WHERE"，实际 6 个方法全是 `WHERE id=$1`（:71-162），与 `repo/tenant.go:209-211` 翻译 Key 的 `id+tenant_id` 双条件不对称 |
| P1-4 | 前端路由无守卫 | **已知权衡（明示设计）** | `AdminApp.tsx:50-52` 注释"刻意不做路由级守卫"；后端 fail-closed 已实测兜底（401/403 全对）。保留，仅做体验加固 |
| P1-5 | 小程序契约漂移 | **真缺陷** | 后端返回 `{"requests":[…],"created_at":number}`（`handlers_guest_assist.go:292`、`domain/guest_request.go:66`），小程序读 `{items, created_at:string}`（`package-staff/.../index.tsx:18-24,33-39`）——字段名和类型双错，运行时会静默空列表 |
| P1-6 | token 30 天无吊销 | **已知权衡（明示设计）** | `middleware.go:71-73` 注释：无状态 token 不可吊销，用每请求回库校验状态补偿；实测停用即时生效 |
| P2-1 | 未知路由 404 纯文本 | **真缺陷** | `router.go` 无兜底路由（实测 `404 page not found` 纯文本） |
| P2-2 | API 无压缩 | **真缺陷** | 实测 `Accept-Encoding: gzip, br` 无 `Content-Encoding` |
| P2-3 | guardNoRawChineseName 死代码 | **真缺陷** | `guest.go:271` 定义，全库仅注释提及（python 扫描 0 调用点） |
| P2-4 | 进程内计数器当护栏 | **已知权衡（注释自述）** | `tenant_translate.go:166`、`handlers_guest_assist.go:65` 均为 `map`；注释自述"尽力而为的护栏不是计费口径"。多实例部署需网关层补 |
| P2-5 | god component ×4 | **真缺陷（技术债）** | AdminApp 911 / PlatformTab 860 / StorePage 710 / Onboarding 676 行；`QrImage` 两处逐字重复且未 `revokeObjectURL` |
| P2-6 | 计费域缺席 | **路线图（非缺陷）** | 《可执行计划_前90天.md:137》"本期不做自助支付：P5=注册（免费）…付费=运营人工收款"；《商家店端MVP_开发任务拆解.md:4》硬约束"计费零改动"。**不是缺失，是范围决策** |
| P2-7 | dish 行仅在翻译时落库 | **待产品决策** | `handlers_public.go:40` 公开页读 `ListDishes`；render-check 夹具注释自认此坑。未翻译店客人端空菜单——若商家确认稿后不翻译就印码，客人看到空白页 |
| P2-8 | CORS 通配符 | **已知权衡（明示设计）** | `middleware.go:34-36` 注释：Bearer token 无 CSRF 风险，改 Cookie 必须收紧 |
| P2-9 | 无部署产物 | **真缺陷（交付缺口）→ 已修（FIX-12，见 §9）** | 曾：无 Dockerfile/nginx/systemd；README 无部署章节；SPA fallback 需部署方自补。现已补 `deploy/nginx.conf` + `deploy/langcross-api.service` + README 部署章节 |
| F-5 | 健康检查报配置非可达性 | **待产品决策（低）** | `router.go` handleHealth 用 `s.tr.Ready()`（装配态）；实测 :8099 黑洞地址仍报 live |
| D-1~D-8 | 文档漂移 8 项 | **文档缺陷（要修文档）** | README 137 vs 表内 160；router.go 注释缺 17 端点；API.md 65 vs 68；0005"四级角色"vs 实际五级；等（详见审计报告 §3.3） |

**统计：真缺陷 17 项 / 已知权衡 4 项 / 路线图 2 项 / 待产品决策 3 项 / 文档缺陷 8 项。**

---

## 2. 修改方案（按执行批次组织）

### 批次 1：止血（0.5 天，无风险，先让 main 恢复全绿）

#### FIX-1 修复 render-check（P0-3）
- **文件**：`web/scripts/guest-render-check.mjs`
- **改法**：
  1. `:341` 底部条期望 `2 → 3`，并断言 primary 按钮含 AI 标识（防再漂移）；
  2. `:400` 评价弹层测试的点击目标从 `.gx-btn--primary` 改为**按文案定位**（先排除含 AI/helper 的按钮，再匹配 /Rate|评价/）——按钮顺序今后再变也不塌；
  3. 短语卡节（`:418` 起）之前，先关掉可能开着的券层（券层关闭钮是 `.gx-btn--block` 不是 `.gx-x`，这是脚本崩溃的直接原因）；
  4. 新增 2 条断言：AI 助手面板可打开、提问后有回复气泡（可直接参考 `web/scripts/indep_guest_probe.mjs` 的实现，已验证 15/15）。
- **验收**：`node scripts/guest-render-check.mjs` 退出码 0；连续跑两遍结果一致。
- **顺手**：把 render-check 接进 CI（需双服务，标 optional job），否则下次还会红。

#### FIX-2 清理过期的错误注释（D-5/D-6/D-7）
- `handlers_guest_assist.go:178` 注释"租户维度：超管维护的平台级/租户级凭据"——在 FIX-4 落地前改成"**当前未接线：客人无账号，按规则兜底；租户级接线见 FIX-4**"（谎言注释比没有注释更危险）；
- `smoke_admin.sh` 注释"角色烘焙进 token，必须重登才生效"改为"角色每请求回库读取，改角色即时生效"（已实测 token 载荷仅 `{sub,exp}`）；
- `assist/rag.go:364` 删除 `guardReplyWifi` 幽灵引用。

#### FIX-3 接入或删除 guardNoRawChineseName（P2-3）
- **文件**：`server/internal/assist/guest.go`
- **改法（建议接入）**：在 `RespondGuest` 守卫链（:85-88，guardCitedDishes/guardPrices/guardPromises 之后）追加 `e.guardNoRawChineseName(rep, kb)`。它是"未翻译菜不漏原文"的第二道保险，与 `rag.go:320-323` 的 `[[NO_NAME_IN_XX]]` 标记机制配对——目前只剩 prompt 单层防护。
- **验收**：补单测（模型回复含未翻译菜中文原文 → 被替换/丢弃），`go test ./internal/assist/` 过。

### 批次 2：生产 hardening（0.5 天）

#### FIX-4 panic recovery 中间件（P0-2）
- **文件**：`server/internal/httpapi/middleware.go`（新增）+ `router.go`（装配）
- **改法**：`withLog(withRecover(withCORS(mux)))`——
```go
func withRecover(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        defer func() {
            if rec := recover(); rec != nil {
                log.Printf("PANIC %s %s: %v\n%s", r.Method, r.URL.Path, rec, debug.Stack())
                fail(w, http.StatusInternalServerError, CodeInternal, "服务内部错误")
            }
        }()
        next.ServeHTTP(w, r)
    })
}
```
- **验收**：新增 `middleware_test.go`：造一个必然 panic 的测试 handler → 断言 500 + `code=INTERNAL` 信封 + 测试进程存活；全量 smoke 回归不受影响（69/0）。

#### FIX-5 未知路由统一 404 信封（P2-1）
- **文件**：`server/internal/httpapi/router.go` `Routes()` 末尾
- **改法**：`mux.Handle("/api/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fail(w, http.StatusNotFound, CodeNotFound, "接口不存在") }))`。Go 1.22 ServeMux 最长匹配优先，不影响已注册路由；非 `/api/` 路径（`/s/xxx` 等）同样处理。
- **验收**：`curl /api/__nope__` → 404 JSON 信封；`bash tools/indep_byte_uat.sh` 的 B4b 由 ✗ 转 ✓；smoke 69 条不回归。

#### FIX-6 生产配置强制 + 显式化（P0-4）
- **文件**：`server/internal/config/config.go` + `cmd/api/main.go`
- **改法**：`config.Load()` 用 `os.LookupEnv` 区分"缺省"与"显式设置"（现在 `env()` 把空串和缺省混同，`config.go:36-41`）；main.go 装配处：
  - `LC_DEV` 显式为非 1 且 `LC_SECRET` 仍为默认占位 → `log.Fatalf` 拒绝启动（生产忘改密钥 = 最严重事故）；
  - `LC_DEV` 缺省（本地零配置）→ 保持可起，但启动日志打**醒目 WARN** + `/api/health` 增加 `dev_mode` 字段（运维一眼可见）。
- **验收**：`LC_DEV=0 go run ./cmd/api` → 退出并提示；本地零配置 → WARN + health 显示 `dev_mode:true`。

### 批次 3：客人助手 LLM 接线（1–2 天，本期最高价值功能修复）

#### FIX-7 按门店租户解析 LLM 凭据（P0-1）★已落地，见 §8
- **文件**：`server/internal/httpapi/handlers_guest_assist.go:178`（+ `handlers_assist.go` 旁路）
- **状态**：**已修**（2026-10-20）。实际落地与下文方案有两处出入——按店日限一道（原方案只写了「或至少暴露调用量」，实际实现为 `reserveGuestLLM` 硬护栏），解析核心抽成 `llmConfigForTenant` 与商家端共用。详见 §8。
- **现状**：`LLM: s.llmConfig(r.Context(), nil)` → `handlers_assist.go:174-176` 的 `acc == nil` 分支返回零值 → 永远规则兜底。
- **改法**：基建已全部就绪——`repo.ActiveAICredentialForTenant`（`repo/ai_credential.go:84-98`）已实现"先租户后平台"解析，`service.AICredVault.Credential(ctx, tenantID)` 已存在。只需：
```go
// guestLLMConfig 按门店租户解析 LLM 凭据（先租户、后平台；都没有则零值走规则兜底）
func (s *Server) guestLLMConfig(ctx context.Context, tenantID string) assist.LLMConfig {
    if s.aiCred == nil { return assist.LLMConfig{} }
    cred, err := s.aiCred.Credential(ctx, tenantID)
    if err != nil || cred == nil { return assist.LLMConfig{} }
    return assist.LLMConfig{BaseURL: cred.BaseURL, APIKey: cred.APIKey, Model: cred.Model}
}
```
  handler 内改为 `LLM: s.guestLLMConfig(r.Context(), st.TenantID)`。
- **注意**：客人端是公开端点，LLM 调用有成本——接线同时把进程内日计数（`tenant_translate.go:166` 同构机制）扩展到客人助手路径，或至少在 `/api/health`/insights 暴露 LLM 调用量，接上《商业测算与缺口清单.md:20》要的"成本可见"。
- **验收**：超管建一条 active AI 凭据 → 客人提问 → `reply.source=llm` 且回复经守卫链（数字核验/菜名核验/价格核验不失效）；无凭据 → `source=rule|fallback`（现有行为不变）。补 handler 级测试（mock LLM server）。

### 批次 4：隔离加固（1 天）

#### FIX-8 AI 凭据 repo 层租户双条件（P1-3）★已落地，见 §8
- **状态**：**已修**（2026-10-20）。最终形态不是下文的朴素双条件 `WHERE id=$1 AND tenant_id=$2`（那个形态会把平台超管挡在自己门外），而是三段式 `WHERE id=$1 AND (tenant_id=$2 OR tenant_id='' OR $2='')`。详见 §8。
- **文件**：`server/internal/repo/ai_credential.go` 的 6 个方法（GetAICredential :71、AICredCiphertext :101、SetAICredentialStatus :112、UpdateAICredential :125、RotateAICredential :139、DeleteAICredential :154）
- **改法**：全部加 `tenantID string` 参数，SQL 改 `WHERE id=$1 AND tenant_id=$2`，与 `repo/tenant.go:209-211` 翻译 Key 完全同构；httpapi 六个 handler（`handlers_assist_admin.go`）传入 `acc.TenantID`（平台超管传 `""`）。**改完文件头 :16-19 的注释才名副其实。**
- **验收**：租户 A 的凭据 ID + 租户 B 的 tenantID → `ErrNotFound`（补 repo 测试或集成验证）；smoke_admin 23 条不回归。

#### FIX-9 tenant_id 补 FK + 孤儿数据清理（P1-1）
- **文件**：新增 `server/migrations/0007_tenant_fk.sql`
- **改法**：`account.tenant_id` / `store.tenant_id` / `ai_credential.tenant_id` / `org.tenant_id` 加 `REFERENCES tenant(id)`（先跑一次性清理 SQL 删孤儿行——本审计实测能造出孤儿账号，现网数据先体检再迁移；注意迁移器要求语句幂等，`migrate.go:30-33`）。RLS 列为独立决策项：若要加，用休眠式策略（未设会话变量时条件恒真）+ repo 连接层 `SetTenantRLS`，工作量大，建议排在 FIX-8 之后单独立项。
- **验收**：新库从头跑全部迁移通过；插入不存在租户的账号 → FK 违例；`bash tools/indep_byte_uat.sh` E 段 15 条不回归。

### 批次 5：测试补层（2–3 天，还测试债）

#### FIX-10 httpapi 中间件 + 租户判定表驱动测试（P1-2）
- **新增** `server/internal/httpapi/middleware_test.go`：withAuth（无 token/坏 token/构造过期 token/停用账号/正常五例）、requireRole（五级 + 超管双条件：role=super_admin 但 tenant_id 非空 → 403）、effectiveTenant（超管带参/超管不带参/普通人忽略参三例）、withRecover（FIX-4 的 panic 路由）。
- **新增** `server/internal/httpapi/tenant_isolation_test.go`：用 httptest + 临时 SQLite/测试 PG（若引入测试库基建）或最小化 mock repo，覆盖"他租户门店 403、列表不含、超管边界"三态——把本审计 E 段的手工结论固化成回归。
- **目标**：httpapi 覆盖率 2.9% → 30%+；总覆盖率 20.2% → 35%+。
- **验收**：`go test -coverprofile` 数字达标；`go tool cover -func | tail -1`。

### 批次 6：前端与交付（1–2 天）

#### FIX-11 小程序契约对齐（P1-5）
- **文件**：`miniapp/src/package-staff/pages/requests/index.tsx:33-39`（`{items}`→`{requests}`，`created_at: string`→`number`）；`miniapp/src/pages/store/index.tsx:29-30`（`wifi_password`→`wifi_pwd`）；`pages/store/index.tsx:142`（语种选择器硬编码 5 个 → 接 `/api/translation/langs`，与 web 纪律一致）。
- **前置**：`npm i`（当前 node_modules 未安装，typecheck 都跑不了）。
- **建议加一道契约对拍**：脚本比对 web `client.ts` 的类型声明与小程序读取字段，防再漂移（本项目已有"声明数 vs 实际注册数"对拍的先例，见 docs/API.md 与 router.go）。
- **验收**：`npm run typecheck` 过；与后端响应字段一一对应。
- **状态（2026-10-20）**：✅ 已修完。店员页 `items`→`requests` + `created_at` 改 number；门店页 `wifi_password`→`wifi_pwd`（后端 `handlePublicStore` 实证发 `wifi_pwd`）；语种选择器接 `/api/translation/langs` 注册表（`Lang{code,native,name_zh,tier}`，未拉到前回落 `FALLBACK_LANGS` 静态占位，标签改母语写法）。逐字段对照过后端 handler 源码（`handleListGuestRequests` 发 `requests`、`handleListStores` 发 `stores` 无需改）。**注意**：本机沙箱网络挡住 npm 安装（broker.sock ECONNREFUSED），miniapp 的 `npm run typecheck` 未能执行——改动均为字段级对齐，已逐处对照后端源码验证。

#### FIX-12 部署产物（P2-9 + P2-2 二选一落地）
- **新增** `deploy/nginx.conf`：SPA history fallback（`/s/{slug}` 深链）、`/api` 反代、`gzip on`（**压缩走 nginx，应用层保持零依赖原则**——这是 FIX-13 的选项 B）、静态资源缓存头、`client_max_body_size`（图片上传 5MB+余量）。
- **README 补部署章节**：构建（`make build`）→ nginx → 环境变量清单（重点标注 LC_SECRET/LC_DEV/LC_PUBLIC_BASE_URL 三项生产必改）。
- **验收**：按文档从零部署一遍，`/s/{slug}` 深链可达、`/api` 通、`curl -H 'Accept-Encoding: gzip'` 有 `Content-Encoding: gzip`（indep_byte_uat.sh B6 由 ✗ 转 ✓）。

#### FIX-13 god component 拆分（P2-5，技术债，可排期）
- 优先级：`StorePage.tsx`（710 行，客人端主路径，拆 RateSheet/HelpSheet/LangSheet/CouponSheet 为独立文件）→ `AdminApp.tsx`（911 行，按 Tab 拆）→ 抽 `QrImage`/`relTime` 公共组件（消灭两处逐字重复 + 补 `revokeObjectURL`）。
- **验收**：单文件 <400 行；`tsc --noEmit` + render-check 不回归。

### 批次 7：文档漂移修复（0.5 天，与批次 1 并行）

| 编号 | 文件 | 改法 |
|---|---|---|
| D-1 | `README.md:102` | "共 137 条断言" → 160（69+29+39+23），或注明统计口径 |
| D-2 | `server/internal/httpapi/router.go:30-89` | 路由表补 17 个漏登记端点（my-tenant/orgs×3/assist×3/ai-credentials×7/guest assist×2/requests×3） |
| D-3 | `docs/API.md` | 补 3 个未登记端点（65→68），并加一句"以 router.go 注册为准" |
| D-4 | `server/migrations/0005_tenant_rbac.sql` 头注 | "四级角色" → 注明 0006 引入 org_admin 成五级 |
| D-8 | `README.md` | 部署章节（同 FIX-12） |

---

## 3. 不改的事（明确记录，防止后续被当缺陷再报）

| 项 | 理由 |
|---|---|
| 计费/订阅域 | 产品文档明确 MVP 不做自助支付（《可执行计划_前90天.md:137》），租户 perms/expires_at 是预留字段，范围决策非缺失 |
| 小程序完整功能（微信登录等） | 《商家冷启动_线上触达执行包.md:198》"小程序原生…均在下一阶段"；当前是迁移骨架，代码注释已自述 |
| 前端路由守卫只查 token | `AdminApp.tsx:50-52` 明示"刻意不做"，后端 fail-closed 实测兜底；仅当接入 Cookie 鉴权时才必须补 |
| CORS `*` | `middleware.go:34-36` 明示前提（Bearer token 无 CSRF）；改 Cookie 鉴权时同步收紧 |
| token 30 天无吊销 | `middleware.go:71-73` 明示补偿设计（每请求回库校验状态），实测有效 |
| TM 缓存跨租户共享 | 刻意设计（数据飞轮），`migrations/0002_i18n.sql:23` 唯一键即全局键；菜品名低敏可接受，已行为确认 |
| 进程内计数器 | 注释自述"护栏不是配额"；多实例部署时在网关层补限流即可（FIX-12 的 nginx 可顺带 `limit_req`） |

## 4. 待产品决策（3 项，需你拍板）

| # | 议题 | 选项 | 建议 |
|---|---|---|---|
| Q1 | dish 落库时机（P2-7）：未翻译店客人端空菜单 | A. confirm 即落库、翻译只更新 names（L3"缺译文"渲染纪律可覆盖未翻译店）B. 维持现状+商家引导强化 | **A**——否则"确认稿后忘翻译就印码"= 客人扫出空白页，是获客事故。**已拍板：A**（见 §6） |
| Q2 | 健康检查语义（F-5）：`:8099` 黑洞地址仍报 live | A. 轻量可达性探测（翻译链 HEAD，1s 超时，结果进 health）B. 字段改名 `translate_configured` 诚实报配置 | **B 先行**（便宜且诚实），A 作为运维增强排期。**已拍板：诚实化，A+B 一并落地**（见 §6） |
| Q3 | RLS（P1-1 下半场） | A. 休眠式策略 + repo 连接层设置会话变量 B. 只补 FK，靠应用层 + 机器门禁 | **B 先行，A 视多租户规模**——RLS 需要全连接层改造，FK + FIX-8 已覆盖当前风险面。**已拍板：B 先行**（见 §6） |

## 5. 执行顺序总览

```
批次1 止血（0.5d）  FIX-1 render-check + FIX-2 注释清理 + FIX-3 守卫接入
批次2 hardening（0.5d）FIX-4 recover + FIX-5 404信封 + FIX-6 配置强制
批次3 核心功能（1-2d） FIX-7 客人助手 LLM 接线 ★本期最高价值
批次4 隔离加固（1d）  FIX-8 凭据双条件 + FIX-9 FK
批次5 测试债（2-3d）  FIX-10 中间件/租户测试，覆盖率 20%→35%
批次6 前端交付（1-2d）FIX-11 小程序契约 + FIX-12 部署产物 + FIX-13 拆分
批次7 文档（0.5d）    D-1~D-8，与批次1 并行
决策   Q1/Q2/Q3
```

每条改完的验收都对应 `tools/indep_byte_uat.sh` 与四套 smoke 的既有断言——**本审计留下的独立脚本就是这次修改的回归网**：B4b（404 信封）、B6（压缩）、E 段（租户隔离）、F1（助手降级源）都会随修复自然由 ✗ 转 ✓，F1 的 `source=llm` 则是 FIX-7 的专属验收。

## 6. 实施记录（2026-10-19 当日已落地）

| 项 | 决策 | 落地内容 | 验收 |
|---|---|---|---|
| Q1 | A（confirm 即落库） | `handleConfirmDraft` 确认即 `ReplaceDishes`，与 `handleTranslate` 共用 `pickedDraftItems` + `dishesFromDraft` 装配；商户端 MenuTab 未翻译横幅（`.lc-alert--warn`）；客人端零译文店提示语诚实化（新增 `untranslatedNote` 10 语种，有译文才说机器翻译） | 活体链路：确认前 0 道 → 确认后 2 道；render-check 新增第 3.5 步零译文夹具 6/6；smoke 69/0、offline 29→32/0 |
| Q2 | 诚实化（A+B） | `service.TranslateReporter` 可选自陈接口（`TranslateEngine` 纯配置 / `TranslateReachable` 懒探测 + 30s TTL）；`handleHealth` 新增 `translate_engine` / `translate_reachable` / `dev_mode`，`translate_mode` 改按「主引擎是否真在用」判定 | 实测 `:8099`（黑洞链）→ `chain` / `false` / `dict-stub`（修订前谎报 live）；`:8080` → `mymemory` / `true` / `live`；indep UAT F3 由「1✓+⚠」转 3✓（58/1，仅剩 B6 压缩待 FIX-12）；新增 service 13 例 + httpapi 3 例单测 |
| Q3 | B（先补 FK，RLS 后置） | `migrations/0007_tenant_fk.sql`：store / translation_api_key 直接 FK（RESTRICT）、org FK（CASCADE，因为建租户会自动建 root org）、account / ai_credential 用 BEFORE INSERT/UPDATE 触发器放行平台级空串、admin_audit 刻意不加（审计要活过租户）；tenant 加 BEFORE DELETE 守卫补齐触发器表的 RESTRICT 语义。配套：`handleCreateStore` 改用 `effectiveTenant`（超管的 `''` 会写出无主门店）；`db.Migrate` 加会话级 advisory lock（实测两实例同时起会死锁，一个直接退出） | 独立 UAT 新增 G 段 10/10（孤儿写入被拒 4、平台级空串放行 2、删租户守卫 2、级联回收 1、幂等 1）；迁移幂等（二次执行无副作用）；smoke 69/0、offline 32/0、render-check 56/0 |
| FIX-1~6 | 全数落地 | 见 §3 | `go test ./...` 全绿、`gofmt -l` 空 |

> Q2 为什么没照「B 先行」走：A（探测）只多 60 行，且 B 改名后仍要回答「连得上吗」——两个字段一起给才是完整答案，于是合并落地。探测用**懒探测 + TTL**而非后台 goroutine：后台任务的生命周期要跟着进程走（测试里会泄漏、优雅停机要多等一轮），而健康检查本身就是低频运维动作，被调用时顺带探测、30s 内复用结果，成本相同却没有所有权问题。

---

## 7. 收尾记录（2026-10-20）

今日四步：完成度检查 → 测试补齐 + 数据清理 → 全量中文注释（门禁化）→ 文档去过时。

| 项 | 内容 |
|---|---|
| 完成度检查 | Q1/Q2/Q3 及 FIX-1~6 全部确认落地；**补抓到一个漏网**：FIX-11 小程序契约三处漂移仍在（见 §6 批次 6 状态行），已当日修完 |
| 测试补齐 | smoke 69→**77**（Q1 落库断言 4 + Q2 诚实断言 4）；smoke_offline 29→**32**（Q2 三条）；render-check 56→**61+1**（商家端横幅 6 条；「+1」为机翻质量条件断言，按设计可跳过，README 计数口径随之改为 193+1）；indep UAT 新增 **G 段 10 条**（0007 三档外键语义）→ 68/1（唯一失败 B6 压缩，排期 FIX-12 走 nginx） |
| 数据清理 | `reset_test_data.sh` 扩面：租户前缀补 `lex`（能言联调遗留的空壳租户）、翻译 Key 支持按名字认（超管冒烟的 Key 建在默认租户 `langcross` 名下，删不掉租户）、**新增孤儿清扫**（event/guest_request 指向已删门店的行——历史清理只匹配当时还存在的门店，实测残留 36+8）。清后库干净：业务表全 0，仅留种子租户 + TM 缓存 + 审计留档 |
| 全量中文注释 | 新增 `tools/check_doc_comments.py` 门禁（棘轮 + selftest，`make doc-comments` / `make test` 都会跑）：Go 导出函数/方法/类型、TS 导出函数/常量/接口，紧邻上一行必须是中文注释。存量 120 个缺口（Go 111 + TS 9）全清，基线钉 0；测试函数注释写「锁定的不变量」而非复述函数名。gofmt/vet/build/test + web tsc 全绿 |
| 文档去过时 | README：小程序从「明确不做」改为「骨架已起步（S4 迁移中）」、技术栈表补 Taro 行、目录结构补 miniapp/tools/0007、`make test` 描述补注释门禁、新增纪律 21（导出符号必须有中文文档注释）；API.md：测试覆盖表计数全部刷新、confirm 补「即落库」语义、`translate_engine` 补 `lexicorn` 取值、smoke_admin 补 BASE 覆盖说明 |
| 提交 | ✅ **已完成**（当日晚些时候环境自行恢复后执行）：`b5000bd` 代码（71 文件，零 `*.md`）+ `0e32097` 文档（4 个 `.md`），已 push 并用 `git fetch` + `git status -sb` 复核 `92ab787..0e32097 main -> main`。口径按「仅代码、不带文档与流程图」：排除 `*.md`；`tools/clean_env.sh`（含本机硬编码路径）与 `web/scripts/indep_guest_probe.mjs`（早轮未引用探针）均留盘不入库 |

**顺带发现（已在 §8 落地）**：FIX-8 的「repo 六方法加 `tenant_id=$2` 双条件」原方案有坑——平台超管的 `tenant_id` 是空串，朴素双条件会把超管挡在自己门外。正确形态是 `WHERE id=$1 AND (tenant_id=$2 OR $2='')`（平台级账号可见全部），且要连带动 `repo/ai_credential.go:18-19` 那句「归属校验一律写进 WHERE」的注释才名副其实。

---

## 8. 批次 3/4 落地记录（2026-10-20 晚）

### FIX-8：AI 凭据 repo 归属口径

| 面 | 落地内容 |
|---|---|
| repo | 6 个 by-ID 方法（`GetAICredential` / `AICredCiphertext` / `SetAICredentialStatus` / `UpdateAICredential` / `RotateAICredential` / `DeleteAICredential`）全部加 `tenantID` 参数，WHERE 改三段式 `id = $1 AND (tenant_id = $2 OR tenant_id = '' OR $2 = '')` |
| service | `AICredStore` 接口签名收紧为 `AICredCiphertext(ctx, id, tenantID)`；`AICredVault.Credential` 解密时传请求方租户——租户解析到平台默认凭据时密文照样读得出来 |
| httpapi | `handlers_assist_admin.go` 五个 handler（rotate / status / reveal / delete / test）调用点传 `currentAccount(r).TenantID`（平台超管为空串 → 可见全部） |
| 文件头注释 | `repo/ai_credential.go` 头注释补上这个口径，并写明**为什么不是**朴素的 `tenant_id = $2` |

**为什么是三段式而不是朴素双条件**：平台级凭据的 `tenant_id` 是空串，而超管自己的 `tenant_id` 也是空串。朴素 `WHERE id=$1 AND tenant_id=$2` 在「租户解析到平台默认凭据、再用超管身份去读密文」这条路径上会查不到行——把超管挡在自己维护的凭据门外。三段式放行两档：本租户的行 + 平台级的行。

**测试**（21 例新增）：`repo/ai_credential_test.go` 15 例（记录型假 SQL 驱动，断言 **SQL 文本里带着作用域子句**而不是断言 Go 里的 if——口径写在 WHERE 里才是真的锁住；6 个方法逐个验 + 平台调用方传空串 + 全列 Scan + 写操作零行归一为 NotFound）；`service/ai_credential_test.go` 6 例（含 `TestAICredVault_TenantReadsPlatformCredential`，锁「租户经 vault 读平台默认凭据」这条空串放行的业务路径）。

### FIX-7：客人助手按门店租户解析 LLM 凭据

| 面 | 落地内容 |
|---|---|
| 解析核心 | `handlers_assist.go` 拆出 `llmConfigForTenant(ctx, tenantID)`，商家端 `llmConfig(acc)` 改为调它——两端共用一份解析逻辑，不会哪天单边漂移 |
| 客人端接线 | `guestLLMConfig(ctx, storeID, tenantID)` = `llmConfigForTenant` + 按店日限；handler 调用点 `LLM: s.guestLLMConfig(r.Context(), st.ID, st.TenantID)`（原为 `s.llmConfig(r.Context(), nil)`） |
| 成本护栏 | `guestLLMDailyLimit = 1000`（var，0 = 不限）、`guestLLMUsage` 按 storeID 记账、按 UTC 日期归零、`reserveGuestLLM` 原子占位。超限 → 收回 LLM 配置，客人仍得规则兜底回答 |
| 计数口径 | 「尝试了一次模型模式」：模型调用失败也计——请求已发出、钱已花，按结果计数会让失败重试变成免费通道 |

**为什么按店限而不是只靠按 IP 限流**：这个端点不登录，分布式脚本（一堆 IP 问同一家店）能绕过 30 次/分钟的 IP 护栏，而账单记在这家店用的凭据上。

**测试**（5 例新增，`handlers_guest_assist_test.go`）：

| 用例 | 锁的不变量 |
|---|---|
| `TestGuestAssist_LLMWiredWhenCredentialExists` | A：配了凭据 → `source=llm`、带回 model、mock LLM 被打 1 次、译文经守卫链原样带回 |
| `TestGuestAssist_NoCredentialKeepsFallback` | B：没配凭据 → 现有行为不变（规则兜底、不打模型、回答非空） |
| `TestGuestAssist_DailyLimitWithdrawsLLM` | C：限额内 2 次走模型，第 3 次超限收回 LLM 但**客人照常得到回答** |
| `TestReserveGuestLLM_CountsAndResets` | 限额内放行 / 达限拒绝 / 跨 UTC 日归零 |
| `TestReserveGuestLLM_ZeroMeansUnlimited` | 0 = 不限 |

**自证测试非空转**：把接线临时回退成 `LLM: assist.LLMConfig{}` 复跑，A 与 C 立刻变红（`实际 fallback`），B 恒绿（本就锁「现有行为不变」）——证明 A/C 真的在守 FIX-7，而不是碰巧通过。

**为什么测试里每个用例用各自的 storeID**：日限计数按 storeID 走，用例共用会让上一个消耗的名额算到下一个头上，绿红全凭执行顺序。

### 回归结果（全绿）

| 套件 | 结果 |
|---|---|
| `go test ./...` | 全绿（repo / service / httpapi / assist / domain / crypt / config） |
| `gofmt -l` / `go vet` | 空 / 干净 |
| `smoke.sh` | 77 / 0 |
| `smoke_admin.sh`（`BASE=http://localhost:8080` 覆盖默认 8081） | 23 / 0 |
| `smoke_offline.sh`（`:8099` 降级后端） | 32 / 0 |
| `guest-render-check.mjs` | 62 / 0 |
| `check_doc_comments.py` | go 0≤0 / fe 0≤0（新增 13 条假驱动方法注释） |

### 剩余（下批次）—— 已全部闭环，见 §10

当时记的四项：FIX-10 覆盖率补层（httpapi 2.9% → 30%）、FIX-12 部署产物（nginx，`gzip on` 让 indep UAT 唯一失败项 B6 转绿）、
FIX-13 god component 拆分、miniapp `npm run typecheck`（当时始终未跑成，沙箱挡了 `npm install` 的 `.bin` 软链阶段）。

→ **四项均已于 2026-10-22 闭环**：FIX-10 见 §9「第二轮补完」，FIX-12 见 §9，
FIX-13 四组件全拆完见 §10.1，miniapp typecheck 归零见 §10.3。

---

## 9. 收尾第二轮（2026-10-20 深夜）：FIX-10 / FIX-12 / FIX-13 部分

### 先修两处「测试自己说谎」（本轮最重要的发现）

**① indep UAT 的 E 段依赖别的脚本的副作用。** 清空业务数据后直接跑，E3/E3c/E4b/E8b 四条一起变红。
查下来**不是多租户回归**：脚本假定「超管已存在」（由 `smoke_admin.sh` psql 提权），而数据清空后没有超管——
登录接口首次登录会**自动建号**（默认 `manager`、默认租户），于是 `admin/*` 一律 403、建出的账号门店全落默认租户。
**环境缺口伪装成了功能回归**，这类假红比漏测更消耗信任（第一眼真的会去查 `effectiveTenant`）。
改法：E 段自行 psql 提权 + 重新取码（验证码一次性）+ 断言 `/api/me` 回报 `super_admin`，跑前零前置；
收尾连同探针账号一起回收并断言残留 0。修完 E 段全绿，UAT 回到 69/1。

**② B6 的断言层次不对。** 它探的是 API 直连 `:8080`，而压缩按设计在网关——照这个写法，
就算 nginx 配好也永远红。改成两条：B6a **反向断言应用层确实不压缩**（防止有人顺手在 Go 里加 gzip 中间件，
让压缩逻辑分裂到两处、网关再压一遍）；B6b 断言 `deploy/nginx.conf` 真开了 gzip 且覆盖 `application/json`、
有 SPA fallback、有 `/api` 反代、有上传体积上限。顺带踩到：nginx 指令可跨行，单行 `grep` 抓 `gzip_types` 必然漏——
**断言写法不对导致的假红，比缺功能更难查**。

### FIX-12：部署产物（P2-9 + P2-2 结案）

| 产物 | 内容 |
|---|---|
| `deploy/nginx.conf` | upstream + `gzip on`（含 `application/json`）、两级 `limit_req`（客人端公开接口最严）、`/api` 反代、`client_max_body_size 8m`（应用层 5MB 留余量）、`/assets/` 一年强缓存、**SPA history fallback**（桌码深链 `/s/{slug}` 靠它）、安全头、TLS 段（注释待解注） |
| `deploy/langcross-api.service` | systemd：非 `LC_DEV` 启动、`LC_SECRET` 走 EnvironmentFile、`Restart=on-failure`、`KillSignal=SIGTERM` + `TimeoutStopSec=20`（配合后端 10s 优雅停机）、`ProtectSystem=strict` + 白名单可写路径 |

**为什么压缩放网关而不是应用层**：应用层保持零依赖——换 Caddy / CDN 不用改一行 Go；也不会出现
「新增接口忘了过压缩中间件」这种没人会发现的债。代价是 API 直连不压缩，这个代价被 B6a 显式锁住。

### FIX-10：测试补层（推进，未达目标）

| 新增 | 用例 | 锁的不变量 |
|---|---|---|
| `httpapi/tenant_isolation_test.go` | 8 | 他租户门店 403；owner 优先于租户判定；**超管跨租户门店详情 403 是设计而非 bug**（可见性出口在后台列表）；列表作用域必须写进 SQL `WHERE`（断言 SQL 文本，不让「取全表回来再筛」蒙混过关）；门店不存在是 404 不是 403 |
| `httpapi/handlers_admin_test.go` | 7 | 后台门禁角色矩阵（含超管可指定租户 / 不传落默认租户）；`keysReady` 缺密钥时明确 500；租户编码字符集；**唯一冲突转 409、非冲突错误仍是 500**（防止把连接故障谎报成「换个编码再来」） |
| `repo/store_test.go` | 2 | `CanAccessStore` 五级角色矩阵（owner 优先、超管仅默认租户、集团管理员限本租户）；`org_admin` 子树查询失败或没分配组织时**按无权处理** |

**覆盖率**：总 30.9% → **32.7%**；httpapi 18.4% → **21.7%**；repo 3.6% → 5.2%。

#### 第二轮补完：FIX-10 达标（覆盖率超过原目标）

| 新增 | 用例 | 锁的不变量 |
|---|---|---|
| `httpapi/handlers_merchant_test.go` | 12 | **未确认的确认稿不许翻译**（纪律 2 的硬规则，客人端「译得很好但其实是错菜」的唯一防线）；建店归属必须走 `effectiveTenant`（纪律 19，超管的空串会写出无主门店）；店员不能建店；重新确认要继承售罄 / 每日剩余 / 既有译文；勾选口径确认与翻译共用；档位与点单模式枚举白名单（含「合法值不能被拧太紧」的反向断言）；门店枚举合法值放行 |
| `httpapi/handlers_admin_test.go` 增补 | 5 | **整条后台路由面逐条过角色门禁**（20 条 × 2 个低角色 + 超管，等于给「新增 admin handler 忘挂 requireRole」这类装配错误加了一道门禁）；`superKeyAdmin` 四态矩阵；防提权（集团管理员建 `tenant_admin` / `super_admin` 一律 403）；`friendlyLexicornError` 的映射与 nil 安全 |

**覆盖率**：httpapi 26.1% → **36.2%**（目标 30%）；总 34.6% → **39.1%**（目标 35%）——**FIX-10 两个目标均达成**。
达成方式是补**不变量**而不是刷行数：一条「未确认不许翻译」比十行边界分支值钱。

★ **测试又抓到一个真 bug**：`friendlyLexicornError(nil)` 会 panic（`err.Error()` 空指针）。
调用方是「试译失败才有 err」的路径，但凡并发收集结果时多绕一层就会命中，把一次
「Key 不可用」的提示变成 500 + 一段 goroutine dump。已加 nil 守卫并留回归用例。
**辅助函数不该有能炸掉的入参**——这条值得记住。

### FIX-13：已完成的一半（QrImage）

`QrImage` 原先在 `AdminApp.tsx` 与 `Onboarding.tsx` 里各有一份**逐字重复**的副本，且两份都漏了
`URL.revokeObjectURL`——`createObjectURL` 造的地址由浏览器一直持有（不参与 GC），门店页反复切换门店
就是随使用时长线性增长的内存泄漏，低配收银平板上很明显。已抽出 `web/src/admin/QrImage.tsx`
（cleanup 里回收，且 URL 变化时旧地址也回收），两处改为引用。

**四组件拆分（AdminApp 911 / PlatformTab 860 / StorePage 710 / Onboarding 676 行）当时未做**：
纯技术债，唯一的安全网是 render-check（当时 62 条），收益是可维护性、风险是回归，按原计划「可排期」保留。
→ **已于 2026-10-22 全部拆完，见 §10。**

#### 第三轮：PlatformTab 拆完，并给拆分补上回归网（2026-10-21）

**为什么先拆它**：860 行里塞了 5 个分区 + 1 个外壳，改任何一处都要在千行里上下翻；而它是纯展示层，
没有跨分区的共享状态（只有一条 `msg` 提示），拆开的风险是四个里最低的。

| 新文件 | 行数 | 内容 |
|---|---|---|
| `PlatformTab.tsx` | 102 | **外壳**：按角色过滤分区清单 + 二级 Tab 切换 + 一条提示消息 |
| `platformHelpers.tsx` | 43 | 共用小工具（角色标签 / 相对时间 / 状态徽标）——五个分区里四个要用 |
| `PlatformOverview.tsx` | 30 | 分区：平台概览（超管，四个总数卡片） |
| `PlatformTenants.tsx` | 148 | 分区：租户增删改 + 展开看门店 |
| `PlatformAccounts.tsx` | 205 | 分区：账号与角色（超管 / 租户管理员共用） |
| `PlatformTranslateKeys.tsx` | 298 | 分区：翻译 Key（超管专属，试译 / 轮换 / 明文） |
| `PlatformAudit.tsx` | 68 | 分区：审计留痕 |

**零语义漂移自证**：把新文件去掉文档注释、`export` 与 import 后与原文件对应段做归一化比对，
七段全部逐字节一致（仅加 `export`、把文档注释挪到文件头）。拆分是纯搬运，不是重写。

**顺带补上原本缺失的回归网**（这是这轮更重要的产出）：render-check 此前**完全不覆盖平台管理**，
也就是说 FIX-13 的安全网对我正在拆的这个组件是空的。新增第 9 步：psql 提权造超管 →
进商家控制台 → 断言分区清单五个全见 → **逐一点开五个分区并断言各自表头/卡片真的渲染出来**。
理由：`tsc` 只证明「类型对」，`vite build` 只证明「模块解析对」，都不证明「点进去渲染得出来」。

写这一步时踩到一个值得记的坑：**按文案找按钮会点错**——商家端左侧导航也有「概览」，
`querySelectorAll('button').find(...)` 命中的是门店级 Tab，表现为「概览分区没渲染」这种假红。
圈定范围后才能点对：找「同时含五个分区名的那个 `.lc-row`」，那是平台管理的二级 Tab 行。

**回归**：render-check 62 → **75 / 0**、smoke 77/0、admin 23/0、offline 32/0、indep UAT 75/0、
`tsc --noEmit` 干净、生产构建通过、七个模块在 dev server 上全部转换正常。

### 回归结果（全绿）

> 下表是**第二轮结束时**（2026-10-20 深夜 / 2026-10-21）的快照，后面的数字已被 §10 刷新；
> 保留它是为了让每一轮的起点可追溯，读当前状态请直接看 §10。

| 套件 | 结果 |
|---|---|
| `go test ./...` | 全绿；`gofmt -l` 空、`go vet` 干净、`tsc --noEmit` 干净 |
| `smoke.sh` / `smoke_admin.sh` / `smoke_offline.sh` | 77 / 0、23 / 0、32 / 0 |
| `guest-render-check.mjs` | 75 / 0（本轮 62 → 75） |
| `tools/indep_byte_uat.sh` | **75 / 0（首次全绿）** |
| `check_doc_comments.py` | 0 / 0 |

### 文档同步

README：新增「部署」章节（拓扑图 + 三步命令 + 「为什么压缩/TLS/限流在网关」表，补 D-8）、
目录结构补 `deploy/`、独立 UAT 计数 68→75、新增纪律 22–24（压缩归网关 / 归属口径写进 SQL / 
公开端点成本按花钱主体计）、「写测试脚本踩过的坑」补两条（脚本别依赖别的脚本的副作用；跨行指令先折叠换行）。
API.md：测试覆盖表刷新并新增单测一行（含覆盖率与目标缺口）、客人助手补「凭据按门店租户解析 + 两级限流 + 超日限回落规则模式」。

---

## 10. 收尾第三轮（2026-10-22）：FIX-13 全拆完 / FIX-14 / miniapp 类型检查归零

### 10.1 FIX-13 收尾：四个 god component 全部拆完

| 组件 | 拆分前 | 现在 | 拆出 |
|---|---|---|---|
| `admin/PlatformTab.tsx` | 860 | 102 | 5 个分区 + `platformHelpers`（§9 第三轮已拆） |
| `admin/Onboarding.tsx` | 661 | 110 | `OnboardingStep1~5` + `onboardingShared`（6 个新文件） |
| `guest/StorePage.tsx` | 719 | 280 | `StoreHero` / `StoreSignature` / `StoreMenu` / `StoreLangSheet` / `StoreRateSheet` / `StoreCouponSheet` / `StoreHelpSheet` + `storeShared`（8 个新文件） |
| `admin/AdminApp.tsx` | 910 | 64 | `AdminLogin` / `AdminConsole` / `AdminOverview` / `AdminMenuTab` / `AdminCouponTab` / `AdminInsightsTab` / `AdminSettingsTab` + `adminShared`（8 个新文件） |

拆完的判据是「**根组件只留状态机与装配**」：`AdminApp` 现在就是 `authed ? <Console/> : <Login/>`。
共用面统一落在 `xxxShared` 里，不搞「从一个大文件里剪出来」的悬空引用。

**零语义漂移自证**：同 §9 的做法——去掉文档注释 / `export` / import 后与原文件对应段归一化逐字节比对。
每次拆完都过一遍，因为「纯搬运」和「顺手改了一点」在 diff 上长得几乎一样，只有归一化比对能分开。

**拆分顺序是刻意排的**：PlatformTab（纯展示、无跨分区共享状态）→ Onboarding（有分步状态机，但步间只传 formData）
→ StorePage（客人端，安全网是 render-check 的 L1–L6）→ AdminApp（商家端根组件，牵动所有 Tab，风险最高）。
风险高的放最后，是因为拆到那时安全网已经补厚了。

#### 拆分过程踩到的坑（都已写进 README 纪律 26 / 27）

- **先确认安全网不是空的**：拆之前查出来 render-check **完全不覆盖平台管理**——
  也就是说 FIX-13 唯一的安全网对正要拆的组件是空的。§9 补的第 9 步（五个分区逐一点开并断言真的渲染出来）
  才是这次敢动手的前提。`tsc` 只证明类型对，`vite build` 只证明模块解析对，**都不证明点进去渲染得出来**。
- **`return ( {jsx} )` 会被当成对象字面量**：`StoreHero` / `StoreSignature` 拆出来后直接崩。
  要么包 Fragment `<>...</>`，要么去掉那层多余括号。
- **未使用的 import 在本仓是编译错误**（`noUnusedLocals` / `noUnusedParameters`）：
  不能 blanket import 一堆再用一部分，必须**逐文件按实际用到的符号推导 import 清单**。
  拆 `storeShared` 时就因为多留了 `langLabel` / `t` 红过一次。
- **注释不能落在 `return` 的 JSX 里**：`StoreSignature` 的 `/* 招牌… */` 放进 return 后成了第二个根节点。
- **别为了好看顺手改名**：`StorePage` 里保留 `const title = (d: Dish) => dishTitle(d, lang)` 这个转发，
  否则要动上百处调用点——那次改动量就会大到「拆」和「改」混在一起，没法再用归一化比对自证。

### 10.2 FIX-14：多语种翻译并发编排（真实性能缺陷，不是技术债）

**症状**：P4「下一步」按钮要等预览接口返回才解禁，商家点下去后页面看着像卡死。

**根因**：翻译是「**每语种、每批一次外部 HTTP 调用**」，而三个调用点
（`handlePreviewTranslate` / `translateCategories` / `handleTranslate`）都**串行遍历语种**。
11 道菜 × 3 语种 + 5 个分类 = 48 次串行往返，冷缓存下 20~30 秒。

**改法**：抽 `translate_parallel.go`——语种间**限量并发** + 整批挂**一个总截止时间**。

| 常量 | 值 | 为什么是这个值 |
|---|---|---|
| `translateTargetConcurrency` | **3** | 公开翻译引擎按 **IP** 限流。并发拉满会吃 429，然后**一个词都翻不出来**——比慢更糟。上限按对方限流定，不按我们 CPU 定 |
| `translateOverallDeadline` | **45s** | 必须**小于**网关 `proxy_read_timeout 60s`。宁可我们自己先超时给出可读错误，也不要被网关砍成 504（客人看到的是「网络错误」，运维看到的是网关日志，两边都查不出是翻译慢） |

**并发收集结果时每 goroutine 收自己那份、Wait 之后合并**。
Go 的 map 并发写是 **fatal error** 不是概率性 race，`-race` 也未必每次抓到——
写成「共享 map + 加锁往里塞」能过测试，`-race` 下也常常是绿的，但生产上会整个进程崩掉。
`handleTranslate`（商家正式翻译）不能复用 `translateAllTargets`：它每个语种的文本集不同（已有译文的跳过），
所以那里是 `forEachTargetParallel` + 各自收集 `perIdx` / `perLang` 再合并。

**实测**：冷缓存 20~30s → **9.7s**；热缓存 **1.5ms**。与页面「约十几秒」的提示对齐了。

**9 条单测锁的不变量**（`translate_parallel_test.go`）：

| 用例 | 锁什么 |
|---|---|
| `RunsInParallel` | 用「凑够 N 个并发才放行」的闸门 + 看门狗：串行实现会一直卡住 |
| `ConcurrencyIsBounded` | 并发真的被限在 3，不是「起了 N 个 goroutine 但看起来像是并发」 |
| `ActuallyFasterThanSerial` | 墙钟时间：15 语种 × 30ms 串行 ≈ 450ms，并发应显著更快 |
| `NoCrossWiring` | en 的位置不许出现 ja 的译文（桩件译文固定 `<语种>:<原文>`，一眼可辨） |
| `EmptyTexts` | 空输入不炸 |
| `WithTranslateDeadline_ShortEnoughForGateway` | **45s < 60s** 这条关系被写死成断言——有人把网关调小或把 deadline 调大就会红 |
| `PreviewTranslate_PassesDeadlineToEngine` | 截止时间真的传到了引擎那一层，不只是加在编排层 |
| `PreviewTranslate_LandsOnRightItems` | 译文落到**正确的条目**上（并发最容易错的就是下标错位） |
| `PreviewTranslate_NoItemsIsBadRequest` | 空 items 是 400，不是 500 |

★ 桩件里的 `hold`（每次调用停留 30ms）不是装饰：`fn` 本身太轻（只是拼几个字符串），
goroutine 大概率「起一个、跑完、再起下一个」，并发计量恒为 1——
那样这条测试会变成**加 `-race` 变绿、不加变红**的飘测试。停留让重叠成为必然，计量才是在测并发。

### 10.3 miniapp `npm run typecheck` 归零（此前一直没跑成）

此前卡在 `npm install`：沙箱挡掉了 `.bin` 链接阶段的 `rename`，`--no-bin-links` 也过不去，
于是 `npx tsc` 起不来，类型检查一直是个「已知跑不了」的洞。

**绕过方式**：把整棵依赖树装到 `/tmp/lc-miniapp-deps`（927 个包 / 395M），再从那里补回本地。
`.bin` 软链缺失时不用 `npx`，直接调 `node node_modules/typescript/lib/tsc.js --noEmit`。
结果：**0 错误**。

★ 这里真正的坑不是软链，而是**被中断的 `npm install` 会留下「半安装的包」**：
目录在、文件不全，`package.json` 甚至整个没有。此时 `tsc` 报
`TS2307: Cannot find module '@tarojs/components' or its corresponding type declarations`——
**看着像代码缺引用，实际是依赖树被砍了一半**。本次有 7 个包中招：

| 包 | 完整 | 本地 |
|---|---|---|
| `@tarojs/components` | 1438 | **93** |
| `@tarojs/runtime` | 171 | 43 |
| `swiper` | 319 | 99 |
| `hls.js` | 165 | 22 |
| `lodash-es` | 429 | 405 |
| `miniprogram-simulate` | 407 | 381 |
| `less` | 128 | **1** |

判别方法不是「重装一遍试试」（重装大概率被同一个原因再打断一次），
而是**跟一份完整树按包比文件数**，只补真正残缺的那几个。
大多数包差 1~2 个文件是 npm 元数据，别被这份噪声带偏——阈值要设（本次取「差 20 个以上」）。

### 回归结果（2026-10-22 复测，全绿）

| 套件 | 结果 |
|---|---|
| `go test ./...` | 全绿；`gofmt -l` 空、`go vet` 干净 |
| `web` `tsc --noEmit` | 0 错误 |
| `miniapp` `tsc --noEmit` | **0 错误（首次跑通）** |
| `server/scripts/smoke.sh` | **77 / 0** |
| `server/scripts/smoke_offline.sh` | **32 / 0** |
| `server/scripts/smoke_admin.sh` | **24 / 0**（本轮 23 → 24） |
| `web/scripts/guest-render-check.mjs` | **92 / 0**（本轮 75 → 92） |
| `tools/indep_byte_uat.sh` | **75 / 0** |
| `tools/check_doc_comments.py` | 0 / 0 |
| 覆盖率 | 总 **40.3%**、httpapi **39.0%**（原目标 35% / 30%） |

四套 + 独立 UAT 合计 **300 条断言**（77 + 32 + 92 + 24 + 75）。

★ 本轮唯一的一次红，是**注释门禁**抓到 5 个新导出符号欠中文注释
（Go 3 个在 `translate_parallel_test.go` 的桩件方法上，TS 2 个是 `TranslateKeys` 与 `ROLE_LABEL`）。
值得记的是**它连测试文件一起扫**——新建测试桩时它的方法名也是导出符号。
处理方式是**补注释**，不是 `--update-baseline` 抬基线：抬基线等于把欠账记为标准，
正是这一路在修的「测试自己说谎」。

### 文档同步（本轮）

- **README**：断言计数 193（本身就与各表分项之和不符）→ **225**（77+32+92+24），并补上独立 UAT 75 条合计 300；
  覆盖率 39%/36% → **40.3%/39.0%**；render-check 74+1 → **92**；超管脚本 **23 → 24**、
  路径 `scripts/` → `server/scripts/`、默认端口 `:8081` → **`:8080`**；
  目录结构按拆分后的实际文件重写（`guest/` 与 `admin/` 各成一组）；
  新增「多语种并发编排」一条与「小程序端类型检查」一节；
  纪律新增 25–27（批量外部调用要并发+有总截止时间 / 断言要等条件不要立刻判定 / 拆分前先看安全网是不是空的）；
  「踩过的坑」补两条（跑之前先确认端口上是自己人；测试文件里的导出符号也在注释门禁范围内）。
- **API.md**：`/api/stores/{id}/translate` 与 `/api/public/translate-preview` 补注「并发 + 45s 截止时间」；
  超管脚本端口与路径刷新；测试覆盖表计数刷新；补一行提醒注释门禁扫 `*_test.go`。
- **脚本提示文案**：`smoke.sh` / `smoke_offline.sh` / `smoke_admin.sh` / `reset_test_data.sh` /
  `seed_demo.sh` 里 `bash scripts/xxx.sh` 一律改成 `bash server/scripts/xxx.sh`（与 Makefile 对齐），
  并给离线兜底补上 `BASE=http://localhost:8099` 前缀。

---

## 附录：验证证据索引

| 结论 | 证据位置 |
|---|---|
| 客人助手断链 | `handlers_guest_assist.go:178`、`handlers_assist.go:173-176`；行为实测 `source=fallback`（**已修**：现为 `s.guestLLMConfig(ctx, st.ID, st.TenantID)`，实测 `source=llm`，见 §8） |
| 无 recover | python 全仓扫描（非测试）= 0 |
| AI 凭据无双条件 | `repo/ai_credential.go:71-162`（6 处 `WHERE id=$1`）；对照 `repo/tenant.go:209-211`（**已修**：三段式 `id=$1 AND (tenant_id=$2 OR tenant_id='' OR $2='')`，见 §8） |
| 死代码 | python 扫描 `guardNoRawChineseName` 0 调用点 |
| 无 RLS/FK | 6 个迁移 RLS=0、tenant_id REFERENCES=0 |
| 小程序漂移 | `domain/guest_request.go:66`（number）+ `handlers_guest_assist.go:292`（requests）vs `package-staff/.../index.tsx:18-24,33-39`（string/items） |
| 404 纯文本 | `router.go` 无兜底；curl 实测 |
| 无压缩 | curl 实测无 `Content-Encoding` |
| 路由无守卫=设计 | `AdminApp.tsx:50-52` 注释 |
| 计费=范围决策 | 《可执行计划_前90天.md:137》、《商家店端MVP_开发任务拆解.md:4》 |
| 小程序=下一阶段 | 《商家冷启动_线上触达执行包.md:198》、`package-admin/pages/home/index.tsx:20` |
| LLM 成本是已知缺口 | 《商业测算与缺口清单.md:20》 |

*本文与审计报告合起来是一次完整闭环：报告说"缺什么"，本文说"哪些真缺、怎么补、哪些本来就不缺"。*
