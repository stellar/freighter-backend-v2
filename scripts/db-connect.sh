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
# Typical flow (two terminals):
#   term 1:  make db-forward ENV=dev
#   term 2:  eval "$(make -s db-url ENV=dev)" && make run
#
# CNPG auto-generates a secret "<cluster>-app" containing a ready-made `uri` key;
# `url` reads it and rewrites the host:port to the local forwarded port.
set -euo pipefail

# ---------------------------------------------------------------------------
# ENV -> (namespace, CNPG cluster) mapping.
# TODO: fill these in once the Provision Postgres work lands. The DB currently
# exists only in wallet-eng-dev; stg/prd are placeholders.
# ---------------------------------------------------------------------------
env_namespace() {
  case "$1" in
    dev) echo "REPLACE_ME_dev_namespace" ;;
    stg) echo "REPLACE_ME_stg_namespace" ;;
    prd) echo "REPLACE_ME_prd_namespace" ;;
    *) return 1 ;;
  esac
}
env_cluster() {
  case "$1" in
    dev) echo "REPLACE_ME_dev_cnpg_cluster" ;;
    stg) echo "REPLACE_ME_stg_cnpg_cluster" ;;
    prd) echo "REPLACE_ME_prd_cnpg_cluster" ;;
    *) return 1 ;;
  esac
}

LOCAL_PORT="${LOCAL_PORT:-5432}"

usage() { echo "usage: $0 {forward|url} <dev|stg|prd>" >&2; exit 1; }

[ $# -eq 2 ] || usage
action="$1"; env="$2"

ns="$(env_namespace "$env")" || usage
cluster="$(env_cluster "$env")" || usage

# Fail clearly if the ENV->namespace/cluster mapping is still the placeholder,
# instead of passing a literal REPLACE_ME name to kubectl and getting an opaque
# "not found" error.
case "$ns$cluster" in
  *REPLACE_ME*)
    echo "ERROR: ENV '$env' is not configured yet — fill in the namespace/cluster mapping in $0 (see the TODO block)." >&2
    exit 1
    ;;
esac

# b64decode reads stdin; tries GNU (--decode) then falls back to BSD (-D).
b64decode() { local s; s="$(cat)"; printf '%s' "$s" | base64 --decode 2>/dev/null || printf '%s' "$s" | base64 -D; }

case "$action" in
  forward)
    echo "Port-forwarding ${cluster}-rw (ns ${ns}) to localhost:${LOCAL_PORT} — leave this running." >&2
    exec kubectl -n "$ns" port-forward "svc/${cluster}-rw" "${LOCAL_PORT}:5432"
    ;;
  url)
    uri="$(kubectl -n "$ns" get secret "${cluster}-app" -o jsonpath='{.data.uri}' | b64decode)"
    if [ -z "$uri" ]; then
      echo "ERROR: secret ${cluster}-app in ns ${ns} has no 'uri' key (or it is empty)." >&2
      exit 1
    fi
    # Rewrite only the @host:port segment to the local tunnel, preserving creds,
    # dbname and query params. [^@/?]+ stops at '/', '?', or end, so it works
    # whether or not a /dbname path follows and never consumes the query string.
    rewritten="$(printf '%s' "$uri" | sed -E "s#@[^@/?]+#@localhost:${LOCAL_PORT}#")"
    echo "export DATABASE_URL='${rewritten}'"
    ;;
  *)
    usage
    ;;
esac
