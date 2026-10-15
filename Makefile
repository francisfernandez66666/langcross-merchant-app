.PHONY: help db api web build smoke clean

help:
	@echo "LangCross 商家店端 · 常用命令"
	@echo "  make db     创建本地数据库 langcross"
	@echo "  make api    启动后端（自动迁移）"
	@echo "  make web    启动前端 dev server（http://localhost:5173）"
	@echo "  make build  构建后端二进制 + 前端静态产物"
	@echo "  make smoke  跑端到端冒烟测试（需后端已启动）"

db:
	createdb langcross 2>/dev/null || echo "数据库 langcross 已存在"

api:
	cd server && go run ./cmd/api

web:
	cd web && npm run dev

build:
	cd server && go build -o bin/api ./cmd/api
	cd web && npm run build

smoke:
	bash server/scripts/smoke.sh

clean:
	rm -rf server/bin web/dist
