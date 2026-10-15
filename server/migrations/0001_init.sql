-- LangCross 商家店端 · P0a 初始化迁移
-- 方言：PostgreSQL 15+
-- 依据：《数据模型与接口契约_全量》§1、《可执行计划_前90天》§2.2
-- 纪律：本期不上 pgvector；结构化字段按「向量友好结构」存，将来上向量不返工。

BEGIN;

CREATE TABLE IF NOT EXISTS schema_migrations (
  version    TEXT PRIMARY KEY,
  applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ── 账号（商家主账号；注册只收手机号） ─────────────────────────────
CREATE TABLE IF NOT EXISTS account (
  id         TEXT PRIMARY KEY,
  phone      TEXT NOT NULL UNIQUE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ── 门店 ────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS store (
  id             TEXT PRIMARY KEY,
  account_id     TEXT NOT NULL REFERENCES account(id) ON DELETE CASCADE,
  name           TEXT NOT NULL,
  addr           TEXT,
  geo_lat        DOUBLE PRECISION,
  geo_lng        DOUBLE PRECISION,
  wifi_ssid      TEXT,                            -- 注册后补填（可选，不设必填）
  wifi_pwd       TEXT,
  business_hours TEXT,
  coupon_enabled BOOLEAN NOT NULL DEFAULT true,   -- 券开关 = 店型标签
  service_tier   TEXT NOT NULL DEFAULT 'pure',    -- pure 纯翻译档 / service 外客服务档
  order_mode     TEXT NOT NULL DEFAULT 'assist',  -- assist 辅助型 / replace 替代型
  slug           TEXT NOT NULL UNIQUE,            -- /s/{slug}（物料定死不可变）
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_store_account ON store(account_id);

-- ── 手机验证码（开发期落库；生产可换 Redis） ────────────────────────
CREATE TABLE IF NOT EXISTS otp (
  phone      TEXT PRIMARY KEY,
  code       TEXT NOT NULL,
  expires_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ── 原文确认稿（唯一真值；未确认不允许翻译） ────────────────────────
CREATE TABLE IF NOT EXISTS source_draft (
  id           TEXT PRIMARY KEY,
  store_id     TEXT NOT NULL REFERENCES store(id) ON DELETE CASCADE,
  lang         TEXT NOT NULL,           -- 原文语种，如 zh
  payload_json JSONB NOT NULL,          -- 菜品清单（原始语言）
  confirmed_at TIMESTAMPTZ,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_draft_store ON source_draft(store_id);

-- ── 菜品（多语化结果） ────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS dish (
  id          TEXT PRIMARY KEY,
  store_id    TEXT NOT NULL REFERENCES store(id) ON DELETE CASCADE,
  name_zh     TEXT NOT NULL,
  name_en     TEXT,
  description TEXT,
  price       NUMERIC(10,2),
  category    TEXT,
  attrs_json  JSONB,                    -- 过敏原 / 辣度 / 宗教
  sort_order  INTEGER NOT NULL DEFAULT 0,
  sold_out    BOOLEAN NOT NULL DEFAULT false,
  remaining   INTEGER,                  -- 每日剩余（唯一动态字段，离线包不显示）
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_dish_store ON dish(store_id);

-- ── 评价（三维度：菜品 / 服务 / 会再来吗） ──────────────────────────
CREATE TABLE IF NOT EXISTS rating (
  id             TEXT PRIMARY KEY,
  store_id       TEXT NOT NULL REFERENCES store(id) ON DELETE CASCADE,
  ref            TEXT,                  -- 入口来源：t07 / door / coupon
  score_dish     SMALLINT NOT NULL CHECK (score_dish    BETWEEN 1 AND 5),
  score_service  SMALLINT NOT NULL CHECK (score_service BETWEEN 1 AND 5),
  score_return   SMALLINT NOT NULL CHECK (score_return  BETWEEN 1 AND 5),
  comment_native TEXT,
  comment_zh     TEXT,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_rating_store ON rating(store_id);

-- ── 券模板（注册后自动配好；发券免费） ──────────────────────────────
CREATE TABLE IF NOT EXISTS coupon_template (
  id           TEXT PRIMARY KEY,
  store_id     TEXT NOT NULL REFERENCES store(id) ON DELETE CASCADE,
  content_json JSONB NOT NULL,          -- 券内容（多语）
  valid_days   INTEGER NOT NULL DEFAULT 14,
  enabled      BOOLEAN NOT NULL DEFAULT true,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_coupon_tpl_store ON coupon_template(store_id);

-- ── 券实例（截图保存，到店核销） ──────────────────────────────────
CREATE TABLE IF NOT EXISTS coupon (
  id          TEXT PRIMARY KEY,
  template_id TEXT NOT NULL REFERENCES coupon_template(id) ON DELETE CASCADE,
  store_id    TEXT NOT NULL REFERENCES store(id) ON DELETE CASCADE,
  code        TEXT NOT NULL UNIQUE,
  lang        TEXT,
  issued_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  expire_at   TIMESTAMPTZ NOT NULL,
  status      TEXT NOT NULL DEFAULT 'issued'   -- issued / redeemed / expired
);
CREATE INDEX IF NOT EXISTS idx_coupon_store ON coupon(store_id);

-- ── 券核销 ──────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS coupon_redeem (
  id          TEXT PRIMARY KEY,
  coupon_id   TEXT NOT NULL REFERENCES coupon(id) ON DELETE CASCADE,
  redeemed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  redeemed_by TEXT,
  online      BOOLEAN NOT NULL DEFAULT true
);

-- ── 埋点事件（高写入，不阻塞主链路） ────────────────────────────────
CREATE TABLE IF NOT EXISTS event (
  id         BIGSERIAL PRIMARY KEY,
  store_id   TEXT,
  name       TEXT NOT NULL,
  payload    JSONB,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_event_name ON event(name);

COMMIT;
