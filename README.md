# hostek

**Holistic service** für Live-Überwachung und Konfiguration eines headless Ubuntu-Servers.
hostek klinkt sich als Service in das [holistic](../holistic)-Dashboard ein: ein **Go-Daemon**
liefert Live-Metriken (CPU, RAM, Disk, Netz + Task-Manager-artige Prozessliste) und verwaltet
OS-seitige Server-Einstellungen; das Frontend ist ein **`@holistic/ui`-Plugin**.

## Architektur

```
Browser ── https://holistic.local (Caddy, same-origin) ─┐
  ├─ /                       → holistic SPA (enthält hostek-Plugin)
  ├─ /api/*                  → holistic FastAPI  (127.0.0.1:8770)
  └─ /api/services/hostek/*  → hostekd (Go)      (127.0.0.1:8771)
```

- **Single Sign-On:** hostekd validiert dieselbe holistic-Session (HS256-JWT im Cookie
  `h_access`, Secret `/etc/holistic/jwt-secret`) — kein eigener Login.
- **Rollen (Single Source of Truth = Linux):** **Admin = Mitglied der `sudo`-Gruppe**.
  Admins sehen alle Metriken inkl. Prozesse und dürfen konfigurieren; alle anderen sehen
  nur die Gesamtauslastung pro Komponente.
- **Least privilege:** Der Daemon läuft als unprivilegierter User `hostek`; Config-Schreib-
  zugriffe gehen ausschließlich über den schmalen sudo-Wrapper `hostek-power-set`.

## Layout

```
backend/        Go-Daemon (hostekd)
  cmd/hostekd/      entry point
  internal/auth/    shared-JWT validation + Linux-group/admin resolution + CSRF
  internal/metrics/ gopsutil sampling, ring buffer, per-process CPU% deltas
  internal/sysconfig/ read/apply headless power settings
  internal/api/     HTTP routes under /api/services/hostek/
ui/             @holistic/ui plugin (linked into holistic/frontend/external/hostek)
hostek          single-file CLI: setup/build/lifecycle. Generates the systemd unit,
                Caddy route, sudoers drop-in + privileged power wrapper inline (no deploy/ tree).
```

## Install

Voraussetzung: das **holistic**-Repo (mit externer-Plugin- + Caddy-import-Unterstützung)
ist vorhanden und das Dashboard installiert.

```bash
sudo ./hostek setup        # HOLISTIC_REPO wird autodetektiert (../holistic, /code/holistic, …)
```

`setup` baut den Daemon, verdrahtet systemd + sudo + Caddy, verlinkt das UI-Plugin und baut
die Dashboard-SPA neu. Danach erscheint **„System"** in der holistic-Sidebar (Admins zusätzlich
mit *Processes* und *Config*). `holistic update` baut die SPA neu; hostek bleibt verlinkt.

Weitere Kommandos: `hostek build` (nur Daemon neu bauen), `hostek start|stop|restart`,
`hostek status`, `hostek power on|off`, `hostek update`, `hostek uninstall [--purge]`.

## API (`/api/services/hostek/`)

| Methode | Pfad | Rolle | Zweck |
|---|---|---|---|
| GET | `summary` | alle | Aggregat (CPU/RAM/Disk/Netz/Load/Uptime) |
| GET | `metrics` | alle | Zeitreihen (Ring-Buffer) für Charts |
| GET | `host` | alle | statische Host-Infos |
| GET | `processes` | **admin** | Prozessliste (PID, CPU%, RAM, Status) |
| GET | `config/power` | **admin** | Headless/Always-on-Zustand (+ BIOS-Info) |
| POST | `config/power` | **admin** | Headless-Settings anwenden (CSRF) |

Fehler folgen holistics Vertrag: `{"detail": "..."}`.

## Konfiguration: „immer an / headless"

`POST config/power {headless:true}` setzt OS-seitig: `HandleLidSwitch=ignore` (logind-Drop-in)
und maskiert `sleep/suspend/hibernate`. Die UEFI-Einstellung **`Restore AC Power Loss = Power On`**
ist firmware-seitig (bereits gesetzt, siehe `My UEFI Config/`) und wird nur **informativ** angezeigt.

## Entwicklung (macOS)

```bash
# Backend (gopsutil ist cross-platform; logind/Wrapper sind Linux-only und werden geguarded)
cd backend && go build ./... && go vet ./...

# Frontend-Plugin im holistic-Dashboard (holistic als Geschwister-Repo)
ln -sfn "$PWD/ui" ../holistic/frontend/external/hostek
( cd ../holistic/frontend && pnpm --filter @holistic/app dev )   # http://localhost:5173
```

## Bekannte Grenzen (v1)

- **Pro-Prozess** werden CPU%, RAM (RSS) und Status erfasst; **Netzwerk pro Prozess** ist
  unprivilegiert nicht zuverlässig (Netz nur auf System-Ebene).
- Live-Transport ist **Polling** (1–2 s) über den geteilten API-Client; SSE ist als spätere
  Optimierung vorgesehen.
- Erfordert Linux ≥ Go 1.22 zum Bauen (Ubuntu 24.04 `golang-go`).
