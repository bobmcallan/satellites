-- 0012_system_seeds.up.sql — registry of embedded system-seed artifacts.
--
-- system_seeds is the "what shipped in this binary" manifest. On every
-- satellites-server boot, the embed.FS bytes under config/seed/system/
-- are hashed and reconciled against this table:
--   - missing row  → insert + mirror to documents (scope=system)
--   - hash matches → no-op (zero writes)
--   - hash differs → update body+hash+applied_at + new document version
--
-- No verb writes to this table. Updates flow exclusively from the boot
-- reconciler, which itself runs from immutable embed.FS bytes — so the
-- table content is pinned to the release the binary was built from.
-- That's the substrate guarantee: "which prompt is running where" can't
-- drift, because mutating the prompts requires a binary release.
--
-- The documents table still serves reads via document_get (so the read
-- API is unchanged); system_seeds is the audit layer that records what
-- got baked in and when it was last applied.

CREATE TABLE system_seeds (
    id              TEXT PRIMARY KEY,                      -- sysd_<8hex>
    name            TEXT NOT NULL UNIQUE,
    body            TEXT NOT NULL,
    embedded_hash   TEXT NOT NULL,                         -- sha256 hex of body bytes
    applied_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
