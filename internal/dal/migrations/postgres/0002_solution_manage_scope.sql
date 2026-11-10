-- The built-in Administrator role is created with every scope there is.
-- solution.manage is new, so the administrators of an existing database get it.
UPDATE identity_roles
SET scopes = scopes || '["solution.manage"]'::jsonb, version = version + 1
WHERE id = 'administrator' AND NOT scopes @> '["solution.manage"]'::jsonb;
