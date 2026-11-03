-- Image optimization is gone. Its pending operations would never run:
-- cancel them, as cancelling an operation does, so the runs waiting for
-- them go on. A workspace's documents.optimize workflow stays, turned off,
-- like any built-in this version no longer has.
DELETE FROM blob_leases WHERE operation_id IN (
    SELECT id FROM operations WHERE kind = 'document.optimize_image' AND status = 'pending');
DELETE FROM activity_references WHERE owner_kind = 'operation' AND owner_id IN (
    SELECT id FROM operations WHERE kind = 'document.optimize_image' AND status = 'pending');
UPDATE operations
SET status = 'cancelled', error_code = '', completed_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP,
    claim_holder = NULL, claim_token = NULL, claim_until = NULL, execution_id = ''
WHERE kind = 'document.optimize_image' AND status = 'pending';
