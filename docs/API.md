# 接口契约

> 基址：`http://localhost:8080`。除「客人端」与「工具」外均需 `Authorization: Bearer <token>`。
> 统一错误响应：`{ "ok": false, "error": { "code": "VALIDATION", "msg": "人话文案" } }`
> 错误码：`UNAUTH` `FORBIDDEN` `NOT_FOUND` `CONFLICT` `VALIDATION` `OFFLINE_ONLY` `RATE_LIMIT` `INTERNAL`
>
> **多语言约定**（贯穿所有返回菜品的接口）：
> - `name_zh` 永远是原文真值，任何情况下都不被译文覆盖。
> - 译文统一放在 `names` 里：`{"en": "...", "ja": "..."}`，**没翻出来的语种直接不出现这个 key**（不是空串、更不是原文）。
> - `name_en` 是给旧代码的兼容字段，恒等于 `names["en"]`（无 en 译文时不存在）。
> - 分类译文放在 `store.category_names`：`{"en": {"热菜": "Hot dishes"}, ...}`。
> - **绝不编造**：译文缺失时前端回落显示原文并标注「未翻译」，接口不塞占位符。

## 健康检查

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/health` | 返回 `ocr_ready` / `ocr_mode` / `translate_ready` / `translate_mode` |

`ocr_mode` ∈ `paddleocr` \| `stub-manual`；`translate_mode` ∈ `live` \| `dict-stub`。

## 认证

| 方法 | 路径 | 入参 | 说明 |
|---|---|---|---|
| POST | `/api/auth/otp` | `{phone}` | 发验证码；开发模式（`LC_DEV=1`）回显 `dev_code` |
| POST | `/api/auth/login` | `{phone, code}` | 登录/自动注册，返回 `token` + `account` |
| GET | `/api/me` | — | 当前账号 + 门店列表 |

## 多语言

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/translation/langs` | **语种注册表，前端语言选择器的唯一真值**。返回 `{source, langs[], engine}`；每项含 `code` / `native`（母语写法）/ `name_zh` / `tier`（1 承诺 · 2 较好 · 3 尽力而为）。公开接口 |

前端**不硬编码语种清单**：新增语种只需在后端注册表登记一行，前端零改动。

## 商家端（需登录）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/stores` | 门店列表 |
| POST | `/api/stores` | 建店；★ **注册后自动配好券模板**，返回双码链接 |
| GET | `/api/stores/{id}` | 门店 + 菜单 + 最新确认稿 + 链接（门店含 `category_names`） |
| PATCH | `/api/stores/{id}` | 更新门店（档位 `service_tier`、点单模式 `order_mode`、WiFi、券开关） |
| GET | `/api/stores/{id}/qr?ref=door\|t01` | 门口码 / 桌码 PNG |
| GET | `/api/stores/{id}/draft` | 最新原文确认稿 |
| POST | `/api/stores/{id}/draft` | 保存确认稿 `{lang, items:[{name,price,category,selected}]}` |
| POST | `/api/stores/{id}/draft/confirm` | 确认原文；★ **未确认不允许翻译**（否则 `VALIDATION`） |
| POST | `/api/stores/{id}/translate` | 多语化：`{targets:["en","ja",...]}` → 只翻 `selected` 的菜，结果写 `dish.names_json`；**同批顺带把分类名多语化**写 `store.category_names`。返回 `{dishes, translated_count, targets, categories, engine}` |
| GET | `/api/stores/{id}/dishes` | 菜单（每道菜含 `name_zh` + `names`） |
| PATCH | `/api/stores/{id}/dishes/{dishId}` | 更新售罄 / 每日剩余 |
| POST | `/api/stores/{id}/dishes/{dishId}/image` | 上传 / 替换菜品图片（multipart 字段 `image`，≤5MB，PNG/JPEG/WebP/GIF）；返回 `{image_url}`，旧图自动删除 |
| DELETE | `/api/stores/{id}/dishes/{dishId}/image` | 删除菜品图片（幂等） |
| GET | `/api/uploads/{file}` | 图片静态服务（文件名 32 位随机 hex，不可枚举即不可见） |
| GET | `/api/stores/{id}/ratings` | 评价列表（含客人母语原文 + 中文译文 `comment_zh`） |
| GET | `/api/stores/{id}/insights` | 经营洞察：`{scans, coupons_issued, coupons_redeemed, rating, scope_note}`；★ 只算「用我们工具的外国客数据」 |
| GET | `/api/stores/{id}/coupon-template` | 券模板（`content` 是多语 map） |
| GET | `/api/stores/{id}/coupons` | 券列表 |
| POST | `/api/stores/{id}/coupon/redeem` | 核销 `{code}`；★ 校验券归属本店，重复核销 / 跨店核销均拒绝 |
| GET | `/api/stores/{id}/requests` | 客人服务请求待办（默认只看 `open`；`?status=done\|cancelled` 看全部；返回 `open_count`） |
| POST | `/api/stores/{id}/requests/{rid}/done` | 标记请求 `open` / `done` / `cancelled`（服务员处理台） |

## AI 经营助手（商家端，需登录）

知识 = 平台产品知识 + 本店经营事实（数字由服务端 `storeFacts` 现算，模型只组织语言）。
无 LLM 凭据时自动回落「规则模式」（话术直配 / 知识库），仍可回答。

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/api/assist/chat` | 一次问答 `{store_id?, question}`；`store_id` 空 = 平台级问答（只答产品怎么用）。返回 `reply{content,actions,source}` + `assist_llm`（是否模型模式） |
| GET | `/api/assist/history` | 对话历史（`?store_id=` 过滤） |
| POST | `/api/assist/clear` | 清空对话（`?store_id=`） |

## 客人端（公开，无需登录）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/public/store/{slug}?ref=` | 门店页数据。返回 `{store, dishes}`；门店含 `category_names`、菜品含 `names`；★ **未填 WiFi 时 `wifi_ssid` 字段不出现**（前端据此条件渲染）。顺带记 `g_scan` |
| POST | `/api/public/store/{slug}/rating` | 提交三维度评价 `{ref, score_dish, score_service, score_return, comment, lang}`。★ **评价即领券**。返回 `{rating, coupon, coupon_gift}`，其中 `coupon_gift` 是**按客人语言本地化后的券面文案** |
| GET | `/api/public/coupon/{code}` | 按券码查券 |
| POST | `/api/public/translate-preview` | P4 注册前多语预览（不落库、不需登录）。入参 `{from, targets[], items[]}`；返回 `{items, categories, source_cats, targets, engine}`，`items[].names` 为译文 |

### 评分契约（有意的不对称）

| 入参 | 行为 | 理由 |
|---|---|---|
| 任一维度 `<= 0` | `400 VALIDATION`「请完成三维度打分」 | 视为「客人根本没点星星」，**绝不伪造 1 星** |
| 任一维度 `> 5` | 收敛到 `5` | UI 是 5 星控件，越界只可能是客户端 bug；宁可收敛也不丢客人评价 |

## 客人端 AI 点菜助手 + 服务请求（公开，无需登录）

扫桌码即用，全程匿名（cookie `lc_guest` 隔离多轮，不登录）。知识 = 本店菜品 + 服务（RAG 现取）。
守卫纪律：**菜品名与价格不许编造**（模型必须把引用的菜名包在 `[[...]]`，服务端再核对真值；价格只信库里的值）。

★ **提议—确认边界**：助手只「建议」动作按钮（`add_dish` / `water` / `bell` / `bill`），客人点一下才真正生成服务请求；助手**绝不**代替客人下单。

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/api/public/store/{slug}/assist` | 客人提问一次（语音转文字后走同一接口）。入参 `{question, lang}`。返回 `{reply:{content,actions,source}, lang, store_id}`；`actions[]` 是建议按钮（`key`+`name`+`arg`=菜品 ID） |
| POST | `/api/public/store/{slug}/assist/clear` | 结束当前会话（清流程 + 历史） |
| POST | `/api/public/store/{slug}/requests` | 发起一条服务请求（助手所有动作的落点）。入参 `{kind, dish_id?, qty?, note?, lang?, ref?}`；`kind∈bell\|water\|add_dish\|bill\|other`，`add_dish` 必带 `dish_id` 且必须属本店。返回 `{request_id, message}` |
| GET | `/api/stores/{id}/requests` | 商家看服务请求（见上「商家端」表） |
| POST | `/api/stores/{id}/requests/{rid}/done` | 标记完成 / 取消（见上「商家端」表） |

- 限流：单 IP 30 次 / 分钟（进程内滑动窗口，防脚本打欠费；多实例各算各的）。
- 语音识别在浏览器端做（Web Speech API），转文字后走 `/assist`；服务端不碰音频、不存音频。
- `source` 取值：`llm`（模型模式）/ `rule`（规则模式）/ `flow`（多轮引导）/ `fallback`（答不上，转「按服务铃找店员」）。

## 平台 AI 凭据管理（超管）

LLM 凭据由平台超管统一维护、密文落库（`LC_SECRET` 派生的 AES-256-GCM）。
权限：列表对**租户管理员只读**（脱敏，只看「平台级 + 本租户」）；**写操作仅超管**（新增 / 启停 / 轮换 / 查看明文 / 删除 / 试译）。
响应里的 `can_write` 标识当前账号是否可写（超管 = true）。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/admin/ai-credentials` | 列凭据（脱敏）+ `can_write` |
| POST | `/api/admin/ai-credentials` | 新增 `{name, tenant_id, base_url, model, api_key}`；`tenant_id` 空 = 平台级默认。明文只在本次响应返回 |
| POST | `/api/admin/ai-credentials/{id}/status` | 启停 `{status: active\|disabled}` |
| POST | `/api/admin/ai-credentials/{id}/rotate` | 轮换 Key `{api_key}`（新密文覆盖，旧的立即失效） |
| POST | `/api/admin/ai-credentials/{id}/reveal` | 查看明文（一次性返回 `api_key` + 落审计） |
| DELETE | `/api/admin/ai-credentials/{id}` | 删除凭据 |
| POST | `/api/admin/ai-credentials/{id}/test` | 试译一次，验证地址 / 模型 / Key 是否可用；返回 `{source, model, content, note}` |

## 平台管理（租户 / 账号 / 翻译 Key / 审计）

对齐 langcross 平台的租户模型。所有接口先认证（`withAuth`），角色判定在 handler 内：
未登录 401、等级不够 403，错误码可区分。

**角色**：`super_admin`（平台超管）> `tenant_admin`（租户管理员）> `org_admin` > `manager` > `staff`。
超管天然通过租户管理员门禁；`effectiveTenant` 收口「谁能看谁的数据」——超管可用 `?tenant_id=` 指定租户，租户管理员永远只能作用于自己租户。

### 租户（★ 仅超管）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/admin/tenants` | 租户列表（附带门店数 / 账号数） |
| POST | `/api/admin/tenants` | 新建 `{code, name, expires_at?, perms?}`；code 是 3-32 位小写字母/数字/中划线且不可重复 |
| PATCH | `/api/admin/tenants/{id}` | 改 `{name?, status?（active/disabled/expired）, expires_at?（null=永久）, perms?}` |
| GET | `/api/admin/tenants/{id}/stores` | 该租户名下的门店（超管跨租户只读查看） |

### 账号与角色（超管 / 租户管理员，各自作用域）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/admin/accounts` | 账号列表（超管看全部；租户管理员看本租户） |
| POST | `/api/admin/accounts` | 新建 `{phone, role, display_name?}`；★ 不能创建同级或更高级角色；手机号已存在则就地改角色（`created:false`） |
| PATCH | `/api/admin/accounts/{id}/role` | 改角色；不能改自己、不能授予同级或更高级 |
| PATCH | `/api/admin/accounts/{id}/status` | 停用 / 启用 `{status: active\|disabled}`；不能停用同级或更高级成员 |
| GET | `/api/admin/my-tenant` | 我所属的租户 + 我的角色 |
| GET | `/api/admin/stats` | 平台总数（租户 / 账号 / 门店 / 翻译 Key），★ 仅超管 |

### 翻译 Key（★ 超管专属增删改查，租户一律不接触）

能言开放平台的凭据，密文落库（与 LLM 凭据复用同一把 `LC_SECRET` 派生的 AES-256-GCM）。
**租户管理员连列表都不可见**——Key 是平台成本凭据，对租户没有实际用途（他既不能用也不能改），
索性不看，少一个信息暴露面。翻译链按租户解析 Key 的逻辑在服务端（`TenantPrimary`），不经过本组接口。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/admin/translate-keys` | Key 列表（脱敏 `key_masked`）+ `active_key_id`（当前生效的那把） |
| POST | `/api/admin/translate-keys` | 新增 `{name, key, mode?, daily_limit?}`；明文只在本次响应说明里承诺「此后只能查看明文复制」 |
| POST | `/api/admin/translate-keys/{id}/reveal` | 查看明文（一次性返回 + 落审计） |
| POST | `/api/admin/translate-keys/{id}/rotate` | 轮换 `{key}`（新密文覆盖，旧的立即失效） |
| POST | `/api/admin/translate-keys/{id}/status` | 启停 `{status: active\|disabled}`；停用后翻译链回落，最多 30s 缓存 TTL 生效 |
| POST | `/api/admin/translate-keys/{id}/test` | 试译 `{text, to}`：真翻一句验证可用性，顺带带回余额；失败返回 `{ok:false, error}` |
| DELETE | `/api/admin/translate-keys/{id}` | 删除 |

### 审计

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/admin/audit` | 最近 200 条审计（超管全量；租户管理员后端按租户过滤）。动作含建租户、改角色、维护 Key、查看明文等 |

超管端到端验证：`bash server/scripts/smoke_admin.sh`（需 psql 提权，见脚本头注释）。

## 工具

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/api/ocr/extract` | 菜单识别代理 `{image_base64}`；无服务时返回 `need_manual: true` |
| POST | `/api/events` | 埋点 `{store_id, name, payload}` |

## 埋点事件

`e_register` `e_ocr_done` `e_source_confirmed` `e_translate_done` `e_pay_intent`
`g_scan` `t_rate_submit` `t_coupon_issue` `t_coupon_redeem` `g_assist`（客人用了 AI 点菜助手）

## 自动化测试覆盖

| 脚本 | 覆盖 |
|---|---|
| `server/scripts/smoke.sh` | 28 步、69 条断言。含语种注册表、一次 5 语种翻译、分类名多语化、注册前多语预览、券面本地化、评分契约、跨店核销拒绝、双码 PNG 魔数、客人端 AI 点菜助手（RAG 检索）、服务请求全链路（发起/待办/标记完成）、AI 凭据 RBAC 守卫、翻译 Key RBAC 守卫（普通商家连列表都 403）、菜品图片（上传/公开访问 PNG 魔数/删除 404） |
| `server/scripts/smoke_admin.sh` | 23 条断言。超管正例：psql 提权 → 租户 CRUD、账号管理、翻译 Key（超管专属，脱敏+启停）、AI 凭据（can_write）、经理 vs 超管权限对照 403、审计留痕 |
| `server/scripts/smoke_offline.sh` | 29 条断言。翻译链指向不可达地址 → 确定性验证内置词典兜底与「绝不编造」纪律 |
| `web/scripts/guest-render-check.mjs` | 39 条断言。真开无头浏览器，验证客人端渲染纪律与语言切换 |
