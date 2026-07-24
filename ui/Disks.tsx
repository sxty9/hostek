import {
  ArrowUpIcon,
  Badge,
  Box,
  Button,
  DiskIcon,
  Donut,
  EmptyState,
  Grid,
  Markdown,
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
  userHasRight,
  type ServiceContextProps,
} from '@holistic/ui';
import { useState, type ReactNode } from 'react';
import type { DiskDevice, DiskPartition, DisksResponse } from './types';

// The "Rate with AI" button routes disk health through the shared `aigentic` service
// (apiFor('aigentic') → POST run). We reuse its per-user Anthropic key + billing rather
// than giving hostek its own credential; hostek and aigentic share the holistic session.
interface AigenticRunResponse {
  data: { output: string; engine?: string; model?: string };
}

// aigentic's chat-handoff contract, mirrored here because hostek can't import across plugins
// (kept in sync with aigentic/ui/types.ts). Writing this seed to localStorage and switching to
// aigentic seeds a new chat with the prompt + answer, so the rating continues as a conversation.
const AIGENTIC_CHAT_SEED_KEY = 'aigentic.chat.seed';
interface AigenticChatSeed {
  prompt: string;
  answer: string;
  engine?: string;
  model?: string;
  folder?: string;
}

// Compact, health-focused view of a disk for the AI prompt (drops noise like WWN/serial).
function ratePayload(disks: DiskDevice[]) {
  return disks.map((d) => ({
    device: `/dev/${d.name}`,
    model: d.model,
    kind: d.type,
    rotational: d.rotational,
    capacityBytes: d.sizeBytes,
    smartHealth: d.health,
    status: d.healthStatus,
    reason: d.healthReason,
    lifeRemainingPct: d.lifePercent,
    agePct: d.agePercent,
    tempC: d.tempC,
    powerOnHours: d.powerOnHours,
    powerCycles: d.powerCycles,
    firmware: d.firmware,
    smart: d.smart,
    partitions: (d.partitions ?? [])
      .filter((p) => p.mount)
      .map((p) => ({ mount: p.mount, fstype: p.fstype, usedBytes: p.used, totalBytes: p.total, percent: p.percent })),
  }));
}

const join = (...parts: (string | undefined | false)[]) => parts.filter(Boolean).join(' · ');

// Health verdict → Badge variant. Anything but critical/warning reads as healthy.
const statusVariant = (s: DiskDevice['healthStatus']): 'success' | 'warning' | 'danger' =>
  s === 'critical' ? 'danger' : s === 'warning' ? 'warning' : 'success';

// Remaining-life % → bar tone (green→amber→red as endurance runs out).
const lifeTone = (p: number): 'ssd' | 'warning' | 'danger' => (p <= 10 ? 'danger' : p <= 25 ? 'warning' : 'ssd');

// Filesystem types that are a container/member, not something the kernel can mount as a
// filesystem. hostek-diskctl refuses them anyway — this only keeps us from offering a
// Mount button that could never work. An empty fstype (unformatted) is equally unmountable.
const CONTAINER_FS = new Set(['swap', 'crypto_luks', 'lvm2_member', 'linux_raid_member', 'zfs_member']);
const isMountable = (p: DiskPartition): boolean => !!p.fstype && !CONTAINER_FS.has(p.fstype.toLowerCase());

// Stable list order: category first (NVMe → SATA → USB → rest), then ascending by
// physical port within the category. catRank buckets by transport; portNum parses
// the SATA port out of the backend's "SATA Port N" label (non-SATA has no number,
// so it sinks to the end of its bucket where the device-name tiebreak orders it).
const catRank = (d: DiskDevice): number => {
  const t = (d.transport ?? '').toLowerCase();
  if (t === 'nvme') return 0;
  if (t === 'sata' || t === 'ata') return 1;
  if (t === 'usb') return 2;
  return 3;
};
const portNum = (d: DiskDevice): number => {
  const m = /Port\s+(\d+)/.exec(d.port ?? '');
  return m ? Number(m[1]) : Number.MAX_SAFE_INTEGER;
};
// Ports restart at 1 on every controller, so disks must be grouped by controller
// before the port number means anything: the platform's own chipset first, then any
// add-in controllers (stable across identical cards via their PCI address).
const ctrlRank = (d: DiskDevice): number => (d.controller?.addOn ? 1 : 0);
const ctrlPci = (d: DiskDevice): string => d.controller?.pci ?? '';

const byCategoryThenPort = (a: DiskDevice, b: DiskDevice): number =>
  catRank(a) - catRank(b) ||
  ctrlRank(a) - ctrlRank(b) ||
  ctrlPci(a).localeCompare(ctrlPci(b)) ||
  portNum(a) - portNum(b) ||
  a.name.localeCompare(b.name);

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

// Shown in the SMART slot of a drive the kernel has just handed us: it is listed and
// sized, but the background probe hasn't read its health yet (`smartPending`). Without
// this the card would render an unexplained gap that reads like "no SMART to report".
// The pulsing halo behind the spinner picks up the ssd accent the card's icon uses.
function SmartPendingBlock() {
  const t = useT();
  return (
    <Stack direction="row" align="center" gap={3} className="border-t border-separator pt-3">
      <Box className="relative flex h-9 w-9 shrink-0 items-center justify-center">
        <Box className="absolute inset-0 animate-ping rounded-full bg-ssd/20" />
        <Spinner className="h-6 w-6 text-ssd" />
      </Box>
      <Stack gap={0} className="min-w-0">
        <Text variant="footnote" weight="medium">
          {t('hostek.smartPending')}
        </Text>
        <Text variant="caption" color="tertiary">
          {t('hostek.smartPendingHint')}
        </Text>
      </Stack>
    </Stack>
  );
}

interface DiskCardProps {
  d: DiskDevice;
  canMount: boolean;
  canEject: boolean;
  busy: string | null; // device name of the action in flight, if any
  onMount: (p: DiskPartition) => void;
  onUnmount: (p: DiskPartition) => void;
  onEject: (d: DiskDevice) => void;
}

function DiskCard({ d, canMount, canEject, busy, onMount, onUnmount, onEject }: DiskCardProps) {
  const t = useT();
  const partitions = d.partitions ?? [];
  const mounts = partitions.filter((p) => p.mount);
  // Usage is filesystem-level (sum over mounted partitions) so the donut tracks the
  // partition rows below; fall back to the raw device size when nothing is mounted.
  // Clamp so unusual backend numbers (e.g. overlapping mounts) can't overdraw the ring.
  const fsTotal = mounts.reduce((s, p) => s + (p.total ?? 0), 0);
  const fsUsed = mounts.reduce((s, p) => s + (p.used ?? 0), 0);
  const total = fsTotal > 0 ? fsTotal : d.sizeBytes;
  const usedPct = total > 0 ? Math.min(100, (fsUsed / total) * 100) : 0;
  const hasUsage = mounts.length > 0 && fsUsed > 0;
  const Icon = d.rotational ? DiskIcon : SsdIcon;
  // Connection (SATA port / NVMe / USB) gets its own labeled row below, so keep
  // the subtitle to the device node + serial.
  const subtitle = join(`/dev/${d.name}`, d.serial || '');
  const connection = d.port || (d.transport && d.transport.toUpperCase()) || '';

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
              {d.unreachable && <Badge variant="danger">{t('hostek.unreachable')}</Badge>}
              {d.isSystem && <Badge variant="accent">{t('hostek.systemBadge')}</Badge>}
              {d.type && <Badge variant="neutral">{d.type}</Badge>}
            </Stack>
            <Marquee text={subtitle} className="text-caption text-text-secondary" />
            {d.unreachable && (
              <Text variant="caption" color="tertiary">
                {t('hostek.unreachableHint')}
              </Text>
            )}
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

        {/* Connection — which port the disk hangs off, plus the controller that owns
            that port (a chipset port 3 and an add-in card's port 3 are different sockets). */}
        {(connection || d.controller) && (
          <Stack gap={1} className="border-t border-separator pt-2">
            {connection && <InfoRow label={t('hostek.connection')}>{connection}</InfoRow>}
            {d.controller && (
              <InfoRow label={t('hostek.controller')}>
                <Stack direction="row" align="center" gap={2} className="min-w-0">
                  <Text variant="footnote" truncate>
                    {d.controller.name || d.controller.pci}
                  </Text>
                  {d.controller.addOn && <Badge variant="accent">{t('hostek.addOn')}</Badge>}
                </Stack>
              </InfoRow>
            )}
          </Stack>
        )}

        {/* SMART / health (symmetric with the System-tab disk card). A drive whose SMART
            is still being read shows a spinner here instead; data always wins over the
            spinner, so a disk with values can never appear to be still loading. */}
        {!hasSmart && d.smartPending && <SmartPendingBlock />}
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

        {/* Partitions — every one, not just the mounted ones: an unmounted partition is
            precisely what you came here to mount, so hiding it would hide the action. */}
        {partitions.length > 0 && (
          <Stack gap={2} className="border-t border-separator pt-2">
            {partitions.map((p) => (
              <Stack key={p.name} direction="row" justify="between" gap={3} align="center">
                <Stack gap={0} className="min-w-0">
                  <Text variant="caption" truncate>
                    {p.mount || `/dev/${p.name}`}
                  </Text>
                  <Text variant="caption" color="tertiary" truncate>
                    {join(p.fstype || t('hostek.noFilesystem'), !p.mount && formatBytes(p.sizeBytes))}
                  </Text>
                </Stack>
                <Stack direction="row" align="center" gap={2} className="shrink-0">
                  {p.mount ? (
                    <Text variant="caption" color="secondary" className="tabular-nums">
                      {formatBytes(p.used ?? 0)} / {formatBytes(p.total ?? p.sizeBytes)} · {(p.percent ?? 0).toFixed(0)}%
                    </Text>
                  ) : null}
                  {/* isMountable gates Unmount too, not just Mount: an active swap partition
                      reports a truthy "[SWAP]" mountpoint but has no filesystem the unmount
                      verb can release, so a bare `p.mount` check would offer a button that
                      always fails. Symmetric gating keeps the pair honest. */}
                  {canMount && p.mount && isMountable(p) && (
                    <Button variant="secondary" size="sm" loading={busy === p.name} onClick={() => onUnmount(p)}>
                      {t('hostek.unmount')}
                    </Button>
                  )}
                  {canMount && !p.mount && isMountable(p) && (
                    <Button variant="tinted" size="sm" loading={busy === p.name} onClick={() => onMount(p)}>
                      {t('hostek.mount')}
                    </Button>
                  )}
                </Stack>
              </Stack>
            ))}
          </Stack>
        )}

        {/* Eject. Hidden for the system disk — the backend refuses it, so offering the
            button would only promise something it will not do. An unreachable disk keeps
            it: ejecting is exactly how you clear the stale node a hot-unplug left behind. */}
        {canEject && !d.isSystem && (
          <Stack direction="row" justify="end" className="border-t border-separator pt-2">
            <Button
              variant="secondary"
              size="sm"
              loading={busy === d.name}
              iconLeft={<ArrowUpIcon className="h-4 w-4" />}
              onClick={() => onEject(d)}
            >
              {t('hostek.eject')}
            </Button>
          </Stack>
        )}
      </Stack>
    </Panel>
  );
}

export function Disks({ api, apiFor, user, ui, nav }: ServiceContextProps) {
  const t = useT();
  const { data, refresh } = useLiveQuery<DisksResponse>(() => api.get<DisksResponse>('disks'), 5000);
  const [aiBusy, setAiBusy] = useState(false);
  const [aiResult, setAiResult] = useState<{ prompt: string; output: string; engine?: string; model?: string } | null>(null);
  // The device name of the storage action in flight — one at a time, so a card can show
  // which control is working without a second click landing on top of it.
  const [busy, setBusy] = useState<string | null>(null);

  if (!data) {
    return (
      <Stack align="center" justify="center" className="py-16">
        <Spinner />
      </Stack>
    );
  }

  // Fixed order: NVMe → SATA → USB, ascending by physical port within each group.
  const disks = [...(data.disks ?? [])].sort(byCategoryThenPort);

  if (disks.length === 0) {
    return <EmptyState icon={<DiskIcon />} title={t('hostek.noDisks')} description={t('hostek.noDisksDesc')} />;
  }

  // Aggregate used / total across every physical drive. Capacity is the raw device
  // size; "used" is filesystem-level (mounted partitions), matching each card's figures.
  const totalCapacity = disks.reduce((s, d) => s + (d.sizeBytes || 0), 0);
  const totalUsed = disks.reduce(
    (s, d) => s + (d.partitions ?? []).filter((p) => p.mount).reduce((a, p) => a + (p.used ?? 0), 0),
    0,
  );
  const overallPct = totalCapacity > 0 ? Math.min(100, (totalUsed / totalCapacity) * 100) : 0;

  // "Rate with AI" routes through aigentic's metered Anthropic API (admins always qualify).
  const canRate = userHasRight(user, 'hp_aigentic_api');
  // Storage actions are separately granted rights; the daemon enforces them regardless —
  // this only decides whether we show a control the user is allowed to use.
  const canMount = userHasRight(user, 'hp_hostek_mount');
  const canEject = userHasRight(user, 'hp_hostek_eject');

  // The three storage actions share a shape: latch the device busy, run it, say what
  // happened, then refresh so the card shows the new kernel state at once instead of up
  // to a poll later. On failure the description is the daemon's forwarded reason from the
  // privileged wrapper ("cannot unmount /dev/sdb1 — target is busy"), which is the only
  // part of a failure the user can actually act on.
  async function storageAction(device: string, failTitle: string, run: () => Promise<{ title: string; description?: string }>) {
    setBusy(device);
    try {
      const done = await run();
      ui.toast({ title: done.title, description: done.description, variant: 'success' });
    } catch (e) {
      ui.toast({ title: failTitle, description: (e as Error).message, variant: 'error' });
    } finally {
      setBusy(null);
      refresh();
    }
  }

  const mountPartition = (p: DiskPartition) =>
    storageAction(p.name, t('hostek.mountFailed'), async () => {
      const res = await api.post<{ mountpoint?: string }>('disks/mount', { name: p.name });
      return { title: t('hostek.mounted'), description: res.mountpoint };
    });

  const unmountPartition = (p: DiskPartition) =>
    storageAction(p.name, t('hostek.unmountFailed'), async () => {
      await api.post('disks/unmount', { name: p.name });
      return { title: t('hostek.unmounted'), description: `/dev/${p.name}` };
    });

  // Eject detaches the drive from the kernel — it leaves the tab and only comes back on a
  // rescan/reboot. That is not undoable by clicking again, so it is confirmed first.
  async function ejectDisk(d: DiskDevice) {
    const go = await ui.confirm({
      title: t('hostek.ejectConfirmTitle', { disk: d.model || `/dev/${d.name}` }),
      description: t('hostek.ejectConfirmBody'),
      danger: true,
      confirmLabel: t('hostek.eject'),
    });
    if (!go) return;
    await storageAction(d.name, t('hostek.ejectFailed'), async () => {
      await api.post('disks/eject', { name: d.name });
      return { title: t('hostek.ejected'), description: `/dev/${d.name}` };
    });
  }

  async function rateWithAI() {
    setAiBusy(true);
    try {
      const lang = typeof navigator !== 'undefined' ? navigator.language : 'en';
      const prompt = [
        'You are a storage-reliability expert. Assess the health of the disks below and give the user concrete, actionable recommendations.',
        `Respond in ${lang} using concise Markdown: start with a one-line overall verdict, then one short bullet per disk (its state plus what to do — watch, back up, plan replacement, improve cooling). Explain SMART warnings in plain terms. Be specific but do not over-alarm: reallocated sectors with zero pending/uncorrectable sectors are a watch item, not an emergency. Spinning HDDs have no wear %, only an age/uptime proxy.`,
        '',
        'Disks (JSON):',
        '```json',
        JSON.stringify(ratePayload(disks), null, 2),
        '```',
      ].join('\n');
      const res = await apiFor('aigentic').post<AigenticRunResponse>('run', {
        header: { kind: 'claude-api' },
        data: { prompt, outputFormat: 'markdown', maxTokens: 1200 },
      });
      setAiResult({ prompt, output: res.data.output, engine: res.data.engine, model: res.data.model });
    } catch (e) {
      ui.toast({ title: t('hostek.rateFailed'), description: (e as Error).message, variant: 'error' });
    } finally {
      setAiBusy(false);
    }
  }

  // Hand the rating off to the aigentic chat tab, where it continues as a conversation —
  // same seed contract the Files "Ask AI" panel uses.
  function continueInChat() {
    if (!aiResult) return;
    const seed: AigenticChatSeed = {
      prompt: aiResult.prompt,
      answer: aiResult.output,
      engine: aiResult.engine,
      model: aiResult.model,
      folder: 'hostek/disks',
    };
    try {
      localStorage.setItem(AIGENTIC_CHAT_SEED_KEY, JSON.stringify(seed));
    } catch {
      // localStorage unavailable (private mode / quota) — fall back to opening an empty chat.
    }
    nav.openService('aigentic');
  }

  return (
    <Stack gap={3}>
      <Stack direction="row" justify="between" align="center" gap={3}>
        <Text variant="subhead" weight="semibold">
          {t('hostek.diskCount', { count: disks.length })}
        </Text>
        {canRate && (
          <Button variant="primary" size="sm" loading={aiBusy} onClick={rateWithAI}>
            {t('hostek.rateWithAI')}
          </Button>
        )}
      </Stack>

      {/* AI health assessment (on demand) */}
      {aiResult && (
        <Panel className="p-4">
          <Stack gap={2}>
            <Stack direction="row" justify="between" align="center" gap={3}>
              <Text variant="subhead" weight="semibold">
                {t('hostek.aiAnalysis')}
              </Text>
              <Button variant="secondary" size="sm" onClick={continueInChat}>
                {t('hostek.continueInChat')}
              </Button>
            </Stack>
            <Markdown text={aiResult.output} />
          </Stack>
        </Panel>
      )}

      {/* Aggregate storage across all drives */}
      <Panel className="p-4">
        <Stack gap={2}>
          <Stack direction="row" justify="between" gap={2} align="baseline">
            <Text variant="footnote" color="secondary">
              {t('hostek.totalStorage')}
            </Text>
            <Text variant="footnote" weight="medium" className="tabular-nums">
              {formatBytes(totalUsed)} / {formatBytes(totalCapacity)} · {overallPct.toFixed(0)}%
            </Text>
          </Stack>
          <ProgressBar value={overallPct} tone="ssd" />
          <Stack direction="row" justify="between" gap={2}>
            <Text variant="caption" color="tertiary">
              {t('hostek.used')} {formatBytes(totalUsed)}
            </Text>
            <Text variant="caption" color="tertiary">
              {t('hostek.free')} {formatBytes(Math.max(0, totalCapacity - totalUsed))}
            </Text>
          </Stack>
        </Stack>
      </Panel>

      <Grid minItemWidth={360} gap={3}>
        {disks.map((d) => (
          <DiskCard
            key={d.name}
            d={d}
            canMount={canMount}
            canEject={canEject}
            busy={busy}
            onMount={mountPartition}
            onUnmount={unmountPartition}
            onEject={ejectDisk}
          />
        ))}
      </Grid>
    </Stack>
  );
}
