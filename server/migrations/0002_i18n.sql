-- LangCross 商家店端 · 多语种支持（迁移 0002）
-- 1) dish 增加 names_json：多语种菜名（name_zh 始终是原文真值）
-- 2) translation_cache：翻译记忆（TM），避免重复调用外部引擎，也是 KB/TM 设计的一部分

BEGIN;

-- 多语种菜名，形如 {"en":"Kung Pao Chicken","ja":"ゴンバオチキン","fr":"..."}
ALTER TABLE dish ADD COLUMN IF NOT EXISTS names_json JSONB NOT NULL DEFAULT '{}'::jsonb;

-- 既有数据回填：把 name_en 镜像进 names_json.en
UPDATE dish
SET names_json = jsonb_build_object('en', name_en)
WHERE name_en IS NOT NULL AND name_en <> '' AND names_json = '{}'::jsonb;

CREATE TABLE IF NOT EXISTS translation_cache (
  id          BIGSERIAL PRIMARY KEY,
  source_lang TEXT NOT NULL,
  target_lang TEXT NOT NULL,
  source_text TEXT NOT NULL,
  target_text TEXT NOT NULL,
  provider    TEXT,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (source_lang, target_lang, source_text)
);
CREATE INDEX IF NOT EXISTS idx_tc_lookup ON translation_cache(source_lang, target_lang, source_text);

COMMIT;
