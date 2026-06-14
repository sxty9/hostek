import { useState } from 'react';
import { ContentRegion, SegmentedControl, Stack, userHasRight, type SegmentedOption, type ServiceContextProps } from '@holistic/ui';
import { System } from './System';
import { Performance } from './Performance';
import { Power } from './Power';
import { Thermal, ThermalWatcher } from './Thermal';
import { Config } from './Config';
import { Disks } from './Disks';
import { Processes } from './Processes';

type Tab = 'system' | 'performance' | 'power' | 'thermal' | 'config' | 'disks' | 'processes';

export function Dashboard(props: ServiceContextProps) {
  const { user } = props;
  const [tab, setTab] = useState<Tab>('system');

  // Everyone (read-only) sees System and Performance (without temperatures/power). Power
  // telemetry, temperatures, the per-process breakdown, the Disks tab and OS power config
  // each need the matching right under the holistic rights standard — admins implicitly,
  // or a non-admin granted the backing group via privleg. The backend enforces the same
  // (gating the routes and redacting the remaining gated values).
  const canPower = userHasRight(user, 'hp_hostek_power');
  const canProc = userHasRight(user, 'hp_hostek_proc');
  const canThermal = userHasRight(user, 'hp_hostek_thermal');
  const canPowerInfo = userHasRight(user, 'hp_hostek_powerinfo');
  const canDisks = userHasRight(user, 'hp_hostek_disks');

  // Order: System · Performance · Power · Thermal · Processes · Disks · Config.
  const options: SegmentedOption<Tab>[] = [
    { value: 'system', label: 'System' },
    { value: 'performance', label: 'Performance' },
  ];
  if (canPowerInfo) options.push({ value: 'power', label: 'Power' });
  if (canThermal) options.push({ value: 'thermal', label: 'Thermal' });
  if (canProc) options.push({ value: 'processes', label: 'Processes' });
  if (canDisks) options.push({ value: 'disks', label: 'Disks' });
  if (canPower) options.push({ value: 'config', label: 'Config' });

  return (
    <ContentRegion>
      {/* Temperature watcher (warning toast on any tab) — only for users who may see temps. */}
      {canThermal && <ThermalWatcher {...props} />}
      <Stack gap={4}>
        <SegmentedControl value={tab} onChange={setTab} options={options} />
        {tab === 'system' && <System {...props} />}
        {tab === 'performance' && <Performance {...props} />}
        {tab === 'power' && canPowerInfo && <Power {...props} />}
        {tab === 'thermal' && canThermal && <Thermal {...props} />}
        {tab === 'config' && canPower && <Config {...props} />}
        {tab === 'disks' && canDisks && <Disks {...props} />}
        {tab === 'processes' && canProc && <Processes {...props} />}
      </Stack>
    </ContentRegion>
  );
}
