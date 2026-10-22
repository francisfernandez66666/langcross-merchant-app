#!/bin/bash
# 超管端到端冒烟：验证平台管理接口在「真超管」身份下的完整行为。
#
# 与 smoke.sh 的分工：
#   - smoke.sh 用普通商家账号跑主链路（它覆盖「非超管访问平台接口被拒」的 RBAC 负例）；
#   - 本脚本跑**正例**：真超管的租户 CRUD、账号管理、翻译 Key（超管专属）、
#     AI 凭据、审计，以及「经理 vs 超管」的权限对照。
#
# ★ 前置条件（脚本自己会做「提权」，但刻意只在开发库上做，见下）：
#   1. 服务已起：make api（即 LC_ADDR=:8080 LC_DEV=1）；
#   2. 数据库可达：psql "postgres://localhost:5432/langcross?sslmode=disable"；
#   3. 脚本会用 dev OTP 建两个测试账号，并把其中一个用 psql 提为 super_admin
#      （UPDATE account SET role='super_admin', tenant_id='' WHERE phone=...）。
#      这只是开发库操作；生产库永远不要跑本脚本。
#
# 用法：bash scripts/smoke_admin.sh   （在仓库根目录执行；BASE 可按需覆盖）
#
# ★ 2026-10-22：默认端口从 8081 改成 8080。
#   8081 是早期手工起服务时用的临时端口，`make api` 用的是 8080。
#   默认端口与实际不符时，二十多条断言会全部变红，
#   报出来的是「平台管理功能全坏了」——真实原因只是连错了端口。
#   现在开头加了 /api/health 前置检查，连不上会一句话说清楚再退出。
set -u
BASE="${BASE:-http://localhost:8080}"
PSQL="${PSQL:-postgres://localhost:5432/langcross?sslmode=disable}"


PASS=0
FAIL=0
eq() {
  if [ "$2" = "$3" ]; then
    PASS=$((PASS + 1)); echo "  ✓ $1"
  else
    FAIL=$((FAIL + 1)); echo "  ✗ $1: 期望[$2] 实际[$3]"
  fi
}
ne() {
  if [ -n "$2" ]; then PASS=$((PASS + 1)); echo "  ✓ $1"; else FAIL=$((FAIL + 1)); echo "  ✗ $1: 期望非空"; fi
}
# 极简 JSON 取值：python3 -c '...' 里用 eval 表达式取字段（与 smoke.sh 的 J 同思路）
jq() { python3 -c "import sys,json;d=json.load(sys.stdin);print(eval(sys.argv[1],{'d':d}))" "$1"; }
# 登录（验证码一次性：先发码再登，LC_DEV 下码恒为 000000）
login() {
  curl --noproxy '*' -s -X POST "$BASE/api/auth/otp" -H 'Content-Type: application/json' \
    -d "{\"phone\":\"$1\"}" > /dev/null
  curl --noproxy '*' -s -X POST "$BASE/api/auth/login" -H 'Content-Type: application/json' \
    -d "{\"phone\":\"$1\",\"code\":\"000000\"}"
}

echo "── 0. 前置检查：目标 API 确实在 $BASE 上 ──"
# ★ 为什么必须有这一步：端口写错时（默认 8081，而开发服务常跑在 8080），
#   后面二十多条断言会全部变红，报出来的是「平台管理功能全坏了」——
#   真实原因只是连错了端口。宁可一行说清楚然后退出，
#   也不要让二十多条假红把排障方向带偏。
if ! curl --noproxy '*' -s --max-time 5 "$BASE/api/health" | grep -q '"ok"'; then
  echo "  ✗ $BASE 上没有可用的 API（/api/health 无响应），终止"
  echo "    先起服务（make api），或用 BASE 指定端口："
  echo "      BASE=http://localhost:8080 bash scripts/smoke_admin.sh"
  exit 1
fi
# ★ ${BASE} 的花括号不能省：bash 下 $BASE 紧跟全角括号时，
#   变量名会被吞进后面那个多字节字符，set -u 直接报 unbound variable。
eq "目标 API 可达（${BASE}）" "ok" "ok"

echo "── 1. 建两个测试账号（dev OTP） ──"
SUP_PHONE=13800001234
MGR_PHONE=13800005678
curl --noproxy '*' -s -X POST "$BASE/api/auth/otp" -H 'Content-Type: application/json' -d "{\"phone\":\"$SUP_PHONE\"}" > /dev/null
login "$SUP_PHONE" > /dev/null # 首次登录即建号（默认 manager）
login "$MGR_PHONE" > /dev/null

echo "── 2. psql 提权：把超管候选账号提升为 super_admin ──"
if psql "$PSQL" -q -c "UPDATE account SET role='super_admin', tenant_id='' WHERE phone='$SUP_PHONE'"; then
  eq "已提权为 super_admin" "ok" "ok"
else
  echo "  ✗ psql 提权失败（数据库不可达？），终止"; exit 1
fi

echo "── 3. 重新登录拿 token（角色不烘焙进 token：载荷仅 {sub,exp}，每请求回库读取，改角色即时生效） ──"
SUP=$(login "$SUP_PHONE" | jq 'd["token"]')
MGR=$(login "$MGR_PHONE" | jq 'd["token"]')
ne "超管 token 非空" "$SUP"
ne "经理 token 非空" "$MGR"
eq "me 回报角色 super_admin" "super_admin" "$(curl --noproxy '*' -s "$BASE/api/me" -H "Authorization: Bearer $SUP" | jq 'd["account"]["role"]')"

echo "── 4. 平台统计 ──"
curl --noproxy '*' -s "$BASE/api/admin/stats" -H "Authorization: Bearer $SUP" > /tmp/lc-admin-stats.json
eq "stats ok" "True" "$(jq 'd.get("ok")==True' < /tmp/lc-admin-stats.json)"
echo "  stats: $(cat /tmp/lc-admin-stats.json | head -c 200)"

echo "── 5. 租户 CRUD ──"
CODE="smoke$(date +%s | tail -c 7)"
TID=$(curl --noproxy '*' -s -X POST "$BASE/api/admin/tenants" -H "Authorization: Bearer $SUP" \
  -H 'Content-Type: application/json' -d "{\"code\":\"$CODE\",\"name\":\"冒烟租户\"}" | jq 'd["tenant"]["id"]')
ne "创建租户返回 id" "$TID"
eq "租户列表可见" "True" "$(curl --noproxy '*' -s "$BASE/api/admin/tenants" -H "Authorization: Bearer $SUP" | jq 'd.get("ok")==True')"
eq "租户改状态 disabled" "disabled" \
  "$(curl --noproxy '*' -s -X PATCH "$BASE/api/admin/tenants/$TID" -H "Authorization: Bearer $SUP" \
     -H 'Content-Type: application/json' -d '{"status":"disabled"}' | jq 'd["tenant"]["status"]')"

echo "── 6. 账号管理（建号 / 改角色 / 停用） ──"
NEW_PHONE=13900007788
NEW_ACC=$(curl --noproxy '*' -s -X POST "$BASE/api/admin/accounts" -H "Authorization: Bearer $SUP" \
  -H 'Content-Type: application/json' -d "{\"phone\":\"$NEW_PHONE\",\"role\":\"manager\",\"display_name\":\"冒烟店员\"}" | jq 'd["account"]["id"]')
ne "新建账号返回 id" "$NEW_ACC"
eq "账号列表可见" "True" "$(curl --noproxy '*' -s "$BASE/api/admin/accounts" -H "Authorization: Bearer $SUP" | jq 'd.get("ok")==True')"
eq "改角色为 staff" "staff" \
  "$(curl --noproxy '*' -s -X PATCH "$BASE/api/admin/accounts/$NEW_ACC/role" -H "Authorization: Bearer $SUP" \
     -H 'Content-Type: application/json' -d '{"role":"staff"}' | jq 'd["account"]["role"]')"
eq "停用账号" "disabled" \
  "$(curl --noproxy '*' -s -X PATCH "$BASE/api/admin/accounts/$NEW_ACC/status" -H "Authorization: Bearer $SUP" \
     -H 'Content-Type: application/json' -d '{"status":"disabled"}' | jq 'd["account"]["status"]')"

echo "── 7. 翻译 Key（★ 超管专属：租户管理员连列表都 403） ──"
KEY_ID=$(curl --noproxy '*' -s -X POST "$BASE/api/admin/translate-keys" -H "Authorization: Bearer $SUP" \
  -H 'Content-Type: application/json' -d '{"name":"冒烟Key","key":"lx-smoke-fake-key-1234567890","mode":"fast"}' | jq 'd["key"]["id"]')
ne "创建翻译 Key 返回 id" "$KEY_ID"
MASKED=$(curl --noproxy '*' -s "$BASE/api/admin/translate-keys" -H "Authorization: Bearer $SUP" | jq 'd["keys"][0]["key_masked"]')
eq "Key 脱敏展示（不含明文）" "True" "$([[ "$MASKED" != *"lx-smoke-fake-key"* ]] && echo True)"
eq "Key 停用 ok" "True" \
  "$(curl --noproxy '*' -s -X POST "$BASE/api/admin/translate-keys/$KEY_ID/status" -H "Authorization: Bearer $SUP" \
     -H 'Content-Type: application/json' -d '{"status":"disabled"}' | jq 'd.get("ok")==True')"

echo "── 8. AI 凭据（LLM） ──"
CRED_ID=$(curl --noproxy '*' -s -X POST "$BASE/api/admin/ai-credentials" -H "Authorization: Bearer $SUP" \
  -H 'Content-Type: application/json' -d '{"name":"冒烟凭据","tenant_id":"","base_url":"https://api.deepseek.com/v1","model":"deepseek-chat","api_key":"sk-smoke-fake-key-1234567890"}' | jq 'd["credential"]["id"]')
ne "创建 AI 凭据返回 id" "$CRED_ID"
eq "凭据列表可见" "True" "$(curl --noproxy '*' -s "$BASE/api/admin/ai-credentials" -H "Authorization: Bearer $SUP" | jq 'd.get("ok")==True')"
eq "超管 can_write=true" "True" "$(curl --noproxy '*' -s "$BASE/api/admin/ai-credentials" -H "Authorization: Bearer $SUP" | jq 'd["can_write"]')"
curl --noproxy '*' -s -X POST "$BASE/api/admin/ai-credentials/$CRED_ID/status" -H "Authorization: Bearer $SUP" \
  -H 'Content-Type: application/json' -d '{"status":"disabled"}' > /dev/null

echo "── 9. RBAC 对照：经理访问平台接口一律 403 ──"
eq "经理 GET stats = 403" "403" "$(curl --noproxy '*' -s -o /dev/null -w '%{http_code}' "$BASE/api/admin/stats" -H "Authorization: Bearer $MGR")"
eq "经理 POST tenants = 403" "403" "$(curl --noproxy '*' -s -o /dev/null -w '%{http_code}' -X POST "$BASE/api/admin/tenants" -H "Authorization: Bearer $MGR" -H 'Content-Type: application/json' -d '{"code":"nope","name":"x"}')"
eq "经理列翻译 Key = 403" "403" "$(curl --noproxy '*' -s -o /dev/null -w '%{http_code}' "$BASE/api/admin/translate-keys" -H "Authorization: Bearer $MGR")"
eq "经理建翻译 Key = 403" "403" "$(curl --noproxy '*' -s -o /dev/null -w '%{http_code}' -X POST "$BASE/api/admin/translate-keys" -H "Authorization: Bearer $MGR" -H 'Content-Type: application/json' -d '{"name":"x","key":"lx-xxxxxxxxxxxxx"}')"

echo "── 10. 审计留痕 ──"
eq "审计可读" "True" "$(curl --noproxy '*' -s "$BASE/api/admin/audit" -H "Authorization: Bearer $SUP" | jq 'd.get("ok")==True')"

echo ""
echo "═══ 超管冒烟：$PASS 通过 / $FAIL 失败 ═══"
[ "$FAIL" -eq 0 ] || exit 1
