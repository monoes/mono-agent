-- 024_profiles_root_dir.sql
-- Lets a profile's folder live somewhere other than the default
-- ~/.monoagent/profiles/<id>/ — e.g. an external drive or synced folder.
-- Empty (the default) means "use the default path", exactly today's
-- behavior — fully backward compatible for every existing profile.
ALTER TABLE profiles ADD COLUMN root_dir TEXT NOT NULL DEFAULT '';
