.PHONY: help db api web build smoke smoke-offline render-check test reset reset-all clean

help:
	@echo "LangCross 商家店端 · 常用命令"
	@echo ""
	@echo "  开发"
	@echo "    make db             创建本地数据库 langcross"
	@echo "    make api            启动后端（自动迁移）"
	@echo "    make web            启动前端 dev server（http://localhost:5173）"
	@echo "    make build          构建后端二进制 + 前端静态产物"
	@echo ""
	@echo "  测试（需先起后端；渲染回归另需前端 dev server）"
	@echo "    make test           Go 单测 + 前端类型检查（CI 用，不需要服务）"
	@echo "    make smoke          端到端冒烟测试（在线翻译引擎）"
	@echo "    make smoke-offline  离线兜底冒烟测试（需另起 :8099 降级后端）"
	@echo "    make render-check   客人端渲染回归（真开浏览器，查语言纪律）"
	@echo ""
	@echo "  清理"
	@echo "    make reset          清理测试数据（保留翻译记忆缓存）"
	@echo "    make reset-all      清空全部业务数据（保留表结构与迁移记录）"
	@echo "    make clean          删除构建产物"

# ── 开发 ──────────────────────────────────────────────────────

db:
	createdb langcross 2>/dev/null || echo "数据库 langcross 已存在"

api:
	cd server && go run ./cmd/api

# 离线降级后端：翻译链指向黑洞地址，用于确定性验证内置词典兜底
api-offline:
	cd server && LC_ADDR=:8099 LC_TRANSLATE_URL=http://127.0.0.1:9 go run ./cmd/api

web:
	cd web && npm run dev

build:
	cd server && go build -o bin/api ./cmd/api
	cd web && npm run build

# ── 测试 ──────────────────────────────────────────────────────

# 纯静态检查 + 单测：不依赖任何服务，可安全放进 CI
test:
	cd server && gofmt -l ./internal ./cmd && go vet ./... && go test ./...
	cd web && npx tsc --noEmit

smoke:
	bash server/scripts/smoke.sh

# 需要另开一个终端跑：make api-offline
smoke-offline:
	BASE=http://localhost:8099 bash server/scripts/smoke_offline.sh

# 需要后端(:8080) + 前端 dev server(:5173) 都在跑
render-check:
	cd web && node scripts/guest-render-check.mjs

# ── 清理 ──────────────────────────────────────────────────────

reset:
	bash server/scripts/reset_test_data.sh

reset-all:
	bash server/scripts/reset_test_data.sh --all

clean:
	rm -rf server/bin web/dist
