#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────────
# LangCross 商家店端 · 离线兜底冒烟测试（确定性版）
#
# 为什么需要单独一个脚本：
#   smoke.sh 跑的是**在线引擎**（MyMemory / 自建链），译文措辞由外网决定，
#   无法断言「内置词典命中」和「未收录词留空、绝不编造」这两条纪律。
#   本脚本把后端指向一个**不可达**的翻译链，使翻译只剩「内置中→英词典」一层，
#   于是结果完全确定，可以下硬断言。
#
# 前置：另起一个后端实例，把翻译链指到黑洞地址：
#   cd server && go build -o /tmp/lc-api ./cmd/api
#   LC_ADDR=:8099 LC_TRANSLATE_URL=http://127.0.0.1:9 /tmp/lc-api
#   （LC_TRANSLATE_URL 非空 → 主引擎换成 HTTPTranslator；地址不可达 → 必然失败
#     → 触发 FallbackTranslator 的内置词典兜底）
#
# ★ 本脚本会先清空 translation_cache（翻译记忆）。
#   不清的话，上一次在线跑出来的缓存会命中，把离线兜底整条路径「盖住」，
#   测试会假绿。这是本脚本最容易踩的坑。
#
# 用法：BASE=http://localhost:8099 bash scripts/smoke_offline.sh
# 退出码：0 = 全部通过；1 = 存在失败断言或前置不满足
# ─────────────────────────────────────────────────────────────
set -uo pipefail

B="${BASE:-http://localhost:8099}"
PHONE="${PHONE:-13800000001}"
DB="${LC_DATABASE_URL:-postgres://localhost:5432/langcross?sslmode=disable}"

J() { python3 -c "import sys,json;d=json.load(sys.stdin);print($1)" 2>/dev/null; }

PASS=0
FAIL=0
ok()  { PASS=$((PASS + 1)); printf '  \033[32m✓\033[0m %s\n' "$1"; }
bad() { FAIL=$((FAIL + 1)); printf '  \033[31m✗\033[0m %s\n' "$1"; }
eq()  { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1（期望「$2」，实际「$3」）"; fi; }
ne()  { if [ -n "$2" ] && [ "$2" != "None" ] && [ "$2" != "null" ]; then ok "$1"; else bad "$1（为空）"; fi; }
step() { printf '\n\033[36m── %s\033[0m\n' "$1"; }

# ── 前置检查 ──────────────────────────────────────────────────
step "0) 前置：目标后端在线且翻译链确实不可达"
H=$(curl -s -m 5 "$B/api/health" || true)
if [ -z "$H" ]; then
  bad "无法连接 $B"
  echo "     请先启动离线降级后端："
  echo "       cd server && go build -o /tmp/lc-api ./cmd/api"
  echo "       LC_ADDR=:8099 LC_TRANSLATE_URL=http://127.0.0.1:9 /tmp/lc-api"
  exit 1
fi
# 注：$VAR 后面紧跟全角字符（如「）」）时必须写成 ${VAR}，
#     否则 bash 会把全角字符的首字节吞进变量名 → "unbound variable"。
ok "后端在线（${B}）"
ne "返回当前翻译引擎" "$(echo "$H" | J 'd["translate_mode"]')"

if ! command -v psql >/dev/null 2>&1; then
  bad "找不到 psql，无法清空翻译记忆缓存（离线测试会假绿）"
  exit 1
fi

step "1) 清空翻译记忆缓存（否则在线缓存会盖住离线兜底）"
psql "$DB" -v ON_ERROR_STOP=1 -q -X -c "DELETE FROM translation_cache;" >/dev/null || {
  bad "清空 translation_cache 失败"; exit 1; }
CACHED=$(psql "$DB" -tAc "select count(*) from translation_cache" | tr -d '[:space:]')
eq "缓存已清空" "0" "$CACHED"

# ── 词典命中（硬断言：措辞由内置词典唯一决定） ──────────────────
step "2) 内置词典兜底命中（主引擎不可达时仍能出译文）"
P=$(curl -s -X POST "$B/api/public/translate-preview" -H 'Content-Type: application/json' -d '{
  "from":"zh","targets":["en"],
  "items":[{"name":"宫保鸡丁","selected":true},{"name":"麻婆豆腐","selected":true},
           {"name":"红烧肉","selected":true},{"name":"酸辣汤","selected":true},
           {"name":"蛋炒饭","selected":true}]}')
NAMES=$(echo "$P" | J 'json.dumps({i["name"]: (i.get("names") or {}).get("en") for i in d["items"]}, ensure_ascii=False)')
eq "宫保鸡丁 → Kung Pao Chicken"  "Kung Pao Chicken"  "$(echo "$NAMES" | J 'd["宫保鸡丁"]')"
eq "麻婆豆腐 → Mapo Tofu"         "Mapo Tofu"         "$(echo "$NAMES" | J 'd["麻婆豆腐"]')"
eq "红烧肉 → Braised Pork Belly"  "Braised Pork Belly" "$(echo "$NAMES" | J 'd["红烧肉"]')"
eq "酸辣汤 → Hot and Sour Soup"   "Hot and Sour Soup"  "$(echo "$NAMES" | J 'd["酸辣汤"]')"
eq "蛋炒饭 → Egg Fried Rice"      "Egg Fried Rice"     "$(echo "$NAMES" | J 'd["蛋炒饭"]')"
eq "name_en 兼容字段同步"          "True" \
  "$(echo "$P" | J 'all(i.get("name_en") == (i.get("names") or {}).get("en") for i in d["items"])')"

step "3) ★ 绝不编造：词典未收录的词必须留空"
P2=$(curl -s -X POST "$B/api/public/translate-preview" -H 'Content-Type: application/json' -d '{
  "from":"zh","targets":["en"],
  "items":[{"name":"不存在的菜名XYZ","selected":true},{"name":"香煎鲈鱼配时蔬","selected":true}]}')
eq "生造词留空"       "" "$(echo "$P2" | J '(d["items"][0].get("names") or {}).get("en","")')"
eq "未收录真实菜名留空" "" "$(echo "$P2" | J '(d["items"][1].get("names") or {}).get("en","")')"
eq "未命中时 name_en 也不填占位" "True" \
  "$(echo "$P2" | J 'all(not i.get("name_en") for i in d["items"])')"

step "4) ★ 词典只覆盖中→英：其它语种必须留空，不得回填原文"
P3=$(curl -s -X POST "$B/api/public/translate-preview" -H 'Content-Type: application/json' -d '{
  "from":"zh","targets":["ja","ko","fr","es","de","pt","vi","th"],
  "items":[{"name":"宫保鸡丁","selected":true},{"name":"麻婆豆腐","selected":true}]}')
eq "非英语种全空（不拿中文冒充译文）" "True" \
  "$(echo "$P3" | J 'all(not (i.get("names") or {}).get(t) for i in d["items"] for t in ["ja","ko","fr","es","de","pt","vi","th"])')"
eq "语种清单仍完整回显（离线也要能选语言）" "de,es,fr,ja,ko,pt,th,vi" \
  "$(echo "$P3" | J '",".join(sorted(d["targets"]))')"

step "5) 分类名同样走兜底词典"
P4=$(curl -s -X POST "$B/api/public/translate-preview" -H 'Content-Type: application/json' -d '{
  "from":"zh","targets":["en"],
  "items":[{"name":"宫保鸡丁","category":"热菜","selected":true},
           {"name":"蛋炒饭","category":"主食","selected":true},
           {"name":"酸辣汤","category":"汤","selected":true}]}')
eq "热菜 → Hot dishes"  "Hot dishes"  "$(echo "$P4" | J '(d.get("categories") or {}).get("en",{}).get("热菜")')"
eq "主食 → Staples"     "Staples"     "$(echo "$P4" | J '(d.get("categories") or {}).get("en",{}).get("主食")')"
eq "汤 → Soups"         "Soups"       "$(echo "$P4" | J '(d.get("categories") or {}).get("en",{}).get("汤")')"

# ── 端到端：确认稿 → 翻译 → 落库 ────────────────────────────────
step "6) 认证 + 建店"
curl -s -X POST "$B/api/auth/otp" -H 'Content-Type: application/json' -d "{\"phone\":\"$PHONE\"}" >/dev/null
TOKEN=$(curl -s -X POST "$B/api/auth/login" -H 'Content-Type: application/json' \
  -d "{\"phone\":\"$PHONE\",\"code\":\"000000\"}" | J 'd["token"]')
ne "拿到 token" "$TOKEN"
STORE=$(curl -s -X POST "$B/api/stores" -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"name":"离线测试店"}')
SID=$(echo "$STORE" | J 'd["store"]["id"]')
SLUG=$(echo "$STORE" | J 'd["store"]["slug"]')
ne "门店 ID" "$SID"

step "7) 确认稿 → 确认 → 多语化（en 走词典兜底）"
curl -s -X POST "$B/api/stores/$SID/draft" -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{
  "lang":"zh","items":[
    {"name":"宫保鸡丁","price":38,"category":"热菜","selected":true},
    {"name":"麻婆豆腐","price":32,"category":"热菜","selected":true},
    {"name":"红烧肉","price":36,"category":"热菜","selected":true}
  ]}' >/dev/null
curl -s -X POST "$B/api/stores/$SID/draft/confirm" -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{}' >/dev/null
T=$(curl -s -X POST "$B/api/stores/$SID/translate" -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"targets":["en","ja"]}')
ne "翻译调用返回" "$(echo "$T" | J 'd.get("engine")')"

step "8) 译文落库：en 必须精确等于词典值"
D=$(curl -s "$B/api/stores/$SID/dishes" -H "Authorization: Bearer $TOKEN")
eq "宫保鸡丁 en 精确命中词典" "Kung Pao Chicken" \
  "$(echo "$D" | J 'next(((x.get("names") or {}).get("en") for x in d["dishes"] if x["name_zh"]=="宫保鸡丁"), "")')"
eq "红烧肉 en 精确命中词典" "Braised Pork Belly" \
  "$(echo "$D" | J 'next(((x.get("names") or {}).get("en") for x in d["dishes"] if x["name_zh"]=="红烧肉"), "")')"
eq "三道菜 en 全部命中" "True" \
  "$(echo "$D" | J 'all((x.get("names") or {}).get("en") for x in d["dishes"])')"
eq "ja 留空（词典不覆盖，不编造）" "True" \
  "$(echo "$D" | J 'all(not (x.get("names") or {}).get("ja") for x in d["dishes"])')"
eq "name_zh 始终保留为原文真值" "True" \
  "$(echo "$D" | J 'all(x.get("name_zh") for x in d["dishes"])')"

step "9) 离线状态下客人端门店页仍可用"
PUB=$(curl -s "$B/api/public/store/$SLUG?ref=door")
eq "门店名正确" "离线测试店" "$(echo "$PUB" | J 'd["store"]["name"]')"
eq "返回 3 道菜" "3" "$(echo "$PUB" | J 'str(len(d["dishes"]))')"
ne "菜品带上多语种菜名" "$(echo "$PUB" | J 'd["dishes"][0].get("names")')"

step "10) 收尾：清空本次离线测试写入的缓存（避免污染后续在线测试）"
psql "$DB" -v ON_ERROR_STOP=1 -q -X -c "DELETE FROM translation_cache;" >/dev/null 2>&1 \
  && ok "缓存已清理" || bad "缓存清理失败"

# ─────────────────────────────────────────────────────────────
printf '\n\033[1m═══ 离线兜底结果：%d 通过 / %d 失败 ═══\033[0m\n' "$PASS" "$FAIL"
echo "清理本次测试数据：bash scripts/reset_test_data.sh"

[ "$FAIL" -eq 0 ] || exit 1
