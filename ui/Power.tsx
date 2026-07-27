import {
  BoltIcon,
  Box,
  EmptyState,
  HoverPanel,
  Panel,
  Spinner,
  Stack,
  StreamGraph,
  Text,
  useLiveQuery,
  useT,
  type ServiceContextProps,
  type StreamSeries,
} from '@holisdk/ui';
import type { Avg, PowerResponse } from './types';

const C = {
  cpu: 'rgb(var(--cpu))',
  gpu: 'rgb(var(--gpu))',
  total: 'rgb(var(--fill))',
};

const watts = (v: number) => `${v.toFixed(1)} W`;

// PwrLegend is a hoverable legend entry: hovering reveals the component's 1/5/15-min
// power average (the same windows as the system load average).
function PwrLegend({ label, color, avg }: { label: string; color: string; avg: Avg }) {
  const t = useT();
  return (
    <HoverPanel
      width={300}
      panel={
        <Stack gap={2}>
          <Stack direction="row" align="center" gap={1}>
            <Box className="h-2 w-2 rounded-full" style={{ backgroundColor: color }} />
            <Text variant="caption" weight="semibold">
              {t('hostek.powerAverage', { label })}
            </Text>
          </Stack>
          <Stack direction="row" gap={5}>
            {([[t('hostek.avg1'), avg.a1], [t('hostek.avg5'), avg.a5], [t('hostek.avg15'), avg.a15]] as const).map(([l, v]) => (
              <Stack key={l} gap={0}>
                <Text variant="subhead" weight="semibold" className="tabular-nums">
                  {watts(v)}
                </Text>
                <Text variant="caption" color="secondary">
                  {l}
                </Text>
              </Stack>
            ))}
          </Stack>
        </Stack>
      }
    >
      <Stack direction="row" align="center" gap={1} className="cursor-help">
        <Box className="h-2 w-2 rounded-full" style={{ backgroundColor: color }} />
        <Text variant="caption" color="secondary">
          {label} · {watts(avg.a1)}
        </Text>
      </Stack>
    </HoverPanel>
  );
}

export function Power({ api }: ServiceContextProps) {
  const t = useT();
  const { data } = useLiveQuery<PowerResponse>(() => api.get<PowerResponse>('power'), 2000);

  if (!data) {
    return (
      <Stack align="center" justify="center" className="py-16">
        <Spinner />
      </Stack>
    );
  }

  const samples = data.samples ?? [];
  const cpuSeries = samples.map((x) => x.cpu);
  const gpuSeries = samples.map((x) => x.gpu);
  const hasCpu = data.cpuAvailable || cpuSeries.some((v) => v > 0);
  const hasGpu = data.gpuAvailable || gpuSeries.some((v) => v > 0);
  // Authoritative per-GPU count: the backend's current count (avg.gpus) when present, else
  // the widest per-GPU sample in the window. Series AND legend derive from this single
  // value so their band count, colours and labels always agree.
  const nGpu = data.avg.gpus?.length ?? Math.max(0, ...samples.map((x) => x.gpus?.length ?? 0));

  // Per-GPU colour: spread --gpu opacity evenly across the GPUs so every stacked band stays
  // distinguishable (1 GPU → full strength; N GPUs → 1.00 … 0.40, even for 3+).
  const gpuColor = (i: number) => `rgb(var(--gpu) / ${(1 - (nGpu > 1 ? i / (nGpu - 1) : 0) * 0.6).toFixed(2)})`;
  // A single GPU stays the bare "GPU"; only disambiguate by index for 2+.
  const gpuLabel = (i: number) => (nGpu > 1 ? `GPU ${i}` : 'GPU');

  // Stack CPU + one series PER GPU; their combined height is the total power.
  const series: StreamSeries[] = [];
  if (hasCpu) series.push({ label: 'CPU', color: C.cpu, data: cpuSeries });
  if (hasGpu) {
    if (nGpu >= 1) {
      for (let i = 0; i < nGpu; i++) {
        series.push({ label: gpuLabel(i), color: gpuColor(i), data: samples.map((x) => x.gpus?.[i] ?? 0) });
      }
    } else {
      series.push({ label: 'GPU', color: C.gpu, data: gpuSeries });
    }
  }

  const currentTotal = samples.length ? samples[samples.length - 1].total : 0;

  return (
    <Stack gap={4}>
      <Panel
        title={t('hostek.powerDraw')}
        actions={
          <Text variant="title3" weight="semibold" className="tabular-nums">
            {watts(currentTotal)}
          </Text>
        }
        className="p-4"
      >
        <Stack gap={3}>
          {series.length === 0 ? (
            <EmptyState
              icon={<BoltIcon />}
              title={t('hostek.noPowerTelemetry')}
              description={t('hostek.noPowerDesc')}
            />
          ) : (
            <StreamGraph series={series} height={220} />
          )}

          <Stack direction="row" gap={5} wrap>
            <PwrLegend label="CPU" color={C.cpu} avg={data.avg.cpu} />
            {hasGpu &&
              (nGpu >= 1
                ? Array.from({ length: nGpu }, (_, i) => (
                    <PwrLegend key={i} label={gpuLabel(i)} color={gpuColor(i)} avg={data.avg.gpus?.[i] ?? data.avg.gpu} />
                  ))
                : <PwrLegend label="GPU" color={C.gpu} avg={data.avg.gpu} />)}
            <PwrLegend label={t('hostek.total')} color={C.total} avg={data.avg.total} />
          </Stack>

          {!data.cpuAvailable && (
            <Text variant="caption" color="tertiary">
              {t('hostek.cpuPowerNote')}
            </Text>
          )}
        </Stack>
      </Panel>
    </Stack>
  );
}
