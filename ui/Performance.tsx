import {
  Box,
  Divider,
  Grid,
  HoverPanel,
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
  useT,
  type ChartSeries,
  type ServiceContextProps,
} from '@holistic/ui';
import type { ReactNode } from 'react';
import type { Avg, Sample, SeriesResponse, Summary } from './types';

const ZERO_AVG: Avg = { a1: 0, a5: 0, a15: 0 };

const C = {
  cpu: 'rgb(var(--cpu))',
  ram: 'rgb(var(--ram))',
  gpu: 'rgb(var(--gpu))',
  net: 'rgb(var(--net))',
  ssd: 'rgb(var(--ssd))',
  netDim: 'rgb(var(--net) / 0.45)',
  ssdDim: 'rgb(var(--ssd) / 0.45)',
};

function HoverTitle({ color, label }: { color: string; label: string }) {
  return (
    <Stack direction="row" align="center" gap={1}>
      <Box className="h-2 w-2 rounded-full" style={{ backgroundColor: color }} />
      <Text variant="caption" weight="semibold">
        {label}
      </Text>
    </Stack>
  );
}

// AvgRow shows a component's 1/5/15-min average (the per-component load average).
function AvgRow({ avg, fmt }: { avg: Avg; fmt: (v: number) => string }) {
  const t = useT();
  return (
    <Stack direction="row" gap={5}>
      {([[t('hostek.avg1'), avg.a1], [t('hostek.avg5'), avg.a5], [t('hostek.avg15'), avg.a15]] as const).map(([l, v]) => (
        <Stack key={l} gap={0}>
          <Text variant="subhead" weight="semibold" className="tabular-nums">
            {fmt(v)}
          </Text>
          <Text variant="caption" color="secondary">
            {l}
          </Text>
        </Stack>
      ))}
    </Stack>
  );
}

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
  const t = useT();
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

  const L = s.loads ?? { cpu: ZERO_AVG, mem: ZERO_AVG, gpu: ZERO_AVG, ssd: ZERO_AVG, net: ZERO_AVG };
  const pct = (v: number) => `${v.toFixed(0)}%`;

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
    { data: memSeries, label: t('hostek.memory'), color: C.ram },
    ...(hasGpu ? [{ data: gpuSeries, label: 'GPU', color: C.gpu }] : []),
    { data: ssdBusySeries, label: 'SSD', color: C.ssd },
    { data: netNorm, label: t('hostek.network'), color: C.net },
  ];
  const legend = [
    { label: 'CPU', color: C.cpu },
    { label: t('hostek.memory'), color: C.ram },
    ...(hasGpu ? [{ label: 'GPU', color: C.gpu }] : []),
    { label: t('hostek.ssdActive'), color: C.ssd },
    { label: t('hostek.networkRelative'), color: C.net },
  ];

  // Tile hover panels (built lazily, only while the panel is open).
  const cpuPanel = () => (
    <Stack gap={3}>
      <HoverTitle color={C.cpu} label={t('hostek.cpuPanelTitle')} />
      <Grid minItemWidth={84} gap={2}>
        {s.perCpu.map((p, i) => (
          <Stack key={i} gap={1}>
            <Text variant="caption" color="secondary">
              {t('hostek.coreN', { n: i })}
            </Text>
            <ProgressBar value={p} tone="cpu" />
          </Stack>
        ))}
      </Grid>
      <Divider />
      <AvgRow avg={L.cpu} fmt={pct} />
    </Stack>
  );

  const gpuPanel = () => (
    <Stack gap={3}>
      <HoverTitle color={C.gpu} label={t('hostek.gpuPanelTitle')} />
      {gpus.map((g, i) => {
        const tp = [g.tempC > 0 ? `${Math.round(g.tempC)} °C` : '', g.powerW > 0 ? `${Math.round(g.powerW)} W` : ''].filter(Boolean).join(' · ');
        return (
          <Stack key={i} gap={1}>
            <Stack direction="row" justify="between" gap={2}>
              <Text variant="caption" color="secondary">
                {t('hostek.gpuUtil', { index: g.index, pct: g.utilPercent.toFixed(0) })}
              </Text>
              {tp && (
                <Text variant="caption" color="secondary">
                  {tp}
                </Text>
              )}
            </Stack>
            <ProgressBar value={g.memPercent} tone="gpu" />
            <Text variant="caption" color="secondary">
              VRAM {formatBytes(g.memUsed)} / {formatBytes(g.memTotal)}
            </Text>
          </Stack>
        );
      })}
      <Divider />
      <AvgRow avg={L.gpu} fmt={pct} />
    </Stack>
  );

  const simplePanel = (color: string, label: string, detail: ReactNode, avg: Avg, fmt: (v: number) => string) => () => (
    <Stack gap={3}>
      <HoverTitle color={color} label={label} />
      {detail}
      <Divider />
      <AvgRow avg={avg} fmt={fmt} />
    </Stack>
  );

  return (
    <Stack gap={4}>
      {/* Top stat tiles — hover any for its detail + 1/5/15-min average */}
      <Grid minItemWidth={220} gap={3}>
        <HoverPanel block width={380} panel={cpuPanel}>
          <Stat className="h-full" label="CPU" value={s.cpuPercent} unit="%" footer={<ProgressBar value={s.cpuPercent} tone="cpu" />} />
        </HoverPanel>

        <HoverPanel
          block
          width={300}
          panel={simplePanel(
            C.ram,
            t('hostek.memory'),
            <Text variant="caption" color="secondary">
              {formatBytes(s.memUsed)} / {formatBytes(s.memTotal)} · {formatBytes(s.memCached)} {t('hostek.cached')}
            </Text>,
            L.mem,
            pct,
          )}
        >
          <Stat
            className="h-full"
            label={t('hostek.memory')}
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
        </HoverPanel>

        {hasGpu && (
          <HoverPanel block width={340} panel={gpuPanel}>
            <Stat
              className="h-full"
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
          </HoverPanel>
        )}

        <HoverPanel
          block
          width={300}
          panel={simplePanel(
            C.ssd,
            t('hostek.systemSsd'),
            <Text variant="caption" color="secondary">
              {s.sysDiskBusyPercent}% active · ↓ {formatRate(s.sysDiskReadRate)} · ↑ {formatRate(s.sysDiskWriteRate)}
            </Text>,
            L.ssd,
            pct,
          )}
        >
          <Stat
            className="h-full"
            label="SSD"
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
        </HoverPanel>

        <HoverPanel
          block
          width={300}
          panel={simplePanel(
            C.net,
            t('hostek.network'),
            <Text variant="caption" color="secondary">
              ↓ {formatRate(s.netRxRate)} · ↑ {formatRate(s.netTxRate)}
            </Text>,
            L.net,
            formatRate,
          )}
        >
          <Stat
            className="h-full"
            label={t('hostek.network')}
            value={formatRate(s.netRxRate)}
            footer={
              <Text variant="caption" color="secondary">
                ↑ {formatRate(s.netTxRate)}
              </Text>
            }
          />
        </HoverPanel>
      </Grid>

      {/* Combined history with legend */}
      <Panel title={t('hostek.historyUtilization')} className="p-4">
        <Stack gap={2}>
          <Legend items={legend} />
          <LineChart series={combined} min={0} max={100} height={170} />
        </Stack>
      </Panel>

      {/* Per-component detail graphs */}
      <Grid minItemWidth={260} gap={3}>
        <MiniChart label="CPU" color={C.cpu} percent caption={`${s.cpuPercent}%`} lines={[{ data: cpuSeries, color: C.cpu, fill: true }]} />
        <MiniChart label={t('hostek.memory')} color={C.ram} percent caption={`${s.memPercent}%`} lines={[{ data: memSeries, color: C.ram, fill: true }]} />
        {hasGpu && <MiniChart label="GPU" color={C.gpu} percent caption={`${gpuPct}%`} lines={[{ data: gpuSeries, color: C.gpu, fill: true }]} />}
        <MiniChart
          label="SSD"
          color={C.ssd}
          caption={`↓ ${formatRate(s.sysDiskReadRate)} · ↑ ${formatRate(s.sysDiskWriteRate)}`}
          lines={[
            { data: ssdReadSeries, color: C.ssd },
            { data: ssdWriteSeries, color: C.ssdDim },
          ]}
        />
        <MiniChart
          label={t('hostek.network')}
          color={C.net}
          caption={`↓ ${formatRate(s.netRxRate)} · ↑ ${formatRate(s.netTxRate)}`}
          lines={[
            { data: netRxSeries, color: C.net },
            { data: netTxSeries, color: C.netDim },
          ]}
        />
      </Grid>

      {/* Uptime / process count */}
      <Panel className="px-4 py-3">
        <Text variant="footnote" color="secondary">
          {t('hostek.uptimeProcs', { uptime: formatDuration(s.uptime), procs: s.procs })}
        </Text>
      </Panel>
    </Stack>
  );
}
