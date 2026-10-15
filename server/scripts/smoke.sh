#!/usr/bin/env bash
# LangCross 商家店端 · 端到端冒烟测试
# 用法：BASE=http://localhost:8080 bash scripts/smoke.sh
set -uo pipefail

B="${BASE:-http://localhost:8080}"
PHONE="${PHONE:-13800000001}"
J() { python3 -c "import sys,json;d=json.load(sys.stdin);print($1)"; }

echo "── 1) 健康检查"
curl -s "$B/api/health"; echo

echo "── 2) 发送验证码"
curl -s -X POST "$B/api/auth/otp" -H 'Content-Type: application/json' \
  -d "{\"phone\":\"$PHONE\"}"; echo

echo "── 3) 登录（手机号 + 验证码）"
TOKEN=$(curl -s -X POST "$B/api/auth/login" -H 'Content-Type: application/json' \
  -d "{\"phone\":\"$PHONE\",\"code\":\"000000\"}" | J 'd["token"]')
echo "token=${TOKEN:0:20}..."

echo "── 4) 创建门店（注册后自动配券 + 生成双码）"
STORE=$(curl -s -X POST "$B/api/stores" -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"name":"老张川菜馆"}')
SID=$(echo "$STORE" | J 'd["store"]["id"]')
SLUG=$(echo "$STORE" | J 'd["store"]["slug"]')
echo "store_id=$SID  slug=$SLUG"
echo "$STORE" | J '"  门店页: "+d["links"]["store_page"]'
echo "$STORE" | J '"  券模板: "+d["coupon_template"]["content"]["zh"]'

echo "── 5) 保存原文确认稿（P2 上传）"
curl -s -X POST "$B/api/stores/$SID/draft" -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{
  "lang":"zh","items":[
    {"name":"宫保鸡丁","price":38,"category":"热菜","selected":true},
    {"name":"麻婆豆腐","price":32,"category":"热菜","selected":true},
    {"name":"水煮鱼","price":68,"category":"热菜","selected":false},
    {"name":"蛋炒饭","price":18,"category":"主食","selected":true},
    {"name":"酸辣汤","price":22,"category":"汤","selected":true}
  ]}' | J '"  draft_id="+d["draft"]["id"]+"  items="+str(len(d["draft"]["items"]))'

echo "── 6) 未确认就翻译（应被拒）"
curl -s -X POST "$B/api/stores/$SID/translate" -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{}' | J '"  → "+d["error"]["code"]+": "+d["error"]["msg"]'

echo "── 7) 确认原文（P3，一次确认即生效）"
curl -s -X POST "$B/api/stores/$SID/draft/confirm" -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{}' | J '"  confirmed draft="+d["draft_id"]'

echo "── 8) 多语化（P4，只翻勾选的菜）"
curl -s -X POST "$B/api/stores/$SID/translate" -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{}' | J '"  translated_count="+str(d["translated_count"])'
curl -s "$B/api/stores/$SID/dishes" -H "Authorization: Bearer $TOKEN" | python3 -c '
import sys,json
for x in json.load(sys.stdin)["dishes"]:
    print("   ", x["name_zh"], "→", x.get("name_en") or "(未命中)", "¥", x.get("price"))'

echo "── 9) 客人端门店页（公开，无需登录）"
curl -s "$B/api/public/store/$SLUG?ref=t01" | J '"  store="+d["store"]["name"]+"  dishes="+str(len(d["dishes"]))+"  coupon_enabled="+str(d["store"]["coupon_enabled"])'

echo "── 10) 客人提交三维度评价 → 自动发券（发券免费）"
RATED=$(curl -s -X POST "$B/api/public/store/$SLUG/rating" -H 'Content-Type: application/json' -d '{
  "ref":"t01","score_dish":5,"score_service":4,"score_return":5,
  "comment":"The Kung Pao Chicken was amazing!","lang":"en"}')
CODE=$(echo "$RATED" | J 'd["coupon"]["code"]')
echo "  券码=$CODE  券面=$(echo "$RATED" | J 'd["coupon"]["status"]')"
echo "$RATED" | J '"  评价中文译文: "+(d["rating"].get("comment_zh") or "(词典未命中，留空不编造)")'

echo "── 11) 店员核销"
curl -s -X POST "$B/api/stores/$SID/coupon/redeem" -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d "{\"code\":\"$CODE\"}" | J '"  核销状态="+d["coupon"]["status"]'

echo "── 12) 重复核销（应被拒）"
curl -s -X POST "$B/api/stores/$SID/coupon/redeem" -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d "{\"code\":\"$CODE\"}" | J '"  → "+d["error"]["msg"]'

echo "── 13) 经营洞察（口径：只算用我们工具的外国客数据）"
curl -s "$B/api/stores/$SID/insights" -H "Authorization: Bearer $TOKEN" | J '"  评价数="+str(d["rating"]["count"])+"  平均菜品分="+str(round(d["rating"]["avg_dish"],2))+"  扫码数="+str(d["scans"])+"  发券="+str(d["coupons_issued"])+"  核销="+str(d["coupons_redeemed"])'

echo "── 14) 双码 PNG"
curl -s -o /tmp/qr-door.png -w "  door码 HTTP=%{http_code} bytes=%{size_download}\n" \
  "$B/api/stores/$SID/qr?ref=door" -H "Authorization: Bearer $TOKEN"

echo "✅ 冒烟测试完成"
