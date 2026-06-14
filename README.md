# hostek

**Holistic service** für Live-Überwachung und Konfiguration eines headless Ubuntu-Servers.
hostek klinkt sich als Service in das [holistic](../holistic)-Dashboard ein: ein **Go-Daemon**
liefert Live-Metriken (CPU, RAM, GPU, System-SSD-I/O, Netz + Task-Manager-artige Prozessliste mit
GPU/Netz pro Prozess), ein Hardware-Inventar und eine Disk-Übersicht, und verwaltet OS-seitige
Server-Einstellungen; das Frontend ist ein **`@holistic/ui`-Plugin**.

## Architektur

```
Browser ── https://holistic.local (Caddy, same-origin) ─┐
  ├─ /                       → holistic SPA (enthält hostek-Plugin)
  ├─ /api/*                  → holistic FastAPI  (127.0.0.1:8770)
  └─ /api/services/hostek/*  → hostekd (Go)      (127.0.0.1:8771)
```

- **Single Sign-On:** hostekd validiert dieselbe holistic-Session (HS256-JWT im Cookie
  `h_access`, Secret `/etc/holistic/jwt-secret`) — kein eigener Login.
- **Rollen (holistic-Rights-Standard, Source of Truth = Linux):** **Admin = `sudo`-Gruppe**;
  Admins haben implizit alle Rechte und sehen alle Tabs. Standard-User sind **read-only** und
  sehen nur `System · Performance` (Performance ohne Temperatur-/Power-Werte; die System-SSD
  bleibt im System-Tab sichtbar). Feinere Rechte (Backing-Gruppe) werden per `privleg` an Nicht-Admins
  vergeben — der Daemon **und** die UI erzwingen `isAdmin || group ∈ user.groups` und redigieren
  gesperrte Werte:
  - `hp_hostek_powerinfo` — Energieinformationen (Watt) + **Power**-Tab
  - `hp_hostek_thermal` — Thermalinformationen (CPU/GPU/Disk) + **Thermal**-Tab
  - `hp_hostek_techinfo` — Software-Informationen (Betriebsstunden, Firmware, Treiber)
  - `hp_hostek_hwdetail` — Identitätsinformationen *(sensibel)*: Seriennummern, MAC-Adressen
  - `hp_hostek_disks` — der **Disks**-Tab (alle Datenträger)
  - `hp_hostek_proc` — Prozessliste + **Processes**-Tab
  - `hp_hostek_power` — OS-Energie/Headless **ändern** + **Config**-Tab (dangerous)
- **Least privilege:** Der Daemon läuft als unprivilegierter User `hostek`; Config-Schreib-
  zugriffe gehen ausschließlich über den schmalen sudo-Wrapper `hostek-power-set`.

## Layout

```
backend/        Go-Daemon (hostekd)
  cmd/hostekd/      entry point
  internal/auth/    shared-JWT validation + Linux-group/admin resolution + CSRF
  internal/metrics/ gopsutil sampling, ring buffer, per-process CPU% deltas, system-disk I/O
  internal/gpu/     NVIDIA sampling via nvidia-smi (overall + per-process)
  internal/netmon/  per-process network via the privileged hostek-netmon co-process
  internal/hardware/ hardware inventory (System tab) + all-disks list (Disks tab)
  internal/diskutil/ root block-device resolution (shared)
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

`setup` baut den Daemon, verdrahtet systemd + sudo + Caddy (inkl. der privilegierten read-only
Wrapper `hostek-hwinfo`/`hostek-netmon`), installiert best-effort die optionalen Probe-Tools
(`lshw dmidecode smartmontools nethogs i2c-tools pciutils`), verlinkt das UI-Plugin und baut die
Dashboard-SPA neu. Danach erscheint **„System"** in der holistic-Sidebar (Nicht-Admins sehen
*System · Performance · Disks*; Admins zusätzlich *Config* und *Processes*). `holistic update`
baut die SPA neu; hostek bleibt verlinkt.

Weitere Kommandos: `hostek build` (nur Daemon neu bauen), `hostek start|stop|restart`,
`hostek status`, `hostek power on|off`, `hostek update`, `hostek uninstall [--purge]`.

## API (`/api/services/hostek/`)

| Methode | Pfad | Rolle | Zweck |
|---|---|---|---|
| GET | `summary` | alle | Aggregat (CPU/RAM/GPU/SSD-I/O/Netz/Load); GPU-Temp/-Power je nach Recht redigiert |
| GET | `metrics` | alle | Zeitreihen (Ring-Buffer): CPU/RAM/GPU %, SSD read/write/busy, Netz |
| GET | `host` | alle | statische Host-Infos |
| GET | `hardware` | alle | Hardware-Inventar (Temp `thermal`, GPU-Power `powerinfo`, Firmware/Treiber `techinfo`, Serial/MAC `hwdetail`) |
| GET | `disks` | `disks` | alle Datenträger (Temp `thermal`, Firmware/Betriebsstunden `techinfo`, Serial `hwdetail`) |
| GET | `power` | `powerinfo` | Power-Telemetrie (CPU/GPU/Total Watt + 1/5/15-Mittel) |
| GET | `thermal` | `thermal` | Temperatur-Zeitreihen + kritische Schwellen |
| GET | `processes` | `proc` | Prozessliste (PID, CPU%, RAM, GPU%/Engine, Netz, Status) |
| GET | `config/power` | `power` | Headless/Always-on-Zustand (+ BIOS-Info) |
| POST | `config/power` | `power` | Headless-Settings anwenden (CSRF) |

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

- **GPU:** NVIDIA via `nvidia-smi` (unprivilegiert). Pro-Prozess-GPU ist **best-effort**
  (nur GPU-nutzende Prozesse via `nvidia-smi pmon`). Ohne NVIDIA-GPU werden die GPU-Bereiche
  ausgeblendet.
- **Netzwerk pro Prozess** ist unprivilegiert nicht erfassbar — es läuft über den optionalen
  privilegierten `hostek-netmon`-Helper (`nethogs`). Fehlt nethogs/Helper, zeigt die Spalte „—".
- **Hardware-Detail** (RAM-Module, Mainboard, SMART) kommt aus `dmidecode`/`smartctl` über
  `hostek-hwinfo`. **RAM-Timings** (CL-tRCD-…) brauchen SPD-Zugriff via `decode-dimms` (i2c) und
  sind je nach Board nicht verfügbar.
- **System-SSD** = das Block-Device hinter `/`; im Verlauf werden Lese/Schreib-Rate und Aktiv-Zeit
  gezeigt (nicht Belegung). Belegung aller Disks lebt im **Disks**-Tab.
- Live-Transport ist **Polling** (1–3 s) über den geteilten API-Client; SSE ist als spätere
  Optimierung vorgesehen.
- Erfordert Linux ≥ Go 1.22 zum Bauen (Ubuntu 24.04 `golang-go`).
