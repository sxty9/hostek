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
  type ServiceContextProps,
  type StreamSeries,
} from '@holistic/ui';
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
  return (
    <HoverPanel
      width={300}
      panel={
        <Stack gap={2}>
          <Stack direction="row" align="center" gap={1}>
            <Box className="h-2 w-2 rounded-full" style={{ backgroundColor: color }} />
            <Text variant="caption" weight="semibold">
              {label} — power average
            </Text>
          </Stack>
          <Stack direction="row" gap={5}>
            {([['1 min', avg.a1], ['5 min', avg.a5], ['15 min', avg.a15]] as const).map(([l, v]) => (
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

  // Stack CPU + GPU; their combined height is the total power.
  const series: StreamSeries[] = [];
  if (hasCpu) series.push({ label: 'CPU', color: C.cpu, data: cpuSeries });
  if (hasGpu) series.push({ label: 'GPU', color: C.gpu, data: gpuSeries });

  const currentTotal = samples.length ? samples[samples.length - 1].total : 0;

  return (
    <Stack gap={4}>
      <Panel title="Power draw" className="p-4">
        <Stack gap={3}>
          <Stack direction="row" justify="between" align="baseline" gap={3}>
            <Text variant="caption" color="secondary">
              Watts drawn per component over time — CPU via RAPL, GPU via nvidia-smi. Hover a legend entry for its 1 / 5 / 15-min average.
            </Text>
            <Text variant="title3" weight="semibold" className="tabular-nums shrink-0">
              {watts(currentTotal)}
            </Text>
          </Stack>

          {series.length === 0 ? (
            <EmptyState
              icon={<BoltIcon />}
              title="No power telemetry"
              description="Neither CPU (RAPL via hostek-powermon) nor GPU (nvidia-smi) power is available on this host."
            />
          ) : (
            <StreamGraph series={series} height={220} />
          )}

          <Stack direction="row" gap={5} wrap>
            <PwrLegend label="CPU" color={C.cpu} avg={data.avg.cpu} />
            <PwrLegend label="GPU" color={C.gpu} avg={data.avg.gpu} />
            <PwrLegend label="Total" color={C.total} avg={data.avg.total} />
          </Stack>

          {!data.cpuAvailable && (
            <Text variant="caption" color="tertiary">
              CPU power reads 0 until the privileged RAPL helper (hostek-powermon) is installed and RAPL is available.
            </Text>
          )}
        </Stack>
      </Panel>
    </Stack>
  );
}
