# 接口契约（P0a 已实现）

> 基址：`http://localhost:8080`。除「客人端」与「工具」外均需 `Authorization: Bearer <token>`。
> 统一错误响应：`{ "ok": false, "error": { "code": "VALIDATION", "msg": "人话文案" } }`
> 错误码：`UNAUTH` `FORBIDDEN` `NOT_FOUND` `CONFLICT` `VALIDATION` `OFFLINE_ONLY` `RATE_LIMIT` `INTERNAL`

## 健康检查

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/health` | 返回 `ocr_ready` / `translate_ready` / 各自 mode（`live` / `stub-manual` / `dict-stub`） |

## 认证

| 方法 | 路径 | 入参 | 说明 |
|---|---|---|---|
| POST | `/api/auth/otp` | `{phone}` | 发验证码；开发模式回显 `dev_code` |
| POST | `/api/auth/login` | `{phone, code}` | 登录/自动注册，返回 `token` + `account` |
| GET | `/api/me` | — | 当前账号 + 门店列表 |

## 商家端（需登录）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/stores` | 门店列表 |
| POST | `/api/stores` | 建店；★ **注册后自动配好券模板**，返回双码链接 |
| GET | `/api/stores/{id}` | 门店 + 菜单 + 最新确认稿 + 链接 |
| PATCH | `/api/stores/{id}` | 更新门店（含档位 `service_tier`、点单模式 `order_mode`、WiFi、券开关） |
| GET | `/api/stores/{id}/qr?ref=door\|t01` | 门口码 / 桌码 PNG |
| GET | `/api/stores/{id}/draft` | 最新原文确认稿 |
| POST | `/api/stores/{id}/draft` | 保存确认稿 `{lang, items:[{name,price,category,selected}]}` |
| POST | `/api/stores/{id}/draft/confirm` | 确认原文；★ **未确认不允许翻译** |
| POST | `/api/stores/{id}/translate` | 多语化（只翻勾选的菜）→ 写入 `dish` |
| GET | `/api/stores/{id}/dishes` | 菜单 |
| PATCH | `/api/stores/{id}/dishes/{dishId}` | 更新售罄 / 每日剩余 |
| GET | `/api/stores/{id}/ratings` | 评价列表（含中文译文） |
| GET | `/api/stores/{id}/insights` | 经营洞察（★ 只算「用我们工具的外国客数据」） |
| GET | `/api/stores/{id}/coupon-template` | 券模板 |
| GET | `/api/stores/{id}/coupons` | 券列表 |
| POST | `/api/stores/{id}/coupon/redeem` | 核销 `{code}`（校验券归属本店） |

## 客人端（公开，无需登录）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/public/store/{slug}?ref=` | 门店页数据（菜单 + 券开关 + WiFi 有则返回）；顺带记 `g_scan` |
| POST | `/api/public/store/{slug}/rating` | 提交三维度评价 `{ref,score_dish,score_service,score_return,comment,lang}`；★ **评价即领券** |
| GET | `/api/public/coupon/{code}` | 按券码查券 |
| POST | `/api/public/translate-preview` | P4 注册前多语预览（不落库、不需登录） |

## 工具

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/api/ocr/extract` | 菜单识别代理 `{image_base64}`；无服务时返回 `need_manual: true` |
| POST | `/api/events` | 埋点 `{store_id, name, payload}` |

## 埋点事件

`e_register` `e_ocr_done` `e_source_confirmed` `e_translate_done` `e_pay_intent`
`g_scan` `t_rate_submit` `t_coupon_issue` `t_coupon_redeem`
