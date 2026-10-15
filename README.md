# LangCross 商家店端（langcross-merchant-app）

> **能言翻译助手** · 面向跨境客流的多语言经营 SaaS —— 商家店端落地包
>
> 一句话：帮餐厅等有跨境客流的商户，用「多语菜单物料 + 店端实时互译 + 饮食知识层（过敏原/辣度/宗教）」接住语言不通的外国客人，把本来会走的客人变成成交。

---

## 本期跑通的闭环（P0a 最小可用版 + 10 语种多语言 + 双 AI 助手）

```
商家上线（引导流 P1–P5）
   → 多语菜单 /s/{slug}
      → 客人扫码
         ├─ AI 点菜助手（RAG 问答：有什么菜 / 多少钱 / 忌口 / 推荐）
         │     → 只「提议」加菜 / 加水 / 服务铃 / 买单 按钮（提议—确认边界）
         │           → 客人点一下才生成服务请求
         ├─ 问店员（人工） / 服务请求（服务铃 / 加水 / 加菜 / 买单）
         │     → 落商家端「服务请求」待办台，可标记完成 / 取消
         └─ 评价领券（三维度：菜品 / 服务 / 会再来吗）
               → 到店核销 → 经营洞察
商家侧另有「AI 经营助手」：基于本店经营事实 + 平台产品知识答疑（无凭据时回落规则模式）
```

- **客人端 AI 点菜助手**：扫桌码即用、不登录；知识 = 本店菜品 + 服务（RAG 现取）；
  守卫纪律 = **菜品名与价格不许编造**；语音识别在浏览器端做（Web Speech API），服务端不碰音频。
- **商家端 AI 经营助手**：基于本店经营事实（数字由服务端现算）+ 平台产品知识；无 LLM 凭据时自动回落「规则模式」。
- **平台 AI 凭据**：LLM 凭据由超管统一维护、密文落库；商家只关心「助手是模型模式还是规则模式」。

**明确不做**（二期）：在线支付、改收银、接 POS、外卖对接、小程序原生、多店后台。

---

## 技术栈

| 层 | 选型 | 说明 |
|---|---|---|
| 后端 | **Go 1.22+**（标准库 `net/http`，方法+通配符路由） | 无 Web 框架依赖 |
| 数据库 | **PostgreSQL** | 本期不上 pgvector，单店菜单用文本相似度足够 |
| 前端 | **React 18 + TypeScript + Vite** | 客人端 H5 与商家端后台同一应用、按路由分端 |
| 二维码 | `github.com/skip2/go-qrcode` | 门口码 / 桌码 PNG |
| OCR | **可插拔**：默认 stub（前端走手动保底）→ 生产接 Python PaddleOCR 微服务 | 服务端识别，端侧无 OCR |
| 翻译 | **可插拔**：TM 缓存（`translation_cache`）→ 主引擎（`LC_TRANSLATE_URL` 自建链／否则 MyMemory）→ 内置中→英词典（离线兜底） | **10 语种**；词典未收录的词返回空，**不编造** |

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
LC_DATABASE_URL="postgres://localhost:5432/langcross?sslmode=disable" go run ./cmd/api

# 3) 起前端
cd web && npm install && npm run dev
```

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

四套入口，共 **132 条断言**，全部可重复执行。

| 命令 | 断言 | 前置 | 覆盖 |
|---|---|---|---|
| `make test` | — | 无 | Go 单测（兜底链路 / 语种注册表 / 接口工具函数）+ `gofmt` / `go vet` / `tsc --noEmit` |
| `make smoke` | 64 | 后端 `:8080` | 端到端 26 步：健康检查 → 语种注册表 → 认证 → 建店（自动配券+双码）→ 保存确认稿 → **未确认拒绝翻译** → 确认 → **一次 5 语种多语化** → 译文落库 → **分类名多语化** → **注册前多语预览** → 公开门店页 → 评价领券（含**券面本地化**）→ **评分契约** → 核销 → 重复核销拒绝 → **跨店核销拒绝** → 经营洞察 → 双码 PNG 魔数 → **客人端 AI 点菜助手（RAG 检索）** → **服务请求全链路**（客人发起 / 商家待办 / 标记完成 / 待办下降）→ **AI 凭据 RBAC 守卫** |
| `make smoke-offline` | 29 | 另起 `:8099`（`make api-offline`） | 把翻译链指向不可达地址 → 确定性验证**内置词典兜底**与「**绝不编造**」纪律（未收录词必须留空、词典只覆盖中→英、其它语种不得回填原文） |
| `make render-check` | 39 | 后端 + 前端 dev server | **真开无头浏览器**，用 CDP 断言客人端渲染纪律 L1–L6、语言选择器 10 项、切语言同步 `<html lang>`、缺译文标注，并回归「评价弹层一打开整页崩」的 React 保留属性 P0 事故 |

```bash
# 典型用法
make test                                  # 静态检查 + 单测，CI 安全
make api & make web &                      # 起服务
make smoke && make render-check
make api-offline & make smoke-offline      # 确定性离线兜底
make reset                                 # 清理测试数据（保留翻译记忆缓存）
```

写测试脚本时踩过的坑（都已写进脚本注释）：

- **清缓存是离线测试的前提**。TM 缓存会命中上一次在线跑出来的译文，把离线兜底整条路径「盖住」，测试假绿。
- **`$VAR` 后面紧跟全角字符必须写成 `${VAR}`**（如 `"（${B}）"`）。否则 bash 会把全角字符的首字节吞进变量名，报 `unbound variable`。
- **无头 Chrome 必须带 `--no-sandbox` / `--remote-allow-origins=*` / `--disable-dev-shm-usage`**。少任一个都连不上：本机沙箱里 Chrome 自带沙箱会 `SIGTRAP` 崩溃，DevTools WebSocket 握手会被拒（表现为 close code 1006）。
- **`dish` 行是翻译时才落库的**。一家「完全没翻过译」的店在客人端是空菜单，测不到「缺译文」态；正确造法是指定一个没翻过的目标语种去看。

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
│   │   └── 0006_org_rbac.sql     # 组织 RBAC + guest_request 服务请求表
│   └── scripts/
│       ├── smoke.sh              # 端到端冒烟测试（在线引擎，20 步带断言）
│       ├── smoke_offline.sh      # 离线兜底冒烟测试（确定性验证内置词典）
│       ├── seed_demo.sh          # 演示门店播种（多语种全开）
│       └── reset_test_data.sh    # 测试数据清理（幂等，打印前后计数）
├── web/                          # React 前端
│   ├── scripts/
│   │   └── guest-render-check.mjs# 客人端渲染回归（无头浏览器 + CDP）
│   └── src/
│       ├── api/client.ts         # API 客户端与类型（含 assist / 服务请求 / AI 凭据）
│       ├── guest/StorePage.tsx   # 客人端门店页（多语优先）+ 评价领券 + 券卡
│       ├── guest/GuestAssistant.tsx # 客人端 AI 点菜助手抽屉（语音 + 文本 + 动作按钮）
│       ├── guest/i18n.ts         # 客人端 10 语种词典 + 问店员短语
│       ├── admin/AdminApp.tsx    # 商家端：概览/菜单(语种覆盖)/券核销/洞察/服务请求/AI凭据/设置
│       ├── admin/RequestsTab.tsx # 服务请求处理台（轮询 + 完成/取消）
│       ├── admin/AICredentialsTab.tsx # 平台 AI 凭据管理（超管；tenant_admin 只读）
│       ├── admin/Onboarding.tsx  # 引导流 P1–P5（P4 支持多语种预览）
│       └── styles/global.css     # 设计令牌 + 客人端/商家端样式
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

---

## 环境变量

见 `.env.example`。全部有默认值，本地零配置可起。

| 变量 | 默认 | 说明 |
|---|---|---|
| `LC_ADDR` | `:8080` | HTTP 监听 |
| `LC_DATABASE_URL` | `postgres://localhost:5432/langcross?sslmode=disable` | DSN |
| `LC_PUBLIC_BASE_URL` | `http://localhost:8080` | 生成门店页 URL / 二维码内容 |
| `LC_SECRET` | 开发用占位值 | token 签名密钥，同时派生 AI/翻译凭据的 AES-256-GCM 加密密钥，**生产必须换**（更换后旧凭据需重新录入） |
| `LC_DEV` | `1` | 开发模式：验证码固定 `000000` 并回显 |
| `LC_OCR_URL` | 空 | PaddleOCR 微服务；空则走 stub |
| `LC_TRANSLATE_URL` | 空 | 既有翻译链（生产首选）；空则走 MyMemory |
| `LC_MT_EMAIL` | 空 | MyMemory 联系邮箱（可选，提升免费配额） |

---

## 与方案文档的关系

本仓库是**实现**。方案文档（《落地方案》《P0引导流_页面级设计》《件④外客服务_可执行细化》《数据模型与接口契约_全量》《UI字节级规范》等）位于项目工作区的 `商家店端方案/` 目录，权威顺序见其中《文档总索引与一致性说明》§2。

冲突时的裁定顺序：**落地方案（范围）→ 数据模型与接口契约（DDL/接口）→ 本文档（技术栈与部署）**。
