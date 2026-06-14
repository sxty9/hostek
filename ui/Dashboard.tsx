import { useState } from 'react';
import { ContentRegion, SegmentedControl, Stack, type SegmentedOption, type ServiceContextProps } from '@holistic/ui';
import { System } from './System';
import { Performance } from './Performance';
import { Config } from './Config';
import { Disks } from './Disks';
import { Processes } from './Processes';

type Tab = 'system' | 'performance' | 'config' | 'disks' | 'processes';

export function Dashboard(props: ServiceContextProps) {
  const { user } = props;
  const [tab, setTab] = useState<Tab>('system');

  // Everyone (read-only) sees System, Performance (aggregate only) and Disks. Admins
  // (Linux sudo) additionally get Config and the per-process Processes breakdown.
  // Order: System · Performance · Config · Disks · Processes.
  const options: SegmentedOption<Tab>[] = [
    { value: 'system', label: 'System' },
    { value: 'performance', label: 'Performance' },
  ];
  if (user.isAdmin) options.push({ value: 'config', label: 'Config' });
  options.push({ value: 'disks', label: 'Disks' });
  if (user.isAdmin) options.push({ value: 'processes', label: 'Processes' });

  return (
    <ContentRegion>
      <Stack gap={4}>
        <SegmentedControl value={tab} onChange={setTab} options={options} />
        {tab === 'system' && <System {...props} />}
        {tab === 'performance' && <Performance {...props} />}
        {tab === 'config' && user.isAdmin && <Config {...props} />}
        {tab === 'disks' && <Disks {...props} />}
        {tab === 'processes' && user.isAdmin && <Processes {...props} />}
      </Stack>
    </ContentRegion>
  );
}
