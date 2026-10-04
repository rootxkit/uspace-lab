-- uspace-ansp bootstrap, as its own compose's `initdb` config
-- (vendor/uspace-ansp/deploy/compose.yaml): POSTGRES_DB creates `ansp`
-- (relational); this creates `ansp_ts` (TimescaleDB) beside it. The
-- extensions and roles are created by the migrations (WP-1).
CREATE DATABASE ansp_ts;
