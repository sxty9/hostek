import {
  Badge,
  Box,
  DiskIcon,
  EmptyState,
  Grid,
  Panel,
  ProgressBar,
  Spinner,
  SsdIcon,
  Stack,
  Text,
  formatBytes,
  useLiveQuery,
  type ServiceContextProps,
} from '@holistic/ui';
import type { DiskDevice, DisksResponse } from './types';

function diskUsed(d: DiskDevice): number {
  return (d.partitions ?? []).reduce((sum, p) => sum + (p.used ?? 0), 0);
}

function DiskCard({ d }: { d: DiskDevice }) {
  const used = diskUsed(d);
  const usedPct = d.sizeBytes > 0 ? (used / d.sizeBytes) * 100 : 0;
  const mounts = (d.partitions ?? []).filter((p) => p.mount);
  const Icon = d.rotational ? DiskIcon : SsdIcon;

  return (
    <Panel className="p-4">
      <Stack gap={3}>
        <Stack direction="row" align="center" gap={3}>
          <Box className="flex h-9 w-9 shrink-0 items-center justify-center rounded-md bg-ssd/15">
            <Icon className="h-5 w-5 text-ssd" />
          </Box>
          <Stack gap={0} className="min-w-0 grow">
            <Stack direction="row" align="center" gap={2}>
              <Text weight="semibold" truncate>
                {d.model || d.name}
              </Text>
              {d.isSystem && <Badge variant="accent">System</Badge>}
              {d.type && <Badge variant="neutral">{d.type}</Badge>}
            </Stack>
            <Text variant="caption" color="secondary" truncate>
              /dev/{d.name}
              {d.port ? ` · ${d.port}` : d.transport ? ` · ${d.transport.toUpperCase()}` : ''}
              {d.serial ? ` · ${d.serial}` : ''}
            </Text>
          </Stack>
          <Text variant="subhead" weight="semibold" className="tabular-nums shrink-0">
            {formatBytes(d.sizeBytes)}
          </Text>
        </Stack>

        <Stack gap={1}>
          <ProgressBar value={usedPct} tone="ssd" />
          <Text variant="caption" color="secondary">
            {formatBytes(used)} used · {formatBytes(Math.max(0, d.sizeBytes - used))} free
          </Text>
        </Stack>

        {mounts.length > 0 && (
          <Stack gap={1}>
            {mounts.map((p) => (
              <Stack key={p.name} direction="row" justify="between" gap={3} align="baseline">
                <Text variant="caption" truncate>
                  {p.mount} <Text as="span" variant="caption" color="tertiary">{p.fstype}</Text>
                </Text>
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
