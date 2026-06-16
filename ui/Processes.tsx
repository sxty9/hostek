import { useEffect, useMemo, useRef, useState } from 'react';
import {
  Badge,
  DataTable,
  EmptyState,
  HoverPanel,
  Legend,
  SearchField,
  Stack,
  StreamGraph,
  Text,
  formatBytes,
  formatRate,
  useLiveQuery,
  useT,
  type Column,
  type ServiceContextProps,
  type StreamSeries,
} from '@holistic/ui';
import type { Process, ProcessesResponse } from './types';

// ~60s of history at the 2s poll cadence, used to draw the per-column stream graphs.
const HISTORY = 30;
// Promote bar: the top-N processes by SMOOTHED value get their own band; the rest fold
// into "Other".
const TOP_N = 6;
// Frames averaged when ranking processes (~12s). Ranking by the smoothed value (not the
// instantaneous one) denoises the rank itself, so a single spike/dip can't reshuffle it.
const SMOOTH = 6;
// Membership hysteresis: once a process crosses the promote bar it keeps its own band and
// only folds back into "Other" after it has stayed BELOW the bar continuously for this long.
// A process bouncing across the bar resets this timer every time it pops back in, so a
// borderline process never flickers in/out — only a genuinely sustained drop demotes it.
const DEMOTE_MS = 60_000;
// Categorical palette for the stacked stream (distinct hues + a muted "Other"). Sticky
// membership can briefly hold more than TOP_N bands at once during contention, so the
// palette also doubles as the hard ceiling (MAX_SHOWN) — every band keeps a distinct hue.
const PALETTE = [
  'rgb(var(--cpu))', // green
  'rgb(var(--ram))', // blue
  'rgb(var(--gpu))', // purple
  'rgb(var(--net))', // orange
  'rgb(var(--ssd))', // teal
  'rgb(var(--warning))', // yellow
  'rgb(var(--danger))', // red — backstop, only reached under heavy contention
];
const MAX_SHOWN = PALETTE.length;
const OTHER_COLOR = 'rgb(var(--fill) / 0.35)';

function shortName(name: string): string {
  return name.length > 18 ? name.slice(0, 17) + '…' : name;
}

interface StreamState {
  // Persistent across hover open/close (lives for the column's lifetime) so membership
  // hysteresis, colours and names survive the panel unmounting between hovers.
  shown: Set<number>; // PIDs currently promoted to their own band
  outAt: Map<number, number>; // pid -> ms timestamp it last dropped below the promote bar
  colorByPid: Map<number, string>;
  names: Map<number, string>; // last-seen short name per pid (kept while a band lingers)
}

// buildStreams reconciles which processes get their own band (membership) and renders the
// stacked history for them plus an aggregated "Other".
//
// Membership uses asymmetric hysteresis: a process is promoted the instant its smoothed
// value enters the top-N, but is only demoted back into "Other" after it has stayed out of
// the top-N for DEMOTE_MS continuously — so a borderline process never flickers in/out.
//
// Stack ORDER is frozen per hover: `frozenOrder` (null on the first paint of a hover) is
// kept as-is; demoted bands drop out without disturbing their neighbours and freshly
// promoted bands are appended on top. The order is only recomputed when a new hover opens
// (a fresh StreamPanel mount passes null again).
function buildStreams(
  history: Process[][],
  get: (p: Process) => number,
  st: StreamState,
  frozenOrder: number[] | null,
  now: number,
): { series: StreamSeries[]; legend: { label: string; color: string }[]; order: number[] } {
  if (history.length === 0) return { series: [], legend: [], order: [] };

  // Smoothed value per pid over the last SMOOTH frames → denoised ranking and promote set.
  const win = history.slice(-SMOOTH);
  const sum = new Map<number, number>();
  for (const frame of win)
    for (const p of frame) {
      sum.set(p.pid, (sum.get(p.pid) ?? 0) + get(p));
      st.names.set(p.pid, shortName(p.name)); // latest name wins; kept for lingering bands
    }
  const ranked = [...sum.entries()]
    .map(([pid, total]) => [pid, total / win.length] as const)
    .filter(([, avg]) => avg > 0)
    .sort((a, b) => b[1] - a[1]);
  const avg = new Map<number, number>(ranked);
  const promote = new Set(ranked.slice(0, TOP_N).map(([pid]) => pid));

  // --- Membership hysteresis ---
  for (const pid of promote) {
    st.shown.add(pid); // instant promote on crossing the bar
    st.outAt.delete(pid);
  }
  for (const pid of [...st.shown]) {
    if (promote.has(pid)) continue; // still in range → no demotion timer
    const since = st.outAt.get(pid);
    if (since === undefined) st.outAt.set(pid, now); // just dropped out → start the clock
    else if (now - since >= DEMOTE_MS) {
      st.shown.delete(pid); // out of the bar long enough → fold back into "Other"
      st.outAt.delete(pid);
    }
  }
  // Backstop ceiling: never show more bands than the palette has hues; drop the ones
  // nearest demotion (longest out of the bar) first.
  if (st.shown.size > MAX_SHOWN) {
    const out = [...st.shown]
      .filter((pid) => st.outAt.has(pid))
      .sort((a, b) => (st.outAt.get(a) ?? 0) - (st.outAt.get(b) ?? 0));
    for (const pid of out) {
      if (st.shown.size <= MAX_SHOWN) break;
      st.shown.delete(pid);
      st.outAt.delete(pid);
    }
  }

  // --- Stable, collision-free colours ---
  // Keep each pid's remembered hue while it is still free, then hand out the rest.
  const used = new Set<string>();
  for (const pid of st.shown) {
    const c = st.colorByPid.get(pid);
    if (c && !used.has(c)) used.add(c);
    else st.colorByPid.delete(pid);
  }
  for (const pid of st.shown) {
    if (st.colorByPid.has(pid)) continue;
    const free = PALETTE.find((c) => !used.has(c)) ?? OTHER_COLOR;
    st.colorByPid.set(pid, free);
    used.add(free);
  }

  // --- Frozen stack order ---
  // Biggest at the bottom on first paint; thereafter drop gone bands and append newly
  // promoted ones on top, leaving survivors in place (StreamGraph stacks series[0] first).
  const byAvgDesc = (a: number, b: number) => (avg.get(b) ?? 0) - (avg.get(a) ?? 0);
  let order = (frozenOrder ?? [...st.shown].sort(byAvgDesc)).filter((pid) => st.shown.has(pid));
  const present = new Set(order);
  order = [...order, ...[...st.shown].filter((pid) => !present.has(pid)).sort(byAvgDesc)];

  const maps = history.map((frame) => {
    const m = new Map<number, Process>();
    for (const p of frame) m.set(p.pid, p);
    return m;
  });
  const series: StreamSeries[] = order.map((pid) => ({
    label: st.names.get(pid) ?? String(pid),
    color: st.colorByPid.get(pid) ?? OTHER_COLOR,
    data: maps.map((m) => {
      const x = m.get(pid);
      return x ? get(x) : 0;
    }),
  }));

  const shownSet = new Set(order);
  const other = history.map((frame) => {
    let total = 0;
    for (const p of frame) {
      if (!shownSet.has(p.pid)) {
        const v = get(p);
        if (v > 0) total += v;
      }
    }
    return total;
  });
  if (other.some((v) => v > 0)) series.push({ label: 'Other', color: OTHER_COLOR, data: other });

  return { series, legend: series.map((s) => ({ label: s.label, color: s.color })), order };
}

// StreamPanel renders the stream graph for one metric. HoverPanel only mounts it while the
// panel is open and unmounts it on close, so its `orderRef` (the frozen stack order) is
// naturally fresh on every hover — the order is snapshotted on the first paint and then
// held still for the rest of that hover (less visual churn). The hysteresis state `st` is
// owned by ColumnHover and outlives the panel, so membership/colours carry across hovers.
function StreamPanel({
  label,
  history,
  get,
  st,
}: {
  label: string;
  history: Process[][];
  get: (p: Process) => number;
  st: StreamState;
}) {
  const t = useT();
  const orderRef = useRef<number[] | null>(null);
  const { series, legend, order } = buildStreams(history, get, st, orderRef.current, Date.now());
  orderRef.current = order;
  return (
    <Stack gap={2}>
      <Text variant="caption" weight="semibold">
        {t('hostek.topProcesses', { label, secs: HISTORY * 2 })}
      </Text>
      {series.length === 0 ? (
        <Text variant="caption" color="tertiary">
          {t('hostek.noActivity')}
        </Text>
      ) : (
        <>
          <StreamGraph series={series} height={120} />
          <Legend items={legend} />
        </>
      )}
    </Stack>
  );
}

// ColumnHover wraps a sortable column header so hovering it reveals a stream graph of the
// top processes for that metric (and stays open while the pointer is over it). The stream
// is built lazily — only while the panel is open — to keep the live table render cheap
// (HoverPanel evaluates the panel function only when shown). The per-column StreamState ref
// carries membership hysteresis, colours and names across hovers.
function ColumnHover({ label, history, get }: { label: string; history: Process[][]; get: (p: Process) => number }) {
  const stateRef = useRef<StreamState>({ shown: new Set(), outAt: new Map(), colorByPid: new Map(), names: new Map() });
  return (
    <HoverPanel width={360} panel={() => <StreamPanel label={label} history={history} get={get} st={stateRef.current} />}>
      {label}
    </HoverPanel>
  );
}

export function Processes({ api }: ServiceContextProps) {
  const t = useT();
  const { data } = useLiveQuery<ProcessesResponse>(() => api.get<ProcessesResponse>('processes'), 2000);
  const [q, setQ] = useState('');
  const [history, setHistory] = useState<Process[][]>([]);

  // Accumulate a rolling per-process history client-side for the column stream graphs.
  useEffect(() => {
    if (!data?.processes) return;
    setHistory((h) => {
      const next = [...h, data.processes];
      return next.length > HISTORY ? next.slice(next.length - HISTORY) : next;
    });
  }, [data]);

  const all = data?.processes ?? [];
  const needle = q.trim().toLowerCase();
  const rows = needle
    ? all.filter((p) => p.name.toLowerCase().includes(needle) || p.user.toLowerCase().includes(needle) || String(p.pid).includes(needle))
    : all;

  // Memoized on `history` (the only changing input the headers close over) so typing in
  // the filter doesn't rebuild the header elements / re-run the stream computation.
  const columns = useMemo<Column<Process>[]>(
    () => [
    { key: 'pid', header: 'PID', align: 'right', sortable: true, sortValue: (p) => p.pid, width: 72 },
    { key: 'name', header: t('hostek.colName'), sortable: true, sortValue: (p) => p.name.toLowerCase(), hideable: false },
    { key: 'user', header: t('hostek.colUser'), sortable: true, sortValue: (p) => p.user, width: 120 },
    {
      key: 'cpu',
      header: <ColumnHover label="CPU %" history={history} get={(p) => p.cpuPercent} />,
      toggleLabel: 'CPU %',
      align: 'right',
      sortable: true,
      sortValue: (p) => p.cpuPercent,
      render: (p) => p.cpuPercent.toFixed(1),
      width: 84,
    },
    {
      key: 'mem',
      header: <ColumnHover label={t('hostek.memory')} history={history} get={(p) => p.memRss} />,
      toggleLabel: t('hostek.memory'),
      align: 'right',
      sortable: true,
      sortValue: (p) => p.memRss,
      render: (p) => formatBytes(p.memRss),
      width: 100,
    },
    { key: 'memPct', header: t('hostek.colMemPct'), align: 'right', sortable: true, sortValue: (p) => p.memPercent, render: (p) => p.memPercent.toFixed(1), width: 80, defaultHidden: true },
    {
      key: 'gpu',
      header: <ColumnHover label="GPU %" history={history} get={(p) => p.gpuPercent} />,
      toggleLabel: 'GPU %',
      align: 'right',
      sortable: true,
      sortValue: (p) => p.gpuPercent,
      render: (p) => (p.gpuPercent > 0 ? p.gpuPercent.toFixed(1) : '—'),
      width: 80,
    },
    {
      key: 'gpuEngine',
      header: t('hostek.colGpuEngine'),
      sortable: true,
      sortValue: (p) => p.gpuEngine ?? '',
      render: (p) => p.gpuEngine || '—',
      width: 120,
      defaultHidden: true,
    },
    {
      key: 'net',
      header: <ColumnHover label={t('hostek.network')} history={history} get={(p) => p.netRxRate + p.netTxRate} />,
      toggleLabel: t('hostek.network'),
      align: 'right',
      sortable: true,
      sortValue: (p) => p.netRxRate + p.netTxRate,
      render: (p) =>
        p.netRxRate + p.netTxRate > 0 ? (
          <Text variant="footnote" className="tabular-nums">
            ↓ {formatRate(p.netRxRate)} · ↑ {formatRate(p.netTxRate)}
          </Text>
        ) : (
          '—'
        ),
      width: 168,
    },
    { key: 'status', header: t('hostek.colStatus'), render: (p) => <Badge variant="neutral">{p.status || '—'}</Badge>, width: 100, defaultHidden: true },
    ],
    [history, t],
  );

  return (
    <Stack gap={3}>
      <Stack direction="row" align="center" justify="between" gap={3}>
        <Text variant="subhead" weight="semibold">
          {t('hostek.procCount', { count: rows.length })}
        </Text>
        <SearchField value={q} onChange={setQ} placeholder={t('hostek.filterProcs')} />
      </Stack>
      <DataTable
        columns={columns}
        rows={rows}
        rowKey={(p) => String(p.pid)}
        initialSort={{ key: 'cpu', dir: 'desc' }}
        maxHeight={560}
        columnToggle
        emptyState={<EmptyState title={t('hostek.noProcs')} description={t('hostek.noProcsDesc')} />}
      />
    </Stack>
  );
}
