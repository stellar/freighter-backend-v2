#!/usr/bin/env bash
#
# Seamless switching between a local DB and a hosted CloudNativePG (CNPG) cluster.
# The service is configured by a single env var, DATABASE_URL — this script just
# makes it one command to obtain that value for a hosted environment, and to open
# the tunnel the value points at.
#
# Usage:
#   scripts/db-connect.sh forward <dev|stg|prd>   # blocking: port-forward CNPG primary
#   scripts/db-connect.sh url     <dev|stg|prd>   # prints: export DATABASE_URL=...
#
# Typical flow (two terminals; make sure your kubectl context points at the env):
#   term 1:  make db-forward ENV=dev
#   term 2:  eval "$(make -s db-url ENV=dev)" && make run
#
# The freighter-backend-v2 CNPG cluster uses a basic-auth creds secret
# (username/password — NOT the default CNPG `-app` secret with a `uri` key), so
# `url` builds the connection string from those fields against the local tunnel.
set -euo pipefail

# ENV -> namespace. The CNPG cluster, creds secret, database, and user names are
# the same across environments (one cluster per service), so only the namespace
# varies. The database currently exists only in wallet-eng-dev; stg/prd are
# provisioned by the "Provision Postgres" work and may not exist yet.
env_namespace() {
  case "$1" in
    dev) echo "wallet-eng-dev" ;;
    stg) echo "wallet-eng-stg" ;;
    prd) echo "wallet-eng-prd" ;;
    *) return 1 ;;
  esac
}

CLUSTER="freighter-backend-v2-db"
SECRET="freighter-backend-v2-db-app-creds" # basic-auth: username + password
DBNAME="freighter-backend-v2"
RW_SVC="${CLUSTER}-rw"
LOCAL_PORT="${LOCAL_PORT:-5432}"

usage() { echo "usage: $0 {forward|url} <dev|stg|prd>" >&2; exit 1; }

[ $# -eq 2 ] || usage
action="$1"; env="$2"
ns="$(env_namespace "$env")" || usage

# b64decode reads stdin; tries GNU (--decode) then falls back to BSD (-D).
b64decode() { local s; s="$(cat)"; printf '%s' "$s" | base64 --decode 2>/dev/null || printf '%s' "$s" | base64 -D; }

# urlencode percent-encodes a string for safe inclusion in a URL (CNPG-generated
# passwords can contain '/', '+', '=', etc.).
urlencode() {
  if command -v python3 >/dev/null 2>&1; then
    python3 -c 'import urllib.parse,sys; print(urllib.parse.quote(sys.argv[1], safe=""))' "$1"
  else
    echo "WARN: python3 not found — password not URL-encoded; special characters may break DATABASE_URL." >&2
    printf '%s' "$1"
  fi
}

case "$action" in
  forward)
    echo "Port-forwarding ${RW_SVC} (ns ${ns}) to localhost:${LOCAL_PORT} — leave this running." >&2
    exec kubectl -n "$ns" port-forward "svc/${RW_SVC}" "${LOCAL_PORT}:5432"
    ;;
  url)
    user="$(kubectl -n "$ns" get secret "$SECRET" -o jsonpath='{.data.username}' | b64decode)"
    pass="$(kubectl -n "$ns" get secret "$SECRET" -o jsonpath='{.data.password}' | b64decode)"
    if [ -z "$user" ] || [ -z "$pass" ]; then
      echo "ERROR: could not read username/password from secret ${SECRET} in ns ${ns} (is the cluster provisioned there, and is your kubectl context set to ${env}?)." >&2
      exit 1
    fi
    # sslmode=require: CNPG serves TLS; we encrypt but don't verify the hostname
    # (the cert is for the in-cluster name, not localhost).
    echo "export DATABASE_URL='postgres://${user}:$(urlencode "$pass")@localhost:${LOCAL_PORT}/${DBNAME}?sslmode=require'"
    ;;
  *)
    usage
    ;;
esac
