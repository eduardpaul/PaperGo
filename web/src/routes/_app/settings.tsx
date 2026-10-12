import { createFileRoute, Outlet } from '@tanstack/react-router';
import { KeyRound, Settings, SlidersHorizontal, UserRound, HardDrive } from 'lucide-react';
import { Page, PageHeader } from '@/components/page';
import { SubNav, SubNavLayout, SubNavLink } from '@/components/sub-nav';
import { authenticationMode } from '@/api/auth';

export const Route = createFileRoute('/_app/settings')({ component: SettingsLayout });

function SettingsLayout() {
  return (
    <Page className="max-w-5xl">
      <PageHeader icon={Settings} title="Settings" description="Your profile, preferences and credentials." />
      <SubNavLayout
        nav={
          <SubNav label="Settings">
            <SubNavLink to="/settings" activeOptions={{ exact: true }}>
              <UserRound /> Profile
            </SubNavLink>
            <SubNavLink to="/settings/preferences">
              <SlidersHorizontal /> Preferences
            </SubNavLink>
            {authenticationMode() === 'bearer' && (
              <SubNavLink to="/settings/tokens">
                <KeyRound /> REST tokens
              </SubNavLink>
            )}
            <SubNavLink to="/settings/webdav">
              <HardDrive /> WebDAV
            </SubNavLink>
          </SubNav>
        }
      >
        <Outlet />
      </SubNavLayout>
    </Page>
  );
}
