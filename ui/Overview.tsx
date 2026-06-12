import {
  Gauge,
  Grid,
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
  toneForLoad,
  useLiveQuery,
  type ServiceContextProps,
} from '@holistic/ui';
import type { HostInfo, Sample, SeriesResponse, Summary } from './types';

export function Overview({ api }: ServiceContextProps) {
  const { data: s } = useLiveQuery<Summary>(() => api.get<Summary>('summary'), 2000);
  const { data: series } = useLiveQuery<SeriesResponse>(() => api.get<SeriesResponse>('metrics'), 2000);
  const { data: host } = useLiveQuery<HostInfo>(() => api.get<HostInfo>('host'), 30000);

  if (!s) {
    return (
      <Stack align="center" justify="center" className="py-16">
        <Spinner />
      </Stack>
    );
  }

  const disk = s.disks[0];
  const samples: Sample[] = series?.samples ?? [];
  const cpuSeries = samples.map((x) => x.cpu);
  const memSeries = samples.map((x) => x.mem);

  return (
    <Stack gap={4}>
      <Grid minItemWidth={220} gap={3}>
        <Stat label="CPU" value={s.cpuPercent} unit="%" footer={<ProgressBar value={s.cpuPercent} tone={toneForLoad(s.cpuPercent)} />} />
        <Stat
          label="Memory"
          value={s.memPercent}
          unit="%"
          footer={
            <Stack gap={1}>
              <ProgressBar value={s.memPercent} tone={toneForLoad(s.memPercent)} />
              <Text variant="caption" color="secondary">
                {formatBytes(s.memUsed)} / {formatBytes(s.memTotal)}
              </Text>
            </Stack>
          }
        />
        {disk && (
          <Stat
            label="Disk"
            value={disk.percent}
            unit="%"
            footer={
              <Stack gap={1}>
                <ProgressBar value={disk.percent} tone={toneForLoad(disk.percent)} />
                <Text variant="caption" color="secondary">
                  {formatBytes(disk.used)} / {formatBytes(disk.total)} · {disk.mount}
                </Text>
              </Stack>
            }
          />
        )}
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

      <Panel title="Processor" className="p-4">
        <Stack direction="row" gap={4} align="center" wrap>
          <Gauge value={s.cpuPercent} tone={toneForLoad(s.cpuPercent)} sublabel="load" />
          <Stack grow gap={2} className="min-w-[240px]">
            <Grid minItemWidth={90} gap={2}>
              {s.perCpu.map((p, i) => (
                <Stack key={i} gap={1}>
                  <Text variant="caption" color="secondary">
                    Core {i}
                  </Text>
                  <ProgressBar value={p} tone={toneForLoad(p)} />
                </Stack>
              ))}
            </Grid>
          </Stack>
        </Stack>
      </Panel>

      <Panel title="History — CPU & Memory" className="p-4">
        <LineChart
          series={[
            { data: cpuSeries, label: 'CPU', fill: true },
            { data: memSeries, label: 'Memory', color: 'rgb(var(--success))' },
          ]}
          min={0}
          max={100}
          height={160}
        />
      </Panel>

      <Grid minItemWidth={260} gap={3}>
        <Panel title="Load average" className="p-4">
          <Stack direction="row" gap={5}>
            <Stack gap={0}>
              <Text variant="title3" weight="semibold" className="tabular-nums">
                {s.load1}
              </Text>
              <Text variant="caption" color="secondary">
                1 min
              </Text>
            </Stack>
            <Stack gap={0}>
              <Text variant="title3" weight="semibold" className="tabular-nums">
                {s.load5}
              </Text>
              <Text variant="caption" color="secondary">
                5 min
              </Text>
            </Stack>
            <Stack gap={0}>
              <Text variant="title3" weight="semibold" className="tabular-nums">
                {s.load15}
              </Text>
              <Text variant="caption" color="secondary">
                15 min
              </Text>
            </Stack>
          </Stack>
        </Panel>

        <Panel title="System" className="p-4">
          <Stack gap={1}>
            <Text weight="semibold">{host?.hostname ?? '—'}</Text>
            <Text variant="footnote" color="secondary">
              {host?.cpuModel ?? '—'} · {host?.cpuThreads ?? '?'} threads
            </Text>
            <Text variant="footnote" color="secondary">
              {host ? `${host.platform} ${host.platformVersion} · ${host.kernel}` : '—'}
            </Text>
            <Text variant="footnote" color="secondary">
              Uptime {formatDuration(s.uptime)} · {s.procs} processes
            </Text>
          </Stack>
        </Panel>
      </Grid>
    </Stack>
  );
}
