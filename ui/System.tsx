import {
  Badge,
  Box,
  CpuIcon,
  Divider,
  EthernetIcon,
  GpuIcon,
  Grid,
  Marquee,
  MemoryIcon,
  MotherboardIcon,
  Panel,
  Spinner,
  SsdIcon,
  Stack,
  Text,
  cn,
  formatBytes,
  useLiveQuery,
  useT,
  type ServiceContextProps,
} from '@holistic/ui';
import type { ReactNode } from 'react';
import type { HardwareInfo } from './types';

// Atomic value formatters — every spec is a single simplified figure.
function mhz(v?: number): string | undefined {
  if (!v || v <= 0) return undefined;
  return v >= 1000 ? `${(v / 1000).toFixed(2)} GHz` : `${Math.round(v)} MHz`;
}
const degC = (v?: number) => (v && v > 0 ? `${Math.round(v)} °C` : undefined);
const join = (...parts: (string | undefined | false)[]) => parts.filter(Boolean).join(' · ') || undefined;

function Spec({ label, value }: { label: string; value?: ReactNode }) {
  if (value === undefined || value === null || value === '') return null;
  return (
    <Stack direction="row" justify="between" gap={3} align="baseline">
      <Text variant="footnote" color="secondary">
        {label}
      </Text>
      <Text variant="footnote" className="text-right tabular-nums">
        {value}
      </Text>
    </Stack>
  );
}

function CompCard({
  icon,
  tileClass,
  title,
  subtitle,
  children,
}: {
  icon: ReactNode;
  tileClass: string;
  title: string;
  subtitle?: string;
  children: ReactNode;
}) {
  return (
    <Panel className="p-4">
      <Stack gap={3}>
        <Stack direction="row" align="center" gap={3}>
          <Box className={cn('flex h-9 w-9 shrink-0 items-center justify-center rounded-md', tileClass)}>{icon}</Box>
          <Stack gap={0} className="min-w-0 grow">
            <Marquee text={title} className="text-subhead font-semibold text-text-primary" />
            {subtitle && <Marquee text={subtitle} className="text-caption text-text-secondary" />}
          </Stack>
        </Stack>
        <Divider />
        <Stack gap={1}>{children}</Stack>
      </Stack>
    </Panel>
  );
}

export function System({ api }: ServiceContextProps) {
  const t = useT();
  // The hardware route carries static specs AND embedded live values (clocks/temps),
  // so non-admins can use this tab without access to the admin-only summary route.
  const { data: hw } = useLiveQuery<HardwareInfo>(() => api.get<HardwareInfo>('hardware'), 3000);

  if (!hw) {
    return (
      <Stack align="center" justify="center" className="py-16">
        <Spinner />
      </Stack>
    );
  }

  const cpu = hw.cpu ?? {};
  const mem = hw.memory ?? {};
  const board = hw.board ?? {};
  const disk = hw.disk ?? {};

  return (
    <Grid minItemWidth={330} gap={3}>
      {/* CPU */}
      <CompCard
        icon={<CpuIcon className="h-5 w-5 text-cpu" />}
        tileClass="bg-cpu/15"
        title={cpu.model || t('hostek.processor')}
        subtitle={join(cpu.vendor, cpu.socket && `${t('hostek.socket')} ${cpu.socket}`)}
      >
        <Spec label={t('hostek.coresThreads')} value={cpu.cores ? `${cpu.cores} / ${cpu.threads ?? '?'}` : undefined} />
        <Spec label={t('hostek.baseClock')} value={mhz(cpu.baseClockMhz)} />
        <Spec label={t('hostek.maxClock')} value={mhz(cpu.maxClockMhz)} />
        <Spec label={t('hostek.currentClock')} value={mhz(cpu.curClockMhz)} />
        <Spec label={t('hostek.temperature')} value={degC(cpu.tempC)} />
        <Spec label={t('hostek.l1cache')} value={cpu.cacheL1} />
        <Spec label={t('hostek.l2cache')} value={cpu.cacheL2} />
        <Spec label={t('hostek.l3cache')} value={cpu.cacheL3} />
        <Spec label={t('hostek.family')} value={cpu.family} />
      </CompCard>

      {/* Memory */}
      <CompCard
        icon={<MemoryIcon className="h-5 w-5 text-ram" />}
        tileClass="bg-ram/15"
        title={mem.totalBytes ? `${formatBytes(mem.totalBytes)} RAM` : t('hostek.memory')}
        subtitle={mem.modules?.length ? t('hostek.moduleCount', { count: mem.modules.length }) : undefined}
      >
        {mem.modules?.length ? (
          mem.modules.map((m, i) => (
            <Stack key={i} gap={0} className="py-1">
              <Stack direction="row" justify="between" gap={3} align="baseline">
                <Text variant="footnote" weight="medium">
                  {m.slot || `Slot ${i + 1}`}
                </Text>
                <Text variant="footnote" className="text-right tabular-nums">
                  {join(m.sizeBytes ? formatBytes(m.sizeBytes) : undefined, m.type, mhz(m.configuredMhz || m.speedMhz))}
                </Text>
              </Stack>
              {(m.manufacturer || m.partNumber || m.timings) && (
                <Marquee text={join(m.manufacturer, m.partNumber, m.timings) ?? ''} className="text-caption text-text-secondary" />
              )}
            </Stack>
          ))
        ) : (
          <Text variant="footnote" color="secondary">
            {t('hostek.moduleDetailsUnavailable')}
          </Text>
        )}
      </CompCard>

      {/* GPU(s) */}
      {hw.gpus?.map((g, i) => (
        <CompCard
          key={i}
          icon={<GpuIcon className="h-5 w-5 text-gpu" />}
          tileClass="bg-gpu/15"
          title={g.name || t('hostek.graphics')}
          subtitle={join(g.driver && `${t('hostek.driver')} ${g.driver}`, g.cuda && `CUDA ${g.cuda}`)}
        >
          <Spec label="VRAM" value={g.memTotalBytes ? formatBytes(g.memTotalBytes) : undefined} />
          <Spec label={t('hostek.baseClock')} value={mhz(g.baseClockMhz)} />
          <Spec label={t('hostek.boostClock')} value={mhz(g.boostClockMhz)} />
          <Spec label={t('hostek.currentClock')} value={mhz(g.curClockMhz)} />
          <Spec label={t('hostek.memoryClock')} value={mhz(g.memClockMhz || g.memMaxClockMhz)} />
          <Spec label={t('hostek.temperature')} value={degC(g.tempC)} />
        </CompCard>
      ))}

      {/* Motherboard */}
      <CompCard
        icon={<MotherboardIcon className="h-5 w-5 text-text-secondary" />}
        tileClass="bg-fill/15"
        title={join(board.manufacturer, board.model) || t('hostek.mainboard')}
        subtitle={board.version || undefined}
      >
        <Spec label={t('hostek.biosVendor')} value={board.biosVendor} />
        <Spec label={t('hostek.biosVersion')} value={board.biosVersion} />
        <Spec label={t('hostek.biosDate')} value={board.biosDate} />
      </CompCard>

      {/* System SSD */}
      <CompCard
        icon={<SsdIcon className="h-5 w-5 text-ssd" />}
        tileClass="bg-ssd/15"
        title={disk.model || disk.device || t('hostek.systemDisk')}
        subtitle={join(disk.type, disk.device && `/dev/${disk.device}`)}
      >
        <Spec label={t('hostek.capacity')} value={disk.sizeBytes ? formatBytes(disk.sizeBytes) : undefined} />
        <Spec
          label={t('hostek.health')}
          value={disk.health ? <Badge variant={disk.health.toUpperCase().includes('PASS') ? 'success' : 'warning'}>{disk.health}</Badge> : undefined}
        />
        <Spec label={t('hostek.temperature')} value={degC(disk.tempC)} />
        <Spec label={t('hostek.powerOnHours')} value={disk.powerOnHours ? disk.powerOnHours.toLocaleString() : undefined} />
        <Spec label={t('hostek.firmware')} value={disk.firmware} />
        <Spec label={t('hostek.serial')} value={disk.serial} />
      </CompCard>

      {/* Network interface(s) */}
      {hw.nics?.map((n, i) => (
        <CompCard
          key={i}
          icon={<EthernetIcon className="h-5 w-5 text-net" />}
          tileClass="bg-net/15"
          title={n.model || n.name || t('hostek.network')}
          subtitle={n.name && n.model ? n.name : undefined}
        >
          <Spec label={t('hostek.link')} value={n.link ? <Badge variant={n.link === 'up' ? 'success' : 'neutral'}>{n.link}</Badge> : undefined} />
          <Spec label={t('hostek.speed')} value={n.speedMbps && n.speedMbps > 0 ? `${n.speedMbps} Mbps` : undefined} />
          <Spec label={t('hostek.driver')} value={n.driver} />
          <Spec label={t('hostek.mac')} value={n.mac} />
        </CompCard>
      ))}
    </Grid>
  );
}
