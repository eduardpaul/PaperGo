# PostgreSQL migrations

Each schema change adds `NNNN_description.sql` here and the file of the same
name in `../sqlite`. [docs/releases.md](../../../../docs/releases.md#database-upgrades)
describes the process.

PaperGo runs every pending file in one transaction, under an advisory lock
so that only one instance upgrades. Do not write `BEGIN` or `COMMIT`, and do
not use statements that cannot run in a transaction, such as
`CREATE INDEX CONCURRENTLY`.

A `CHECK` changes in place. PostgreSQL names a column's unnamed check
`<table>_<column>_check`:

```sql
ALTER TABLE list_views DROP CONSTRAINT list_views_layout_check;
ALTER TABLE list_views ADD CONSTRAINT list_views_layout_check
    CHECK (layout IN ('table', 'board', 'calendar', 'gallery', 'navigation', 'timeline'));
```
