import { ActivityIcon, type ServicePlugin } from '@holistic/ui';
import { Dashboard } from './Dashboard';
import './i18n';

// hostek's dashboard plugin. Linked into holistic/frontend/external/hostek at install
// time and discovered by the host SPA's build-time registry. id MUST equal the link dir.
const plugin: ServicePlugin = {
  id: 'hostek',
  displayName: 'System',
  icon: ActivityIcon,
  order: 20,
  Component: Dashboard,
};

export default plugin;
