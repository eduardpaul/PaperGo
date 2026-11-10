-- Workspaces keep their installed solutions as a setting: 'solutions'
-- joins the keys a setting may have.
ALTER TABLE settings DROP CONSTRAINT settings_key_check;
ALTER TABLE settings ADD CONSTRAINT settings_key_check
    CHECK ("key" IN ('comments', 'trusted_hosts', 'solutions'));
