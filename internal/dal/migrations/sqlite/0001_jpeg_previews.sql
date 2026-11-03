-- Previews are JPEG. Earlier previews were WebP: forget them, and queue
-- their objects for deletion once nothing references them. Rendering the
-- item's preview again stores a JPEG; failed renders stay recorded.
INSERT INTO blob_deletions (id, created_at, object_key)
SELECT id, CURRENT_TIMESTAMP, object_key FROM blob_previews WHERE status = 'ready'
ON CONFLICT (object_key) DO NOTHING;
DELETE FROM blob_previews WHERE status = 'ready';
