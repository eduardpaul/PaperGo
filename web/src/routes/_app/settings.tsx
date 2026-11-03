import { createFileRoute, Outlet } from '@tanstack/react-router';
import { Bot, KeyRound, Settings, SlidersHorizontal, UserRound, HardDrive } from 'lucide-react';
import { Page, PageHeader } from '@/components/page';
import { SubNav, SubNavLayout, SubNavLink } from '@/components/sub-nav';
import { useTranslation } from '@/i18n';

export const Route = createFileRoute('/_app/settings')({ component: SettingsLayout });

function SettingsLayout() {
  const { t } = useTranslation('settings');
  return (
    <Page className="max-w-5xl">
      <PageHeader icon={Settings} title={t('layout.title')} description={t('layout.description')} />
      <SubNavLayout
        nav={
          <SubNav label={t('layout.nav.label')}>
            <SubNavLink to="/settings" activeOptions={{ exact: true }}>
              <UserRound /> {t('layout.nav.profile')}
            </SubNavLink>
            <SubNavLink to="/settings/preferences">
              <SlidersHorizontal /> {t('layout.nav.preferences')}
            </SubNavLink>
            <SubNavLink to="/settings/assistants">
              <Bot /> {t('layout.nav.assistants')}
            </SubNavLink>
            <SubNavLink to="/settings/tokens">
              <KeyRound /> {t('layout.nav.tokens')}
            </SubNavLink>
            <SubNavLink to="/settings/webdav">
              <HardDrive /> {t('layout.nav.webdav')}
            </SubNavLink>
          </SubNav>
        }
      >
        <Outlet />
      </SubNavLayout>
    </Page>
  );
}
