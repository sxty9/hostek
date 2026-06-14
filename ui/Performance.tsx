import {
  Box,
  Gauge,
  Grid,
  Legend,
  LineChart,
  Panel,
  ProgressBar,
  Spinner,
  Stack,
  Stat,
  Text,
  formatBytes,
  formatDuration,
  formatRate,
  useLiveQuery,
  type ChartSeries,
  type ServiceContextProps,
} from '@holistic/ui';
import type { ReactNode } from 'react';
import type { Sample, SeriesResponse, Summary } from './types';

const C = {
  cpu: 'rgb(var(--cpu))',
  ram: 'rgb(var(--ram))',
  gpu: 'rgb(var(--gpu))',
  net: 'rgb(var(--net))',
  ssd: 'rgb(var(--ssd))',
  netDim: 'rgb(var(--net) / 0.45)',
  ssdDim: 'rgb(var(--ssd) / 0.45)',
};

function MiniChart({ label, color, lines, percent, caption }: { label: string; color: string; lines: ChartSeries[]; percent?: boolean; caption?: ReactNode }) {
  return (
    <Panel className="p-3">
      <Stack gap={2}>
        <Stack direction="row" justify="between" align="center" gap={2}>
          <Stack direction="row" align="center" gap={1}>
            <Box className="h-2 w-2 rounded-full" style={{ backgroundColor: color }} />
            <Text variant="caption" weight="medium">
              {label}
            </Text>
          </Stack>
          {caption && (
            <Text variant="caption" color="secondary" className="tabular-nums">
              {caption}
            </Text>
          )}
        </Stack>
        <LineChart series={lines} height={72} min={percent ? 0 : undefined} max={percent ? 100 : undefined} />
      </Stack>
    </Panel>
  );
}

export function Performance({ api }: ServiceContextProps) {
  const { data: s } = useLiveQuery<Summary>(() => api.get<Summary>('summary'), 2000);
  const { data: series } = useLiveQuery<SeriesResponse>(() => api.get<SeriesResponse>('metrics'), 2000);

  if (!s) {
    return (
      <Stack align="center" justify="center" className="py-16">
        <Spinner />
      </Stack>
    );
  }

  const gpus = s.gpus ?? [];
  const hasGpu = gpus.length > 0;
  const gpuPct = gpus.reduce((m, g) => Math.max(m, g.utilPercent), 0);

  const samples: Sample[] = series?.samples ?? [];
  const cpuSeries = samples.map((x) => x.cpu);
  const memSeries = samples.map((x) => x.mem);
  const gpuSeries = samples.map((x) => x.gpu);
  const ssdBusySeries = samples.map((x) => x.ssdBusy);
  const ssdReadSeries = samples.map((x) => x.ssdRead);
  const ssdWriteSeries = samples.map((x) => x.ssdWrite);
  const netRxSeries = samples.map((x) => x.netRx);
  const netTxSeries = samples.map((x) => x.netTx);

  // Network has no natural percentage, so normalize total throughput to the window max
  // for the combined utilization chart (the per-component graph shows real bytes/sec).
  const netTotal = samples.map((x) => x.netRx + x.netTx);
  const netMax = Math.max(1, ...netTotal);
  const netNorm = netTotal.map((v) => (v / netMax) * 100);

  const combined: ChartSeries[] = [
    { data: cpuSeries, label: 'CPU', color: C.cpu },
    { data: memSeries, label: 'Memory', color: C.ram },
    ...(hasGpu ? [{ data: gpuSeries, label: 'GPU', color: C.gpu }] : []),
    { data: ssdBusySeries, label: 'SSD', color: C.ssd },
    { data: netNorm, label: 'Network', color: C.net },
  ];
  const legend = [
    { label: 'CPU', color: C.cpu },
    { label: 'Memory', color: C.ram },
    ...(hasGpu ? [{ label: 'GPU', color: C.gpu }] : []),
    { label: 'SSD (active)', color: C.ssd },
    { label: 'Network (relative)', color: C.net },
  ];

  return (
    <Stack gap={4}>
      {/* Top stat tiles */}
      <Grid minItemWidth={220} gap={3}>
        <Stat label="CPU" value={s.cpuPercent} unit="%" footer={<ProgressBar value={s.cpuPercent} tone="cpu" />} />
        <Stat
          label="Memory"
          value={s.memPercent}
          unit="%"
          footer={
            <Stack gap={1}>
              <ProgressBar value={s.memPercent} tone="ram" />
              <Text variant="caption" color="secondary">
                {formatBytes(s.memUsed)} / {formatBytes(s.memTotal)}
              </Text>
            </Stack>
          }
        />
        {hasGpu && (
          <Stat
            label="GPU"
            value={gpuPct}
            unit="%"
            footer={
              <Stack gap={1}>
                <ProgressBar value={gpuPct} tone="gpu" />
                <Text variant="caption" color="secondary">
                  {formatBytes(gpus[0].memUsed)} / {formatBytes(gpus[0].memTotal)}
                </Text>
              </Stack>
            }
          />
        )}
        <Stat
          label="System SSD"
          value={s.sysDiskBusyPercent}
          unit="%"
          footer={
            <Stack gap={1}>
              <ProgressBar value={s.sysDiskBusyPercent} tone="ssd" />
              <Text variant="caption" color="secondary">
                ↓ {formatRate(s.sysDiskReadRate)} · ↑ {formatRate(s.sysDiskWriteRate)}
              </Text>
            </Stack>
          }
        />
        <Stat
          label="Network"
          value={formatRate(s.netRxRate)}
          footer={
            <Text variant="caption" color="secondary">
              ↑ {formatRate(s.netTxRate)}
            </Text>
          }
        />
      </Grid>

      {/* Processor + GPU gauges */}
      <Grid minItemWidth={hasGpu ? 320 : 480} gap={3}>
        <Panel title="Processor" className="p-4">
          <Stack direction="row" gap={4} align="center" wrap>
            <Gauge value={s.cpuPercent} tone="cpu" sublabel="load" />
            <Stack grow gap={2} className="min-w-[220px]">
              <Grid minItemWidth={90} gap={2}>
                {s.perCpu.map((p, i) => (
                  <Stack key={i} gap={1}>
                    <Text variant="caption" color="secondary">
                      Core {i}
                    </Text>
                    <ProgressBar value={p} tone="cpu" />
                  </Stack>
                ))}
              </Grid>
            </Stack>
          </Stack>
        </Panel>

        {hasGpu && (
          <Panel title="Graphics" className="p-4">
            <Stack direction="row" gap={4} align="center" wrap>
              {gpus.map((g, i) => {
                const tp = [g.tempC > 0 ? `${Math.round(g.tempC)} °C` : '', g.powerW > 0 ? `${Math.round(g.powerW)} W` : '']
                  .filter(Boolean)
                  .join(' · ');
                return (
                  <Stack key={i} direction="row" gap={3} align="center">
                    <Gauge value={g.utilPercent} tone="gpu" sublabel={`GPU ${g.index}`} />
                    <Stack gap={1} className="min-w-[150px]">
                      <Text variant="caption" color="secondary">
                        VRAM
                      </Text>
                      <ProgressBar value={g.memPercent} tone="gpu" />
                      <Text variant="caption" color="secondary">
                        {formatBytes(g.memUsed)} / {formatBytes(g.memTotal)}
                      </Text>
                      {tp && (
                        <Text variant="caption" color="secondary">
                          {tp}
                        </Text>
                      )}
                    </Stack>
                  </Stack>
                );
              })}
            </Stack>
          </Panel>
        )}
      </Grid>

      {/* Combined history with legend */}
      <Panel title="History — utilization" className="p-4">
        <Stack gap={2}>
          <Legend items={legend} />
          <LineChart series={combined} min={0} max={100} height={170} />
          <Text variant="caption" color="tertiary">
            CPU / Memory / GPU / SSD are % utilization; Network is scaled relative to the window peak.
          </Text>
        </Stack>
      </Panel>

      {/* Per-component detail graphs */}
      <Grid minItemWidth={260} gap={3}>
        <MiniChart label="CPU" color={C.cpu} percent caption={`${s.cpuPercent}%`} lines={[{ data: cpuSeries, color: C.cpu, fill: true }]} />
        <MiniChart label="Memory" color={C.ram} percent caption={`${s.memPercent}%`} lines={[{ data: memSeries, color: C.ram, fill: true }]} />
        {hasGpu && <MiniChart label="GPU" color={C.gpu} percent caption={`${gpuPct}%`} lines={[{ data: gpuSeries, color: C.gpu, fill: true }]} />}
        <MiniChart
          label="System SSD"
          color={C.ssd}
          caption={`↓ ${formatRate(s.sysDiskReadRate)} · ↑ ${formatRate(s.sysDiskWriteRate)}`}
          lines={[
            { data: ssdReadSeries, color: C.ssd },
            { data: ssdWriteSeries, color: C.ssdDim },
          ]}
        />
        <MiniChart
          label="Network"
          color={C.net}
          caption={`↓ ${formatRate(s.netRxRate)} · ↑ ${formatRate(s.netTxRate)}`}
          lines={[
            { data: netRxSeries, color: C.net },
            { data: netTxSeries, color: C.netDim },
          ]}
        />
      </Grid>

      {/* Load average + uptime */}
      <Panel title="Load average — system-wide CPU run-queue" className="p-4">
        <Stack gap={2}>
          <Stack direction="row" gap={5} wrap align="center">
            {([['1 min', s.load1], ['5 min', s.load5], ['15 min', s.load15]] as const).map(([label, v]) => (
              <Stack key={label} gap={0}>
                <Text variant="title3" weight="semibold" className="tabular-nums">
                  {v}
                </Text>
                <Text variant="caption" color="secondary">
                  {label}
                </Text>
              </Stack>
            ))}
            <Stack grow gap={0} className="min-w-[180px]">
              <Text variant="footnote" color="secondary">
                Uptime {formatDuration(s.uptime)}
              </Text>
              <Text variant="footnote" color="secondary">
                {s.procs} processes
              </Text>
            </Stack>
          </Stack>
          <Text variant="caption" color="tertiary">
            System-wide CPU run-queue averaged over 1 / 5 / 15 min · relative to {s.perCpu.length} logical CPUs
          </Text>
        </Stack>
      </Panel>
    </Stack>
  );
}
