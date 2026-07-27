import { Badge, useT } from '@holisdk/ui';
import type { SmartHealth } from './types';

// Join the truthy parts with " · " (drops empty/undefined/false) — hostek's spec-line
// separator, shared by the System and Disks cards so subtitles read the same everywhere.
export const joinDot = (...parts: (string | undefined | false | null)[]): string => parts.filter(Boolean).join(' · ');

// A drive's SMART health as a Badge: the derived verdict (healthy/warning/critical) when the
// backend supplies one, else the raw smartctl PASS/FAIL string. A single component so the
// health pill reads identically on the System-tab system-disk card and every Disks-tab card —
// both feed it the shared SmartHealth shape. Renders nothing when a drive reports no health.
export function DiskHealthBadge({ disk }: { disk: SmartHealth }) {
  const t = useT();
  const { healthStatus, health } = disk;
  if (healthStatus) {
    return (
      <Badge variant={healthStatus === 'critical' ? 'danger' : healthStatus === 'warning' ? 'warning' : 'success'}>
        {t(healthStatus === 'critical' ? 'hostek.statusCritical' : healthStatus === 'warning' ? 'hostek.statusWarning' : 'hostek.statusHealthy')}
      </Badge>
    );
  }
  if (health) {
    return <Badge variant={health.toUpperCase().includes('PASS') ? 'success' : 'warning'}>{health}</Badge>;
  }
  return null;
}
