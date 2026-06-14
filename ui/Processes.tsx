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
  type Column,
  type ServiceContextProps,
  type StreamSeries,
} from '@holistic/ui';
import type { Process, ProcessesResponse } from './types';

// ~60s of history at the 2s poll cadence, used to draw the per-column stream graphs.
const HISTORY = 30;
const TOP_N = 6;
// Categorical palette for the stacked stream (distinct hues + a muted "Other").
const PALETTE = [
  'rgb(var(--cpu))',
  'rgb(var(--ram))',
  'rgb(var(--gpu))',
  'rgb(var(--net))',
  'rgb(var(--ssd))',
  'rgb(var(--warning))',
];
const OTHER_COLOR = 'rgb(var(--fill) / 0.35)';

function shortName(name: string): string {
  return name.length > 18 ? name.slice(0, 17) + '…' : name;
}

interface StreamState {
  colorByPid: Map<number, string>;
  order: number[];
  count: number;
}

// buildStreams stacks the top-N processes' contribution to a metric over the history
// window (plus an aggregated "Other"). It persists per-PID color and a first-seen stack
// order in `st`, so a process keeps the same color and layer even after it dips into
// "Other" and reappears (instead of being re-colored/re-ordered each frame).
function buildStreams(
  history: Process[][],
  get: (p: Process) => number,
  st: StreamState,
): { series: StreamSeries[]; legend: { label: string; color: string }[] } {
  if (history.length === 0) return { series: [], legend: [] };
  const latest = history[history.length - 1];
  const ranked = latest.filter((p) => get(p) > 0).sort((a, b) => get(b) - get(a)).slice(0, TOP_N);
  if (ranked.length === 0) return { series: [], legend: [] };

  for (const p of ranked) {
    if (!st.colorByPid.has(p.pid)) {
      st.colorByPid.set(p.pid, PALETTE[st.count % PALETTE.length]);
      st.count += 1;
    }
    if (!st.order.includes(p.pid)) st.order.push(p.pid);
  }

  const topPids = new Set(ranked.map((p) => p.pid));
  const nameByPid = new Map(ranked.map((p) => [p.pid, shortName(p.name)]));
  const maps = history.map((frame) => {
    const m = new Map<number, Process>();
    for (const p of frame) m.set(p.pid, p);
    return m;
  });

  // Stable stack order: lay the shown PIDs out by their first-seen position.
  const shown = [...ranked].sort((a, b) => st.order.indexOf(a.pid) - st.order.indexOf(b.pid));
  const series: StreamSeries[] = shown.map((p) => ({
    label: nameByPid.get(p.pid) ?? String(p.pid),
    color: st.colorByPid.get(p.pid) ?? OTHER_COLOR,
    data: maps.map((m) => {
      const x = m.get(p.pid);
      return x ? get(x) : 0;
    }),
  }));

  const other = history.map((frame) => {
    let sum = 0;
    for (const p of frame) {
      if (!topPids.has(p.pid)) {
        const v = get(p);
        if (v > 0) sum += v;
      }
    }
    return sum;
  });
  if (other.some((v) => v > 0)) series.push({ label: 'Other', color: OTHER_COLOR, data: other });

  return { series, legend: series.map((s) => ({ label: s.label, color: s.color })) };
}

// ColumnHover wraps a sortable column header so hovering it reveals a stream graph of
// the top processes for that metric (and stays open while the pointer is over it). The
// stream is built lazily — only while the panel is actually open — to keep the live
// table render cheap (HoverPanel evaluates the panel function only when shown). The
// per-column StreamState ref keeps process colors/order stable across frames.
function ColumnHover({ label, history, get }: { label: string; history: Process[][]; get: (p: Process) => number }) {
  const stateRef = useRef<StreamState>({ colorByPid: new Map(), order: [], count: 0 });
  return (
    <HoverPanel
      width={360}
      panel={() => {
        const { series, legend } = buildStreams(history, get, stateRef.current);
        return (
          <Stack gap={2}>
            <Text variant="caption" weight="semibold">
              {label} — top processes (last {HISTORY * 2}s)
            </Text>
            {series.length === 0 ? (
              <Text variant="caption" color="tertiary">
                No activity recorded yet.
              </Text>
            ) : (
              <>
                <StreamGraph series={series} height={120} />
                <Legend items={legend} />
              </>
            )}
          </Stack>
        );
      }}
    >
      {label}
    </HoverPanel>
  );
}

export function Processes({ api }: ServiceContextProps) {
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
    { key: 'name', header: 'Name', sortable: true, sortValue: (p) => p.name.toLowerCase(), hideable: false },
    { key: 'user', header: 'User', sortable: true, sortValue: (p) => p.user, width: 120 },
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
      header: <ColumnHover label="Memory" history={history} get={(p) => p.memRss} />,
      toggleLabel: 'Memory',
      align: 'right',
      sortable: true,
      sortValue: (p) => p.memRss,
      render: (p) => formatBytes(p.memRss),
      width: 100,
    },
    { key: 'memPct', header: 'Mem %', align: 'right', sortable: true, sortValue: (p) => p.memPercent, render: (p) => p.memPercent.toFixed(1), width: 80, defaultHidden: true },
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
      header: 'GPU engine',
      sortable: true,
      sortValue: (p) => p.gpuEngine ?? '',
      render: (p) => p.gpuEngine || '—',
      width: 120,
      defaultHidden: true,
    },
    {
      key: 'net',
      header: <ColumnHover label="Network" history={history} get={(p) => p.netRxRate + p.netTxRate} />,
      toggleLabel: 'Network',
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
    { key: 'status', header: 'Status', render: (p) => <Badge variant="neutral">{p.status || '—'}</Badge>, width: 100, defaultHidden: true },
    ],
    [history],
  );

  return (
    <Stack gap={3}>
      <Stack direction="row" align="center" justify="between" gap={3}>
        <Text variant="subhead" weight="semibold">
          {rows.length} processes
        </Text>
        <SearchField value={q} onChange={setQ} placeholder="Filter by name, user or PID" />
      </Stack>
      <DataTable
        columns={columns}
        rows={rows}
        rowKey={(p) => String(p.pid)}
        initialSort={{ key: 'cpu', dir: 'desc' }}
        maxHeight={560}
        columnToggle
        emptyState={<EmptyState title="No processes" description="Nothing matches your filter." />}
      />
    </Stack>
  );
}
