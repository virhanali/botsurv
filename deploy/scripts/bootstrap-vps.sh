#!/usr/bin/env bash
set -euo pipefail

if [ "$(id -u)" -ne 0 ]; then
  echo "Run as root." >&2
  exit 1
fi

APP_USER="${APP_USER:-botsurv}"
APP_HOME="${APP_HOME:-/opt/botsurv}"
DB_NAME="${DB_NAME:-botsurv}"
DB_USER="${DB_USER:-botsurv}"
DB_PASSWORD="${DB_PASSWORD:-botsurv}"

apt-get update
apt-get install -y ca-certificates curl git postgresql postgresql-contrib

if ! id "$APP_USER" >/dev/null 2>&1; then
  useradd --system --home "$APP_HOME" --shell /usr/sbin/nologin "$APP_USER"
fi

install -d -o "$APP_USER" -g "$APP_USER" "$APP_HOME" "$APP_HOME/releases" /var/lib/botsurv /var/log/botsurv
install -d -m 0750 /etc/botsurv

systemctl enable --now postgresql

sudo -u postgres psql -v ON_ERROR_STOP=1 <<SQL
DO \$\$
BEGIN
   IF NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = '${DB_USER}') THEN
      CREATE ROLE ${DB_USER} LOGIN PASSWORD '${DB_PASSWORD}';
   ELSE
      ALTER ROLE ${DB_USER} WITH PASSWORD '${DB_PASSWORD}';
   END IF;
END
\$\$;

SELECT 'CREATE DATABASE ${DB_NAME} OWNER ${DB_USER}'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = '${DB_NAME}')\gexec
SQL

cat >/etc/botsurv/paper.env <<EOF
DATABASE_DSN=postgres://${DB_USER}:${DB_PASSWORD}@localhost:5432/${DB_NAME}?sslmode=disable
OPENROUTER_API_KEY=
OPENROUTER_SITE_URL=
OPENROUTER_APP_NAME=BotSurv
TELEGRAM_BOT_TOKEN=
TELEGRAM_CHAT_ID=
ENABLE_LIVE_TRADING=false
EOF

chmod 0640 /etc/botsurv/paper.env
chown root:"$APP_USER" /etc/botsurv/paper.env

echo "Bootstrap complete."
echo "Edit /etc/botsurv/paper.env if you change DB password or enable optional integrations."

