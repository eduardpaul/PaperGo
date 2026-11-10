-- The built-in Administrator role is created with every scope there is.
-- solution.manage is new, so the administrators of an existing database get it.
UPDATE identity_roles
SET scopes = json_insert(scopes, '$[#]', 'solution.manage'), version = version + 1
WHERE id = 'administrator'
  AND NOT EXISTS (SELECT 1 FROM json_each(identity_roles.scopes) WHERE value = 'solution.manage');
