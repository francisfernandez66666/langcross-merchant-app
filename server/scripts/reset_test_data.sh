#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────────
# LangCross · 测试数据清理
#
# 目的：把冒烟测试（scripts/smoke.sh）与演示播种（scripts/seed_demo.sh）
#       造出来的数据清干净，让下一次跑测试从零开始、断言可复现。
#
# 清理口径（默认）：
#   1. 测试账号（默认手机号 13800000001 / 13900000002）及其名下门店；
#   2. 名字命中「冒烟测试店 / 老张川菜馆」的门店（防止手机号换过但店还在）；
#   3. 上述门店的从属数据：source_draft / dish / rating / coupon_template /
#      coupon / coupon_redeem —— 全部走 ON DELETE CASCADE，删 store 即连带清掉；
#   4. event 表没有外键，需要按 store_id 单独清；
#   5. 上述账号的 otp 验证码行。
#   注：translation_cache（翻译记忆）是纯缓存、可随时重建，默认**保留**以加速后续测试；
#       需要一并清空时加 --purge-cache。
#
# 用法：
#   bash scripts/reset_test_data.sh                 # 清理测试数据（保留 TM 缓存）
#   bash scripts/reset_test_data.sh --purge-cache   # 同时清空翻译记忆
#   bash scripts/reset_test_data.sh --all           # 清空全部业务数据（保留表结构与迁移记录）
#   bash scripts/reset_test_data.sh --dry-run       # 只打印将要删除的条数，不实际删
#
# 环境变量：
#   LC_DATABASE_URL  PostgreSQL DSN（默认 postgres://localhost:5432/langcross?sslmode=disable）
#   TEST_PHONES      测试手机号列表，空格分隔（默认 "13800000001 13900000002"）
#   TEST_STORE_NAMES 测试店名列表，空格分隔（默认 "冒烟测试店 离线测试店 老张川菜馆"）
#
# 退出码：0 = 成功；1 = 参数错误或数据库报错
# ─────────────────────────────────────────────────────────────
set -uo pipefail

DB="${LC_DATABASE_URL:-postgres://localhost:5432/langcross?sslmode=disable}"
TEST_PHONES="${TEST_PHONES:-13800000001 13900000002}"
TEST_STORE_NAMES="${TEST_STORE_NAMES:-冒烟测试店 离线测试店 老张川菜馆}"

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

PHONES_SQL="$(sql_list "$TEST_PHONES")"
NAMES_SQL="$(sql_list "$TEST_STORE_NAMES")"

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

TABLES="account store source_draft dish rating coupon_template coupon coupon_redeem event otp translation_cache"

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

  # 先算出要删的 store id，后续所有删除都基于这份名单，避免边删边算
  SQL=$(cat <<EOSQL
BEGIN;

CREATE TEMP TABLE _test_store ON COMMIT DROP AS
SELECT s.id
  FROM store s
  LEFT JOIN account a ON a.id = s.account_id
 WHERE a.phone IN ($PHONES_SQL)
    OR s.name IN ($NAMES_SQL);

CREATE TEMP TABLE _test_account ON COMMIT DROP AS
SELECT id FROM account WHERE phone IN ($PHONES_SQL);

-- event 无外键，必须显式清（否则会留下指向已删门店的孤儿行）
DELETE FROM event WHERE store_id IN (SELECT id FROM _test_store);

-- store 删除会级联清掉 source_draft / dish / rating / coupon_template / coupon / coupon_redeem
DELETE FROM store WHERE id IN (SELECT id FROM _test_store);

DELETE FROM otp WHERE phone IN ($PHONES_SQL);
DELETE FROM account WHERE id IN (SELECT id FROM _test_account);
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
PREVIEW=$(cat <<EOSQL
select 'account',         count(*) from account         where phone in ($PHONES_SQL)
union all
select 'store',           count(*) from store           where id in (
         select s.id from store s left join account a on a.id=s.account_id
          where a.phone in ($PHONES_SQL) or s.name in ($NAMES_SQL))
union all
select 'event',           count(*) from event           where store_id in (
         select s.id from store s left join account a on a.id=s.account_id
          where a.phone in ($PHONES_SQL) or s.name in ($NAMES_SQL))
union all
select 'otp',             count(*) from otp             where phone in ($PHONES_SQL);
EOSQL
)

echo
echo "── 预计影响行数（store 连带 dish/rating/coupon 等）"
if [ "$WIPE_ALL" -eq 1 ]; then
  "${PSQL[@]}" -c "select '全部业务表', '将 TRUNCATE' as note" 2>/dev/null || true
else
  "${PSQL[@]}" -tAF' = ' -c "$PREVIEW" 2>/dev/null | sed 's/^/   /' || true
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
echo "  重新播种演示店：bash scripts/seed_demo.sh"
echo "  重新跑冒烟测试：bash scripts/smoke.sh"
