#!/usr/bin/env bash
#
# Ensure a local Postgres has the app's role + database, creating them if
# missing. Connects as whatever superuser this machine provides:
#   - Homebrew's postgresql formula makes a superuser named after your macOS
#     user ($USER) — there is no "postgres" role by default.
#   - Linux / Docker images use a "postgres" superuser.
# We try $USER first, then "postgres", and use whichever connects.
#
# Invoked by `make db` (and therefore `make dev`). Tunables come in as env:
#   DB_NAME, DB_USER, DB_PASS.

set -euo pipefail

DB_NAME="${DB_NAME:-fleet_lite_app}"
DB_USER="${DB_USER:-dimo}"
DB_PASS="${DB_PASS:-dimo}"
HOST=localhost
PORT=5432
export PGCONNECT_TIMEOUT=5   # fail fast instead of hanging

brew_hint() {
  echo "    brew install postgresql@16"
  echo "    brew services start postgresql@16"
}

# 1. Is the psql client even installed?
if ! command -v psql >/dev/null 2>&1; then
  echo "✗ psql not found — install Postgres (client + server) with Homebrew:"
  brew_hint
  exit 1
fi

# 2. Find a superuser connection that works ($USER on brew, postgres elsewhere).
#    -w never prompts for a password, so a misconfigured auth fails fast.
ADMIN_USER=""
for candidate in "$USER" postgres; do
  [ -n "$candidate" ] || continue
  if psql -w -h "$HOST" -p "$PORT" -U "$candidate" -d postgres -tAc 'SELECT 1' >/dev/null 2>&1; then
    ADMIN_USER="$candidate"
    break
  fi
done

if [ -z "$ADMIN_USER" ]; then
  echo "✗ Couldn't connect to Postgres on $HOST:$PORT as a superuser (tried: $USER, postgres)."
  echo "  Make sure Postgres is installed and running:"
  brew_hint
  echo "  (check status with: brew services list)"
  exit 1
fi
echo "✓ connected to Postgres as superuser '$ADMIN_USER'"

admin() { psql -w -h "$HOST" -p "$PORT" -U "$ADMIN_USER" -d postgres "$@"; }

# 3. Create the app role if it doesn't exist.
if [ "$(admin -tAc "SELECT 1 FROM pg_roles WHERE rolname='$DB_USER'")" != "1" ]; then
  echo "▶ creating role '$DB_USER'…"
  admin -c "CREATE ROLE $DB_USER WITH LOGIN PASSWORD '$DB_PASS';"
fi

# 4. Create the database if it doesn't exist.
if [ "$(admin -tAc "SELECT 1 FROM pg_database WHERE datname='$DB_NAME'")" != "1" ]; then
  echo "▶ creating database '$DB_NAME'…"
  admin -c "CREATE DATABASE $DB_NAME WITH OWNER $DB_USER;"
fi

# 5. Verify the app's own credentials actually connect — catches a password
#    mismatch here, with a clear fix, rather than later in WaitForDB.
if ! PGPASSWORD="$DB_PASS" psql -w -h "$HOST" -p "$PORT" -U "$DB_USER" -d "$DB_NAME" -tAc 'SELECT 1' >/dev/null 2>&1; then
  echo "✗ role '$DB_USER' can't connect to '$DB_NAME' with the password the app expects."
  echo "  The backend connects as $DB_USER/$DB_PASS. Reset the role password with:"
  echo "    psql -h $HOST -U $ADMIN_USER -d postgres -c \"ALTER ROLE $DB_USER WITH LOGIN PASSWORD '$DB_PASS';\""
  exit 1
fi

echo "✓ postgres ready ($DB_USER can connect to $DB_NAME)"
