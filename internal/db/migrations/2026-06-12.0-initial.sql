-- Initial migration. Intentionally empty: it establishes the sql-migrate
-- tooling and the gorp_migrations bookkeeping table. Migrations are applied by
-- the `migrate up` command (a deploy Job, or the docker-compose migrate service)
-- — never on the `serve` boot path. Feature schemas are added in their own migrations.

-- +migrate Up

-- +migrate Down
