import {
  Badge,
  Box,
  DiskIcon,
  Donut,
  EmptyState,
  Grid,
  Marquee,
  Panel,
  ProgressBar,
  Spinner,
  SsdIcon,
  Stack,
  Text,
  formatBytes,
  useLiveQuery,
  useT,
  type ServiceContextProps,
} from '@holistic/ui';
import type { ReactNode } from 'react';
import type { DiskDevice, DisksResponse } from './types';

const join = (...parts: (string | undefined | false)[]) => parts.filter(Boolean).join(' · ');

// Health verdict → Badge variant. Anything but critical/warning reads as healthy.
const statusVariant = (s: DiskDevice['healthStatus']): 'success' | 'warning' | 'danger' =>
  s === 'critical' ? 'danger' : s === 'warning' ? 'warning' : 'success';

// Remaining-life % → bar tone (green→amber→red as endurance runs out).
const lifeTone = (p: number): 'ssd' | 'warning' | 'danger' => (p <= 10 ? 'danger' : p <= 25 ? 'warning' : 'ssd');

// One label/value row in the SMART block; a string value is rendered as tabular text,
// anything else (e.g. a Badge) is rendered as-is.
function InfoRow({ label, children }: { label: string; children: ReactNode }) {
  return (
    <Stack direction="row" justify="between" gap={2} align="baseline">
      <Text variant="footnote" color="secondary">
        {label}
      </Text>
      {typeof children === 'string' ? (
        <Text variant="footnote" className="tabular-nums">
          {children}
        </Text>
      ) : (
        children
      )}
    </Stack>
  );
}

function DiskCard({ d }: { d: DiskDevice }) {
  const t = useT();
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

  const statusLabel: Record<'healthy' | 'warning' | 'critical', string> = {
    healthy: t('hostek.statusHealthy'),
    warning: t('hostek.statusWarning'),
    critical: t('hostek.statusCritical'),
  };

  // Raw SMART/NVMe counters — only present when the viewer holds the techinfo right
  // (the backend nils the whole object otherwise). 0 is a real, reassuring value.
  const raw = d.smart;
  const smartRows: { label: string; value: string }[] = [];
  if (raw) {
    const numOf = (v?: number) => (typeof v === 'number' ? v.toLocaleString() : undefined);
    const add = (label: string, value: string | undefined) => {
      if (value !== undefined) smartRows.push({ label, value });
    };
    add(t('hostek.reallocated'), numOf(raw.reallocatedSectors));
    add(t('hostek.pendingSectors'), numOf(raw.pendingSectors));
    add(t('hostek.uncorrectable'), numOf(raw.offlineUncorrectable ?? raw.reportedUncorrect));
    if (typeof raw.availableSpare === 'number') {
      const thr = typeof raw.availableSpareThreshold === 'number' ? ` (≥ ${raw.availableSpareThreshold} %)` : '';
      add(t('hostek.availableSpare'), `${raw.availableSpare} %${thr}`);
    }
    add(t('hostek.mediaErrors'), numOf(raw.mediaErrors));
    add(t('hostek.crcErrors'), numOf(raw.udmaCrc));
    if (typeof raw.tbwBytes === 'number' && raw.tbwBytes > 0) add(t('hostek.tbw'), formatBytes(raw.tbwBytes));
  }

  const hasSmart =
    d.healthStatus || d.health || d.tempC || d.firmware || d.powerOnHours || d.powerCycles || typeof d.lifePercent === 'number' || typeof d.agePercent === 'number';

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
              {d.isSystem && <Badge variant="accent">{t('hostek.systemBadge')}</Badge>}
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
                    {t('hostek.usedShort')}
                  </Text>
                </Stack>
              ) : (
                <Text variant="caption" color="tertiary">
                  {t('hostek.freeShort')}
                </Text>
              )
            }
          />
          <Stack gap={1} grow className="min-w-0">
            <Stack direction="row" justify="between" gap={2} align="baseline">
              <Text variant="footnote" color="secondary">
                {t('hostek.capacity')}
              </Text>
              <Text variant="footnote" weight="medium" className="tabular-nums">
                {formatBytes(d.sizeBytes)}
              </Text>
            </Stack>
            <Stack direction="row" justify="between" gap={2} align="baseline">
              <Text variant="footnote" color="secondary">
                {t('hostek.used')}
              </Text>
              <Text variant="footnote" className="tabular-nums">
                {hasUsage ? `${formatBytes(fsUsed)} · ${usedPct.toFixed(0)}%` : '—'}
              </Text>
            </Stack>
            <Stack direction="row" justify="between" gap={2} align="baseline">
              <Text variant="footnote" color="secondary">
                {t('hostek.free')}
              </Text>
              <Text variant="footnote" className="tabular-nums">
                {formatBytes(Math.max(0, total - fsUsed))}
              </Text>
            </Stack>
          </Stack>
        </Stack>

        {/* SMART / health (symmetric with the System-tab disk card) */}
        {hasSmart && (
          <Stack gap={2} className="border-t border-separator pt-2">
            {/* Derived verdict + optional reason */}
            {(d.healthStatus || d.health) && (
              <Stack gap={0.5}>
                <InfoRow label={t('hostek.health')}>
                  {d.healthStatus ? (
                    <Badge variant={statusVariant(d.healthStatus)}>{statusLabel[d.healthStatus]}</Badge>
                  ) : (
                    <Badge variant={d.health!.toUpperCase().includes('PASS') ? 'success' : 'warning'}>{d.health}</Badge>
                  )}
                </InfoRow>
                {d.healthReason && d.healthStatus && d.healthStatus !== 'healthy' && (
                  <Text variant="caption" color="tertiary" className="text-right">
                    {d.healthReason}
                  </Text>
                )}
              </Stack>
            )}

            {/* Lebensdauer % (SSD/NVMe) or Betriebszeit-Alter (HDD) */}
            {typeof d.lifePercent === 'number' ? (
              <Stack gap={1}>
                <InfoRow label={t('hostek.lifespan')}>
                  <Text variant="footnote" weight="medium" className="tabular-nums">
                    {d.lifePercent} %
                  </Text>
                </InfoRow>
                <ProgressBar value={d.lifePercent} tone={lifeTone(d.lifePercent)} />
              </Stack>
            ) : typeof d.agePercent === 'number' ? (
              <Stack gap={1}>
                <InfoRow label={t('hostek.uptimeAge')}>
                  <Text variant="footnote" weight="medium" className="tabular-nums">
                    {d.agePercent} %
                  </Text>
                </InfoRow>
                <ProgressBar value={d.agePercent} tone="accent" />
              </Stack>
            ) : null}

            {/* Vitals */}
            {d.tempC ? <InfoRow label={t('hostek.temperature')}>{`${Math.round(d.tempC)} °C`}</InfoRow> : null}
            {d.powerOnHours ? <InfoRow label={t('hostek.powerOnHours')}>{d.powerOnHours.toLocaleString()}</InfoRow> : null}
            {d.powerCycles ? <InfoRow label={t('hostek.powerCycles')}>{d.powerCycles.toLocaleString()}</InfoRow> : null}
            {d.firmware ? <InfoRow label={t('hostek.firmware')}>{d.firmware}</InfoRow> : null}

            {/* Raw SMART drill-down — present only for viewers with the techinfo right */}
            {smartRows.length > 0 && (
              <Stack gap={1} className="border-t border-separator/60 pt-2">
                <Text variant="caption" color="tertiary">
                  {t('hostek.smartDetails')}
                </Text>
                {smartRows.map((r) => (
                  <InfoRow key={r.label} label={r.label}>
                    {r.value}
                  </InfoRow>
                ))}
              </Stack>
            )}
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
  const t = useT();
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
    return <EmptyState icon={<DiskIcon />} title={t('hostek.noDisks')} description={t('hostek.noDisksDesc')} />;
  }

  return (
    <Stack gap={3}>
      <Text variant="subhead" weight="semibold">
        {t('hostek.diskCount', { count: disks.length })}
      </Text>
      <Grid minItemWidth={360} gap={3}>
        {disks.map((d) => (
          <DiskCard key={d.name} d={d} />
        ))}
      </Grid>
    </Stack>
  );
}
