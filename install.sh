#!/usr/bin/env bash
set -euo pipefail

# hostek installer — registers hostek as a holistic service. Idempotent; run as root.
#   • builds the Go daemon -> /opt/hostek/bin/hostekd + systemd unit (127.0.0.1:8771)
#   • installs the privileged power wrapper + a dedicated /etc/sudoers.d/hostek
#   • grants read on the shared JWT secret so it validates the dashboard session
#   • drops a Caddy route into /etc/caddy/conf.d (imported by holistic's Caddyfile)
#   • links the @holistic/ui plugin into holistic and rebuilds the dashboard SPA
#
# Requires the holistic repo (env HOLISTIC_REPO, default /code/holistic) with the
# external-plugin + Caddy-import support installed.

[[ $EUID -eq 0 ]] || { echo "[hostek] ERROR: run as root (sudo)" >&2; exit 1; }

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
HOLISTIC_REPO="${HOLISTIC_REPO:-/code/holistic}"
APP=/opt/hostek

[[ -d "$HOLISTIC_REPO" ]] || { echo "[hostek] ERROR: holistic repo not found at $HOLISTIC_REPO (set HOLISTIC_REPO)" >&2; exit 1; }

echo "[hostek] system user + directories..."
getent group hostek  >/dev/null || groupadd --system hostek
getent passwd hostek >/dev/null || useradd --system --gid hostek --shell /usr/sbin/nologin --home-dir /var/lib/hostek hostek
install -d -o hostek -g hostek -m 0755 "$APP" "$APP/bin" /etc/hostek /var/lib/hostek

if ! command -v go >/dev/null; then
    echo "[hostek] installing Go toolchain..."
    apt-get update -qq
    apt-get install -y -qq golang-go >/dev/null
fi

echo "[hostek] building hostekd..."
( cd "$HERE/backend" && GOCACHE=/tmp/hostek-gocache go build -o "$APP/bin/hostekd" ./cmd/hostekd )
chown -R hostek:hostek "$APP"

echo "[hostek] installing privileged power wrapper + sudoers..."
install -m 0750 -o root -g root "$HERE/deploy/sbin/hostek-power-set" /usr/local/sbin/hostek-power-set
install -m 0440 -o root -g root "$HERE/deploy/sudoers.d/hostek" /etc/sudoers.d/hostek
if ! visudo -cf /etc/sudoers.d/hostek >/dev/null; then
    rm -f /etc/sudoers.d/hostek
    echo "[hostek] ERROR: sudoers validation failed; removed" >&2
    exit 1
fi

echo "[hostek] granting read on the shared JWT secret..."
if [[ -f /etc/holistic/jwt-secret ]]; then
    getent group holistic >/dev/null || { echo "[hostek] ERROR: group 'holistic' missing — install the dashboard first" >&2; exit 1; }
    usermod -aG holistic hostek          # NOT swallowed: must succeed or the daemon can't read the secret
    chgrp holistic /etc/holistic/jwt-secret
    chmod 0640 /etc/holistic/jwt-secret  # intentional: scoped to the holistic + hostek service accounts
else
    echo "[hostek] WARNING: /etc/holistic/jwt-secret not found — install the holistic dashboard first" >&2
fi

echo "[hostek] configuring systemd..."
install -m 0644 "$HERE/deploy/systemd/hostek.service" /etc/systemd/system/hostek.service
systemctl daemon-reload
systemctl enable hostek >/dev/null 2>&1 || true

echo "[hostek] adding Caddy route..."
install -d /etc/caddy/conf.d
install -m 0644 "$HERE/deploy/caddy/hostek.caddy" /etc/caddy/conf.d/hostek.caddy

echo "[hostek] linking UI plugin into holistic + rebuilding dashboard SPA..."
install -d "$HOLISTIC_REPO/frontend/external"
ln -sfn "$HERE/ui" "$HOLISTIC_REPO/frontend/external/hostek"
# Rebuilds the SPA (now bundling hostek's plugin), recopies www, reinstalls the
# import-enabled Caddyfile, and reloads Caddy — picking up conf.d/hostek.caddy.
"$HOLISTIC_REPO/holistic" setup dashboard

systemctl restart hostek
if ! systemctl is-active --quiet hostek; then
    echo "[hostek] ERROR: hostek failed to start:" >&2
    journalctl -u hostek -n 20 --no-pager >&2 || true
    exit 1
fi

echo "[hostek] installed and started"
