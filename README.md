# hostek

**Holistic Service** zur Live-Überwachung und Verwaltung eines headless Linux-Servers (Ubuntu).

hostek vereint zwei Rollen in einem Dienst:

1. **Live-Hardware-Reporting** — Echtzeit-Metriken über den Server (CPU, RAM, Disk, Netzwerk) und – für Admins – eine prozessgenaue Aufschlüsselung, welche Prozesse welche Komponenten beanspruchen (inspiriert vom Windows Task-Manager).
2. **Server-Konfiguration** — alle technischen Einstellungen des Servers werden über hostek vorgenommen (Backend zum Verwalten des Ubuntu-Servers).

## Rollen- & Rechtemodell

Die Wahrheit über Berechtigungen sind die **Linux-User**:

- **Admin** = Linux-User mit `sudo`-Rechten. Nur Admins dürfen **alle** Metriken global erfassen — inklusive der laufenden Prozesse und deren Ressourcenverbrauch.
- **Standard-User** sehen ausschließlich die **Gesamtauslastung** einzelner Hardware-Komponenten — keine einzelnen Prozesse.

## Zielsystem (Referenz-Hardware)

Der konkrete Server, für den hostek gebaut wird:

| Komponente | Wert |
|---|---|
| Mainboard | ASUS ROG (AMD-Plattform, AMI BIOS 2.20.1271) |
| RAM | DDR4-3200 (D.O.C.P. aktiv), 1.35 V |
| CPU | AMD, Basis ~3000 MHz |
| Storage | Crucial MX500 1 TB (SSD) + Seagate Barracuda ST1000DM010 1 TB (HDD) |
| OS | Ubuntu (headless) |

### Headless / „immer an"

Ein klassisches Must-Have für reine Server: Der Rechner läuft auch **ohne angeschlossenen Monitor** und fährt nach Stromausfall selbstständig wieder hoch.

Die zugehörige UEFI-Einstellung ist bereits gesetzt (siehe `My UEFI Config/`):

> `Restore AC Power Loss: Power Off → Power On`

## Status

🚧 Initiales Repo. Backend- und Frontend-Implementierung folgen in der Planphase
(siehe `CLAUDE.MD` für die Projekt-Leitlinien).
