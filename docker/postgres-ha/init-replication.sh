#!/bin/bash
# Runs once on the primary's first init (docker-entrypoint-initdb.d). Creates the
# replication role the standby uses for pg_basebackup + streaming.
#
# REPLICATION_PASSWORD should be its own secret: the replicator can stream a full
# copy of the database, and sharing the superuser's password meant compromising
# either credential compromised both. docker-compose.postgres-ha.yml falls back
# to DB_PASS when it is unset so existing stacks keep working.
set -euo pipefail

REPLICATION_PASSWORD="${REPLICATION_PASSWORD:-$POSTGRES_PASSWORD}"
# Where the standby may connect from. Default "samenet" = the primary's own
# subnet(s), i.e. the compose network. Set a CIDR (e.g. 192.0.2.10/32) for a
# cross-box DR standby — and prefer TLS for that link (see the compose file).
REPLICATION_ALLOWED_CIDR="${REPLICATION_ALLOWED_CIDR:-samenet}"

# This value is appended verbatim to pg_hba.conf, so accept only an address /
# CIDR or one of pg_hba's address keywords — never free text.
case "$REPLICATION_ALLOWED_CIDR" in
	samenet|samehost|all) ;;
	*)
		if ! [[ "$REPLICATION_ALLOWED_CIDR" =~ ^[0-9A-Fa-f:.]+(/[0-9]{1,3})?$ ]]; then
			echo "init-replication: REPLICATION_ALLOWED_CIDR must be an IP/CIDR, samenet, samehost or all" >&2
			exit 1
		fi
		;;
esac

# The password reaches psql through the environment (\getenv, psql >= 15 — not
# argv, which `ps` shows) and is expanded with :'pw', which quotes it as an SQL
# literal. It used to be spliced into the SQL text by the
# shell ('$POSTGRES_PASSWORD'), so a quote in the password broke the statement
# or injected extra SQL. The quoted heredoc delimiter stops the shell from
# expanding anything inside. Statement logging is switched off for this session
# so the cleartext password can't land in the server log under log_statement.
export REPLICATION_PASSWORD
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<-'SQL'
	\getenv pw REPLICATION_PASSWORD
	SET log_statement = 'none';
	SET log_min_duration_statement = -1;
	CREATE ROLE replicator WITH REPLICATION LOGIN PASSWORD :'pw';
SQL

# The base image's default pg_hba has no host-replication entry, so the standby's
# pg_basebackup/streaming connection is rejected. Allow the replicator role in
# (password-authenticated, from the allowed source only) and reload.
echo "host replication replicator ${REPLICATION_ALLOWED_CIDR} scram-sha-256" >> "$PGDATA/pg_hba.conf"
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" \
	-c "SELECT pg_reload_conf();"
