import { useState } from 'react';
import {
  Badge,
  DataTable,
  EmptyState,
  SearchField,
  Stack,
  Text,
  formatBytes,
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
    { key: 'name', header: 'Name', sortable: true, sortValue: (p) => p.name.toLowerCase() },
    { key: 'user', header: 'User', sortable: true, sortValue: (p) => p.user, width: 120 },
    { key: 'cpu', header: 'CPU %', align: 'right', sortable: true, sortValue: (p) => p.cpuPercent, render: (p) => p.cpuPercent.toFixed(1), width: 88 },
    { key: 'mem', header: 'Memory', align: 'right', sortable: true, sortValue: (p) => p.memRss, render: (p) => formatBytes(p.memRss), width: 104 },
    { key: 'memPct', header: 'Mem %', align: 'right', sortable: true, sortValue: (p) => p.memPercent, render: (p) => p.memPercent.toFixed(1), width: 84 },
    { key: 'status', header: 'Status', render: (p) => <Badge variant="neutral">{p.status || '—'}</Badge>, width: 100 },
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
        maxHeight={520}
        emptyState={<EmptyState title="No processes" description="Nothing matches your filter." />}
      />
    </Stack>
  );
}
