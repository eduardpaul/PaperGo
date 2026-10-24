# SQLite migrations

Each schema change adds `NNNN_description.sql` here and the file of the same
name in `../postgres`. [docs/releases.md](../../../../docs/releases.md#database-upgrades)
describes the process.

PaperGo runs every pending file in one transaction with foreign keys off,
then checks foreign keys and integrity before it commits. Do not write
`BEGIN`, `COMMIT` or `PRAGMA foreign_keys`.

`ALTER TABLE` can add a column, rename a column or a table, and drop a
column that no index or constraint uses. Anything else, such as changing a
`CHECK`, a type, `NOT NULL` or a foreign key, means rebuilding the table:

```sql
CREATE TABLE tasks_new (
    -- the table exactly as the schema master now defines it
);
INSERT INTO tasks_new (id, title, status)
SELECT id, title, status FROM tasks;
DROP TABLE tasks;
ALTER TABLE tasks_new RENAME TO tasks;
-- recreate the table's indexes as the schema master defines them
CREATE INDEX task_status ON tasks(status);
```

Foreign keys in other tables refer to the table by name, so they follow the
rebuilt table.
