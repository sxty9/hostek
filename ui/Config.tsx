import { Badge, Panel, Stack, Switch, Text, useLiveQuery, type ServiceContextProps } from '@holistic/ui';
import type { PowerState } from './types';

export function Config({ api, ui }: ServiceContextProps) {
  const { data, loading, refresh } = useLiveQuery<PowerState>(() => api.get<PowerState>('config/power'), 5000);

  if (!data) {
    return loading ? (
      <Text color="secondary">Loading…</Text>
    ) : (
      <Text color="danger">Could not load power configuration.</Text>
    );
  }

  async function setHeadless(next: boolean) {
    const ok = await ui.confirm({
      title: next ? 'Enable always-on / headless mode?' : 'Disable headless mode?',
      description: next
        ? 'Ignore the lid switch and mask suspend / sleep / hibernate so the server stays on without a monitor.'
        : 'Re-enable normal suspend and lid behavior.',
      confirmLabel: next ? 'Enable' : 'Disable',
    });
    if (!ok) return;
    try {
      await api.post('config/power', { headless: next });
      ui.toast({ title: next ? 'Headless mode enabled' : 'Headless mode disabled', variant: 'success' });
      refresh();
    } catch (e) {
      ui.toast({ title: 'Could not apply', description: (e as Error).message, variant: 'error' });
    }
  }

  return (
    <Stack gap={4}>
      <Panel title="Always-on / headless" className="p-4">
        <Stack gap={3}>
          {!data.supported && (
            <Text variant="footnote" color="warning">
              OS-level power management is only available on Linux (current platform: {data.platform}).
            </Text>
          )}
          <Stack direction="row" align="center" justify="between" gap={3}>
            <Stack gap={1}>
              <Text weight="semibold">Keep the server on without a monitor</Text>
              <Text variant="footnote" color="secondary">
                Ignore the lid switch and mask suspend / sleep / hibernate.
              </Text>
            </Stack>
            <Switch checked={data.headless} disabled={!data.supported} onChange={setHeadless} />
          </Stack>
          <Stack direction="row" gap={2}>
            <Badge variant={data.lidIgnore ? 'success' : 'neutral'}>Lid: {data.lidIgnore ? 'ignored' : 'default'}</Badge>
            <Badge variant={data.suspendMasked ? 'success' : 'neutral'}>Suspend: {data.suspendMasked ? 'masked' : 'enabled'}</Badge>
          </Stack>
        </Stack>
      </Panel>

      <Panel title="Firmware (read-only)" className="p-4">
        <Stack gap={1}>
          <Stack direction="row" align="center" gap={2}>
            <Text weight="semibold">{data.biosAutoPowerOn.setting}</Text>
            <Badge variant="accent">{data.biosAutoPowerOn.value}</Badge>
          </Stack>
          <Text variant="footnote" color="secondary">
            {data.biosAutoPowerOn.note}
          </Text>
        </Stack>
      </Panel>
    </Stack>
  );
}
