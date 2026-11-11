-- 每次配置写入产生不可复用的版本；回滚和恢复数据库也不会命中旧缓存。
ALTER TABLE site_settings ADD COLUMN IF NOT EXISTS cache_revision text NOT NULL DEFAULT gen_random_uuid()::text;
CREATE OR REPLACE FUNCTION refresh_setting_revision() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  NEW.cache_revision := gen_random_uuid()::text;
  RETURN NEW;
END $$;
CREATE TRIGGER setting_revision BEFORE UPDATE OF value ON site_settings
FOR EACH ROW EXECUTE FUNCTION refresh_setting_revision();
