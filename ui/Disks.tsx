import {
  Badge,
  Box,
  DiskIcon,
  Donut,
  EmptyState,
  Grid,
  Marquee,
  Panel,
  Spinner,
  SsdIcon,
  Stack,
  Text,
  formatBytes,
  useLiveQuery,
  type ServiceContextProps,
} from '@holistic/ui';
import type { DiskDevice, DisksResponse } from './types';

const join = (...parts: (string | undefined | false)[]) => parts.filter(Boolean).join(' · ');

function DiskCard({ d }: { d: DiskDevice }) {
  const mounts = (d.partitions ?? []).filter((p) => p.mount);
  // Usage is filesystem-level (sum over mounted partitions) so the donut tracks the
  // partition rows below; fall back to the raw device size when nothing is mounted.
  // Clamp so unusual backend numbers (e.g. overlapping mounts) can't overdraw the ring.
  const fsTotal = mounts.reduce((s, p) => s + (p.total ?? 0), 0);
  const fsUsed = mounts.reduce((s, p) => s + (p.used ?? 0), 0);
  const total = fsTotal > 0 ? fsTotal : d.sizeBytes;
  const usedPct = total > 0 ? Math.min(100, (fsUsed / total) * 100) : 0;
  const hasUsage = mounts.length > 0 && fsUsed > 0;
  const Icon = d.rotational ? DiskIcon : SsdIcon;
  const subtitle = join(`/dev/${d.name}`, d.port || (d.transport && d.transport.toUpperCase()) || '', d.serial || '');

  return (
    <Panel className="p-4">
      <Stack gap={3}>
        {/* Header */}
        <Stack direction="row" align="center" gap={3}>
          <Box className="flex h-9 w-9 shrink-0 items-center justify-center rounded-md bg-ssd/15">
            <Icon className="h-5 w-5 text-ssd" />
          </Box>
          <Stack gap={0} className="min-w-0 grow">
            <Stack direction="row" align="center" gap={2}>
              <Marquee text={d.model || d.name} className="min-w-0 grow text-subhead font-semibold text-text-primary" />
              {d.isSystem && <Badge variant="accent">System</Badge>}
              {d.type && <Badge variant="neutral">{d.type}</Badge>}
            </Stack>
            <Marquee text={subtitle} className="text-caption text-text-secondary" />
          </Stack>
        </Stack>

        {/* Usage donut + figures */}
        <Stack direction="row" align="center" gap={4}>
          <Donut
            segments={[{ value: fsUsed, color: 'rgb(var(--ssd))' }]}
            total={total || 1}
            size={92}
            thickness={11}
            center={
              hasUsage ? (
                <Stack gap={0} align="center">
                  <Text variant="subhead" weight="semibold" className="tabular-nums">
                    {usedPct.toFixed(0)}%
                  </Text>
                  <Text variant="caption" color="tertiary">
                    used
                  </Text>
                </Stack>
              ) : (
                <Text variant="caption" color="tertiary">
                  free
                </Text>
              )
            }
          />
          <Stack gap={1} grow className="min-w-0">
            <Stack direction="row" justify="between" gap={2} align="baseline">
              <Text variant="footnote" color="secondary">
                Capacity
              </Text>
              <Text variant="footnote" weight="medium" className="tabular-nums">
                {formatBytes(d.sizeBytes)}
              </Text>
            </Stack>
            <Stack direction="row" justify="between" gap={2} align="baseline">
              <Text variant="footnote" color="secondary">
                Used
              </Text>
              <Text variant="footnote" className="tabular-nums">
                {hasUsage ? `${formatBytes(fsUsed)} · ${usedPct.toFixed(0)}%` : '—'}
              </Text>
            </Stack>
            <Stack direction="row" justify="between" gap={2} align="baseline">
              <Text variant="footnote" color="secondary">
                Free
              </Text>
              <Text variant="footnote" className="tabular-nums">
                {formatBytes(Math.max(0, total - fsUsed))}
              </Text>
            </Stack>
          </Stack>
        </Stack>

        {/* SMART (symmetric with the System-tab disk card) */}
        {(d.health || d.tempC || d.firmware || d.powerOnHours) && (
          <Stack gap={1} className="border-t border-separator pt-2">
            {d.health && (
              <Stack direction="row" justify="between" gap={2} align="baseline">
                <Text variant="footnote" color="secondary">
                  Health
                </Text>
                <Badge variant={d.health.toUpperCase().includes('PASS') ? 'success' : 'warning'}>{d.health}</Badge>
              </Stack>
            )}
            {d.tempC ? (
              <Stack direction="row" justify="between" gap={2} align="baseline">
                <Text variant="footnote" color="secondary">
                  Temperature
                </Text>
                <Text variant="footnote" className="tabular-nums">
                  {Math.round(d.tempC)} °C
                </Text>
              </Stack>
            ) : null}
            {d.firmware && (
              <Stack direction="row" justify="between" gap={2} align="baseline">
                <Text variant="footnote" color="secondary">
                  Firmware
                </Text>
                <Text variant="footnote" className="tabular-nums">
                  {d.firmware}
                </Text>
              </Stack>
            )}
            {d.powerOnHours ? (
              <Stack direction="row" justify="between" gap={2} align="baseline">
                <Text variant="footnote" color="secondary">
                  Power-on hours
                </Text>
                <Text variant="footnote" className="tabular-nums">
                  {d.powerOnHours.toLocaleString()}
                </Text>
              </Stack>
            ) : null}
          </Stack>
        )}

        {/* Mounted partitions */}
        {mounts.length > 0 && (
          <Stack gap={1} className="border-t border-separator pt-2">
            {mounts.map((p) => (
              <Stack key={p.name} direction="row" justify="between" gap={3} align="baseline">
                <Stack direction="row" gap={1} align="baseline" className="min-w-0">
                  <Text variant="caption" truncate>
                    {p.mount}
                  </Text>
                  <Text variant="caption" color="tertiary">
                    {p.fstype}
                  </Text>
                </Stack>
                <Text variant="caption" color="secondary" className="tabular-nums shrink-0">
                  {formatBytes(p.used ?? 0)} / {formatBytes(p.total ?? p.sizeBytes)} · {(p.percent ?? 0).toFixed(0)}%
                </Text>
              </Stack>
            ))}
          </Stack>
        )}
      </Stack>
    </Panel>
  );
}

export function Disks({ api }: ServiceContextProps) {
  const { data } = useLiveQuery<DisksResponse>(() => api.get<DisksResponse>('disks'), 5000);

  if (!data) {
    return (
      <Stack align="center" justify="center" className="py-16">
        <Spinner />
      </Stack>
    );
  }

  // System disk first, then alphabetically by device name.
  const disks = [...(data.disks ?? [])].sort((a, b) => Number(b.isSystem) - Number(a.isSystem) || a.name.localeCompare(b.name));

  if (disks.length === 0) {
    return <EmptyState icon={<DiskIcon />} title="No disks" description="No block devices were reported." />;
  }

  return (
    <Stack gap={3}>
      <Text variant="subhead" weight="semibold">
        {disks.length} disk{disks.length > 1 ? 's' : ''}
      </Text>
      <Grid minItemWidth={360} gap={3}>
        {disks.map((d) => (
          <DiskCard key={d.name} d={d} />
        ))}
      </Grid>
    </Stack>
  );
}
