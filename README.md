# LangCross 商家店端（langcross-merchant-app）

> **能言翻译助手** · 面向跨境客流的多语言经营 SaaS —— 商家店端落地包
>
> 一句话：帮餐厅等有跨境客流的商户，用「多语菜单物料 + 店端实时互译 + 饮食知识层（过敏原/辣度/宗教）」接住语言不通的外国客人，把本来会走的客人变成成交。

---

## 本期跑通的闭环（P0a 最小可用版 + 10 语种多语言 + 双 AI 助手 + 平台管理）

```
商家上线（引导流 P1–P5）
   → 多语菜单 /s/{slug}（菜品图片：有图显图 / 无图首字占位）
      → 客人扫码
         ├─ AI 点菜助手（RAG 问答：有什么菜 / 多少钱 / 忌口 / 推荐）
         │     → 只「提议」加菜 / 加水 / 服务铃 / 买单 按钮（提议—确认边界）
         │           → 客人点一下才生成服务请求
         ├─ 问店员（人工） / 服务请求（服务铃 / 加水 / 加菜 / 买单）
         │     → 落商家端「服务请求」待办台，可标记完成 / 取消
         └─ 评价领券（三维度：菜品 / 服务 / 会再来吗）
               → 到店核销 → 经营洞察
商家侧另有「AI 经营助手」：基于本店经营事实 + 平台产品知识答疑（无凭据时回落规则模式）
平台侧「管理台」（超管 / 租户管理员）：租户、账号角色、翻译 Key（超管专属）、审计
```

- **客人端 AI 点菜助手**：扫桌码即用、不登录；知识 = 本店菜品 + 服务（RAG 现取）；
  守卫纪律 = **菜品名与价格不许编造**；语音识别在浏览器端做（Web Speech API），服务端不碰音频。
  助手只「提议」动作按钮（加菜 / 加水 / 服务铃 / 买单），**客人点一下才生成服务请求**（提议—确认边界）。
- **服务请求闭环**：客人点一下 → 商家端「服务请求」待办台（8 秒轮询）→ 服务员标记完成。
- **菜品图片**：商家在菜单页逐菜上传（≤5MB，PNG/JPEG/WebP/GIF，本地存储可换对象存储）；
  客人端有图显图、无图渲染首字占位块，不出现参差不齐的空洞。
- **商家端 AI 经营助手**：基于本店经营事实（数字由服务端现算）+ 平台产品知识；无 LLM 凭据时自动回落「规则模式」。
- **平台 AI 凭据**：LLM 凭据由超管统一维护、密文落库；商家只关心「助手是模型模式还是规则模式」。
- **平台管理**（超管控制台 + 租户管理员后台）：租户增删改、平台统计、账号与角色（五级）、
  翻译 Key（★ 超管专属增删改查——能言开放平台的凭据，租户一律不接触）、审计留痕。
  超管管租户，租户管理员管自己人；`effectiveTenant` 在服务端收口「谁能看谁」。
- **翻译引擎链**：租户配了能言 Key 走能言开放平台（按租户解析，失败即降级），
  否则走自建链 / MyMemory，再不成交给内置词典——整条链对菜品价格与菜名**绝不编造**。

**明确不做**（二期）：在线支付、改收银、接 POS、外卖对接、多店后台。小程序端已起步（`miniapp/`，Taro 骨架：客人端门店页 / 商家后台首页 / 店员待办三页已迁移，其余 Tab 待 S4），不算「不做」也不算「做完」。

---

## 技术栈

| 层 | 选型 | 说明 |
|---|---|---|
| 后端 | **Go 1.22+**（标准库 `net/http`，方法+通配符路由） | 无 Web 框架依赖 |
| 数据库 | **PostgreSQL** | 本期不上 pgvector，单店菜单用文本相似度足够 |
| 前端 | **React 18 + TypeScript + Vite** | 客人端 H5 与商家端后台同一应用、按路由分端 |
| 小程序 | **Taro 4 + React**（`miniapp/`） | 微信小程序端骨架：客人端门店页 / 商家后台首页 / 店员待办已迁移，其余 Tab 待 S4；表格改 flex 行、select 改 Picker（迁移规则见方案 §4.3） |
| 二维码 | `github.com/skip2/go-qrcode` | 门口码 / 桌码 PNG |
| OCR | **可插拔**：默认 stub（前端走手动保底）→ 生产接 Python PaddleOCR 微服务 | 服务端识别，端侧无 OCR |
| 翻译 | **可插拔**：TM 缓存（`translation_cache`）→ 主引擎（租户配了能言 Key 走能言开放平台 → 否则 `LC_TRANSLATE_URL` 自建链／MyMemory）→ 内置中→英词典（离线兜底） | **10 语种**；词典未收录的词返回空，**不编造**；能言 Key 由超管在后台维护，密文落库 |

### 多语言（10 语种）

| Tier | 语种 | 口径 |
|---|---|---|
| 1 · 承诺质量 | `zh` `en` | 对外可承诺 |
| 2 · 较好 | `ja` `ko` `th` | 主客源国，优先保证 |
| 3 · 尽力而为 | `fr` `es` `de` `pt` `vi` | 机器翻译直出，页面如实标注「机器翻译」 |

- 语种清单的唯一真值在 `server/internal/service/lang.go` 的 `SupportedLangs`；**新增语种 = 登记一行**，业务代码零改动。
- 菜名存 `dish.names_json`、分类名存 `store.category_names`（都是 JSONB）；`name_zh` 始终是原文真值。
- ★ **渲染纪律**：页面主标题**永远是目标语言**，原文只作小字辅助；译文缺失时显示原文并明确标注「未翻译」，**绝不让中文冒充外语大字**。
- 设计细节见 `商家店端方案/多语言能力设计_10语种与客人端渲染规范.md`。

---

## 快速开始

```bash
# 0) 依赖：Go 1.22+、Node 18+、PostgreSQL

# 1) 建库
createdb langcross

# 2) 起后端（启动时自动执行迁移）
cd server
LC_DEV=1 LC_DATABASE_URL="postgres://localhost:5432/langcross?sslmode=disable" go run ./cmd/api
#     ^^^^^^ 开发模式：验证码固定 000000 并回显。不设即生产模式——
#            token 密钥不允许用默认占位值（main.go 会拒绝启动）。

# 3) 起前端
cd web && npm install && npm run dev
```

小程序端（`miniapp/`，Taro 骨架）另行安装依赖后用微信开发者工具打开
`miniapp/dist/dev/mp-weixin`（`npm run dev:weapp` 编译）；H5 预览走 `npm run dev:h5`。

| 入口 | 地址 |
|---|---|
| 商家端后台 | http://localhost:5173/admin |
| 引导流 P1–P5 | http://localhost:5173/admin/onboarding |
| 客人端门店页 | http://localhost:5173/s/{slug} |

**演示门店播种**（造一家 17 道菜 / 6 分类 / 9 语种全开的店，用于人工验收）：

```bash
bash server/scripts/seed_demo.sh          # 结束时会打印门店页 URL
```

---

## 测试

四套入口，共 **193 条断言**，全部可重复执行（另有 1 条机翻质量条件断言：回复是否提及菜单菜品，命中才计分，未命中按设计跳过）。

| 命令 | 断言 | 前置 | 覆盖 |
|---|---|---|---|
| `make test` | — | 无 | Go 单测（兜底链路 / 语种注册表 / 接口工具函数）+ `gofmt` / `go vet` / `tsc --noEmit` + 导出面中文文档注释门禁（`tools/check_doc_comments.py`，棘轮：新导出符号不补注释就红） |
| `make smoke` | 77 | 后端 `:8080` | 端到端 28 步：健康检查 → 语种注册表 → 认证 → 建店（自动配券+双码）→ 保存确认稿 → **未确认拒绝翻译** → 确认 → **一次 5 语种多语化** → 译文落库 → **分类名多语化** → **注册前多语预览** → 公开门店页 → 评价领券（含**券面本地化**）→ **评分契约** → 核销 → 重复核销拒绝 → **跨店核销拒绝** → 经营洞察 → 双码 PNG 魔数 → **客人端 AI 点菜助手（RAG 检索）** → **服务请求全链路**（客人发起 / 商家待办 / 标记完成 / 待办下降）→ **AI 凭据 RBAC 守卫** → **翻译 Key RBAC 守卫**（超管专属，普通商家连列表都 403）→ **菜品图片**（上传 / 公开访问 PNG 魔数 / 删除 404） |
| `make smoke-offline` | 32 | 另起 `:8099`（`make api-offline`） | 把翻译链指向不可达地址 → 确定性验证**内置词典兜底**与「**绝不编造**」纪律（未收录词必须留空、词典只覆盖中→英、其它语种不得回填原文）；并回归**健康检查诚实口径**（黑洞地址必须报 `translate_reachable=false` / `translate_mode=dict-stub`，而不是谎报 live） |
| `make render-check` | 61+1 | 后端 + 前端 dev server | **真开无头浏览器**，用 CDP 断言客人端渲染纪律 L1–L6、语言选择器 10 项、切语言同步 `<html lang>`、缺译文标注，并回归「评价弹层一打开整页崩」的 React 保留属性 P0 事故；含**零译文店夹具**（确认即有菜单、未翻译如实标注而非谎称机器翻译）、**AI 助手面板**交互，以及**商家端未翻译提醒横幅**（零译文弹出、补翻后消失）。「+1」为机翻质量条件断言（回复是否提及菜单菜品），按设计可跳过 |
| `bash scripts/smoke_admin.sh` | 23 | 后端（脚本默认 `:8081`，可用 `BASE=` 覆盖）+ 开发库 psql | 超管正例：psql 提权 → 租户 CRUD、账号管理、翻译 Key（超管专属：脱敏+启停）、AI 凭据（can_write）、经理 vs 超管权限对照 403、审计留痕。**只跑开发库**（提权是危险操作，脚本刻意不自动化） |

```bash
# 典型用法
make test                                  # 静态检查 + 单测，CI 安全
make api & make web &                      # 起服务
make smoke && make render-check
make api-offline & make smoke-offline      # 确定性离线兜底
make reset                                 # 清理测试数据（保留翻译记忆缓存）

bash tools/indep_byte_uat.sh               # 独立字节级 UAT（75 条，另起一套断言，不复用上面任何脚本）
```

独立 UAT 额外覆盖这四套不碰的层次：特殊字符**字节级**双向比对（API 写 → psql 直读 → API 读回）、
经 Vite 代理的真实部署路径、401/403/404/400/405/OPTIONS 错误信封统一性、token 载荷字段集（防角色烘焙进 token）、
多租户隔离（一次性租户 → 本租户可见 → 他租户不可见 → 超管边界 → 回收断言残留 0）、租户外键三档语义（0007）。
它**自带环境自证**（第 0 段）并**自行 psql 提权**——跑它不需要先跑过别的脚本。

写测试脚本时踩过的坑（都已写进脚本注释）：

- **清缓存是离线测试的前提**。TM 缓存会命中上一次在线跑出来的译文，把离线兜底整条路径「盖住」，测试假绿。
- **`$VAR` 后面紧跟全角字符必须写成 `${VAR}`**（如 `"（${B}）"`）。否则 bash 会把全角字符的首字节吞进变量名，报 `unbound variable`。
- **无头 Chrome 必须带 `--no-sandbox` / `--remote-allow-origins=*` / `--disable-dev-shm-usage`**。少任一个都连不上：本机沙箱里 Chrome 自带沙箱会 `SIGTRAP` 崩溃，DevTools WebSocket 握手会被拒（表现为 close code 1006）。
- **`dish` 行在确认草稿时就落库**（2026-10-19 Q1 修订：确认与翻译共用同一装配，确认即有菜单）。未翻译只是 `names` 各语种为空，客人端展示原文并挂「未翻译」标、提示语如实说「尚未翻译」——测「缺译文」态直接建一家不翻译的店即可。
- **独立脚本不要依赖别的脚本的副作用**（2026-10-20 修）：`indep_byte_uat.sh` 的 E 段原先假定「超管已存在」（由 `smoke_admin.sh` 提权）。清空业务数据后直接跑，四条断言一起变红——**看着像多租户回归，实际只是没有超管**（登录接口首次登录会建号，默认 `manager`，于是 `admin/*` 一律 403）。环境缺口伪装成功能回归，比漏测更消耗信任，所以脚本改为自行提权并自证角色已生效。
- **断言跨行指令要先折叠换行**（2026-10-20 修）：`gzip_types` 这类 nginx 指令后面跟着三行类型，单行 `grep` 必然漏；断言写法不对导致的假红，比缺功能更难查。

---

## 部署

产物在 `deploy/`，拓扑是「nginx 是唯一对外端口」：

```
浏览器 / 微信 ──▶ nginx (:80/:443)
                    ├─ /api/*        ──▶ Go API (:8080)   反代 + 限流
                    └─ 其它路径      ──▶ web/dist 静态 + history fallback
```

```bash
# 1) 构建前端产物（落到 web/dist）
cd web && npm run build

# 2) 放置静态产物与 nginx 配置
sudo mkdir -p /var/www/langcross && sudo cp -r web/dist /var/www/langcross/dist
sudo cp deploy/nginx.conf /etc/nginx/conf.d/langcross.conf   # 记得改 server_name 与 TLS 段

# 3) 构建并起后端（★ 不要设 LC_DEV；LC_SECRET 必须显式给真实值）
cd server && go build -o bin/api ./cmd/api
sudo cp deploy/langcross-api.service /etc/systemd/system/
sudo systemctl daemon-reload && sudo systemctl enable --now langcross-api
```

| 关注点 | 放在哪 | 为什么 |
|---|---|---|
| 压缩（gzip） | **nginx** | 应用层保持零依赖：换 Caddy / CDN 不用改一行 Go；也不会出现「新增接口忘了过压缩中间件」这种没人会发现的债。因此 **API 直连 `:8080` 不带 `Content-Encoding` 是设计如此**，不是缺陷 |
| TLS / 会话复用 | **nginx** | 客人是餐厅里扫码进来的，移动端网络差，会话复用省一次往返 |
| 限流 | **两层** | 应用层是**进程内**计数（多实例各算各的），网关这层按真实来源 IP 计数，是挡分布式脚本的第一道门 |
| SPA history fallback | **nginx** | 客人扫桌码进来是 `/s/{slug}`，磁盘上没有这个文件，必须回落 `index.html`，否则深链 404 |
| 上传体积 | **nginx 8m** | 应用层 `MaxImageBytes` 是 5MB，网关留余量；不配的话大图上传会被网关 413 挡掉 |

`deploy/langcross-api.service` 里 `KillSignal=SIGTERM` + `TimeoutStopSec=20` 是配合后端的优雅停机
（收到 SIGTERM 后跑完 `Shutdown` 的 10s 宽限，在途请求与埋点 sink 有时间收尾）。

---

## 界面

客人端门店页（同一家店、同一份数据，切语言即整页切换）：

| English | 日本語 | ไทย |
|---|---|---|
| ![en](docs/screenshots/guest-en-full.png) | ![ja](docs/screenshots/guest-ja-full.png) | ![th](docs/screenshots/guest-th-full.png) |

| 语言选择器（10 语种） | 译文缺失 → 诚实标注「未翻译」 | 评价 → 领券 |
|---|---|---|
| ![lang](docs/screenshots/guest-lang-picker.png) | ![pending](docs/screenshots/guest-pending-ja.png) | ![coupon](docs/screenshots/guest-coupon.png) |

> 截图为演示店实拍：先 `bash server/scripts/seed_demo.sh` 播种，再用无头浏览器按 500px 手机宽度抓取（macOS 无头窗口最小 CSS 宽就是 500px，再窄会被裁切）。

---

## 目录结构

```
langcross-merchant-app/
├── server/                       # Go 后端
│   ├── cmd/api/main.go           # 入口：连库 → 迁移 → 装配 → 启动
│   ├── internal/
│   │   ├── config/               # 环境变量配置
│   │   ├── db/                   # 连接池 + 迁移执行器
│   │   ├── domain/               # 领域模型（含 assist / guest_request / tenant RBAC）
│   │   ├── repo/                 # 数据访问层（全部 SQL 集中在此）
│   │   ├── service/              # 外部能力抽象：OCR / 翻译（10 语种）/ 二维码 / 上传 / 租户翻译 Key
│   │   ├── assist/               # AI 助手引擎：RAG 知识库（rag.go）+ 守卫 + 会话 + 经营助手
│   │   ├── crypt/                # 凭据 AES-256-GCM 加密（LC_SECRET 派生）
│   │   ├── idgen/                # 随机 ID 与券码
│   │   └── httpapi/              # 路由 / 鉴权 / 处理器（含 handlers_assist* / handlers_guest_assist）
│   ├── migrations/               # SQL 迁移（embed 进二进制，按序号执行）
│   │   ├── 0001_init.sql         # 11 张业务表（不含 schema_migrations）
│   │   ├── 0002_i18n.sql         # dish.names_json + translation_cache
│   │   ├── 0003_category_i18n.sql# store.category_names
│   │   ├── 0004_dish_image.sql   # 菜品图片
│   │   ├── 0005_tenant_rbac.sql  # 租户 / 角色 RBAC
│   │   ├── 0006_org_rbac.sql     # 组织 RBAC + guest_request 服务请求表
│   │   └── 0007_tenant_fk.sql    # 租户外键（先 FK 后 RLS）：三档约束 + 删租户守卫 + 孤儿硬失败
│   └── scripts/
│       ├── smoke.sh              # 端到端冒烟测试（在线引擎，28 步带断言）
│       ├── smoke_admin.sh        # 超管端到端冒烟（psql 提权；只跑开发库）
│       ├── smoke_offline.sh      # 离线兜底冒烟测试（确定性验证内置词典）
│       ├── seed_demo.sh          # 演示门店播种（多语种全开）
│       └── reset_test_data.sh    # 测试数据清理（幂等，打印前后计数）
├── web/                          # React 前端
│   ├── scripts/
│   │   └── guest-render-check.mjs# 客人端渲染回归（无头浏览器 + CDP）
│   └── src/
│       ├── api/client.ts         # API 客户端与类型（含 assist / 服务请求 / AI 凭据 / 平台管理）
│       ├── guest/StorePage.tsx   # 客人端门店页（多语优先）+ 菜品图片 + 评价领券 + 券卡
│       ├── guest/GuestAssistant.tsx # 客人端 AI 点菜助手抽屉（语音 + 文本 + 动作按钮）
│       ├── guest/i18n.ts         # 客人端 10 语种词典 + 问店员短语
│       ├── admin/AdminApp.tsx    # 商家端：概览/菜单(语种覆盖+图片)/券核销/洞察/服务请求/平台管理/AI凭据/设置
│       ├── admin/RequestsTab.tsx # 服务请求处理台（轮询 + 完成/取消）
│       ├── admin/PlatformTab.tsx # 平台管理：超管(概览/租户/翻译Key) + 租户管理员(账号/审计)
│       ├── admin/AICredentialsTab.tsx # 平台 AI 凭据管理（超管；tenant_admin 只读）
│       ├── admin/Onboarding.tsx  # 引导流 P1–P5（P4 支持多语种预览）
│       └── styles/global.css     # 设计令牌 + 客人端/商家端样式
├── miniapp/                      # Taro 小程序端（骨架，S4 迁移中）
│   ├── config/                   # Taro 编译配置（dev / prod）
│   └── src/
│       ├── pages/store/          # 客人端门店页（扫普通链接码解析 slug+ref）
│       ├── package-admin/        # 商家后台分包（首页占位，table→flex 行迁移规则）
│       ├── package-staff/        # 店员分包（服务请求待办 + 订阅消息）
│       ├── utils/                # http（token 注入）/ storage（Taro 同步缓存）/ lang
│       └── i18n/                 # 小程序端语种工具
├── tools/                        # 独立验证工具（不进 make 常规链）
│   ├── indep_byte_uat.sh         # 独立字节级 UAT（75 条，另起一套断言，自带环境自证）
│   └── check_doc_comments.py     # 导出面中文文档注释门禁（棘轮 + selftest）
├── deploy/                       # 部署产物
│   ├── nginx.conf                # SPA fallback + /api 反代 + gzip + 限流 + 上传体积
│   └── langcross-api.service     # systemd 单元（含优雅停机宽限）
├── docs/API.md                   # 接口契约
├── Makefile
└── .env.example
```

---

## 写代码时必须守住的纪律

1. **安全底线**：过敏 / 宗教 / 禁忌的回答只走结构化字段与知识包，**绝不走 LLM 生成**。
2. **未确认不翻译**：原文确认稿（`source_draft`）未 `confirmed_at` 时，翻译接口一律拒绝。
3. **数据口径**：经营洞察只说「外国客点单数据」，**不说「全店经营数据」**；只做趋势 / 占比 / 排序，不编精确百分比。
4. **不编造**：翻译未命中的词返回空串，由前端回落显示原文。
5. **语言纪律**：页面主标题**永远是客人选的目标语言**；原文只作小字辅助；译文缺失时显示原文并标注「未翻译」（弱化样式），**绝不让中文冒充外语大字**。
6. **物料定死**：门店页 URL `/s/{slug}` 印在物料上后不可变。
7. **券是免费的**：券是「满意度 + 复购」两个一期核心指标唯一的客观数据源，不卡在付费墙后。
8. **兜底不依赖 WiFi**：WiFi 可选、注册后补填；未填则客人端不渲染 WiFi 行。
9. **本期无支付**：引导流 P5 = 注册（免费），不是付费；只采集付费意愿（`e_pay_intent`）。
10. **React 保留属性**：自定义组件不要用 `ref` / `key` 作为业务 prop 名（`ref="t01"` 会被当成字符串 ref 并让组件崩掉）；本仓库统一用 `refCode`。
11. **兜底链路不能静默丢弃**：翻译主引擎失败时返回的可能是 `nil` 或短切片；进兜底前必须先用 `normalizeLen` 规整长度。否则 `for i := range out` 迭代 0 次，离线词典兜底会被**静默吃掉**（这是真实发生过的 bug，`smoke_offline.sh` 是它的回归测试）。
12. **语种清单只有一处真值**：后端 `service.SupportedLangs`。前端一律从 `GET /api/translation/langs` 取，**任何地方都不许硬编码语种清单**；新增语种 = 登记一行，业务代码零改动。
13. **原文是真值**：`dish.name_zh` 永不被译文覆盖；译文一律写进 `names` JSONB，没翻出来的语种**不要出现这个 key**（不是空串、更不是原文）。
14. **AI 助手绝不编造菜品/价格**：客人端点菜助手必须把引用的菜名包进 `[[...]]`，服务端拿真实菜单核对；价格只信库里的值。答不上来就诚实转「按服务铃找店员」，绝不编一道不存在的菜或乱报一个价。
15. **提议—确认边界**：AI 点菜助手只「建议」动作按钮（加菜 / 加水 / 服务铃 / 买单），客人点一下才真正生成服务请求；助手**绝不**代替客人下单。
16. **凭据只归超管**：LLM 凭据由平台超管统一维护、密文落库（AES-256-GCM）；明文只在「创建后 / 轮换后 / reveal」三个一次性响应里出现，列表与审计只存脱敏串。
17. **翻译 Key 是超管专属**：能言 Key 的增删改查（连列表）只归超管——租户管理员连脱敏列表都看不到。Key 是平台成本凭据，对租户没有实际用途（他既不能用也不能改），索性不看，少一个信息暴露面。服务端翻译链按租户解析 Key 的逻辑不经过后台接口。
18. **图片是替换不是追加**：菜品图片上传走「同字段覆盖」——新图落盘后删旧文件；写库失败必须回滚删掉刚落的文件，否则会留孤儿文件。文件名用 32 位随机 hex，「知道文件名 = 能看图」，故不另加鉴权、也不在任何接口返回图片清单。
19. **租户归属必须有主**（2026-10-19 Q3）：`store` / `org` / `translation_api_key` 的 `tenant_id` 必须是真实存在的租户（0007 起有外键）；`account` / `ai_credential` 允许空串表示**平台级**（超管账号、平台默认 LLM 凭据），但非空就必须存在（触发器校验）。写门店归属一律走 `effectiveTenant(r, acc)`，**不要直接拿 `acc.TenantID`**——超管是空串，会写出一条谁都看不见的「无主门店」。删租户前必须先清账号与凭据（守卫会挡）；`org` 例外，它随租户级联删除。`admin_audit` 刻意不加约束：审计要活过租户。
20. **状态报告只报事实**（2026-10-19 Q2）：健康检查与启动日志的翻译字段必须分开说「配了什么」（`translate_engine`）与「连得上吗」（`translate_reachable`）。翻译链配成黑洞地址时要如实报 `dict-stub` / `不可达`，**不许因为装配自带词典兜底就报 `live`**——装配成功 ≠ 引擎可用。
21. **导出符号必须有中文文档注释**（2026-10-20）：Go 的导出函数 / 方法 / 类型、TS 的导出函数 / 常量 / 接口，紧邻上一行必须是中文 `//` 注释，写用途与边界，**不许复述符号名**。门禁 `tools/check_doc_comments.py`（`make doc-comments`）棘轮守住：新代码欠账就 exit 1。
22. **压缩 / TLS / 限流归网关**（2026-10-20 FIX-12）：应用层保持零依赖，这些横切关注点交给 nginx（`deploy/nginx.conf`）。**API 直连不带 `Content-Encoding` 是设计如此**，别顺手在 Go 里加个 gzip 中间件——那样压缩逻辑会分裂到两处，网关再压一遍就是双重压缩，而且没人会发现。同理，SPA 深链回落必须配在网关，否则桌码扫出来 404。
23. **归属口径写进 SQL，不写进 Go 的 if**（2026-10-20 FIX-8/10）：`WHERE id=$1 AND (tenant_id=$2 OR tenant_id='' OR $2='')`——平台级凭据与超管的 `tenant_id` 都是空串，朴素的 `tenant_id=$2` 会把超管挡在自己维护的凭据门外；列表的作用域同理必须落在 `WHERE` 里，不能「取全表回来再筛」。写测试时**断言 SQL 文本**而不是只断言响应，否则后一种实现照样能绿。
24. **公开端点的成本护栏按「花钱的那个主体」计**（2026-10-20 FIX-7）：客人助手不登录，IP 限流挡不住分布式脚本，所以日限按**门店**计（`reserveGuestLLM`，默认 1000/店/天，0=不限，按 UTC 归零）。超限**收回 LLM 配置但客人照常得到回答**——护栏的目的是「不再花钱」，不是「让客人干等」。计数口径是「尝试了一次模型模式」，失败也计：请求已发出、钱已花，按结果计数会让失败重试变成免费通道。

---

## 环境变量

见 `.env.example`。全部有默认值，本地零配置可起。

| 变量 | 默认 | 说明 |
|---|---|---|
| `LC_ADDR` | `:8080` | HTTP 监听 |
| `LC_DATABASE_URL` | `postgres://localhost:5432/langcross?sslmode=disable` | DSN |
| `LC_PUBLIC_BASE_URL` | `http://localhost:8080` | 生成门店页 URL / 二维码内容 |
| `LC_SECRET` | 开发用占位值 | token 签名密钥，同时派生 AI/翻译凭据的 AES-256-GCM 加密密钥，**生产必须换**（更换后旧凭据需重新录入）；生产模式下仍是占位值或不足 32 字符将拒绝启动 |
| `LC_DEV` | 不设（生产模式） | 开发模式：验证码固定 `000000` 并回显。**只有显式设为 `1` 才开启**——忘了设的生产部署不会因此敞开登录（2026-10-19 审计 P0-4） |
| `LC_OCR_URL` | 空 | PaddleOCR 微服务；空则走 stub |
| `LC_TRANSLATE_URL` | 空 | 既有翻译链（生产首选）；空则走 MyMemory |
| `LC_MT_EMAIL` | 空 | MyMemory 联系邮箱（可选，提升免费配额） |

---

## 与方案文档的关系

本仓库是**实现**。方案文档（《落地方案》《P0引导流_页面级设计》《件④外客服务_可执行细化》《数据模型与接口契约_全量》《UI字节级规范》等）位于项目工作区的 `商家店端方案/` 目录，权威顺序见其中《文档总索引与一致性说明》§2。

冲突时的裁定顺序：**落地方案（范围）→ 数据模型与接口契约（DDL/接口）→ 本文档（技术栈与部署）**。
