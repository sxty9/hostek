import { useState } from 'react';
import { ContentRegion, SegmentedControl, Stack, type SegmentedOption, type ServiceContextProps } from '@holistic/ui';
import { Overview } from './Overview';
import { Processes } from './Processes';
import { Config } from './Config';

type Tab = 'overview' | 'processes' | 'config';

export function Dashboard(props: ServiceContextProps) {
  const { user } = props;
  const [tab, setTab] = useState<Tab>('overview');

  // Admins (Linux sudo) get the per-process breakdown and server configuration.
  // Everyone else sees only the aggregate Overview.
  const options: SegmentedOption<Tab>[] = [{ value: 'overview', label: 'Overview' }];
  if (user.isAdmin) {
    options.push({ value: 'processes', label: 'Processes' }, { value: 'config', label: 'Config' });
  }

  return (
    <ContentRegion>
      <Stack gap={4}>
        {user.isAdmin && <SegmentedControl value={tab} onChange={setTab} options={options} />}
        {tab === 'overview' && <Overview {...props} />}
        {tab === 'processes' && user.isAdmin && <Processes {...props} />}
        {tab === 'config' && user.isAdmin && <Config {...props} />}
      </Stack>
    </ContentRegion>
  );
}
