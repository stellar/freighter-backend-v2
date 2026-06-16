# Database

`freighter-backend-v2` uses PostgreSQL. On boot the server opens a connection
pool and pings it, **failing fast** if the database is misconfigured or
unreachable. It does **not** run migrations on boot — see [Migrations](#migrations).

**Day-to-day development is expected to run against a hosted environment**
(dev/stg/prd, which run PostgreSQL under CloudNativePG). Running a local Postgres
is an edge case for offline work. Switching between the two changes exactly one
thing — the **`DATABASE_URL`** — and the helpers below make that a single command.

Once running, DB connectivity is reported by the dedicated `GET /api/v1/db-health`
endpoint, which always returns HTTP `200` and conveys reachability in the body
(`{"status":"healthy"}` / `{"status":"unhealthy"}`) — mirroring `rpc-health`. The
general `GET /api/v1/ping` liveness check is intentionally **dependency-free**: a
DB outage must not restart or depool a pod whose other (non-DB) routes still work.

The connection string is always supplied via the **`DATABASE_URL`** env var (or
the equivalent `--database-url` flag). It is a hard dependency — the server will
not start without it.

```
postgres://<user>:<password>@<host>:<port>/<dbname>?sslmode=<mode>
```

## Local development

`deployments/docker-compose.yml` runs a Postgres alongside the API and Redis.
Bring the whole stack up with:

```sh
make docker-build-up   # build the image and start api + redis + postgres
# or, if the image is already built:
make docker-up
```

The compose `api` service is pre-wired with:

```
DATABASE_URL=postgres://freighter:freighter@postgres:5432/freighter?sslmode=disable
```

Connect to the local database directly with `psql`:

```sh
# from the host (port 5432 is published)
psql "postgres://freighter:freighter@localhost:5432/freighter?sslmode=disable"

# or exec into the container
docker exec -it freighter-backend-postgres psql -U freighter -d freighter
```

To run the server outside compose (e.g. `make run`) against your own Postgres,
export `DATABASE_URL` first:

```sh
export DATABASE_URL="postgres://freighter:freighter@localhost:5432/freighter?sslmode=disable"
make run
```

## Migrations

Migrations are `.sql` files under `internal/db/migrations`, embedded into the
binary and applied with [`sql-migrate`](https://github.com/rubenv/sql-migrate).
They are **idempotent** — re-running applies nothing once the schema is current.

Migrations run **out-of-band from `serve`**, via a dedicated subcommand:

```sh
freighter-backend migrate up        # apply all pending migrations
freighter-backend migrate up 1      # apply only the next 1
freighter-backend migrate down 1    # roll back the last 1
```

This is deliberate. `serve` never mutates schema, so any number of replicas or
local processes can point at a **shared** hosted database without racing to
migrate it or applying a feature-branch migration on boot. The local compose
stack runs `migrate up` automatically as a one-shot `migrate` service before
`api` starts.

> **Deployed environments:** because `serve` no longer migrates, each release
> **must** run `freighter-backend migrate up` (a one-shot Job / init step) before
> or alongside the rollout. This manifest lives in the `stellar/kube` deployment
> repo, not here. `serve`'s boot check only pings the DB (connectivity), **not**
> the schema — so a pod will start "healthy" against an unmigrated database and
> only fail at the first query that needs a missing table. Don't ship a schema
> change without the corresponding migrate Job.

To add a migration, create a new file named with an increasing date prefix
(e.g. `2026-07-01.0-add_widgets.sql`) using the `sql-migrate` up/down markers:

```sql
-- +migrate Up
CREATE TABLE widgets (...);

-- +migrate Down
DROP TABLE widgets;
```

## Connecting to deployed environments

In deployed environments `DATABASE_URL` is **not** set by hand — it is injected
into the pod from a secret managed by ExternalSecrets. Inside a running pod the
value is already present in the environment:

```sh
kubectl exec -it deploy/<freighter-backend-deployment> -n <namespace> -- printenv DATABASE_URL
```

> The database currently exists only in **wallet-eng-dev**. Other environments
> are provisioned separately (see the "Provision Postgres" issue).

### Running locally against a hosted DB (the common case)

Two `make` targets wrap the CNPG mechanics so switching to a hosted env is one
command each. `db-url` reads the cluster's basic-auth creds secret
(`freighter-backend-v2-db-app-creds` — `username`/`password`) and builds a
`DATABASE_URL` pointed at your local tunnel, with `sslmode=require`.

First make sure your `kubectl` context points at the target environment, then:

```sh
# terminal 1 — open the tunnel to the CNPG primary (blocking; leave it running)
make db-forward ENV=dev

# terminal 2 — point this shell at the tunnel, then run the service
eval "$(make -s db-url ENV=dev)"
make run
```

`ENV` is `dev` | `stg` | `prd`; `LOCAL_PORT` overrides the local port (default 5432).

> **`dev` is wired** (`wallet-eng-dev` / cluster `freighter-backend-v2-db`). The
> database currently exists **only** in `wallet-eng-dev`; `stg`/`prd` are
> provisioned by the "Provision Postgres" work and `db-url` will error clearly
> there until they exist.

Verify a hosted connection works:

```sh
freighter-backend migrate up                 # → "Applied migrations up count=N" (0 on re-run)
curl -s localhost:3002/api/v1/db-health       # → {"status":"healthy"} once `serve` is up
```

To just read the value already injected into a running pod:

```sh
kubectl exec -it deploy/<freighter-backend-deployment> -n <namespace> -- printenv DATABASE_URL
```

## Pool configuration

Connection-pool sizing is tuned for the service's ~6k MAU light workload and is
configurable via flags / env vars (env names are the upper-snake-case of the flag):

| Flag | Env | Default | Meaning |
|------|-----|---------|---------|
| `--db-max-conns` | `DB_MAX_CONNS` | `10` | Max connections in the pool |
| `--db-min-conns` | `DB_MIN_CONNS` | `2` | Min idle connections kept warm |
| `--db-max-conn-lifetime` | `DB_MAX_CONN_LIFETIME` | `5m` | Max lifetime before a connection is recycled |
| `--db-max-conn-idle-time` | `DB_MAX_CONN_IDLE_TIME` | `10s` | Max idle time before a connection is closed |
