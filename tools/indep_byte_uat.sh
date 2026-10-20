#!/bin/bash
# ─────────────────────────────────────────────────────────────
# LangCross 独立字节级 UAT（indep_byte_uat.sh）
#
# 与项目自带 smoke 的关系：不复用其任何断言函数/变量/helper，
# 从零实现同一层次的验证目标。本脚本重点不在「功能通不通」
# （那由 smoke 负责），而在：
#   A. BE↔BE 字节级：特殊字符（引号/<>&/中文/重音/emoji）经
#      API 写入 → psql 直读 → API 读回 → psql 直写 → API 读回
#      双向逐字节比对；纯端点字节级幂等（同请求两次响应同指纹）
#   B. FE↔BE：经 Vite 代理打后端（部署形态的真实路径）、
#      SPA 深路由回落、/api 未知路径不得走 SPA、压缩检查
#   C. 分支/异常：401/403/404/400/405/OPTIONS/错误信封统一性
#   D. Token 载荷：解码自签 token，核对字段集（防角色烘焙）
#   E. 多租户隔离：一次性租户 → 本租户可见 → 他租户不可见 →
#      超管跨租户边界 → 回收并断言残留 0
#   F. 产品专项：客人助手降级源、TM 跨租户共享、健康检查口径
#
# 用法：bash tools/indep_byte_uat.sh   （需 :8080 API 与 :5173 vite 在手）
# ★ :8080 API 必须以 LC_DEV=1 开发模式启动：认证走固定验证码 000000。
#   2026-10-19 起 LC_DEV 默认关闭（fail-safe），启动命令示例：
#   bash tools/clean_env.sh env LC_DEV=1 go run ./cmd/api
# 退出码：0 = 全部通过；1 = 存在失败
# ─────────────────────────────────────────────────────────────
set -uo pipefail

B="${B:-http://127.0.0.1:8080}"
W="${W:-http://localhost:5173}"
PSQL_BIN="${PSQL_BIN:-/opt/homebrew/bin/psql}"
PY="${PY:-/Users/zhangzifei/.workbuddy/binaries/python/versions/3.13.12/bin/python3}"
DSN="host=127.0.0.1 user=$(whoami) dbname=langcross"
TS=$(date +%s)
TAG="ibv$TS"

PASS=0; FAIL=0; FAILED_ITEMS=()
ok()  { PASS=$((PASS+1)); printf '  \033[32m✓\033[0m %s\n' "$1"; }
bad() { FAIL=$((FAIL+1)); FAILED_ITEMS+=("$1"); printf '  \033[31m✗\033[0m %s\n' "$1"; }
eq()  { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1（期望「$2」，实际「$3」）"; fi; }
ne()  { if [ -n "$2" ] && [ "$2" != "None" ] && [ "$2" != "null" ]; then ok "$1"; else bad "$1（为空）"; fi; }
step(){ printf '\n\033[36m── %s\033[0m\n' "$1"; }

# JSON 取值（独立实现，不借项目 J）
jget(){ "$PY" -c "import sys,json
try:
    d=json.load(sys.stdin); v=eval(sys.argv[1],{'d':d}); print('' if v is None else v)
except Exception as e: print('')" "$1"; }
# 响应体字节指纹
bsha(){ printf '%s' "$1" | shasum -a 256 | cut -d' ' -f1; }
# HTTP 状态码
hc(){ curl -s --noproxy '*' -o /dev/null -w '%{http_code}' "$@"; }
# SQL（-tA 无表头无对齐）
Q(){ "$PSQL_BIN" -h 127.0.0.1 -U "$(whoami)" -d langcross -tAc "$1" 2>&1; }
# SQL 字符串字面量转义（单引号翻倍）
sqlesc(){ printf '%s' "$1" | /usr/bin/sed "s/'/''/g"; }
# 从 INSERT...RETURNING 输出取首行 ID（psql 会额外回显 INSERT 0 1 状态行，必须只取首行）
firstline(){ printf '%s' "$1" | /usr/bin/head -n1 | tr -d '[:space:]'; }
# 校验 32 位十六进制
ishex32(){ printf '%s' "$1" | /usr/bin/grep -qE '^[0-9a-f]{32}$'; }

step "0) 环境自证（本机 grep 是 toybox，先证明脚手架自身可信）"
GT=$(grep --version 2>&1 | head -1)
case "$GT" in toybox*) ok "确认本机默认 grep 为 toybox（脚本断言不依赖它）";; *) bad "grep 真身异常: $GT";; esac
H=$(curl -s --noproxy '*' "$B/api/health")
eq "API 健康 ok=true" "True" "$(echo "$H" | jget 'd["ok"]')"
HC=$(hc --noproxy '*' "$W/")
case "$HC" in 200|304) ok "Vite dev server 可达 ($HC)";; *) bad "Vite dev server 不可达: $HC";; esac
eq "DB 连通（tenant 表可读）" "True" "$(Q "SELECT count(*)>=1 FROM tenant" >/dev/null && echo True)"

step "A) BE↔BE 字节级：特殊字符双向贯通"
# A1 登录默认租户商家（dev OTP 固定 000000）
curl -s --noproxy '*' -X POST "$B/api/auth/otp" -H 'Content-Type: application/json' \
  -d "{\"phone\":\"13800000001\"}" > /dev/null
TOK=$(curl -s --noproxy '*' -X POST "$B/api/auth/login" -H 'Content-Type: application/json' \
  -d '{"phone":"13800000001","code":"000000"}' | jget 'd["token"]')
ne "A1 商家 token" "$TOK"
AH="Authorization: Bearer $TOK"

# A2 建店：店名含引号/尖括号/&/中文/重音/emoji/反斜杠
NAME="$TAG-特殊<&\"'>与中文é€😀\\END"
PJSON=$("$PY" -c "import json,sys;print(json.dumps({'name':sys.argv[1]},ensure_ascii=False))" "$NAME")
STORE=$(curl -s --noproxy '*' -X POST "$B/api/stores" -H "$AH" -H 'Content-Type: application/json' -d "$PJSON")
SID=$(echo "$STORE" | jget 'd["store"]["id"]')
SLUG=$(echo "$STORE" | jget 'd["store"]["slug"]')
ne "A2 门店 ID" "$SID"

# A3 API 读回逐字节相等
GOT=$(curl -s --noproxy '*' "$B/api/stores/$SID" -H "$AH" | jget 'd["store"]["name"]')
eq "A3 API 写入→读回 逐字节相等" "$NAME" "$GOT"

# A4 psql 直读原文（BE↔BE 正向）
DBV=$(Q "SELECT name FROM store WHERE id='$SID'")
eq "A4 API 写入→psql 直读 逐字节相等" "$NAME" "$DBV"

# A5 psql 直写 → API 读回（BE↔BE 反向）
REV="$TAG-反向<&'>é€😀写入"
Q "UPDATE store SET name='$(sqlesc "$REV")' WHERE id='$SID'" > /dev/null
GOT2=$(curl -s --noproxy '*' "$B/api/stores/$SID" -H "$AH" | jget 'd["store"]["name"]')
eq "A5 psql 直写→API 读回 逐字节相等" "$REV" "$GOT2"

# A6 菜单特殊字符：draft → confirm → translate → API/psql 双向
# （2026-10-19 Q1 起 dish 行在**确认**时就落库、翻译只更新 names；
#   这里仍走完整三步，是为了连「翻译后 names 写入」一起覆盖）
DISH_IBV="$TAG-宫保鸡丁<&\"'>é€😀"
DJSON=$("$PY" -c "import json,sys;print(json.dumps({'lang':'zh','items':[{'name':sys.argv[1],'price':38,'category':'热菜','selected':True}]},ensure_ascii=False))" "$DISH_IBV")
curl -s --noproxy '*' -X POST "$B/api/stores/$SID/draft" -H "$AH" -H 'Content-Type: application/json' -d "$DJSON" > /dev/null
curl -s --noproxy '*' -X POST "$B/api/stores/$SID/draft/confirm" -H "$AH" -H 'Content-Type: application/json' -d '{}' > /dev/null
curl -s --noproxy '*' -X POST "$B/api/stores/$SID/translate" -H "$AH" -H 'Content-Type: application/json' -d '{"targets":["en"]}' > /dev/null
sleep 0.5
DDBS=$(Q "SELECT count(*) FROM dish WHERE store_id='$SID' AND names_json::text LIKE '%$(sqlesc "$TAG")%'")
eq "A6 特殊字符菜品落库（psql 可查）" "1" "$DDBS"
DAPI=$(curl -s --noproxy '*' "$B/api/stores/$SID/dishes" -H "$AH" | jget '[x["name_zh"] for x in d["dishes"]][0]')
eq "A6b 特殊字符菜品 API 读回一致" "$DISH_IBV" "$DAPI"

# A7 字节级幂等：纯端点同请求两次，响应体 sha256 相同
R1=$(curl -s --noproxy '*' "$B/api/translation/langs"); R2=$(curl -s --noproxy '*' "$B/api/translation/langs")
eq "A7 /api/translation/langs 两次响应字节指纹相同" "$(bsha "$R1")" "$(bsha "$R2")"
P1=$(curl -s --noproxy '*' "$B/api/public/store/$SLUG"); P2=$(curl -s --noproxy '*' "$B/api/public/store/$SLUG")
eq "A7b 客人端门店页两次响应字节指纹相同" "$(bsha "$P1")" "$(bsha "$P2")"

step "B) FE↔BE：经 Vite 代理的真实部署路径"
# B1 代理健康
PH=$(curl -s --noproxy '*' "$W/api/health")
eq "B1 Vite /api/health 代理通（ok=true）" "True" "$(echo "$PH" | jget 'd["ok"]')"
# B2 代理带鉴权的业务端点
PL=$(curl -s --noproxy '*' "$W/api/stores" -H "$AH" | jget 'str(len(d.get("stores",[])))')
ne "B2 Vite 代理 /api/stores 带 token 可读" "$PL"
# B3 SPA 深路由回落（客人端链接的传播形态）
eq "B3 /s/\$slug 深路由回落 200" "200" "$(hc --noproxy '*' "$W/s/$SLUG")"
eq "B3b /admin/onboarding 深路由回落 200" "200" "$(hc --noproxy '*' "$W/admin/onboarding")"
# B4 /api 未知路径不得回落 SPA（必须是 404，不能是 index.html）
U=$(curl -s --noproxy '*' "$W/api/__nonexistent__")
eq "B4 /api 未知路径经代理返回 404" "404" "$(hc --noproxy '*' "$W/api/__nonexistent__")"
# B4b 真实发现：未知路由 404 是 Go mux 默认纯文本，非统一 JSON 信封（错误契约不一致）
case "$U" in *'"code"'*) ok "B4b 未知路由 404 是统一 JSON 信封";; *) bad "B4b 未知路由 404 为纯文本「${U}」（非统一信封，API 契约不一致）";; esac
# B5 后端直连不服务 SPA
eq "B5 后端直连 /s/ 返回 404 JSON" "404" "$(hc --noproxy '*' "$B/s/$SLUG")"
# B6 压缩检查（生产缺口探针）
CE=$(curl -s --noproxy '*' -D - -o /dev/null -H 'Accept-Encoding: gzip, br' "$B/api/translation/langs" | tr -d '\r' | awk 'tolower($1)=="content-encoding:"{print $2}')
if [ -n "$CE" ]; then ok "B6 API 响应有压缩 ($CE)"; else bad "B6 API 无 gzip/br 压缩（Content-Encoding 缺失）"; fi

step "C) 分支/异常与错误信封统一性"
# C1 无 token
R=$(curl -s --noproxy '*' "$B/api/me"); C=$(hc --noproxy '*' "$B/api/me")
eq "C1 无 token → 401" "401" "$C"
eq "C1b 信封含 code=UNAUTH" "UNAUTH" "$(echo "$R" | jget 'd["error"]["code"]')"
# C2 坏 token
C=$(hc --noproxy '*' -H 'Authorization: Bearer garbage.token.here' "$B/api/me")
eq "C2 坏 token → 401" "401" "$C"
# C3 过期 token（手工构造：payload exp=1，签名用默认 dev secret 无意义 → 必然验签失败）
C=$(hc --noproxy '*' -H 'Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ4IiwiZXhwIjoxfQ.AAAA' "$B/api/me")
eq "C3 构造过期 token → 401" "401" "$C"
# C4 角色不足：manager 访问超管接口
C=$(hc --noproxy '*' -H "$AH" "$B/api/admin/tenants")
eq "C4 manager 访问 /api/admin/tenants → 403" "403" "$C"
R=$(curl -s --noproxy '*' -H "$AH" "$B/api/admin/tenants")
eq "C4b 信封含 code=FORBIDDEN" "FORBIDDEN" "$(echo "$R" | jget 'd["error"]["code"]')"
# C5 不存在的门店
C=$(hc --noproxy '*' -H "$AH" "$B/api/stores/__nope__")
eq "C5 不存在门店 → 404" "404" "$C"
# C6 非法 JSON
C=$(hc --noproxy '*' -X POST "$B/api/stores" -H "$AH" -H 'Content-Type: application/json' -d '{bad json')
eq "C6 非法 JSON → 400" "400" "$C"
# C7 方法不符（GET 打 POST-only 端点）
C=$(hc --noproxy '*' "$B/api/auth/otp")
eq "C7 GET /api/auth/otp → 405" "405" "$C"
# C8 OPTIONS 预检
C=$(curl -s --noproxy '*' -o /dev/null -w '%{http_code}' -X OPTIONS "$B/api/stores")
eq "C8 OPTIONS 预检 → 204" "204" "$C"
# C9 公开端点无鉴权可用
eq "C9 客人端公开门店页无 token 200" "200" "$(hc --noproxy '*' "$B/api/public/store/$SLUG")"
# C10 上传端点越界（未登录传图）
C=$(hc --noproxy '*' -X POST "$B/api/stores/$SID/dishes/x/image")
eq "C10 未登录传图 → 401" "401" "$C"
# C11 客人助手限流端点存在且公开
C=$(hc --noproxy '*' -X POST "$B/api/public/store/$SLUG/assist" -H 'Content-Type: application/json' -d '{"question":"你好"}')
eq "C11 客人助手公开可问（200）" "200" "$C"

step "D) Token 载荷核对（防角色烘焙进 token）"
# 自签 token = base64url(payload).base64url(hmac)；解 payload 看字段集
B64PAY=$(printf '%s' "$TOK" | cut -d. -f1)
FIELDS=$("$PY" -c "import base64,json,sys
p=sys.argv[1]
d=json.loads(base64.urlsafe_b64decode(p+'=='*(-len(p)%4)))
print(','.join(sorted(d.keys())))" "$B64PAY" 2>/dev/null)
eq "D1 token 载荷字段 = exp,sub（无 role/tenant）" "exp,sub" "$FIELDS"
SUB=$("$PY" -c "import base64,json,sys
p=sys.argv[1]
print(json.loads(base64.urlsafe_b64decode(p+'=='*(-len(p)%4))).get('sub',''))" "$B64PAY" 2>/dev/null)
ne "D2 token sub 非空" "$SUB"

step "E) 多租户隔离（一次性租户，结束全回收）"
# E1 超管登录（smoke_admin 提权的账号）
curl -s --noproxy '*' -X POST "$B/api/auth/otp" -H 'Content-Type: application/json' -d '{"phone":"13800001234"}' > /dev/null
SUP=$(curl -s --noproxy '*' -X POST "$B/api/auth/login" -H 'Content-Type: application/json' -d '{"phone":"13800001234","code":"000000"}' | jget 'd["token"]')
ne "E1 超管 token" "$SUP"
SAH="Authorization: Bearer $SUP"

# E2 造隔离租户（SQL 直插，ID 用 32-hex）
TID=$("$PY" -c "import secrets;print(secrets.token_hex(16))")
RAW=$(Q "INSERT INTO tenant(id,code,name) VALUES ('$TID','$TAG','$TAG 隔离租户') RETURNING id")
TID=$(firstline "$RAW")
if ishex32 "$TID"; then ok "E2 一次性租户已建（${TID}）"; else bad "E2 租户创建失败: $RAW"; fi

# E3 超管给该租户建商家账号
T2PHONE="139${TS: -8}"
CR=$(curl -s --noproxy '*' -X POST "$B/api/admin/accounts?tenant_id=$TID" -H "$SAH" -H 'Content-Type: application/json' \
  -d "{\"phone\":\"$T2PHONE\",\"role\":\"manager\",\"display_name\":\"$TAG\"}")
eq "E3 超管为 T2 建账号 ok=true" "True" "$(echo "$CR" | jget 'd["ok"]')"
curl -s --noproxy '*' -X POST "$B/api/auth/otp" -H 'Content-Type: application/json' -d "{\"phone\":\"$T2PHONE\"}" > /dev/null
T2TOK=$(curl -s --noproxy '*' -X POST "$B/api/auth/login" -H 'Content-Type: application/json' -d "{\"phone\":\"$T2PHONE\",\"code\":\"000000\"}" | jget 'd["token"]')
ne "E3b T2 商家 token" "$T2TOK"
T2AH="Authorization: Bearer $T2TOK"
eq "E3c T2 账号归属 T2（psql 直读）" "$TID" "$(Q "SELECT tenant_id FROM account WHERE phone='$T2PHONE'")"

# E4 T2 建店
T2STORE=$(curl -s --noproxy '*' -X POST "$B/api/stores" -H "$T2AH" -H 'Content-Type: application/json' -d "{\"name\":\"$TAG T2店\"}")
T2SID=$(echo "$T2STORE" | jget 'd["store"]["id"]')
ne "E4 T2 门店 ID" "$T2SID"
eq "E4b T2 门店归属 T2（psql 直读）" "$TID" "$(Q "SELECT tenant_id FROM store WHERE id='$T2SID'")"

# E5 本租户可见
eq "E5 T2 商家看自己店 200" "200" "$(hc --noproxy '*' -H "$T2AH" "$B/api/stores/$T2SID")"
# E6 他租户不可见（默认租户商家访问 T2 店）
C6=$(hc --noproxy '*' -H "$AH" "$B/api/stores/$T2SID")
case "$C6" in 403|404) ok "E6 默认租户商家访问 T2 门店被拒（${C6}）";; *) bad "E6 默认租户商家竟能访问 T2 门店: ${C6}";; esac
# E7 他租户商家列表不应包含 T2 店
L7=$(curl -s --noproxy '*' -H "$AH" "$B/api/stores" | jget 'str(sum(1 for s in d.get("stores",[]) if s["id"]=="'$T2SID'"))')
eq "E7 默认租户商家门店列表不含 T2 店" "0" "$L7"
# E8 超管显式 tenant_id：门店详情 API 的设计边界是「超管仅默认租户」（CanAccessStore store.go:139-140），
# 跨租户门店可见性由 GET /api/admin/tenants/{id}/stores 提供（列表），管理动作不开放——此处断言设计行为
C8=$(hc --noproxy '*' -H "$SAH" "$B/api/stores/$T2SID?tenant_id=$TID")
eq "E8 超管带 tenant_id 访问 T2 门店详情 = 403（CanAccessStore 设计边界）" "403" "$C8"
E8B=$(curl -s --noproxy '*' -H "$SAH" "$B/api/admin/tenants/$TID/stores" | jget 'str(sum(1 for s in d.get("stores",[]) if s["id"]=="'$T2SID'"))')
eq "E8b 超管经 admin 租户门店列表可见 T2 店（跨租户可见性有出口）" "1" "$E8B"
# E9 超管不带 tenant_id → 作用于默认租户，看不到 T2 店
C9=$(hc --noproxy '*' -H "$SAH" "$B/api/stores/$T2SID")
case "$C9" in 403|404) ok "E9 超管无租户声明不看 T2 店（${C9}，落默认租户）";; *) bad "E9 超管无声明可见 T2 店: ${C9}";; esac
# E10 防提权：manager 不能建 tenant_admin 账号
C10=$(hc --noproxy '*' -X POST "$B/api/admin/accounts" -H "$AH" -H 'Content-Type: application/json' -d '{"phone":"13911112222","role":"tenant_admin"}')
eq "E10 manager 建 tenant_admin 账号被拒（403）" "403" "$C10"

# E11 回收：删租户及其账号/门店，断言残留 0
Q "DELETE FROM account WHERE phone='$T2PHONE'" > /dev/null
Q "DELETE FROM store WHERE id='$T2SID'" > /dev/null
Q "DELETE FROM tenant WHERE id='$TID'" > /dev/null
eq "E11a 回收后 tenant 残留 0" "0" "$(Q "SELECT count(*) FROM tenant WHERE id='$TID'")"
eq "E11b 回收后 account 残留 0" "0" "$(Q "SELECT count(*) FROM account WHERE phone='$T2PHONE'")"
eq "E11c 回收后 store 残留 0" "0" "$(Q "SELECT count(*) FROM store WHERE id='$T2SID'")"

step "F) 产品专项（代码审读发现的行为化确认）"
# F1 客人助手降级源：无 LLM 凭据时应答 source 应为 rule/fallback（LLM 装配断链的行为确认）
GA=$(curl -s --noproxy '*' -X POST "$B/api/public/store/$SLUG/assist" -H 'Content-Type: application/json' -d '{"question":"有什么辣的菜"}')
SRC=$(echo "$GA" | jget 'd["reply"]["source"]')
ne "F1 客人助手有应答" "$(echo "$GA" | jget 'd["reply"]["content"]')"
printf '     客人助手 source = %s（LLM 装配断链时应为 rule/fallback）\n' "$SRC"
case "$SRC" in llm) bad "F1 客人助手竟走了 LLM（与代码审读结论矛盾，需复核）";; rule|fallback|flow) ok "F1 客人助手走确定性降级（source=${SRC}）——确认 LLM 未接线";; "") bad "F1 拿不到 source 字段: $(echo "$GA" | head -c 120)";; *) ok "F1 客人助手 source=$SRC";; esac
# F2 TM 跨租户共享：同一原文在 T1/T2 各翻一次，translation_cache 只应有 1 行
UTXT="$TAG 特色烤鱼"
Q "DELETE FROM translation_cache WHERE source_text='$(sqlesc "$UTXT")'" > /dev/null
curl -s --noproxy '*' -X POST "$B/api/public/translate-preview" -H 'Content-Type: application/json' \
  -d "{\"items\":[{\"name\":\"$UTXT\"}],\"targets\":[\"en\"]}" > /dev/null
sleep 0.5
N1=$(Q "SELECT count(*) FROM translation_cache WHERE source_text='$(sqlesc "$UTXT")'")
# T2 商家对同一原文再翻一次（建稿确认后翻译，TM 按 source_text 全局键命中）
T2STORE2=$(curl -s --noproxy '*' -X POST "$B/api/stores" -H "$T2AH" -H 'Content-Type: application/json' -d "{\"name\":\"$TAG T2 TM店\"}")
T2SID2=$(echo "$T2STORE2" | jget 'd["store"]["id"]')
curl -s --noproxy '*' -X POST "$B/api/stores/$T2SID2/draft" -H "$T2AH" -H 'Content-Type: application/json' \
  -d "{\"lang\":\"zh\",\"items\":[{\"name\":\"$UTXT\",\"price\":1,\"category\":\"探针\",\"selected\":true}]}" > /dev/null
curl -s --noproxy '*' -X POST "$B/api/stores/$T2SID2/draft/confirm" -H "$T2AH" -H 'Content-Type: application/json' -d '{}' > /dev/null
curl -s --noproxy '*' -X POST "$B/api/stores/$T2SID2/translate" -H "$T2AH" -H 'Content-Type: application/json' -d '{"targets":["en"]}' > /dev/null
sleep 0.5
TMC=$(Q "SELECT count(*) FROM translation_cache WHERE source_text='$(sqlesc "$UTXT")'")
printf '     TM 探针行数：T1 翻后=%s，T2 翻后=%s（=1 即跨租户共享同一缓存条目）\n' "$N1" "$TMC"
if [ "$N1" = "1" ] && [ "$TMC" = "1" ]; then ok "F2 TM 缓存跨租户共享（设计如此，已确认）"; else bad "F2 TM 缓存行为异常: N1=$N1 TMC=$TMC"; fi
Q "DELETE FROM store WHERE id='$T2SID2'" > /dev/null
# F3 健康检查诚实口径：离线实例（:8099）翻译链指向黑洞地址。
# 修订前这里会断言 translate_mode=live 并打 ⚠ 说「健康检查反映配置而非可达性」；
# 2026-10-19 修订后，健康检查必须如实报「配了 chain 但连不上、实际在走词典」。
H8099=$(curl -s --noproxy '*' --max-time 2 http://127.0.0.1:8099/api/health 2>/dev/null)
if [ -n "$H8099" ]; then
  eq "F3 离线实例如实报出配的是自建链" "chain" "$(echo "$H8099" | jget 'd["translate_engine"]')"
  eq "F3 ★ 黑洞地址如实报不可达（修订前谎报 live）" "False" "$(echo "$H8099" | jget 'd["translate_reachable"]')"
  eq "F3 如实报出实际在走词典兜底（不再是 live）" "dict-stub" "$(echo "$H8099" | jget 'd["translate_mode"]')"
else
  printf '     :8099 未运行，跳过 F3（不影响结论：smoke_offline 已覆盖请求时降级与诚实口径）\n'
fi

step "G) 租户外键（0007：先 FK 后 RLS 的上半场）"
# 隔离此前纯靠应用层 WHERE，一次漏写就是一次跨租户泄露。0007 补上数据库层约束，
# 分三档：store/org/translation_api_key 直接 FK；account/ai_credential 用触发器
# （放行平台级空串）；admin_audit 刻意不加（审计要活过租户）。
REAL_ACC=$(Q "SELECT id FROM account LIMIT 1")
# expect_fail：SQL 应当失败，且错误信息命中给定关键字（否则算「约束没生效」）。
expect_fail(){
  local label="$1" sql="$2" pat="$3" out
  out=$(Q "$sql" 2>&1)
  # ★ 交替正则必须 -E：本机 grep 不支持 BRE 的 \|，会静默 0 命中（老坑）。
  if printf '%s' "$out" | /usr/bin/grep -Eqi "$pat"; then ok "$label"; else bad "$label（约束没生效：$(printf '%s' "$out" | /usr/bin/head -n2 | tr '\n' ' ')）"; fi
}
expect_ok(){
  local label="$1" sql="$2" out
  out=$(Q "$sql" 2>&1)
  if printf '%s' "$out" | /usr/bin/grep -qi 'ERROR'; then bad "$label（本该成功却报错：$(printf '%s' "$out" | /usr/bin/head -n2 | tr '\n' ' ')）"; else ok "$label"; fi
}

expect_fail "G1 孤儿门店被外键拒绝" \
  "BEGIN; INSERT INTO store(id,account_id,tenant_id,name,slug) VALUES ('ibt1','$REAL_ACC','no-such-tenant','x','x'); ROLLBACK;" \
  'fk_store_tenant|foreign key'
expect_fail "G2 孤儿组织被外键拒绝" \
  "BEGIN; INSERT INTO org(id,tenant_id,name) VALUES ('ibo1','no-such-tenant','x'); ROLLBACK;" \
  'fk_org_tenant|foreign key'
expect_fail "G3 孤儿翻译 Key 被外键拒绝" \
  "BEGIN; INSERT INTO translation_api_key(id,tenant_id,name,key_enc,key_masked) VALUES ('ibk1','no-such','n','\x00','xx'); ROLLBACK;" \
  'fk_tak_tenant|foreign key'
expect_fail "G4 孤儿账号被触发器拒绝" \
  "BEGIN; INSERT INTO account(id,phone,tenant_id,role) VALUES ('iba1','13900000096','no-such-tenant','staff'); ROLLBACK;" \
  '不存在|foreign_key_violation'
# 平台级空串是设计内合法值（超管提权就是 UPDATE account SET tenant_id=''）：
# 若它也被拒，说明约束做过头了——这里必须放行。
expect_ok "G5 平台级空串账号放行（超管提权路径）" \
  "BEGIN; INSERT INTO account(id,phone,tenant_id,role) VALUES ('iba2','13900000095','','super_admin'); ROLLBACK;"
expect_ok "G6 平台级空串 AI 凭据放行" \
  "BEGIN; INSERT INTO ai_credential(id,tenant_id,name,base_url,model,key_enc,key_masked) VALUES ('ibc1','','t','http://x','m','\x00','xx'); ROLLBACK;"

# 删除方向：有账号 / 有凭据就不能删租户（触发器的 RESTRICT 补齐）；
# 只剩组织树则放行（org 走 CASCADE，否则被自动建的 root org 卡住）。
GT="ibtenant$TS"
Q "INSERT INTO tenant(id, code, name) VALUES ('$GT','ib$TS','外键探针租户')" > /dev/null
Q "INSERT INTO account(id, phone, tenant_id, role) VALUES ('ibacc1','13900000094','$GT','staff')" > /dev/null
expect_fail "G7 租户还有账号 → 删租户被守卫挡住" "DELETE FROM tenant WHERE id='$GT'" '仍有.*账号'
Q "DELETE FROM account WHERE id='ibacc1'" > /dev/null
Q "INSERT INTO ai_credential(id,tenant_id,name,base_url,model,key_enc,key_masked) VALUES ('ibc2','$GT','t','http://x','m','\x00','xx')" > /dev/null
expect_fail "G8 租户还有 AI 凭据 → 删租户被守卫挡住" "DELETE FROM tenant WHERE id='$GT'" '仍有.*凭据'
Q "DELETE FROM ai_credential WHERE id='ibc2'" > /dev/null
Q "INSERT INTO org(id, tenant_id, name, type) VALUES ('ibo2','$GT','根','root')" > /dev/null
expect_ok "G9 只剩组织树 → 删租户放行（org 级联）" "DELETE FROM tenant WHERE id='$GT'"
eq "G10 回收干净：租户 / 组织残留 0" "0|0" "$(Q "SELECT (SELECT count(*) FROM tenant WHERE id='$GT') || '|' || (SELECT count(*) FROM org WHERE id='ibo2')")"

step "汇总"
printf '\n\033[1m═══ 独立字节级 UAT：%s 通过 / %s 失败 ═══\n' "$PASS" "$FAIL"
if [ $FAIL -gt 0 ]; then
  printf '失败项：\n'
  for f in "${FAILED_ITEMS[@]}"; do printf '  - %s\n' "$f"; done
fi
exit $([ $FAIL -eq 0 ] && echo 0 || echo 1)
