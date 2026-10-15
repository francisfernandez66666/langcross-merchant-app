#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────────
# LangCross 商家店端 · 端到端冒烟测试（在线引擎版）
#
# 覆盖范围（含 2026-10-15 多语言改造）：
#   健康检查 → 语种注册表 → 认证 → 建店（自动配券+双码）→ 原文确认稿
#   → 未确认拒绝翻译 → 确认 → 一次多语种翻译 → 分类名多语化
#   → 注册前多语预览 → 客人端门店页 → 评价领券（含券面本地化）
#   → 评分契约 → 核销 → 重复核销拒绝 → 跨店核销拒绝 → 经营洞察 → 双码 PNG
#
# 依赖外网的项一律用「软断言」（soft/softb）：失败只告警，不计入失败。
# 需要**确定性**验证离线内置词典兜底与「绝不编造」纪律时，请跑：
#   bash scripts/smoke_offline.sh
#
# 用法：BASE=http://localhost:8080 bash scripts/smoke.sh
# 退出码：0 = 全部通过；1 = 存在失败断言
# ─────────────────────────────────────────────────────────────
set -uo pipefail

B="${BASE:-http://localhost:8080}"
PHONE="${PHONE:-13800000001}"

# J 从 stdin 解析 JSON 并求值一段 Python 表达式，方便取字段
J() { python3 -c "import sys,json;d=json.load(sys.stdin);print($1)" 2>/dev/null; }

PASS=0
FAIL=0
ok()  { PASS=$((PASS + 1)); printf '  \033[32m✓\033[0m %s\n' "$1"; }
bad() { FAIL=$((FAIL + 1)); printf '  \033[31m✗\033[0m %s\n' "$1"; }
# 断言相等
eq() { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1（期望「$2」，实际「$3」）"; fi; }
# 断言非空
ne() { if [ -n "$2" ] && [ "$2" != "None" ] && [ "$2" != "null" ]; then ok "$1"; else bad "$1（为空）"; fi; }
# 断言包含子串
has() { case "$2" in *"$3"*) ok "$1" ;; *) bad "$1（「$2」不含「$3」）" ;; esac; }
# 软断言：失败只告警，不计入失败（用于依赖外网的项）
soft() { if [ -n "$2" ] && [ "$2" != "None" ]; then ok "$1"; else printf '  \033[33m!\033[0m %s（外网依赖，跳过）\n' "$1"; fi; }
# 软断言（布尔版）：值为 True 才算通过
softb() { case "$2" in True|true) ok "$1" ;; *) printf '  \033[33m!\033[0m %s（外网依赖，跳过）\n' "$1" ;; esac; }
# 软断言（计数版）：值为 >0 的整数才算通过
softn() { case "$2" in ''|0|None|null) printf '  \033[33m!\033[0m %s（外网依赖，跳过）\n' "$1" ;; *) ok "$1（$2 条）" ;; esac; }

step() { printf '\n\033[36m── %s\033[0m\n' "$1"; }

step "1) 健康检查"
H=$(curl -s "$B/api/health")
eq "服务在线" "True" "$(echo "$H" | J 'd["ok"]')"
echo "     OCR=$(echo "$H" | J 'd["ocr_mode"]')  翻译=$(echo "$H" | J 'd["translate_mode"]')"

step "2) 语种注册表（前端语种选择器的唯一真值）"
L=$(curl -s "$B/api/translation/langs")
eq "原文语种 = zh" "zh" "$(echo "$L" | J 'd["source"]')"
eq "语种数量 = 10" "10" "$(echo "$L" | J 'str(len(d["langs"]))')"
eq "语种清单完整" \
  "de,en,es,fr,ja,ko,pt,th,vi,zh" \
  "$(echo "$L" | J '",".join(sorted(l["code"] for l in d["langs"]))')"
eq "每项都有母语写法与中文名" "True" \
  "$(echo "$L" | J 'all(l.get("native") and l.get("name_zh") and l.get("tier") in (1,2,3) for l in d["langs"])')"
ne "返回当前翻译引擎名" "$(echo "$L" | J 'd["engine"]')"

step "3) 发送验证码"
eq "验证码已下发" "True" "$(curl -s -X POST "$B/api/auth/otp" -H 'Content-Type: application/json' \
  -d "{\"phone\":\"$PHONE\"}" | J 'd["ok"]')"

step "4) 登录（手机号 + 验证码）"
TOKEN=$(curl -s -X POST "$B/api/auth/login" -H 'Content-Type: application/json' \
  -d "{\"phone\":\"$PHONE\",\"code\":\"000000\"}" | J 'd["token"]')
ne "拿到 token" "$TOKEN"

step "5) 创建门店（注册后自动配券 + 生成双码）"
STORE=$(curl -s -X POST "$B/api/stores" -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"name":"冒烟测试店"}')
SID=$(echo "$STORE" | J 'd["store"]["id"]')
SLUG=$(echo "$STORE" | J 'd["store"]["slug"]')
ne "门店 ID" "$SID"
ne "门店 slug" "$SLUG"
eq "自动配好复购券模板" "凭券到店赠小食一份" "$(echo "$STORE" | J 'd["coupon_template"]["content"]["zh"]')"
ne "门口码链接" "$(echo "$STORE" | J 'd["links"]["door"]')"
ne "桌码链接" "$(echo "$STORE" | J 'd["links"]["table"]')"

step "6) 保存原文确认稿（P2 上传）"
# ★ 故意只用内置词典收录的菜：这样即使完全离线，en 译文也能断言成功，测试与网络解耦
DRAFT=$(curl -s -X POST "$B/api/stores/$SID/draft" -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{
  "lang":"zh","items":[
    {"name":"宫保鸡丁","price":38,"category":"热菜","selected":true},
    {"name":"麻婆豆腐","price":32,"category":"热菜","selected":true},
    {"name":"水煮鱼","price":68,"category":"热菜","selected":false},
    {"name":"蛋炒饭","price":18,"category":"主食","selected":true},
    {"name":"酸辣汤","price":22,"category":"汤","selected":true}
  ]}')
eq "确认稿含 5 道菜" "5" "$(echo "$DRAFT" | J 'str(len(d["draft"]["items"]))')"

step "7) 未确认就翻译（必须被拒）"
eq "拒绝码 = VALIDATION" "VALIDATION" \
  "$(curl -s -X POST "$B/api/stores/$SID/translate" -H "Authorization: Bearer $TOKEN" \
     -H 'Content-Type: application/json' -d '{}' | J 'd["error"]["code"]')"

step "8) 确认原文（P3，一次确认即生效）"
eq "确认成功" "True" \
  "$(curl -s -X POST "$B/api/stores/$SID/draft/confirm" -H "Authorization: Bearer $TOKEN" \
     -H 'Content-Type: application/json' -d '{}' | J 'd["ok"]')"

step "9) 多语化（P4，只翻勾选的菜；一次 5 个语种）"
T=$(curl -s -X POST "$B/api/stores/$SID/translate" -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"targets":["en","ja","ko","fr","th"]}')
eq "回显 targets" "en,ja,ko,fr,th" "$(echo "$T" | J '",".join(d["targets"])')"
eq "只翻勾选的 4 道（水煮鱼未勾选）" "4" "$(echo "$T" | J 'str(len(d["dishes"]))')"
echo "     译文条数=$(echo "$T" | J 'd["translated_count"]')  引擎=$(echo "$T" | J 'd["engine"]')"

step "10) 译文落库校验"
DISHES=$(curl -s "$B/api/stores/$SID/dishes" -H "Authorization: Bearer $TOKEN")
# 硬断言：译文必须存在，且不能等于中文原文（等于原文 = 根本没翻）
eq "4 道菜都有 en 译文" "True" \
  "$(echo "$DISHES" | J 'all((d.get("names") or {}).get("en") for d in d["dishes"])')"
eq "en 译文不等于中文原文" "True" \
  "$(echo "$DISHES" | J 'all((d.get("names") or {}).get("en","") != d.get("name_zh") for d in d["dishes"])')"
# 措辞随引擎而变（MyMemory 给 "Kung Pao chicken"，内置词典给 "Kung Pao Chicken"），
# 故此处只做大小写无关的软断言；精确措辞的硬断言放在 smoke_offline.sh（引擎确定性）
soft "en 命中「宫保鸡丁」常见译名" \
  "$(echo "$DISHES" | J 'next(((d.get("names") or {}).get("en","") for d in d["dishes"] if d.get("name_zh")=="宫保鸡丁" and "kung pao" in ((d.get("names") or {}).get("en","") or "").lower()), "")')"
softn "非英语种已产出译文" \
  "$(echo "$DISHES" | J 'sum(1 for d in d["dishes"] if (d.get("names") or {}).get("ja"))')"
eq "name_zh 始终保留为原文真值" "True" \
  "$(echo "$DISHES" | J 'all(d.get("name_zh") for d in d["dishes"])')"

step "11) 分类名多语化（消除「外语菜名 + 中文标题」割裂）"
CATS=$(curl -s "$B/api/stores/$SID" -H "Authorization: Bearer $TOKEN" | J 'd["store"].get("category_names")')
ne "store.category_names 已写入" "$CATS"
eq "en 覆盖全部分类（热菜/主食/汤）" "True" \
  "$(curl -s "$B/api/stores/$SID" -H "Authorization: Bearer $TOKEN" \
     | J 'set((d["store"].get("category_names") or {}).get("en",{}).keys())=={"热菜","主食","汤"}')"

step "12) 注册前多语预览（不落库、不需要登录）"
P=$(curl -s -X POST "$B/api/public/translate-preview" -H 'Content-Type: application/json' -d '{
  "from":"zh","targets":["en","ja","ko"],
  "items":[{"name":"宫保鸡丁","price":38,"category":"热菜","selected":true},
           {"name":"米饭","price":3,"category":"主食","selected":true}]}')
eq "预览回显 targets" "en,ja,ko" "$(echo "$P" | J '",".join(d["targets"])')"
eq "菜名已多语化（en 非空且非原文）" "True" \
  "$(echo "$P" | J 'all((i.get("names") or {}).get("en") and i["names"]["en"] != i["name"] for i in d["items"])')"
ne "分类名随预览返回" "$(echo "$P" | J 'd["categories"].get("en")')"
eq "name_en 兼容字段保留（等于 names.en）" "True" \
  "$(echo "$P" | J 'all(i.get("name_en") == (i.get("names") or {}).get("en") for i in d["items"])')"
soft "en 命中「宫保鸡丁」常见译名" \
  "$(echo "$P" | J 'next(((i.get("names") or {}).get("en","") for i in d["items"] if i["name"]=="宫保鸡丁" and "kung pao" in ((i.get("names") or {}).get("en","") or "").lower()), "")')"
# 注：「未收录词留空、绝不编造」是**离线内置词典**的纪律；在线引擎（MyMemory/自建链）
# 对任意文本都会给出机器译文。该纪律的确定性硬断言在 smoke_offline.sh（引擎不可达）。

step "13) 客人端门店页（公开，无需登录）"
PUB=$(curl -s "$B/api/public/store/$SLUG?ref=t01")
eq "门店名正确" "冒烟测试店" "$(echo "$PUB" | J 'd["store"]["name"]')"
eq "返回 4 道菜" "4" "$(echo "$PUB" | J 'str(len(d["dishes"]))')"
eq "发券开关打开" "True" "$(echo "$PUB" | J 'str(d["store"]["coupon_enabled"])')"
ne "门店页带上分类多语种表" "$(echo "$PUB" | J 'd["store"].get("category_names")')"
ne "菜品带上多语种菜名" "$(echo "$PUB" | J 'd["dishes"][0].get("names")')"
# ★ 兜底不依赖 WiFi：未填 WiFi 时字段不应出现
eq "未填 WiFi 则不返回该字段" "True" "$(echo "$PUB" | J '"wifi_ssid" not in d["store"]')"

step "14) 客人提交三维度评价 → 自动发券（发券免费）"
RATED=$(curl -s -X POST "$B/api/public/store/$SLUG/rating" -H 'Content-Type: application/json' -d '{
  "ref":"t01","score_dish":5,"score_service":4,"score_return":5,
  "comment":"The Kung Pao Chicken was amazing!","lang":"en"}')
CODE=$(echo "$RATED" | J 'd["coupon"]["code"]')
ne "已发券" "$CODE"
eq "券状态 = issued" "issued" "$(echo "$RATED" | J 'd["coupon"]["status"]')"
eq "评价维度落库" "5" "$(echo "$RATED" | J 'str(d["rating"]["score_dish"])')"
ne "客人母语评论已译成中文（供商家阅读）" "$(echo "$RATED" | J 'd["rating"].get("comment_zh")')"

step "15) 券面文案本地化（模板只有 zh/en，其它语种走翻译链补齐）"
RJ=$(curl -s -X POST "$B/api/public/store/$SLUG/rating" -H 'Content-Type: application/json' \
  -d '{"ref":"t01","score_dish":5,"score_service":5,"score_return":5,"lang":"ja"}')
eq "响应含 coupon_gift 字段" "True" "$(echo "$RJ" | J '"coupon_gift" in d')"
soft "ja 券面已本地化（非英文原文）" "$(echo "$RJ" | J 'd.get("coupon_gift") or ""')"

# 评分契约（有意的不对称）：
#   >5  → 收敛到 5：防御客户端 bug，不因越界丢掉客人评价
#   <=0 → 视为「没打分」，直接 400 拒绝：不伪造 1 星
eq "越界评分 >5 收敛为 5" "5" \
  "$(curl -s -X POST "$B/api/public/store/$SLUG/rating" -H 'Content-Type: application/json' \
     -d '{"ref":"t01","score_dish":99,"score_service":4,"score_return":3,"lang":"en"}' \
     | J 'str(d["rating"]["score_dish"])')"
eq "未打分(0) 被拒（不伪造分数）" "VALIDATION" \
  "$(curl -s -X POST "$B/api/public/store/$SLUG/rating" -H 'Content-Type: application/json' \
     -d '{"ref":"t01","score_dish":5,"score_service":0,"score_return":3,"lang":"en"}' \
     | J 'd["error"]["code"]')"

step "16) 店员核销"
eq "核销成功" "redeemed" \
  "$(curl -s -X POST "$B/api/stores/$SID/coupon/redeem" -H "Authorization: Bearer $TOKEN" \
     -H 'Content-Type: application/json' -d "{\"code\":\"$CODE\"}" | J 'd["coupon"]["status"]')"

step "17) 重复核销（必须被拒）"
eq "重复核销被拒" "True" \
  "$(curl -s -X POST "$B/api/stores/$SID/coupon/redeem" -H "Authorization: Bearer $TOKEN" \
     -H 'Content-Type: application/json' -d "{\"code\":\"$CODE\"}" | J '"error" in d')"

step "18) 跨店核销（必须被拒：券只属于本店）"
eq "跨店核销被拒" "True" \
  "$(curl -s -X POST "$B/api/stores/00000000000000000000000000000000/coupon/redeem" \
     -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
     -d "{\"code\":\"$CODE\"}" | J '"error" in d')"

step "19) 经营洞察（口径：只算用我们工具的外国客数据）"
INS=$(curl -s "$B/api/stores/$SID/insights" -H "Authorization: Bearer $TOKEN")
ne "评价数 > 0" "$(echo "$INS" | J 'd["rating"]["count"]')"
ne "扫码次数已统计" "$(echo "$INS" | J 'd["scans"]')"
eq "发券/核销计数正确" "True" \
  "$(echo "$INS" | J 'd["coupons_issued"]>=1 and d["coupons_redeemed"]>=1')"
has "口径说明存在" "$(echo "$INS" | J 'd["scope_note"]')" "外国客"

step "20) 双码 PNG"
eq "门口码可生成" "200" \
  "$(curl -s -o /tmp/lc-smoke-qr.png -w '%{http_code}' "$B/api/stores/$SID/qr?ref=door" \
     -H "Authorization: Bearer $TOKEN")"
eq "门口码是有效 PNG" "89504e47" "$(xxd -p -l 4 /tmp/lc-smoke-qr.png)"

# ─────────────────────────────────────────────────────────────
# 客人端 AI 点菜助手 + 服务请求（本期新增，2026-10-15）
# 知识 = 本店菜品 + 服务（RAG）；无 LLM 凭据时走确定性兜底（只陈述菜单事实）。
# 守卫纪律：菜品名与价格不许编造；助手只「提议」动作，客人点一下才生成请求。
# ─────────────────────────────────────────────────────────────

step "21) 客人端 AI 点菜助手（RAG，无 LLM 走规则兜底）"
AS=$(curl -s -X POST "$B/api/public/store/$SLUG/assist" -H 'Content-Type: application/json' \
  -d '{"question":"Do you have mapo doufu?","lang":"en"}')
ACONTENT=$(echo "$AS" | J 'd["reply"]["content"]')
ne "助手给出回答" "$ACONTENT"
eq "回答来源合法（llm/rule/flow/fallback）" "True" \
  "$(echo "$AS" | J 'd["reply"]["source"] in ("llm","rule","flow","fallback")')"
# 大小写不敏感检索：问 "mapo doufu" 应命中「麻婆豆腐」的 en 名 Mapo doufu
# （无 LLM 时兜底直接列出菜名；有 LLM 时模型一般也会复述菜名，故软断言）
soft "大小写不敏感命中麻婆豆腐(Mapo doufu)" \
  "$(echo "$AS" | J '("mapo" in ((d["reply"]["content"] or "")).lower())')"

step "22) 服务请求：客人发起（提议—确认边界）"
BELL=$(curl -s -X POST "$B/api/public/store/$SLUG/requests" -H 'Content-Type: application/json' \
  -d '{"kind":"bell","ref":"t01","lang":"en"}')
ne "服务铃已生成 request_id" "$(echo "$BELL" | J 'd["request_id"]')"
DISHID=$(echo "$DISHES" | J 'd["dishes"][0]["id"]')
ADD=$(curl -s -X POST "$B/api/public/store/$SLUG/requests" -H 'Content-Type: application/json' \
  -d "{\"kind\":\"add_dish\",\"dish_id\":\"$DISHID\",\"qty\":1,\"note\":\"extra spicy\",\"lang\":\"en\",\"ref\":\"t01\"}")
ne "加菜已生成 request_id" "$(echo "$ADD" | J 'd["request_id"]')"

step "23) 商家看服务请求待办"
REQS=$(curl -s "$B/api/stores/$SID/requests" -H "Authorization: Bearer $TOKEN")
ne "返回待办列表" "$(echo "$REQS" | J 'd["requests"]')"
eq "待办数 >= 2（服务铃 + 加菜）" "True" "$(echo "$REQS" | J 'd["open_count"]>=2')"

step "24) 标记加菜请求完成（客人点过才到这一步）"
RID=$(echo "$REQS" | J 'next((r["id"] for r in d["requests"] if r["kind"]=="add_dish"), "")')
ne "取到加菜请求 id" "$RID"
eq "标记完成成功" "True" \
  "$(curl -s -X POST "$B/api/stores/$SID/requests/$RID/done" -H "Authorization: Bearer $TOKEN" \
     -H 'Content-Type: application/json' -d '{"status":"done"}' | J 'd["ok"]')"

step "25) 处理后待办下降"
REQS2=$(curl -s "$B/api/stores/$SID/requests" -H "Authorization: Bearer $TOKEN")
eq "待办数下降为 1（仅剩服务铃）" "True" "$(echo "$REQS2" | J 'd["open_count"]==1')"

step "26) 平台 AI 凭据管理（RBAC：非超管不可访问）"
eq "普通商家访问被拒(FORBIDDEN)" "FORBIDDEN" \
  "$(curl -s "$B/api/admin/ai-credentials" -H "Authorization: Bearer $TOKEN" | J 'd["error"]["code"]')"

# ─────────────────────────────────────────────────────────────
printf '\n\033[1m═══ 结果：%d 通过 / %d 失败 ═══\033[0m\n' "$PASS" "$FAIL"
echo "本次测试门店：$B/s/$SLUG"
echo "确定性离线兜底测试：bash scripts/smoke_offline.sh"
echo "清理本次测试数据　：bash scripts/reset_test_data.sh"

[ "$FAIL" -eq 0 ] || exit 1
[ "$FAIL" -eq 0 ] || exit 1
