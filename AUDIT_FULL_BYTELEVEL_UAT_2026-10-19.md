# LangCross 商家店端 · 全栈字节级 UAT 审计报告

- **审计日期**：2026-10-19
- **审计对象**：`langcross-merchant-app/`（Go 后端 63 文件 / 12,660 行 + React web 12 文件 / 5,874 行 + Taro 小程序 10 文件），HEAD = `92ab787`
- **审计方式**：端到端全量读码（3 个并行 Explore agent + 亲自读主入口/中间件/核心域）+ 跑项目自带 4 套回归 + **另写独立字节级 UAT 脚本从零复验**（不复用项目任何断言）+ 真浏览器 CDP 探针
- **环境**：本机默认 grep 为 toybox（已用干净 PATH 包装器规避）；HTTP 代理会拦截 localhost（全部 curl 走 `--noproxy '*'`，Chrome 加 `--no-proxy-server`）；PostgreSQL 14（homebrew）、Redis、Go 1.26.5、Node 22.22.2 实测可用

---

## 1. 执行摘要

**一句话结论：主链路（注册→建店→菜单多语化→客人扫码→评价领券→核销→服务请求→平台管理）在字节级验证下全部贯通，工程质量显著高于常规原型；但离"可投产"还差 4 件 P0——客人 AI 助手 LLM 装配断链、全仓无 panic recovery、项目自带 P0 浏览器回归在 main 上呈红色、默认开发态配置（LC_DEV=1 + 默认密钥）。**

| 维度 | 评分 | 依据（实测/实证） |
|---|---|---|
| 主链路贯通性 | **9/10** | smoke 69/0 + smoke_admin 23/0 + smoke_offline 29/0 + 独立 UAT 55 项中 53 项通过（2 项失败为真实契约/性能发现） |
| 代码质量与纪律 | **8.5/10** | 生产代码 0 处 TODO/FIXME（python 独立扫描）、统一信封 288 处调用、注释固化踩坑根因；扣分：4 个 god component（911/860/710/676 行）、1 处死代码 |
| 测试体系 | **5.5/10** | API 冒烟 121 条断言全绿、翻译链 40 个单测；但总覆盖率 20.2%、httpapi 包 2.9%、repo 0%、**render-check 在 main 上红** |
| 架构设计 | **7.5/10** | 纯标准库 + 2 个第三方依赖、分层干净、fail-closed 鉴权；扣分：无 RLS、无 panic recovery、无机器门禁 |
| 安全 | **7/10** | 每请求回库校验账号、AES-256-GCM+HKDF、审计留痕、防提权规则；扣分：默认 dev secret、CORS `*`、AI 凭据租户 WHERE 不对称、隔离无 DB 兜底 |
| 多语言核心能力 | **8.5/10** | 10 语种注册表驱动（前端零硬编码，python 扫描 0 处）、翻译四层装配、"绝不编造"有守卫代码；扣分：客人助手 LLM 未接线 |
| 可投产度 | **6/10** | 主链路通、RBAC/审计就绪；缺计费域、缺压测、缺部署配置、E2E 红 |
| 商业价值 | **7/10** | 商业内核清晰（见 §6）；缺变现闭环与规模化兜底 |

---

## 2. 字节级 UAT 实测（独立脚本 `tools/indep_byte_uat.sh`，55 项）

### 2.1 BE↔BE：特殊字符双向贯通（9/9 通过）

载荷含**引号、`<>&`、反斜杠、中文、重音 `é`、`€`、emoji `😀`**，四向验证：

| 断言 | 结果 |
|---|---|
| API 写入店名（特殊字符）→ API 读回，逐字节相等 | ✓ |
| API 写入 → **psql 直读原文**，逐字节相等 | ✓ |
| **psql 直写** → API 读回，逐字节相等 | ✓ |
| 特殊字符菜品 draft→confirm→translate→落库，psql/API 双向一致 | ✓ |
| 纯端点字节级幂等：`/api/translation/langs` 与客人端门店页同请求两次，响应体 SHA-256 相同 | ✓ |

### 2.2 FE↔BE：经 Vite 代理的真实部署路径（6/8）

| 断言 | 结果 |
|---|---|
| Vite `/api/health`、`/api/stores`（带 token）代理通 | ✓ |
| `/s/{slug}`、`/admin/onboarding` SPA 深路由回落 200 | ✓ |
| `/api` 未知路径经代理返回 404（**不回落 SPA**） | ✓ |
| **未知路由 404 为纯文本 `404 page not found`，非统一 JSON 信封** | ✗ **真实发现 F-1** |
| 后端直连 `/s/` 返回 404 JSON（后端不服务 SPA，边界正确） | ✓ |
| **API 无 gzip/br 压缩**（`Accept-Encoding: gzip, br` 无 `Content-Encoding`） | ✗ **真实发现 F-2** |

### 2.3 FE↔FE：诚实说明

**"前端↔前端字节级"在本项目不存在**——web 与 miniapp 是两套独立代码库，无共享运行时。实际验证分两层：
- **真浏览器 E2E（已做）**：自研 CDP 探针（`web/scripts/indep_guest_probe.mjs`）15/15 通过——三个底部按钮各自行为正确（AI 助手面板可提问并收到回复、问店员 6 条短语卡、评价 15 星提交后弹券）、全程 0 JS 异常。
- **jsdom 单测（不存在）**：web 无 vitest/jest，`package.json` 无 test 脚本；miniapp `node_modules` 未安装，跑不了 typecheck。
- 宽区间断言（如仅查状态码 200）只能防白屏，不能证明渲染正确——本项目 render-check 的 L1–L5 语言纪律断言（主标题等于接口 `names.en`、无 CJK 混入、缺译文挂标）是真浏览器里最有价值的一层，**但脚本已过期（见 §3.2）**。

### 2.4 分支/异常与错误信封（13/13 通过）

无 token/坏 token/构造过期 token → 401 + `code=UNAUTH`；manager 访问超管接口 → 403 + `code=FORBIDDEN`；不存在门店 → 404；非法 JSON → 400；GET 打 POST-only 端点 → 405；OPTIONS 预检 → 204 + CORS 头；客人端公开接口无 token 200；未登录传图 → 401；客人助手公开可问 → 200。**统一信封率：已注册路由 100%，未知路由 0%（F-1）。**

### 2.5 Token 载荷核对（2/2 通过）

解码自签 token：载荷字段恰为 `{sub, exp}`，**无 role/tenant 烘焙**——smoke_admin.sh 注释"角色烘焙进 token，必须重登才生效"与事实不符（实际每请求回库取账号，角色/停用即时生效，middleware.go:71-73,87-91 注释与行为一致）。token 30 天 TTL、HMAC-SHA256 定长比较验签。

### 2.6 多租户隔离（15/15 通过）

一次性租户（SQL 直插 32-hex ID）→ 超管 API 建商家账号 → T2 建店 → **本租户可见 200 → 默认租户商家访问 403、列表不含该店 → 超管无租户声明 403（落默认租户）→ manager 建 tenant_admin 被拒 403 → 回收后 tenant/account/store 残留均为 0**。超管跨租户门店**详情**按设计 403（CanAccessStore：super_admin 仅默认租户，store.go:139-140），但经 `GET /api/admin/tenants/{id}/stores` 列表可见——**跨租户"可看不可管"是设计边界，非缺陷**。

### 2.7 产品专项（代码审读发现的行为化确认）

- **F-3 客人 AI 点菜助手 LLM 装配断链（行为确认）**：`POST /api/public/store/{slug}/assist` 实测 `source=fallback`。根因 `handlers_guest_assist.go:178` 显式传 `nil` → `handlers_assist.go:173-176` 恒返回空配置 → 永远走规则兜底。注释宣称的"租户维度凭据"未实现。
- **F-4 TM 缓存跨租户共享（设计确认）**：同一原文在 T1/T2 各翻译一次，`translation_cache` 恒 1 行——语言对缓存复用是刻意设计（数据飞轮），但租户 A 的菜名译文可被租户 B 同名菜命中。
- **F-5 健康检查反映配置而非可达性**：`:8099` 实例翻译链指向黑洞地址，`/api/health` 仍报 `translate_mode=live`。请求时降级由 smoke_offline 29 条覆盖（确实有效），但探活语义偏乐观。
- **F-6 隔离数据无 FK 兜底（行为确认）**：超管带**不存在的** `?tenant_id=` 建账号成功（account.tenant_id 无 FK，0005 迁移仅建索引）——产生孤儿账号，仍可登录建店。数据完整性靠应用层自律。

---

## 3. 全量回归总账

### 3.1 项目自带套件（2026-10-19 实测）

| 套件 | 结果 | 判定 |
|---|---|---|
| `gofmt -l` / `go vet ./...` | 零输出 / 通过 | ✓ |
| `go test ./...`（103 个测试函数） | **全部通过** | ✓ |
| `tsc --noEmit`（web） | 通过 | ✓ |
| `smoke.sh`（在线引擎） | **69 通过 / 0 失败**（28 步） | ✓ 与 README 声明一致 |
| `smoke_admin.sh`（超管正例，psql 提权） | **23 通过 / 0 失败** | ✓ 与 README 声明一致 |
| `smoke_offline.sh`（离线词典兜底） | **29 通过 / 0 失败** | ✓ 与 README 声明一致 |
| `render-check`（真浏览器） | **执行 35 条：30 通过 / 5 失败 + 脚本崩溃** | ✗ **见 3.2 归因** |

**覆盖率真值**（`go test -coverprofile` + `go tool cover -func`）：**总 20.2%**。逐包：crypt 86.2% / assist 66.4% / service 58.6% / domain 17.0% / **httpapi 2.9%** / repo·db·config·idgen·cmd 0%。典型金字塔倒置：无副作用纯逻辑包覆盖高，带 IO/HTTP 的骨架包近零——**无任何 HTTP 层集成测试、无中间件单测、无 repo 层测试**。

### 3.2 render-check 红色归因（本项目最重要的过程发现）

失败项：① "底部两个动作按钮"期望 2 实际 3；② 评价弹层不打开（0 张 sheet / 0 星）；③ 脚本在短语卡节崩溃（`Uncaught`）。

**三分归因**：
1. **是我的脚手架假设错了吗**——否，跑的是项目原版脚本，未改一行。
2. **是环境产物吗**——否，Chrome/服务均正常，其余章节全绿。
3. **是真实缺陷吗**——**是"测试过期"这一类真实缺陷，但不是产品缺陷**。git 史证明：`98e5bf3 feat: 客人 AI 点菜助手` 给底部条加了 primary 按钮（StorePage.tsx:335-347 三按钮），而 render-check 最后修改于更早的 `b5202c6`。脚本按文案定位 `.gx-btn--primary` 点击——现在 primary 是 AI 助手而非评价，于是后续断言全部落空。

**独立探针复验产品行为（15/15 通过）**：按正确按钮点击后，评价弹层 15 星正常打开、提交后弹券、问店员 6 条短语卡、AI 助手可提问并收到回复、全程 0 JS 异常。**结论：产品行为正确；项目自带 P0 浏览器回归在 main 上呈红色，是"特性提交未同步更新浏览器测试"的过程缺口。** 交付前必须修，否则 CI 永远红、P0 回归名存实亡。

### 3.3 文档声明 vs 实测（漂移表）

| # | 文档声明 | 实测 | 严重度 |
|---|---|---|---|
| D-1 | README「四套入口共 **137** 条断言」 | 表内合计 69+29+39+23=**160**；137 恰为不含 smoke_admin 的三项和 | 低（算错） |
| D-2 | router.go 包注释路由表 | 缺 17 个实际注册端点（my-tenant、orgs×3、assist×3、ai-credentials×7、guest assist×2、requests×3） | 中 |
| D-3 | docs/API.md 65 个端点行 | 实际注册 68 个 | 低 |
| D-4 | 0005 迁移注释「四级角色」 | 0006 引入 org_admin，实际五级 | 低 |
| D-5 | handlers_guest_assist.go:178 注释「租户维度：超管维护的平台级/租户级凭据」 | 客人端 LLM 恒不接线（F-3） | **高（注释与行为相反）** |
| D-6 | smoke_admin.sh 注释「角色烘焙进 token，必须重登才生效」 | token 载荷仅 {sub,exp}，每请求回库（§2.5） | 低（保守但错误） |
| D-7 | assist/rag.go:364 注释引用 `guardReplyWifi` | 全库无此函数（幽灵引用） | 低 |
| D-8 | README 无部署章节、仓库无 nginx 配置 | 生产 SPA fallback 需部署方自补 | 中 |

---

## 4. 还缺什么（P0/P1/P2 分级，均带 file:line）

### P0——投产前必须解决

| # | 缺口 | 证据 | 为什么 | 怎么修 |
|---|---|---|---|---|
| P0-1 | **客人 AI 点菜助手 LLM 装配断链** | `handlers_guest_assist.go:178` 传 `nil`；`handlers_assist.go:173-176` 空配置恒降级 | 产品当前最核心的"AI"体验实际是规则模式；注释宣称的能力不存在（D-5） | 按 `st.TenantID` 经 `aiCred.Credential` 解析租户级 LLM 凭据传入；补一条 handler 级贯通测试 |
| P0-2 | **全仓无 panic recovery** | python 独立扫描：非测试 Go 代码 `recover()` **0 处** | 任一 handler panic → 连接被静默断开，客户端无响应、无 500、无错误日志（只有 withLog 耗时行）。生产上一次 panic 就是一次"无声事故" | 加 recovery 中间件（包在 withLog 内层），panic → 500 + INTERNAL 信封 + 堆栈日志 |
| P0-3 | **项目自带 P0 浏览器回归在 main 上红色** | §3.2：render-check 5 失败 + 崩溃，根因是 `98e5bf3` 未同步更新脚本 | CI 无法转绿；"评价弹层 P0 回归"名存实亡；下次真回归会被淹没在红色噪音里 | 脚本按钮定位改为按文案语义（排除 AI 按钮）或加 `data-testid`；期望值 2→3；把 render-check 纳入 `make test` 或 CI |
| P0-4 | **默认开发态配置可直接进生产** | `config.go:61-62`：`LC_DEV` 默认 `"1"`（OTP 固定 000000 且回显 `dev_code`）、`LC_SECRET` 有默认占位值 | 忘设环境变量 = 验证码人人可登 + token 可被伪造。`.env.example` 有说明但代码不强制 | 生产构建/启动时校验：`LC_DEV` 非显式 `"0"` 且 `LC_SECRET` 为默认值 → `log.Fatalf` 拒绝启动 |

### P1——规模化前解决

| # | 缺口 | 证据 | 说明 |
|---|---|---|---|
| P1-1 | **租户隔离无数据库兜底** | 6 个迁移 0 处 RLS（agent 审读 + 实测确认）；account/store.tenant_id 无 FK | 隔离 100% 靠应用层 WHERE。当前已审代码均带条件，但任何新查询漏写即越权，数据库不拦。补 FK + 关键表 RLS（休眠式策略也可） |
| P1-2 | **测试金字塔倒置** | 总覆盖 20.2%；httpapi 2.9%、repo 0% | 翻译纯逻辑覆盖 58–86%，但 HTTP/中间件/租户判定零集成测试。至少补：withAuth/requireRole/requireStore 表驱动单测 + 一条租户隔离集成测试 |
| P1-3 | **AI 凭据 repo 层缺租户 WHERE** | `repo/ai_credential.go:71-73` 等仅按 `id` 查，与翻译 Key 的 `id+tenant_id` 双条件（tenant.go:209-211）不对称 | 安全依赖 httpapi 单点门禁（当前 superOnly 有效），但不对称本身就是隐患。改成双条件 |
| P1-4 | **前端路由无守卫** | `AdminApp.tsx:56` 仅查 localStorage token 字符串存在 | **后端 fail-closed 已实测兜底（401/403 全部正确）**，故这是体验问题非安全漏洞：过期 token 的体感是"进了控制台再被打出来"。加路由级守卫 + `/api/me` 预验 |
| P1-5 | **小程序契约漂移 + 未完成** | miniapp 按 `{items, created_at:string}` 读，web 按 `{requests, created_at:number}`（`package-staff/.../index.tsx:18-24` vs `client.ts:128-139`）；`wifi_password` vs `wifi_pwd`；`node_modules` 未安装；无登录实现；语种选择器硬编码 5 个（`pages/store/index.tsx:142`） | 小程序端按错字段读接口会静默拿到空列表。要么补齐（含微信登录），要么明确标注为占位骨架 |
| P1-6 | **token 30 天无吊销机制** | auth.go TTL 30 天；止损靠每请求回库校验状态（有效但增一次查询/请求） | 可接受；规模化后考虑短 token + refresh，或 Redis 黑名单 |

### P2——打磨项

| # | 缺口 | 证据 |
|---|---|---|
| P2-1 | 未知路由 404 纯文本，错误信封不统一 | 实测 B4b：`404 page not found`。加 mux 兜底路由返回 fail 信封 |
| P2-2 | API 无 gzip/br 压缩 | 实测 B6。加压缩中间件（或前置 nginx） |
| P2-3 | `guardNoRawChineseName` 死代码 | `assist/guest.go:271` 定义未接入 `RespondGuest`（guest.go:85-88），"未翻译菜不漏原文"只剩 prompt 单层防护。接入或删除 |
| P2-4 | 进程内计数器当护栏 | 日限额（tenant_translate.go:244）与客人助手 IP 限流（guest_assist.go:73-90）重启清零、多实例各算各——注释自述"防打欠费"可，不能当配额 |
| P2-5 | god component 与重复实现 | AdminApp 911 / PlatformTab 860 / StorePage 710 / Onboarding 676 行；`QrImage` 两处逐字重复且未 `revokeObjectURL`；`relTime` 重复 |
| P2-6 | 计费域缺席 | 无任何计费/支付/订阅表；tenant.perms/expires_at"只建字段不启用"（0005 注释）。商业变现缺载体 |
| P2-7 | dish 行仅在翻译时落库 | 未翻译店客人端空菜单（render-check 夹具注释自认的坑）。确认稿阶段应可见 |
| P2-8 | CORS 通配符 `*` | middleware.go:40。Bearer token 模式下无 CSRF 风险（注释已警示），改 Cookie 鉴权时必须收紧 |
| P2-9 | 无部署产物 | 无 nginx 配置、无 Dockerfile、README 无部署章节（D-8） |

---

## 5. 架构评价

### 5.1 分层（实测装配顺序，main.go:31-115）

```
cmd/api（装配 + fail-fast + 优雅退出）
  └─ httpapi（68 路由 / 中间件 withLog→withCORS→[withAuth 按路由] / 统一信封 respond.go）
       ├─ service（翻译四层装配 / OCR / 二维码 / 图片存储 / KeyVault / AICredVault）
       ├─ assist（经营助手 / 客人助手引擎：LLM + RAG + 数字守卫 + 承诺守卫）
       ├─ repo（手写 SQL，列清单常量化，事务仅 2 处：ReplaceDishes / RedeemCoupon FOR UPDATE）
       └─ domain（实体 + 五级角色 + 校验器）
```

**做对的关键决策**（都有证据）：
1. **依赖极少**：全后端仅 pgx + go-qrcode 两个第三方库，net/http 1.22 原生路由——供应链面与升级风险极小。
2. **统一信封是真统一**：288 处 ok/fail 调用，业务 handler 零裸写响应（仅二维码 PNG / 静态文件 / CORS 预检三处有意例外）。
3. **withAuth fail-closed 且每请求回库**：token 无状态不可吊销，就用"权限判断永远读最新事实"补偿——停用账号即时失效（middleware.go:87-91），且我实测坏/过期 token 一律 401。
4. **平台超管双条件**：角色 5 级**且** tenant_id 为空串（middleware.go:141-144），防"role=super_admin 挂在租户下"的旁路账号——注释记录了翻译助手现网真实事故，这是有故事的安全设计。
5. **翻译四层装配 + "绝不编造"纪律工程化**：TM 缓存 → 降级编排（normalizeLen 规整，防短切片静默吃兜底）→ 主引擎 → 内置词典；出站有数字核验/承诺边界守卫；offline 冒烟 29 条确定性验证"未收录必须留空、不得回填原文"。
6. **密钥工程**：AES-256-GCM + HKDF 派生，明文只在创建/reveal/轮换三个一次性响应且全部落 admin_audit。
7. **注释即文档**：踩坑根因（refCode 因 React 保留字、dish NULLIF、normalizeLen 回归）写在代码里，7 个提交没漂移。

**主要架构问题**：
1. **隔离策略无数据库兜底**（P1-1）：应用层 WHERE 是单点，新增查询漏条件无第二道防线。
2. **无 panic recovery**（P0-2）：http.Server 对 handler panic 的默认行为是断连，不是 500。
3. **测试重心与风险重心错位**：最需要测试的 HTTP/租户判定层覆盖率 2.9%，而纯函数包 80%+。
4. **counter/limiter 用进程内内存**：多实例部署即失效（注释已自述，但架构上留了坑）。
5. **双前端（web/miniapp）无契约同源**：类型各写各的，已漂移（P1-5）。

---

## 6. 商业价值

### 6.1 商业内核（读代码得出，非文档话术）

这不是一个"翻译工具"，是**"帮中国门店做外国客人生意"的基础设施**，代码里的产品逻辑分四层：

1. **获客入口**：门店页 10 语种多语化（注册表驱动，后端加语种前端零改动）+ 门口码/桌码双二维码（换域名即废已印物料，qr.go 注释自述）。
2. **转化钩子**：评价领券——**发券免费、不卡付费墙**（repo/coupon.go:93），用券换三维度评价数据。
3. **体验层**：客人 AI 点菜助手（语音+文字+加菜/服务铃动作）+ 服务请求闭环（open→done）。
4. **平台层**：租户/组织/五级 RBAC/审计/翻译 Key 超管专属——已是"多租户 SaaS 平台"形态，不是单店软件。

**数据飞轮已在代码里**：TM 缓存跨租户共享（F-4 实测确认）——菜名译文复用越多，新店冷启动翻译越准越快；评价/扫码/核销埋点进 insights，口径写明"只算用我们工具的外国客数据"。

### 6.2 核心资产排序

1. **翻译链四层装配 + TM 飞轮**——最重、最完整、测试最厚的子系统（40 个单测 + 2 套冒烟），是产品差异化的根。
2. **租户/RBAC/审计体系**——平台化已就绪，接第二个客户只需开租户。
3. **客人端语言纪律工程**——零硬编码 + 真浏览器 L1–L5 回归（虽然脚本过期，纪律本身是真的）。
4. **服务请求闭环 + AI 助手守卫链**（数字核验/承诺边界/菜名价格核验）——体验差异点。

### 6.3 制约短板

- **无变现载体**：计费域整体缺席（P2-6），租户到期字段"只建不启用"。当前是纯成本系统（MyMemory 免费额度 + LLM 按量）。
- **AI 体验天花板**：客人助手 LLM 未接线（P0-1），"AI 点菜"目前是检索+规则。
- **规模化三个未答问题**：租户隔离无 DB 兜底、限流/配额进程内、无压测数据。
- **交付信心缺口**：E2E 红色（P0-3）+ HTTP 层零集成测试。

---

## 7. 优先行动建议表

| 优先级 | 动作 | 预估 | 依据 |
|---|---|---|---|
| 1 | 修 render-check 按钮定位 + 期望值，纳入 CI（P0-3） | 0.5 天 | §3.2 |
| 2 | 加 panic recovery 中间件（P0-2） | 0.5 天 | §2.7/P0 表 |
| 3 | 生产启动强制校验 LC_DEV/LC_SECRET（P0-4） | 0.5 天 | P0 表 |
| 4 | 客人助手 LLM 按租户接线 + 贯通测试（P0-1） | 1–2 天 | §2.7 F-3 |
| 5 | mux 兜底路由统一 404 信封 + gzip 中间件（P2-1/2） | 0.5 天 | §2.2 |
| 6 | httpapi 中间件表驱动单测 + 租户隔离集成测试（P1-2） | 2–3 天 | §3.1 |
| 7 | account/store.tenant_id 补 FK；AI 凭据 repo 双条件（P1-1/3） | 1 天 | §4 |
| 8 | 前端路由守卫 + token 预验（P1-4） | 0.5 天 | §4 |
| 9 | 小程序契约对齐或明确占位（P1-5） | 1–2 天 | §4 |
| 10 | 计费域设计（按量翻译配额已半成：日限额+能言 Key） | 产品决策 | §6.3 |

---

## 8. 附录

### 8.1 复核方法学（可复现）

1. 代码结论一律以 `file:line` 为证；否定结论（"无 X"）一律用 python 独立扫描复核，不用本机 toybox grep（本机 `grep --version` = toybox 0.8.13，实测会静默零命中）。
2. 项目自带断言一律不信任为最终证据——另写 `tools/indep_byte_uat.sh`（55 项）与 `web/scripts/indep_guest_probe.mjs`（15 项）从零实现。
3. 失败项三分归因（脚手架错 / 环境产物 / 真实缺陷），**修正前的脚手架失败不进报告**。本轮归因记录：WebSocket API 用错（`.on` vs `onmessage`）、Chrome 走系统代理（`--no-proxy-server` 修复）、`$VAR（` 中文括号吞变量名（`{}` 花括号修复，4 处）、psql `INSERT...RETURNING` 状态行污染 ID（取首行修复）、SQL 单引号未转义、`names_json` 未转 text、 dishes 仅在翻译时落库（产品行为，脚本流补 translate 步）、预览接口请求体形状用错。共 7 类脚手架错误，全部修正后重跑通过。
4. 隔离数据用一次性租户/手机号，结束全回收并断言残留 0（§2.6）。

### 8.2 实测命令与产物

```bash
# 服务
cd server && go run ./cmd/api                                  # :8080（健康 ok）
cd web && npm run dev                                          # :5173（注意本机走 localhost 不走 127.0.0.1）
LC_ADDR=:8099 LC_TRANSLATE_URL=http://127.0.0.1:9 go run ./cmd/api   # 离线降级实例

# 项目自带回归
cd server && go test -coverprofile=/tmp/cov.out -covermode=set ./...   # 103 函数全过，总覆盖 20.2%
cd server && gofmt -l ./internal ./cmd && go vet ./...                 # 零输出
cd web && npx tsc --noEmit                                                # 通过
bash server/scripts/smoke.sh            # 69/0
BASE=http://localhost:8080 bash server/scripts/smoke_admin.sh   # 23/0
BASE=http://localhost:8099 bash server/scripts/smoke_offline.sh # 29/0
cd web && node scripts/guest-render-check.mjs   # 30/5 + 崩溃（过期测试，见 §3.2）

# 独立复验（本报告新增，留在 tools/ 供下次复核）
bash tools/indep_byte_uat.sh             # 55 项：53 过 / 2 真实发现（F-1 信封 / F-2 压缩）
cd web && node scripts/indep_guest_probe.mjs  # 15/15（render-check 失败项的产品侧复验）
```

产物路径：`tools/clean_env.sh`（干净 PATH/代理包装器）、`tools/indep_byte_uat.sh`（独立字节级 UAT）、`web/scripts/indep_guest_probe.mjs`（真浏览器探针）。

### 8.3 本机环境陷阱（复现时需知）

1. PATH 注入 toybox grep——断言脚本禁依赖；`grep -P`/`\|`/`\s` 均不可信。
2. HTTP 代理（127.0.0.1:56093）拦截 localhost——curl 一律 `--noproxy '*'`，Chrome 加 `--no-proxy-server`；且 vite 只绑 IPv6，**用 `localhost` 不用 `127.0.0.1`**。
3. `nohup ... &` 在工具调用返回后即死——长跑服务必须用后台任务托管。
4. bash 里 `$VAR` 后紧跟中文全角标点会报 `unbound variable`——一律 `${VAR}`。

---

*报告完。实测数字均可按 §8.2 命令复现；所有缺陷项可按 §4 的 file:line 直接定位。*
