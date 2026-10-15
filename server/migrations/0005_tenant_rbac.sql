-- 0005：租户隔离 + 四级角色 + 翻译凭据表 + 管理审计
--
-- 背景（对齐 langcross 平台的租户化方向）：
--   本应用将来作为能言平台上的一个租户运行，因此本机必须先具备 tenant 概念，
--   否则平台侧一旦按租户切分，这里的数据归属就要推倒重来。
-- 设计参考 /Users/zhangzifei/Desktop/翻译助手 的 tenants/users 模型，
--   按商家店端的体量裁剪为「租户 → 账号 → 门店」三级，去掉组织树与部门概念。
--
-- 角色体系（四级，等级 4 > 3 > 2 > 1）：
--   super_admin  平台超管：租户 CRUD、跨租户只读、调整任意账号角色
--   tenant_admin 租户管理员：本租户账号管理、维护本租户翻译 API Key、看本租户全部门店
--   manager      门店管理员：建店、菜单、翻译、券（只管自己的店）
--   staff        店员（默认）：看菜单、核销券、看评价

-- ── 1) 租户 ────────────────────────────────────────────────
-- perms 放配额与开关（如允许的语种、日字符上限），本期只建字段不启用具体策略，
-- 先把「租户是一条可配置记录」的事实固定下来。
CREATE TABLE IF NOT EXISTS tenant (
  id         TEXT PRIMARY KEY,
  code       TEXT NOT NULL UNIQUE,             -- 租户编码（对外短标识，如 langcross）
  name       TEXT NOT NULL,                    -- 租户显示名
  status     TEXT NOT NULL DEFAULT 'active',   -- active / disabled / expired
  expires_at TIMESTAMPTZ,                      -- 到期时间；NULL = 永不过期
  perms      JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 平台默认租户：存量门店 / 账号全部归属它。
-- ⚠️ 这里的常量与 Go 侧 domain.DefaultTenantID 必须一致（本仓库用测试钉死，见 repo 层测试）。
INSERT INTO tenant(id, code, name) VALUES ('00000000000000000000000000000001', 'langcross', 'LangCross 平台租户')
ON CONFLICT (id) DO NOTHING;

-- ── 2) 账号：租户归属 + 角色 + 状态 ─────────────────────────
ALTER TABLE account ADD COLUMN IF NOT EXISTS tenant_id    TEXT NOT NULL DEFAULT '';
ALTER TABLE account ADD COLUMN IF NOT EXISTS role         TEXT NOT NULL DEFAULT 'staff';
ALTER TABLE account ADD COLUMN IF NOT EXISTS status       TEXT NOT NULL DEFAULT 'active'; -- active / disabled
ALTER TABLE account ADD COLUMN IF NOT EXISTS display_name TEXT NOT NULL DEFAULT '';

-- 存量账号归入默认租户；超管的 tenant_id 为空串（平台级账号），由后台手工设置。
UPDATE account SET tenant_id = '00000000000000000000000000000001' WHERE tenant_id = '';

-- 存量账号里「开了店的」本质就是经营者，直接给租户管理员，避免升级后反而不能维护自己的店。
UPDATE account SET role = 'tenant_admin'
 WHERE role = 'staff' AND id IN (SELECT DISTINCT account_id FROM store);

-- 同租户内手机号唯一。
-- ★ 刻意保留 account.phone 的全局 UNIQUE（0001 建的约束，不在此删除）：
--   登录态只靠手机号 + 验证码，没有租户选择器，若允许跨租户同号，
--   登录时就无法判断该进哪个租户。真正的跨租户同号要等平台侧 SSO 接入后再做。
CREATE INDEX IF NOT EXISTS idx_account_tenant_phone ON account(tenant_id, phone);
CREATE INDEX IF NOT EXISTS idx_account_role ON account(role);

-- ── 3) 门店：归属租户 ──────────────────────────────────────
-- 菜品 / 评价 / 券都以 store_id 归属，门店带上 tenant_id 即等于全链路带上租户边界。
ALTER TABLE store ADD COLUMN IF NOT EXISTS tenant_id TEXT NOT NULL DEFAULT '';
UPDATE store SET tenant_id = COALESCE(
  (SELECT a.tenant_id FROM account a WHERE a.id = store.account_id),
  '00000000000000000000000000000001'
) WHERE tenant_id = '';
CREATE INDEX IF NOT EXISTS idx_store_tenant ON store(tenant_id);

-- ── 4) 翻译 API Key（租户级凭据） ──────────────────────────
-- ★ 密文存储：key_enc 是 AES-256-GCM 密文（nonce‖密文‖tag），
--   密钥由 LC_SECRET 派生（见 internal/crypt）。绝不落明文，
--   否则一次数据库备份泄露就等于把所有租户的能言 Key 全部送出去。
-- 一个租户可有多把 Key（主 / 备），按需启停；翻译时取第一把 active 的。
CREATE TABLE IF NOT EXISTS translation_api_key (
  id           TEXT PRIMARY KEY,
  tenant_id    TEXT NOT NULL,
  name         TEXT NOT NULL,                    -- Key 别名（如「生产 Key 2026-10」）
  provider     TEXT NOT NULL DEFAULT 'lexicorn', -- 提供方：目前只有能言开放平台
  key_enc      BYTEA NOT NULL,                   -- 密文
  key_masked   TEXT NOT NULL,                    -- 脱敏展示（前后各留 4 位）
  mode         TEXT NOT NULL DEFAULT 'pro',      -- fast / pro
  status       TEXT NOT NULL DEFAULT 'active',   -- active / disabled
  daily_limit  INTEGER NOT NULL DEFAULT 0,       -- 当日调用上限；0 = 不限
  last_used_at TIMESTAMPTZ,
  created_by   TEXT NOT NULL DEFAULT '',         -- 创建者账号 ID
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_tak_tenant ON translation_api_key(tenant_id, status);

-- ── 5) 管理审计 ────────────────────────────────────────────
-- 谁能看 Key 明文、谁改了角色、谁建了租户，都必须留痕：
-- 这是「超管可以动一切」的唯一制衡手段。
CREATE TABLE IF NOT EXISTS admin_audit (
  id         BIGSERIAL PRIMARY KEY,
  tenant_id  TEXT NOT NULL DEFAULT '',   -- 被操作对象所属租户
  actor_id   TEXT NOT NULL DEFAULT '',   -- 操作者账号 ID
  actor_role TEXT NOT NULL DEFAULT '',   -- 操作者当时角色（角色后来被改也不影响历史事实）
  action     TEXT NOT NULL,              -- 如 tenant_create / key_reveal / role_change
  target     TEXT NOT NULL DEFAULT '',   -- 对象 ID
  detail     TEXT NOT NULL DEFAULT '',   -- 人类可读摘要（绝不放明文 Key）
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_audit_created ON admin_audit(created_at DESC);
