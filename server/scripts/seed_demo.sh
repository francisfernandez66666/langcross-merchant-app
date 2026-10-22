#!/usr/bin/env bash
# LangCross · 演示门店播种
#
# 造一家「菜品够多、分类够全、多语种全开」的店，用于人工验收客人端渲染。
# ★ 前置后端须以 LC_DEV=1 开发模式启动（登录走固定验证码 000000）。
# 用法（仓库根目录）：BASE=http://localhost:8080 bash server/scripts/seed_demo.sh [店名]
set -uo pipefail

B="${BASE:-http://localhost:8080}"
NAME="${1:-老张川菜馆}"
PHONE="${PHONE:-13900000002}"
J() { python3 -c "import sys,json;d=json.load(sys.stdin);print($1)"; }

echo "── 登录 / 注册"
curl -s -X POST "$B/api/auth/otp" -H 'Content-Type: application/json' -d "{\"phone\":\"$PHONE\"}" >/dev/null
TOKEN=$(curl -s -X POST "$B/api/auth/login" -H 'Content-Type: application/json' \
  -d "{\"phone\":\"$PHONE\",\"code\":\"000000\"}" | J 'd["token"]')

echo "── 创建门店"
STORE=$(curl -s -X POST "$B/api/stores" -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d "{\"name\":\"$NAME\",\"wifi_ssid\":\"Laozhang_Guest\",\"wifi_pwd\":\"88888888\"}")
SID=$(echo "$STORE" | J 'd["store"]["id"]')
SLUG=$(echo "$STORE" | J 'd["store"]["slug"]')
echo "   store_id=$SID"
echo "   slug=$SLUG"

echo "── 写入菜单（17 道 / 6 分类）"
curl -s -X POST "$B/api/stores/$SID/draft" -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{
  "lang":"zh","items":[
    {"name":"宫保鸡丁","price":38,"category":"热菜","selected":true},
    {"name":"麻婆豆腐","price":32,"category":"热菜","selected":true},
    {"name":"水煮鱼","price":68,"category":"热菜","selected":true},
    {"name":"糖醋里脊","price":42,"category":"热菜","selected":true},
    {"name":"番茄炒蛋","price":26,"category":"热菜","selected":true},
    {"name":"回锅肉","price":36,"category":"热菜","selected":true},
    {"name":"拍黄瓜","price":18,"category":"凉菜","selected":true},
    {"name":"口水鸡","price":28,"category":"凉菜","selected":true},
    {"name":"清炒时蔬","price":22,"category":"素菜","selected":true},
    {"name":"干煸四季豆","price":26,"category":"素菜","selected":true},
    {"name":"蛋炒饭","price":18,"category":"主食","selected":true},
    {"name":"米饭","price":3,"category":"主食","selected":true},
    {"name":"酸辣汤","price":22,"category":"汤","selected":true},
    {"name":"番茄蛋汤","price":20,"category":"汤","selected":true},
    {"name":"可乐","price":8,"category":"饮品","selected":true},
    {"name":"啤酒","price":12,"category":"饮品","selected":true},
    {"name":"酸梅汤","price":10,"category":"饮品","selected":true}
  ]}' | J '"   items="+str(len(d["draft"]["items"]))'

echo "── 确认原文"
curl -s -X POST "$B/api/stores/$SID/draft/confirm" -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{}' | J '"   confirmed="+d["draft_id"]'

echo "── 多语化（9 个语种，逐语种调用，请耐心等）"
curl -s -X POST "$B/api/stores/$SID/translate" -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"targets":["en","ja","ko","th","fr","es","de","pt","vi"]}' \
  | J '"   translated_count="+str(d["translated_count"])+"  targets="+str(len(d["targets"]))'

echo "── 语种覆盖"
curl -s "$B/api/stores/$SID/dishes" -H "Authorization: Bearer $TOKEN" | python3 -c '
import sys,json
ds=json.load(sys.stdin)["dishes"]
codes=["en","ja","ko","th","fr","es","de","pt","vi"]
print("   共",len(ds),"道菜")
for c in codes:
    n=sum(1 for d in ds if (d.get("names") or {}).get(c))
    print(f"   {c}: {n}/{len(ds)}")
print("   示例:", ds[0]["name_zh"], "→", json.dumps(ds[0].get("names"), ensure_ascii=False))'

echo
echo "✅ 演示门店就绪"
echo "   门店页：$B/s/$SLUG"
echo "   门口码：$B/s/$SLUG?ref=door"
echo "   桌码　：$B/s/$SLUG?ref=t01"
echo "$SLUG" > /tmp/lc_demo_slug.txt
