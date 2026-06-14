import { useState } from 'react';
import {
  Badge,
  DataTable,
  EmptyState,
  SearchField,
  Stack,
  Text,
  formatBytes,
  formatRate,
  useLiveQuery,
  type Column,
  type ServiceContextProps,
} from '@holistic/ui';
import type { Process, ProcessesResponse } from './types';

export function Processes({ api }: ServiceContextProps) {
  const { data } = useLiveQuery<ProcessesResponse>(() => api.get<ProcessesResponse>('processes'), 2000);
  const [q, setQ] = useState('');

  const all = data?.processes ?? [];
  const needle = q.trim().toLowerCase();
  const rows = needle ? all.filter((p) => p.name.toLowerCase().includes(needle) || p.user.toLowerCase().includes(needle)) : all;

  const columns: Column<Process>[] = [
    { key: 'pid', header: 'PID', align: 'right', sortable: true, sortValue: (p) => p.pid, width: 72 },
    { key: 'name', header: 'Name', sortable: true, sortValue: (p) => p.name.toLowerCase(), hideable: false },
    { key: 'user', header: 'User', sortable: true, sortValue: (p) => p.user, width: 120 },
    { key: 'cpu', header: 'CPU %', align: 'right', sortable: true, sortValue: (p) => p.cpuPercent, render: (p) => p.cpuPercent.toFixed(1), width: 84 },
    { key: 'mem', header: 'Memory', align: 'right', sortable: true, sortValue: (p) => p.memRss, render: (p) => formatBytes(p.memRss), width: 100 },
    { key: 'memPct', header: 'Mem %', align: 'right', sortable: true, sortValue: (p) => p.memPercent, render: (p) => p.memPercent.toFixed(1), width: 80, defaultHidden: true },
    {
      key: 'gpu',
      header: 'GPU %',
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
      header: 'Network',
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
  ];

  return (
    <Stack gap={3}>
      <Stack direction="row" align="center" justify="between" gap={3}>
        <Text variant="subhead" weight="semibold">
          {rows.length} processes
        </Text>
        <SearchField value={q} onChange={setQ} placeholder="Filter by name or user" />
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
