-- 0006：组织架构（租户 → 组织 → 门店）+ AI 助手凭据
--
-- 设计来源：/Users/zhangzifei/Desktop/翻译助手 的 tenants + orgs + 四级角色模型。
-- 逻辑机制一致，产品对象不同：
--
--	翻译助手（能言 SaaS）          本应用（LangCross 商家店端）
--	─────────────────────────      ─────────────────────────
--	tenant   客户企业               tenant   餐饮集团 / 渠道客户
--	org      组织 → 部门            org      品牌 → 区域（root 组织 = 集团根）
--	user     用户                   account  账号
--	dept_admin 部门管理员           org_admin    品牌 / 区域管理员
--	tenant_admin 租户管理员         tenant_admin 集团管理员
--	super_admin 平台超管            super_admin  平台超管
--
-- 为什么中间要插一层 org：连锁餐饮的「一个集团多个品牌、一个品牌多家店」是主形态，
-- 若只有 tenant → store 两层，品牌管理员（只管自己品牌下的店）就无处安放。

-- ── 1) 组织树 ───────────────────────────────────────────
-- type：root 集团根（每个租户一个）/ brand 品牌 / region 区域（可嵌套）。
-- 品牌与区域共用一张表而不分成两张：二者都是「门店的分组」，分层表达即可。
CREATE TABLE IF NOT EXISTS org (
  id         TEXT PRIMARY KEY,
  tenant_id  TEXT NOT NULL,
  parent_id  TEXT NOT NULL DEFAULT '',   -- 空串 = 挂在本租户 root 下
  name       TEXT NOT NULL,
  type       TEXT NOT NULL DEFAULT 'brand',  -- root / brand / region
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_org_tenant ON org(tenant_id, parent_id);

-- ── 2) 账号与门店挂到组织上 ─────────────────────────────
ALTER TABLE account ADD COLUMN IF NOT EXISTS org_id TEXT NOT NULL DEFAULT '';
ALTER TABLE store   ADD COLUMN IF NOT EXISTS org_id TEXT NOT NULL DEFAULT '';

-- ── 3) AI 助手凭据（超管维护，密文存储） ────────────────
-- 翻译用能言 Key 在 translation_api_key；AI 经营助手用 LLM（OpenAI 兼容协议），
-- 凭据形态不同（base_url + model + key 三元组），故单独一张表，同样密文落库。
CREATE TABLE IF NOT EXISTS ai_credential (
  id         TEXT PRIMARY KEY,
  tenant_id  TEXT NOT NULL DEFAULT '',      -- 空串 = 平台级（默认）
  name       TEXT NOT NULL,
  base_url   TEXT NOT NULL,                 -- 如 https://api.openai.com/v1
  model      TEXT NOT NULL,                 -- 如 gpt-4o-mini / deepseek-chat
  key_enc    BYTEA NOT NULL,                -- AES-256-GCM 密文
  key_masked TEXT NOT NULL,
  status     TEXT NOT NULL DEFAULT 'active', -- active / disabled
  created_by TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_ai_cred_tenant ON ai_credential(tenant_id, status);

-- ── 4) AI 助手会话（多轮流程的在途状态） ─────────────────
-- 为什么不放内存：商家端可能多实例部署，流程状态放内存会导致「上一步在 A 实例、
-- 下一步在 B 实例」时流程丢失。表很轻（一行一会话，定期清理）。
CREATE TABLE IF NOT EXISTS assist_session (
  id         TEXT PRIMARY KEY,
  account_id TEXT NOT NULL,
  store_id   TEXT NOT NULL DEFAULT '',   -- 空串 = 不绑定门店（平台级问答）
  in_flow    TEXT NOT NULL DEFAULT '',   -- 进行中的流程 key
  flow_step  INTEGER NOT NULL DEFAULT 0,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_assist_sess_account ON assist_session(account_id);

-- ── 5) AI 助手对话历史 ──────────────────────────────────
-- 为什么要落库：① 商家刷新 / 换设备后要能看到之前的问答；
-- ② 排障时要能复盘「助手当时基于什么事实答的」（事实快照随消息一起存）。
-- 只存最近若干条（见 repo.AssistHistoryLimit），不追求完整审计。
CREATE TABLE IF NOT EXISTS assist_message (
  id         TEXT PRIMARY KEY,
  account_id TEXT NOT NULL,
  store_id   TEXT NOT NULL DEFAULT '',
  role       TEXT NOT NULL,              -- user / assistant
  content    TEXT NOT NULL,
  source     TEXT NOT NULL DEFAULT '',   -- llm / rule / flow / fallback（仅助手侧）
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_assist_msg_account ON assist_message(account_id, created_at DESC);


-- ── 6) 客人服务请求（服务铃 / 加水 / 加菜） ──────────────
-- 这是「扫桌码的外国客人 → 门店服务员」的唯一通道，也是 AI 点菜助手
-- 所有动作的落点：助手只提议按钮，客人点一下才在这里生成一条请求。
--
-- 为什么单独一张表而不是复用 event 埋点：埋点是「发生过什么」的流水，
-- 服务请求是「待处理的任务」——它有状态（open/done），要被商家轮询、
-- 要能按桌号聚合，两者生命周期完全不同。
CREATE TABLE IF NOT EXISTS guest_request (
  id         TEXT PRIMARY KEY,
  store_id   TEXT NOT NULL,
  table_ref  TEXT NOT NULL DEFAULT '',   -- 桌码 ref（如 t01）；空 = 未关联桌
  kind       TEXT NOT NULL,              -- bell / water / add_dish / bill / other
  payload    JSONB NOT NULL DEFAULT '{}', -- add_dish 时存 {"dish_id":"...","qty":1}
  lang       TEXT NOT NULL DEFAULT '',   -- 客人界面语言（商家端据此展示对应提示）
  note       TEXT NOT NULL DEFAULT '',   -- 客人原话（语音转写后的文本）
  status     TEXT NOT NULL DEFAULT 'open', -- open / done / cancelled
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  done_at    TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_guest_req_store ON guest_request(store_id, status, created_at DESC);
