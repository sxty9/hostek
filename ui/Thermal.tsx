import { useEffect, useRef } from 'react';
import {
  Badge,
  Box,
  EmptyState,
  Grid,
  LineChart,
  Marquee,
  Panel,
  Spinner,
  Stack,
  Text,
  ThermometerIcon,
  toast,
  useLiveQuery,
  useT,
  type ServiceContextProps,
} from '@holistic/ui';
import type { ThermalResponse, ThermalSample } from './types';

function compColor(key: string): string {
  if (key === 'cpu') return 'rgb(var(--cpu))';
  if (key.startsWith('gpu')) return 'rgb(var(--gpu))';
  return 'rgb(var(--ssd))'; // a disk
}

// buildSeries forward/back-fills a component's temperature so the line has no gaps.
function buildSeries(samples: ThermalSample[], key: string): number[] {
  const raw = samples.map((s) => {
    const v = s.temps[key];
    return v != null && v > 0 ? v : NaN;
  });
  let last = NaN;
  for (let i = 0; i < raw.length; i++) {
    if (Number.isFinite(raw[i])) last = raw[i];
    else raw[i] = last;
  }
  const first = raw.find((v) => Number.isFinite(v)) ?? 0;
  for (let i = 0; i < raw.length; i++) if (!Number.isFinite(raw[i])) raw[i] = first;
  return raw;
}

// ThermalWatcher renders nothing; it polls temperatures and raises a warning toast when
// a component reaches its critical limit (with hysteresis so it fires once per episode).
// Mounted at the dashboard level so the warning fires regardless of the active tab.
export function ThermalWatcher({ api }: ServiceContextProps) {
  const t = useT();
  const { data } = useLiveQuery<ThermalResponse>(() => api.get<ThermalResponse>('thermal'), 5000);
  const alarmed = useRef<Set<string>>(new Set());

  useEffect(() => {
    if (!data?.samples?.length) return;
    const last = data.samples[data.samples.length - 1].temps;
    for (const c of data.components) {
      const temp = last[c.key];
      if (temp == null) continue;
      if (temp >= c.criticalC) {
        if (!alarmed.current.has(c.key)) {
          alarmed.current.add(c.key);
          toast({
            variant: 'error',
            title: t('hostek.criticalTempTitle', { label: c.label }),
            description: t('hostek.criticalTempDesc', { temp: Math.round(temp), crit: Math.round(c.criticalC) }),
          });
        }
      } else if (temp < c.criticalC - 3) {
        alarmed.current.delete(c.key); // re-arm once it cools 3 °C below critical
      }
    }
  }, [data, t]);

  return null;
}

export function Thermal({ api }: ServiceContextProps) {
  const t = useT();
  const { data } = useLiveQuery<ThermalResponse>(() => api.get<ThermalResponse>('thermal'), 2000);

  if (!data) {
    return (
      <Stack align="center" justify="center" className="py-16">
        <Spinner />
      </Stack>
    );
  }

  const samples = data.samples ?? [];
  const components = data.components ?? [];
  if (components.length === 0) {
    return <EmptyState icon={<ThermometerIcon />} title={t('hostek.noTempSensors')} description={t('hostek.noTempSensorsDesc')} />;
  }

  return (
    <Grid minItemWidth={360} gap={3}>
      {components.map((c) => {
        const series = buildSeries(samples, c.key);
        const cur = series.length ? series[series.length - 1] : 0;
        const obsMax = series.length ? Math.max(...series) : 0;
        const chartMax = Math.max(c.criticalC + 8, obsMax + 5);
        const overCritical = cur >= c.criticalC;
        const color = compColor(c.key);
        return (
          <Panel key={c.key} className="p-3">
            <Stack gap={2}>
              <Stack direction="row" justify="between" align="center" gap={2}>
                <Stack direction="row" align="center" gap={1} className="min-w-0 grow">
                  <Box className="h-2 w-2 shrink-0 rounded-full" style={{ backgroundColor: color }} />
                  <Marquee text={c.label} className="text-caption font-medium text-text-primary" />
                </Stack>
                <Stack direction="row" align="center" gap={2} className="shrink-0">
                  <Text variant="subhead" weight="semibold" className="tabular-nums">
                    {Math.round(cur)} °C
                  </Text>
                  {overCritical && <Badge variant="danger">{t('hostek.critical')}</Badge>}
                </Stack>
              </Stack>
              <LineChart
                series={[{ data: series, color, fill: true }]}
                min={20}
                max={chartMax}
                height={96}
                refLines={[{ value: c.criticalC }]}
              />
              <Text variant="caption" color="tertiary">
                {t('hostek.criticalCaption', { crit: Math.round(c.criticalC) })}
              </Text>
            </Stack>
          </Panel>
        );
      })}
    </Grid>
  );
}
