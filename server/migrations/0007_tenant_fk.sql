-- 0007：tenant_id 外键约束（P1-1 上半场：先 FK，RLS 后置）
--
-- 背景：租户隔离此前**完全靠应用层 WHERE**——每次查询都要记得带 tenant_id，
-- 漏写一处就是一次跨租户数据泄露，而这类 bug 在代码评审里极难发现。
--
-- 为什么先 FK 后 RLS：
--   - RLS 要改造整个连接层（每连接按请求设置会话变量 app.tenant_id），
--     是跨层改造，且一旦有连接池复用 / 异步任务就可能取错变量；
--   - FK 只需一次迁移，能立刻消灭「无主数据」这一整类问题（租户删了却留下
--     孤儿门店 / 孤儿账号），也是 RLS 的地基：先保证数据是「有主的」，
--     再谈「谁能看」。
--
-- FK 挡不住什么（别对它期望过高）：它只保证**引用有效**，不保证**访问合法**。
-- 「A 租户的人能不能看 B 租户的店」仍然是应用层的职责（requireStore /
-- effectiveTenant 那套），本迁移不改变这条边界。
--
-- ── 三档处理，差异来自「空串是不是合法值」─────────────────
--   1) store / org / translation_api_key：tenant_id 恒为真实租户 → 直接 FK
--      - store             ON DELETE RESTRICT：业务数据，绝不静默删
--      - translation_api_key ON DELETE RESTRICT：成本凭据是资产，先清再删
--      - org               ON DELETE CASCADE：组织树是租户的内部结构，且建租户
--                          时会自动建 root org（repo.EnsureRootOrg）——用 RESTRICT
--                          会让「删租户」被一个自动创建的行卡住
--   2) account / ai_credential：tenant_id 空串 = 平台级（超管账号、平台默认
--      LLM 凭据），是**设计内的合法值**（见 smoke_admin.sh：提权就是
--      `UPDATE account SET tenant_id=''`）。FK 会连空串一起拒绝，故改用
--      BEFORE INSERT/UPDATE 触发器：放行空串、校验非空必须存在。
--   3) admin_audit：审计记录的生命周期必须长于租户——租户删了，「谁在什么时候
--      动过它」不能跟着消失。现存 8 条指向已删租户的孤儿行正是这个语义的产物，
--      因此**刻意不加任何约束**（加上就得先删审计，等于抹掉证据）。
--
-- 幂等：整份可重复执行。FK 用 DROP IF EXISTS + ADD（Postgres 没有
-- ADD CONSTRAINT IF NOT EXISTS）；函数用 CREATE OR REPLACE；触发器用
-- DROP IF EXISTS + CREATE。原因见 db.Migrate：「执行成功」与「记账成功」是两步，
-- 两步之间崩溃会重跑整份迁移。

BEGIN;

-- ── 0) 前置：把「未分配」归入默认租户 ──────────────────────
-- 与 0005 对存量数据的处理保持一致：空串 = 还没归属，不是「指向空租户」。
-- 默认租户常量与 0005 / domain.DefaultTenantID 一致。
UPDATE store             SET tenant_id = '00000000000000000000000000000001' WHERE tenant_id = '';
UPDATE org               SET tenant_id = '00000000000000000000000000000001' WHERE tenant_id = '';
UPDATE translation_api_key SET tenant_id = '00000000000000000000000000000001' WHERE tenant_id = '';

-- ── 1) 孤儿数据：硬失败，不替你搬家 ────────────────────────
-- 指向不存在的租户，本迁移**拒绝自动修复**：把门店改挂到默认租户等于
-- 跨租户搬数据，是这个系统最不能犯的错。宁可让迁移失败、服务起不来
-- （运维立刻可见），也不静默改归属。
DO $$
DECLARE
  n  BIGINT;
  sid TEXT;
BEGIN
  SELECT count(*), coalesce(min(id), '') INTO n, sid
    FROM store x
   WHERE x.tenant_id <> '' AND NOT EXISTS (SELECT 1 FROM tenant t WHERE t.id = x.tenant_id);
  IF n > 0 THEN
    RAISE EXCEPTION '0007 中止：store 有 % 条孤儿行（tenant_id 指向不存在的租户，样例 id=%）。请先人工确认归属再升级——本迁移不会替你搬家。', n, sid;
  END IF;

  SELECT count(*), coalesce(min(id), '') INTO n, sid
    FROM org x
   WHERE x.tenant_id <> '' AND NOT EXISTS (SELECT 1 FROM tenant t WHERE t.id = x.tenant_id);
  IF n > 0 THEN
    RAISE EXCEPTION '0007 中止：org 有 % 条孤儿行（样例 id=%）。请先人工确认归属再升级。', n, sid;
  END IF;

  SELECT count(*), coalesce(min(id), '') INTO n, sid
    FROM translation_api_key x
   WHERE x.tenant_id <> '' AND NOT EXISTS (SELECT 1 FROM tenant t WHERE t.id = x.tenant_id);
  IF n > 0 THEN
    RAISE EXCEPTION '0007 中止：translation_api_key 有 % 条孤儿行（样例 id=%）。请先人工确认归属再升级。', n, sid;
  END IF;
END $$;

-- ── 2) 直接外键 ───────────────────────────────────────────
ALTER TABLE store DROP CONSTRAINT IF EXISTS fk_store_tenant;
ALTER TABLE store ADD CONSTRAINT fk_store_tenant
  FOREIGN KEY (tenant_id) REFERENCES tenant(id) ON DELETE RESTRICT;

ALTER TABLE translation_api_key DROP CONSTRAINT IF EXISTS fk_tak_tenant;
ALTER TABLE translation_api_key ADD CONSTRAINT fk_tak_tenant
  FOREIGN KEY (tenant_id) REFERENCES tenant(id) ON DELETE RESTRICT;

ALTER TABLE org DROP CONSTRAINT IF EXISTS fk_org_tenant;
ALTER TABLE org ADD CONSTRAINT fk_org_tenant
  FOREIGN KEY (tenant_id) REFERENCES tenant(id) ON DELETE CASCADE;

-- ── 3) 平台级空串：触发器校验（FK 做不到的那半）────────────
-- 为什么不用 CHECK + FK：FK 要求值必须存在于被引用表，而平台级记录的
-- tenant_id 语义是「不属于任何租户」，没有任何 tenant 行可以引用它。
CREATE OR REPLACE FUNCTION lc_tenant_ref_allow_platform() RETURNS trigger AS $$
BEGIN
  -- 空串 = 平台级（超管账号 / 平台默认 LLM 凭据），合法，直接放行。
  -- 注意这里不能用 NULL：各列都是 NOT NULL DEFAULT ''，NULL 会破坏
  -- 应用层「空串即平台级」的统一判据（domain.IsPlatformSuperAdmin 等）。
  IF NEW.tenant_id = '' THEN
    RETURN NEW;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM tenant t WHERE t.id = NEW.tenant_id) THEN
    RAISE EXCEPTION 'tenant_id=% 不存在（表 %）：不允许写入无主记录；平台级记录请把 tenant_id 留空串。',
      NEW.tenant_id, TG_TABLE_NAME
      USING ERRCODE = 'foreign_key_violation';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_account_tenant_ref ON account;
CREATE TRIGGER trg_account_tenant_ref
  BEFORE INSERT OR UPDATE OF tenant_id ON account
  FOR EACH ROW EXECUTE FUNCTION lc_tenant_ref_allow_platform();

DROP TRIGGER IF EXISTS trg_ai_credential_tenant_ref ON ai_credential;
CREATE TRIGGER trg_ai_credential_tenant_ref
  BEFORE INSERT OR UPDATE OF tenant_id ON ai_credential
  FOR EACH ROW EXECUTE FUNCTION lc_tenant_ref_allow_platform();

-- ── 4) 删租户的守卫：补齐触发器表的 RESTRICT 语义 ───────────
-- 触发器只管写入方向，管不了「租户被删了、引用它的行还在」。这里在
-- tenant 上补一道 BEFORE DELETE，把 store/org/key 之外的两张表也纳入
-- 「有数据就不能删」——否则删租户会静默留下一批无主账号。
CREATE OR REPLACE FUNCTION lc_tenant_delete_guard() RETURNS trigger AS $$
DECLARE
  n BIGINT;
BEGIN
  SELECT count(*) INTO n FROM account WHERE tenant_id = OLD.id;
  IF n > 0 THEN
    RAISE EXCEPTION '租户 % 仍有 % 个账号：先迁移或删除这些账号再删租户（账号是登录主体，不能静默变无主）。', OLD.id, n
      USING ERRCODE = 'foreign_key_violation';
  END IF;

  SELECT count(*) INTO n FROM ai_credential WHERE tenant_id = OLD.id;
  IF n > 0 THEN
    RAISE EXCEPTION '租户 % 仍有 % 条 AI 凭据：先删除凭据再删租户。', OLD.id, n
      USING ERRCODE = 'foreign_key_violation';
  END IF;

  RETURN OLD;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_tenant_delete_guard ON tenant;
CREATE TRIGGER trg_tenant_delete_guard
  BEFORE DELETE ON tenant
  FOR EACH ROW EXECUTE FUNCTION lc_tenant_delete_guard();

COMMIT;
