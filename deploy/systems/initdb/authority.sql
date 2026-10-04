-- uspace-authority bootstrap on the droplet's one timescaledb-ha
-- container (plan D3, M37). POSTGRES_DB creates the relational database
-- `authority`; this creates the telemetry database beside it, as the
-- authority's development stack holds them (two containers there, one
-- here). Extensions and roles are created by the authority's own
-- migrations (`uspace-authority migrate`), which is why its processes log
-- in as the superuser and SET ROLE (PG_ROLE, TS_*_ROLE).
CREATE DATABASE authority_ts;
