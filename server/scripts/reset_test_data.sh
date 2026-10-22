#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────────
# LangCross · 测试数据清理
#
# 目的：把冒烟测试（scripts/smoke.sh）与演示播种（scripts/seed_demo.sh）
#       造出来的数据清干净，让下一次跑测试从零开始、断言可复现。
#
# 清理口径（默认）：
#   1. 测试账号（默认手机号名单）及其名下门店；
#   2. 名字命中测试店名名单、或命中测试店名前缀（如 UAT 的 ibv<时间戳>）的门店
#      —— 防止手机号换过但店还在；
#   3. 上述门店的从属数据：source_draft / dish / rating / coupon_template /
#      coupon / coupon_redeem —— 全部走 ON DELETE CASCADE，删 store 即连带清掉；
#   4. 没有外键、必须按 store_id / account_id 单独清的表：
#      event / guest_request / assist_session / assist_message；
#      另加一道孤儿清扫：这些表里指向「已不存在门店」的行（历史清理的漏网）一并清掉；
#   5. 测试租户（code 命中前缀名单）及其组织树、翻译 Key、AI 凭据、审计留痕；
#      翻译 Key 与 AI 凭据额外支持按名字匹配——测试 Key/凭据可能落在默认租户
#      （langcross）名下，删不掉租户，只能按名字认；
#   6. 上述账号的 otp 验证码行。
#   注：translation_cache（翻译记忆）是纯缓存、可随时重建，默认**保留**以加速后续测试；
#       需要一并清空时加 --purge-cache。
#
# ★ 2026-10-19 扩面：原先只认两个手机号 + 三个店名，导致 渲染检查 / 独立 UAT /
#   超管冒烟 造的数据（渲染测试店、ibv<ts>-…、smoke<ts> 租户、平台级 AI 凭据…）
#   长期残留，库里一度堆到 37 家店。现在改为「名单 + 前缀」双匹配，并把租户与
#   凭据一并纳入，删完应是干净的库。
# ★ 2026-10-20 再扩面：① 补 lex 租户前缀（2026-10-16 能言翻译联调遗留的
#   lex105343 等空壳租户）；② 翻译 Key 支持按名字匹配（超管冒烟的 Key 建在
#   默认租户 langcross 名下，删不掉租户，只能按名字认，与凭据同模式）。
#
# 用法：
#   bash server/scripts/reset_test_data.sh                 # 清理测试数据（保留 TM 缓存）
#   bash server/scripts/reset_test_data.sh --purge-cache   # 同时清空翻译记忆
#   bash server/scripts/reset_test_data.sh --all           # 清空全部业务数据（保留表结构与迁移记录）
#   bash server/scripts/reset_test_data.sh --dry-run       # 只打印将要删除的条数，不实际删
#
# 环境变量：
#   LC_DATABASE_URL      PostgreSQL DSN（默认 postgres://localhost:5432/langcross?sslmode=disable）
#   TEST_PHONES          测试手机号，空格分隔
#   TEST_STORE_NAMES     测试店名全称，空格分隔
#   TEST_STORE_PREFIXES  测试店名前缀，空格分隔（LIKE '前缀%'，兜住带时间戳的店名）
#   TEST_TENANT_PREFIXES 测试租户 code 前缀，空格分隔
#   TEST_CRED_NAMES      AI 凭据名（平台级凭据没有租户可依，只能按名字认）
#   TEST_KEY_NAMES       翻译 Key 名（超管冒烟的 Key 落在默认租户名下，只能按名字认）
#
# 退出码：0 = 成功；1 = 参数错误或数据库报错
# ─────────────────────────────────────────────────────────────
set -uo pipefail

DB="${LC_DATABASE_URL:-postgres://localhost:5432/langcross?sslmode=disable}"
# 各测试脚本实际使用的手机号（smoke / smoke_offline / smoke_admin / 独立 UAT / 渲染检查）
TEST_PHONES="${TEST_PHONES:-13800000001 13900000002 13800001234 13800005678 13900007788 13911112222 13800000009 13900000009 13800009999 13700090001 13800003456 13900004455 13900006677}"
TEST_STORE_NAMES="${TEST_STORE_NAMES:-冒烟测试店 离线测试店 老张川菜馆 渲染测试店 渲染测试店缺译文 渲染测试店零译文 图片冒烟店 图片冒烟店2 能言降级店 测试小馆 AI助手联调店}"
# 前缀匹配：独立 UAT 的门店名带时间戳（ibv1792386710-…）；超管冒烟建的租户 code 带时间戳
TEST_STORE_PREFIXES="${TEST_STORE_PREFIXES:-ibv 渲染 冒烟 Q1验证 租户链终验}"
TEST_TENANT_PREFIXES="${TEST_TENANT_PREFIXES:-smoke ib lex}"
TEST_CRED_NAMES="${TEST_CRED_NAMES:-冒烟凭据}"
# 翻译 Key：超管冒烟建的 Key 落在默认租户（langcross）名下，删不掉租户，只能按名字认
TEST_KEY_NAMES="${TEST_KEY_NAMES:-冒烟Key 租户假Key}"

PURGE_CACHE=0
WIPE_ALL=0
DRY=0

while [ $# -gt 0 ]; do
  case "$1" in
    --purge-cache) PURGE_CACHE=1 ;;
    --all)         WIPE_ALL=1 ;;
    --dry-run)     DRY=1 ;;
    -h|--help)     sed -n '2,32p' "$0"; exit 0 ;;
    *)             echo "未知参数：$1（-h 查看用法）" >&2; exit 1 ;;
  esac
  shift
done

# q：单引号包裹 + 内部单引号转义，安全拼进 SQL 字面量
q() { printf "'%s'" "$(printf '%s' "$1" | sed "s/'/''/g")"; }

# 把空格分隔的列表拼成 SQL IN 列表：'a','b'
sql_list() {
  local out="" v
  for v in $1; do
    [ -z "$out" ] && out="$(q "$v")" || out="$out,$(q "$v")"
  done
  printf '%s' "$out"
}

# 把空格分隔的前缀拼成 OR 连接的 LIKE：col LIKE 'a%' OR col LIKE 'b%'
# 空列表时返回 false，保证拼进 WHERE 后不匹配任何行（而不是语法错误）。
# 用途：测试店名 / 租户 code 常带时间戳后缀（ibv1792386710-…、smoke4821），
# 全称匹配兜不住，必须按前缀认。
like_any() {
  local col="$1" out="" v
  for v in $2; do
    [ -z "$out" ] && out="$col LIKE '${v}%'" || out="$out OR $col LIKE '${v}%'"
  done
  [ -z "$out" ] && out="false"
  printf '%s' "$out"
}

PHONES_SQL="$(sql_list "$TEST_PHONES")"
NAMES_SQL="$(sql_list "$TEST_STORE_NAMES")"
CREDS_SQL="$(sql_list "$TEST_CRED_NAMES")"
KEYS_SQL="$(sql_list "$TEST_KEY_NAMES")"
TENANT_COND="$(like_any code "$TEST_TENANT_PREFIXES")"
STORE_PREFIX_COND="$(like_any name "$TEST_STORE_PREFIXES")"

if ! command -v psql >/dev/null 2>&1; then
  echo "找不到 psql，请先安装 PostgreSQL 客户端" >&2
  exit 1
fi

# psql 直连 DSN；-v ON_ERROR_STOP=1 保证 SQL 报错时退出码非 0
PSQL=(psql "$DB" -v ON_ERROR_STOP=1 -q -X)

# ── 计数快照 ──────────────────────────────────────────────────
# snapshot <表名> → 行数（表不存在时输出 0）
snapshot() {
  "${PSQL[@]}" -tAc "select count(*) from $1" 2>/dev/null | tr -d '[:space:]' || echo 0
}

TABLES="account store source_draft dish rating coupon_template coupon coupon_redeem event otp translation_cache guest_request assist_session assist_message tenant org translation_api_key ai_credential admin_audit"

echo "── 数据库：$DB"
echo "── 清理前计数"
BEFORE=""
for t in $TABLES; do
  n="$(snapshot "$t")"
  [ -z "$n" ] && n=0
  BEFORE="$BEFORE $t=$n"
  printf '   %-18s %s\n' "$t" "$n"
done

# ── 组装 SQL ──────────────────────────────────────────────────
if [ "$WIPE_ALL" -eq 1 ]; then
  echo
  echo "── 模式：--all（清空全部业务数据，保留表结构与 schema_migrations）"
  SQL=$(cat <<'EOSQL'
BEGIN;
TRUNCATE TABLE
  coupon_redeem, coupon, coupon_template, rating, dish,
  source_draft, event, otp, translation_cache, store, account
RESTART IDENTITY CASCADE;
COMMIT;
EOSQL
)
else
  echo
  echo "── 模式：清理测试数据"
  echo "   测试手机号：$TEST_PHONES"
  echo "   测试店名  ：$TEST_STORE_NAMES"
  [ "$PURGE_CACHE" -eq 1 ] && echo "   翻译记忆  ：一并清空"

  # 先算出要删的 id 名单，后续所有删除都基于这份名单，避免边删边算。
  # 范围 = 测试手机号 / 测试租户 的账号，以及 名单命中 或 前缀命中 的门店。
  SQL=$(cat <<EOSQL
BEGIN;

CREATE TEMP TABLE _test_tenant ON COMMIT DROP AS
SELECT id FROM tenant WHERE $TENANT_COND;

CREATE TEMP TABLE _test_account ON COMMIT DROP AS
SELECT id FROM account
 WHERE phone IN ($PHONES_SQL)
    OR tenant_id IN (SELECT id FROM _test_tenant);

CREATE TEMP TABLE _test_store ON COMMIT DROP AS
SELECT id FROM store
 WHERE name IN ($NAMES_SQL)
    OR ($STORE_PREFIX_COND)
    OR account_id IN (SELECT id FROM _test_account)
    OR tenant_id IN (SELECT id FROM _test_tenant);

-- 无外键的从属表必须显式清，否则留下指向已删门店/账号的孤儿行
DELETE FROM event          WHERE store_id IN (SELECT id FROM _test_store);
DELETE FROM guest_request  WHERE store_id IN (SELECT id FROM _test_store);
DELETE FROM assist_message WHERE store_id IN (SELECT id FROM _test_store)
                              OR account_id IN (SELECT id FROM _test_account);
DELETE FROM assist_session WHERE store_id IN (SELECT id FROM _test_store)
                              OR account_id IN (SELECT id FROM _test_account);

-- store 删除会级联清掉 source_draft / dish / rating / coupon_template / coupon / coupon_redeem
DELETE FROM store WHERE id IN (SELECT id FROM _test_store);

-- 孤儿清扫：event / guest_request 无外键，历史清理只匹配「当时还存在」的门店，
-- 指向已删门店的行会长期残留（本次实测残留 36 + 8）。凡明确指向不存在门店的行，一律是垃圾。
-- 该条件在预演里同样成立（孤儿行的 store_id 本就不在 store 表里），口径与预演一致。
DELETE FROM event         WHERE store_id IS NOT NULL AND store_id NOT IN (SELECT id FROM store);
DELETE FROM guest_request WHERE store_id IS NOT NULL AND store_id NOT IN (SELECT id FROM store);

DELETE FROM otp WHERE phone IN ($PHONES_SQL);
DELETE FROM account WHERE id IN (SELECT id FROM _test_account);

-- 租户侧（0007 起有外键，顺序不能反）：先清审计 / Key / 凭据 / 组织，最后删租户。
-- org 虽是 CASCADE 也显式清一遍，输出计数才看得出清了多少。
DELETE FROM admin_audit         WHERE tenant_id IN (SELECT id FROM _test_tenant);
-- 翻译 Key：租户命中的跟着租户删；挂在默认租户名下的测试 Key 只能按名字认（同凭据模式）
DELETE FROM translation_api_key WHERE tenant_id IN (SELECT id FROM _test_tenant)
                                   OR name IN ($KEYS_SQL);
-- 平台级凭据（tenant_id=''）没有租户可依，只能按名字认
DELETE FROM ai_credential       WHERE tenant_id IN (SELECT id FROM _test_tenant)
                                   OR name IN ($CREDS_SQL);
DELETE FROM org    WHERE tenant_id IN (SELECT id FROM _test_tenant);
DELETE FROM tenant WHERE id IN (SELECT id FROM _test_tenant);
EOSQL
)

  if [ "$PURGE_CACHE" -eq 1 ]; then
    SQL="$SQL
DELETE FROM translation_cache;
"
  fi

  SQL="$SQL
COMMIT;
"
fi

# 统计将被删除的条数（只读预演，供 dry-run 与常规输出共用）
# 预演与正式删除共用同一套判据（临时表在预演里重建一遍，只读、不改数据）
PREVIEW_TMP=$(cat <<EOSQL
CREATE TEMP TABLE _p_tenant  AS SELECT id FROM tenant WHERE $TENANT_COND;
CREATE TEMP TABLE _p_account AS SELECT id FROM account
  WHERE phone IN ($PHONES_SQL) OR tenant_id IN (SELECT id FROM _p_tenant);
CREATE TEMP TABLE _p_store   AS SELECT id FROM store
  WHERE name IN ($NAMES_SQL) OR ($STORE_PREFIX_COND)
     OR account_id IN (SELECT id FROM _p_account)
     OR tenant_id  IN (SELECT id FROM _p_tenant);
EOSQL
)
PREVIEW=$(cat <<EOSQL
select 'account',          count(*) from account          where id in (select id from _p_account)
union all
select 'store',            count(*) from store            where id in (select id from _p_store)
union all
select 'event',            count(*) from event            where store_id in (select id from _p_store) or (store_id is not null and store_id not in (select id from store))
union all
select 'guest_request',    count(*) from guest_request    where store_id in (select id from _p_store) or (store_id is not null and store_id not in (select id from store))
union all
select 'assist_session',   count(*) from assist_session   where store_id in (select id from _p_store) or account_id in (select id from _p_account)
union all
select 'assist_message',   count(*) from assist_message   where store_id in (select id from _p_store) or account_id in (select id from _p_account)
union all
select 'otp',              count(*) from otp              where phone in ($PHONES_SQL)
union all
select 'tenant',           count(*) from tenant           where id in (select id from _p_tenant)
union all
select 'org',              count(*) from org              where tenant_id in (select id from _p_tenant)
union all
select 'translation_key',  count(*) from translation_api_key where tenant_id in (select id from _p_tenant) or name in ($KEYS_SQL)
union all
select 'ai_credential',    count(*) from ai_credential    where tenant_id in (select id from _p_tenant) or name in ($CREDS_SQL);
EOSQL
)

echo
echo "── 预计影响行数（store 连带 dish/rating/coupon 等）"
if [ "$WIPE_ALL" -eq 1 ]; then
  "${PSQL[@]}" -c "select '全部业务表', '将 TRUNCATE' as note" 2>/dev/null || true
else
  # 临时表必须和查询在同一条 psql 调用里（TEMP 表随会话结束而消失）
  "${PSQL[@]}" -tAF' = ' -c "$PREVIEW_TMP $PREVIEW" 2>/dev/null | sed 's/^/   /' || true
fi

if [ "$DRY" -eq 1 ]; then
  echo
  echo "── --dry-run：未执行任何删除。"
  exit 0
fi

echo
echo "── 执行删除"
if ! printf '%s\n' "$SQL" | "${PSQL[@]}" -f - >/dev/null; then
  echo "清理失败，请检查上面的报错信息。" >&2
  exit 1
fi
echo "   已提交。"

echo
echo "── 清理后计数"
for t in $TABLES; do
  n="$(snapshot "$t")"
  [ -z "$n" ] && n=0
  printf '   %-18s %s\n' "$t" "$n"
done

echo
echo "✓ 测试数据已清理。"
echo "  重新播种演示店：bash server/scripts/seed_demo.sh"
echo "  重新跑冒烟测试：bash server/scripts/smoke.sh"
