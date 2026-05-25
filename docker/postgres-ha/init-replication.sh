#!/bin/bash
# Runs once on the primary's first init (docker-entrypoint-initdb.d). Creates the
# replication role the standby uses for pg_basebackup + streaming. Reuses
# POSTGRES_PASSWORD so the stack has a single credential.
set -euo pipefail

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<-SQL
	CREATE ROLE replicator WITH REPLICATION LOGIN PASSWORD '$POSTGRES_PASSWORD';
SQL

# The base image's default pg_hba has no host-replication entry, so the standby's
# pg_basebackup/streaming connection is rejected. Allow the replicator role in
# (password-authenticated) and reload.
echo "host replication replicator all scram-sha-256" >> "$PGDATA/pg_hba.conf"
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" \
	-c "SELECT pg_reload_conf();"
