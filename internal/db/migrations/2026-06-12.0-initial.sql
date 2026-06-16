-- Initial migration. Intentionally empty: it establishes the sql-migrate
-- tooling and the gorp_migrations bookkeeping table so the migration step runs
-- (and is idempotent) on boot. Feature schemas are added in their own migrations.

-- +migrate Up

-- +migrate Down
