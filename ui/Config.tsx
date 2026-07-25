import { useState } from 'react';
import { Badge, Button, Field, Panel, PasswordInput, Stack, Switch, Text, useLiveQuery, useT, type ServiceContextProps } from '@holistic/ui';
import type { PowerState, ShutdownConfig } from './types';

export function Config({ api, ui }: ServiceContextProps) {
  const t = useT();
  const { data, loading, refresh } = useLiveQuery<PowerState>(() => api.get<PowerState>('config/power'), 5000);

  if (!data) {
    return loading ? (
      <Text color="secondary">{t('hostek.loading')}</Text>
    ) : (
      <Text color="danger">{t('hostek.loadPowerConfigError')}</Text>
    );
  }

  async function setHeadless(next: boolean) {
    const ok = await ui.confirm({
      title: next ? t('hostek.enableHeadlessTitle') : t('hostek.disableHeadlessTitle'),
      description: next ? t('hostek.enableHeadlessDesc') : t('hostek.disableHeadlessDesc'),
      confirmLabel: next ? t('hostek.enable') : t('hostek.disable'),
    });
    if (!ok) return;
    try {
      await api.post('config/power', { headless: next });
      ui.toast({ title: next ? t('hostek.headlessEnabled') : t('hostek.headlessDisabled'), variant: 'success' });
      refresh();
    } catch (e) {
      ui.toast({ title: t('hostek.applyFailed'), description: (e as Error).message, variant: 'error' });
    }
  }

  async function setTmuxPersist(next: boolean) {
    const ok = await ui.confirm({
      title: next ? t('hostek.enableTmuxTitle') : t('hostek.disableTmuxTitle'),
      description: next ? t('hostek.enableTmuxDesc') : t('hostek.disableTmuxDesc'),
      confirmLabel: next ? t('hostek.enable') : t('hostek.disable'),
    });
    if (!ok) return;
    try {
      await api.post('config/power', { tmuxPersist: next });
      ui.toast({ title: next ? t('hostek.tmuxEnabled') : t('hostek.tmuxDisabled'), variant: 'success' });
      refresh();
    } catch (e) {
      ui.toast({ title: t('hostek.applyFailed'), description: (e as Error).message, variant: 'error' });
    }
  }

  async function setTmuxResume(next: boolean) {
    const ok = await ui.confirm({
      title: next ? t('hostek.enableTmuxResumeTitle') : t('hostek.disableTmuxResumeTitle'),
      description: next ? t('hostek.enableTmuxResumeDesc') : t('hostek.disableTmuxResumeDesc'),
      confirmLabel: next ? t('hostek.enable') : t('hostek.disable'),
    });
    if (!ok) return;
    try {
      await api.post('config/power', { tmuxResume: next });
      ui.toast({ title: next ? t('hostek.tmuxResumeEnabled') : t('hostek.tmuxResumeDisabled'), variant: 'success' });
      refresh();
    } catch (e) {
      ui.toast({ title: t('hostek.applyFailed'), description: (e as Error).message, variant: 'error' });
    }
  }

  return (
    <Stack gap={4}>
      <Panel title={t('hostek.alwaysOnHeadless')} className="p-4">
        <Stack gap={3}>
          {!data.supported && (
            <Text variant="footnote" color="warning">
              {t('hostek.osPowerLinuxOnly', { platform: data.platform })}
            </Text>
          )}
          <Stack direction="row" align="center" justify="between" gap={3}>
            <Stack gap={1}>
              <Text weight="semibold">{t('hostek.keepServerOn')}</Text>
              <Text variant="footnote" color="secondary">
                {t('hostek.ignoreLid')}
              </Text>
            </Stack>
            <Switch checked={data.headless} disabled={!data.supported} onChange={setHeadless} />
          </Stack>
          <Stack direction="row" gap={2}>
            <Badge variant={data.lidIgnore ? 'success' : 'neutral'}>{t('hostek.lidLabel')}: {data.lidIgnore ? t('hostek.ignored') : t('hostek.default')}</Badge>
            <Badge variant={data.suspendMasked ? 'success' : 'neutral'}>{t('hostek.suspendLabel')}: {data.suspendMasked ? t('hostek.masked') : t('hostek.enabledWord')}</Badge>
          </Stack>
        </Stack>
      </Panel>

      <Panel title={t('hostek.tmuxPersistTitle')} className="p-4">
        <Stack gap={3}>
          {!data.supported && (
            <Text variant="footnote" color="warning">
              {t('hostek.osPowerLinuxOnly', { platform: data.platform })}
            </Text>
          )}
          <Stack direction="row" align="center" justify="between" gap={3}>
            <Stack gap={1}>
              <Text weight="semibold">{t('hostek.tmuxPersistLabel')}</Text>
              <Text variant="footnote" color="secondary">
                {t('hostek.tmuxPersistDesc')}
              </Text>
            </Stack>
            <Switch checked={data.tmuxPersist} disabled={!data.supported} onChange={setTmuxPersist} />
          </Stack>
          <Stack direction="row" align="center" justify="between" gap={3}>
            <Stack gap={1}>
              <Text weight="semibold">{t('hostek.tmuxResumeLabel')}</Text>
              <Text variant="footnote" color="secondary">
                {t('hostek.tmuxResumeDesc')}
              </Text>
            </Stack>
            <Switch checked={data.tmuxResume} disabled={!data.supported || !data.tmuxPersist} onChange={setTmuxResume} />
          </Stack>
          <Stack direction="row" gap={2}>
            <Badge variant={data.tmuxPersist ? 'success' : 'neutral'}>
              {t('hostek.tmuxSessionLabel')}: {data.tmuxPersist ? t('hostek.tmuxPersistent') : t('hostek.tmuxEphemeral')}
            </Badge>
            <Badge variant={data.tmuxResume ? 'success' : 'neutral'}>
              {t('hostek.tmuxResumeBadgeLabel')}: {data.tmuxResume ? t('hostek.tmuxResumeOn') : t('hostek.tmuxResumeOff')}
            </Badge>
          </Stack>
        </Stack>
      </Panel>

      <NotAusPanel api={api} ui={ui} />

      <Panel title={t('hostek.firmwareReadonly')} className="p-4">
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

// NotAusPanel arms the emergency-shutdown feature and sets its shared password. Once
// armed, a „Not-Aus" button appears on the holistic login page (pre-login) so someone
// without an account can power the server down cleanly — e.g. an approaching storm while
// the admin is away. The password is write-only: the server only ever reports whether
// one is configured, never its value.
function NotAusPanel({ api, ui }: Pick<ServiceContextProps, 'api' | 'ui'>) {
  const t = useT();
  const { data, refresh } = useLiveQuery<ShutdownConfig>(() => api.get<ShutdownConfig>('config/shutdown'), 5000);
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);

  if (!data) return null;
  const { armed, configured } = data;

  async function savePassword() {
    if (password.length < 6) {
      ui.toast({ title: t('hostek.notausPwTooShort'), variant: 'error' });
      return;
    }
    setBusy(true);
    try {
      await api.post('config/shutdown', { password });
      setPassword('');
      ui.toast({ title: t('hostek.notausPwSaved'), variant: 'success' });
      refresh();
    } catch (e) {
      ui.toast({ title: t('hostek.notausSaveFailed'), description: (e as Error).message, variant: 'error' });
    } finally {
      setBusy(false);
    }
  }

  async function setArmed(next: boolean) {
    if (next && !configured) {
      ui.toast({ title: t('hostek.notausNeedPw'), variant: 'error' });
      return;
    }
    const ok = await ui.confirm({
      title: next ? t('hostek.notausArmTitle') : t('hostek.notausDisarmTitle'),
      description: next ? t('hostek.notausArmDesc') : t('hostek.notausDisarmDesc'),
      danger: next,
      confirmLabel: next ? t('hostek.notausArm') : t('hostek.notausDisarm'),
    });
    if (!ok) return;
    try {
      await api.post('config/shutdown', { armed: next });
      ui.toast({ title: next ? t('hostek.notausArmed') : t('hostek.notausDisarmed'), variant: 'success' });
      refresh();
    } catch (e) {
      ui.toast({ title: t('hostek.applyFailed'), description: (e as Error).message, variant: 'error' });
    }
  }

  return (
    <Panel title={t('hostek.notausTitle')} className="p-4">
      <Stack gap={3}>
        <Text variant="footnote" color="secondary">
          {t('hostek.notausIntro')}
        </Text>
        <Stack direction="row" align="center" justify="between" gap={3}>
          <Stack gap={1}>
            <Text weight="semibold">{t('hostek.notausArmLabel')}</Text>
            <Text variant="footnote" color="secondary">
              {t('hostek.notausArmHint')}
            </Text>
          </Stack>
          <Switch checked={armed} disabled={!configured && !armed} onChange={setArmed} />
        </Stack>
        <Field label={configured ? t('hostek.notausChangePw') : t('hostek.notausSetPw')} hint={t('hostek.notausPwHint')}>
          <Stack direction="row" align="center" gap={2}>
            <PasswordInput
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              autoComplete="new-password"
              className="w-64"
            />
            <Button variant="primary" onClick={savePassword} loading={busy} disabled={!password}>
              {t('hostek.notausSave')}
            </Button>
          </Stack>
        </Field>
        <Stack direction="row" gap={2}>
          <Badge variant={configured ? 'success' : 'neutral'}>
            {t('hostek.notausPwStateLabel')}: {configured ? t('hostek.notausConfigured') : t('hostek.notausNotConfigured')}
          </Badge>
          <Badge variant={armed ? 'success' : 'neutral'}>
            {t('hostek.notausStateLabel')}: {armed ? t('hostek.notausOn') : t('hostek.notausOff')}
          </Badge>
        </Stack>
      </Stack>
    </Panel>
  );
}
