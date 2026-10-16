import { createFileRoute, Outlet } from '@tanstack/react-router';
import { Building2, Download, KeyRound, FolderTree, ScrollText, ShieldCheck, ShieldHalf, UserRound, Users } from 'lucide-react';
import { Page, PageHeader } from '@/components/page';
import { SubNav, SubNavLayout, SubNavLink } from '@/components/sub-nav';
import { useHasScope } from '@/features/admin/common';

export const Route = createFileRoute('/_app/admin')({ component: Administration });

function Administration() {
  const users = useHasScope('user.read');
  const groups = useHasScope('group.read');
  const roles = useHasScope('role.read');
  const taxonomy = useHasScope('taxonomy.read');
  const contentTypes = useHasScope('contentType.read');
  const audit = useHasScope('audit.read');
  const maintenance = useHasScope('organization.manage');
  return (
    <Page className="max-w-6xl">
      <PageHeader icon={ShieldCheck} title="Administration" description="Settings of the whole organization." />
      <SubNavLayout
        nav={
          <SubNav label="Administration">
            <SubNavLink to="/admin" activeOptions={{ exact: true }}>
              <Building2 /> Organization
            </SubNavLink>
            {users && (
              <SubNavLink to="/admin/users">
                <UserRound /> Users
              </SubNavLink>
            )}
            {groups && (
              <SubNavLink to="/admin/groups">
                <Users /> Groups
              </SubNavLink>
            )}
            {roles && (
              <SubNavLink to="/admin/roles">
                <ShieldHalf /> Roles
              </SubNavLink>
            )}
            {taxonomy && (
              <SubNavLink to="/admin/relationship-types">
                <FolderTree /> Relationship types
              </SubNavLink>
            )}
            {taxonomy && (
              <SubNavLink to="/admin/terms">
                <FolderTree /> Term store
              </SubNavLink>
            )}
            {contentTypes && (
              <SubNavLink to="/admin/content-types">
                <FolderTree /> Content types
              </SubNavLink>
            )}
            {audit && (
              <SubNavLink to="/admin/audit">
                <ScrollText /> Audit log
              </SubNavLink>
            )}
            {maintenance && <SubNavLink to="/admin/tokens"><KeyRound /> Access tokens</SubNavLink>}
            {maintenance && (
              <SubNavLink to="/admin/maintenance">
                <Download /> Export and import
              </SubNavLink>
            )}
          </SubNav>
        }
      >
        <Outlet />
      </SubNavLayout>
    </Page>
  );
}
