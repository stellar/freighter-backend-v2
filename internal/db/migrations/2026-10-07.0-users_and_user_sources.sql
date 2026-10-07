-- Unified User Model v2: the first user-scoped tables.
--
-- `users.id` is the internal foreign key for every user-scoped table that
-- follows. `users.canonical_source_id` is the user id as exposed to clients.
-- Proofs of possession are verified at link time and discarded: no table here
-- stores a consent signature.

-- +migrate Up

CREATE TABLE users (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    canonical_source_id TEXT NOT NULL, -- the root source: the sub that created this user; THE user id, as exposed; never changes
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON COLUMN users.canonical_source_id IS
    'The root source (hex ed25519 pubkey) that created this user. This is THE user id as exposed to clients. Set once on insert; no code path updates it.';

-- every seed phrase and every account secret key the user has proven possession of;
-- roughly one row per account plus one per phrase
CREATE TABLE user_sources (
    source_id   TEXT PRIMARY KEY,              -- hex ed25519 pubkey
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind        TEXT NOT NULL,                 -- 'phrase' | 'secret_key'; read from the signed consent
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The primary key is source_id, so lookups by user need their own index.
CREATE INDEX user_sources_user_id_idx ON user_sources (user_id);

-- +migrate Down

DROP TABLE user_sources;
DROP TABLE users;
