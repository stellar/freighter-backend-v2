-- Unified User Model v2: the first user-scoped tables.
--
-- `users.id` is the internal foreign key for every user-scoped table that
-- follows. `users.canonical_source_id` is the user id as exposed to clients.
-- Proofs of possession are verified at link time and discarded: no table here
-- stores a consent signature.
--
-- The CHECK constraints are the last guard on rows that are never retired: a
-- source id must be the canonical form auth.IsCanonicalSourceID accepts (64
-- lowercase hex characters, the form ResolveUser looks up), and a kind must be
-- auth.SourceKindPhrase or auth.SourceKindSecretKey. The link handler rejects
-- anything else first; these catch a caller that skipped it.

-- +migrate Up

CREATE TABLE users (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    canonical_source_id TEXT NOT NULL UNIQUE, -- the root source: the sub that created this user; THE user id, as exposed; never changes. UNIQUE doubles as its lookup index.
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT users_canonical_source_id_canonical CHECK (canonical_source_id ~ '^[0-9a-f]{64}$')
);

COMMENT ON COLUMN users.canonical_source_id IS
    'The root source (hex ed25519 pubkey) that created this user. This is THE user id as exposed to clients. Set once on insert; no code path updates it.';

-- every seed phrase and every account secret key the user has proven possession of;
-- roughly one row per account plus one per phrase
CREATE TABLE user_sources (
    source_id   TEXT PRIMARY KEY,              -- hex ed25519 pubkey
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind        TEXT NOT NULL,                 -- 'phrase' | 'secret_key'; read from the signed consent
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT user_sources_source_id_canonical CHECK (source_id ~ '^[0-9a-f]{64}$'),
    CONSTRAINT user_sources_kind_known CHECK (kind IN ('phrase', 'secret_key'))
);

-- The primary key is source_id, so lookups by user need their own index.
CREATE INDEX user_sources_user_id_idx ON user_sources (user_id);

-- +migrate Down

DROP TABLE user_sources;
DROP TABLE users;
