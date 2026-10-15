-- 0003 · 分类名多语化
--
-- 为什么需要：菜单分类（热菜 / 主食 / 汤 …）在客人端是显眼的区块标题。
-- 若只翻菜名不翻分类，客人会看到「外语菜名 + 中文标题」的割裂页面——
-- 本质仍是「中文没有让位给目标语言」。故分类名纳入同一条翻译链。
--
-- 存储形态：store.category_names = {"en": {"热菜":"Hot Dishes"}, "ja": {...}}
-- 分类是每店少量、稳定的词，一次翻译长期复用（走 TM 缓存，成本可忽略）。

ALTER TABLE store ADD COLUMN IF NOT EXISTS category_names JSONB NOT NULL DEFAULT '{}'::jsonb;
