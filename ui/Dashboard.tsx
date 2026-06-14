import { useState } from 'react';
import { ContentRegion, SegmentedControl, Stack, userHasRight, type SegmentedOption, type ServiceContextProps } from '@holistic/ui';
import { System } from './System';
import { Performance } from './Performance';
import { Config } from './Config';
import { Disks } from './Disks';
import { Processes } from './Processes';

type Tab = 'system' | 'performance' | 'config' | 'disks' | 'processes';

export function Dashboard(props: ServiceContextProps) {
  const { user } = props;
  const [tab, setTab] = useState<Tab>('system');

  // Everyone (read-only) sees System, Performance (aggregate only) and Disks. Config
  // (power) and the per-process Processes breakdown need the matching right under the
  // holistic rights standard — admins implicitly, or a non-admin granted the backing
  // group (hp_hostek_power / hp_hostek_proc) via privleg. The backend enforces the same.
  const canPower = userHasRight(user, 'hp_hostek_power');
  const canProc = userHasRight(user, 'hp_hostek_proc');

  const options: SegmentedOption<Tab>[] = [
    { value: 'system', label: 'System' },
    { value: 'performance', label: 'Performance' },
  ];
  if (canPower) options.push({ value: 'config', label: 'Config' });
  options.push({ value: 'disks', label: 'Disks' });
  if (canProc) options.push({ value: 'processes', label: 'Processes' });

  return (
    <ContentRegion>
      <Stack gap={4}>
        <SegmentedControl value={tab} onChange={setTab} options={options} />
        {tab === 'system' && <System {...props} />}
        {tab === 'performance' && <Performance {...props} />}
        {tab === 'config' && canPower && <Config {...props} />}
        {tab === 'disks' && <Disks {...props} />}
        {tab === 'processes' && canProc && <Processes {...props} />}
      </Stack>
    </ContentRegion>
  );
}
