import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { toast } from 'sonner';
import { api } from '@/api/http';
import { keys } from '@/api/queries';
import type { Preferences } from '@/api/types';
import { Button } from '@/components/ui/button';
import { CurrencyPicker } from '@/features/fields/currency-picker';
import { Input } from '@/components/ui/input';
import { Select } from '@/components/ui/select';
import { cultures, cultureName, dateFormats, timeZones, withValue } from '@/features/settings/preference-options';
import { SettingRow, SettingsSection } from '@/features/settings/section';

const fields = [
  'language',
  'time_zone',
  'date_format',
  'time_format',
  'number_format',
  'theme',
  'document_languages',
  'default_currency',
] as const;

export function PreferencesForm({
  organization = false,
  canEdit = true,
}: {
  organization?: boolean;
  canEdit?: boolean;
}) {
  const queryClient = useQueryClient();
  const path = organization ? '/v1/organization/preferences' : '/v1/identity/me/preferences';
  const preferences = useQuery({
    queryKey: organization ? keys.orgPreferences : keys.preferences,
    queryFn: async () => (await api<Preferences>(path)).data,
    staleTime: 5 * 60_000,
  });
  const [draft, setDraft] = useState<Partial<Preferences>>({});
  const current = { ...preferences.data, ...draft } as Preferences;
  const inherited = new Set(preferences.data?.inherited ?? []);
  const save = useMutation({
    mutationFn: () => {
      const patch: Record<string, string> = {};
      for (const field of fields) {
        if (draft[field] !== undefined && preferences.data && draft[field] !== preferences.data[field])
          patch[field] = String(draft[field]);
      }
      return api<Preferences>(path, {
        method: 'PATCH',
        ifMatch: preferences.data?.version ?? 0,
        body: patch,
      }).then((result) => result.data);
    },
    onSuccess: (updated) => {
      queryClient.setQueryData(organization ? keys.orgPreferences : keys.preferences, updated);
      if (organization) void queryClient.invalidateQueries({ queryKey: keys.preferences });
      setDraft({});
      toast.success('Preferences saved');
    },
  });

  function set<K extends (typeof fields)[number]>(key: K, value: Preferences[K]) {
    setDraft((currentDraft) => ({ ...currentDraft, [key]: value }));
  }

  if (!preferences.data) return null;
  const zones = withValue(timeZones(), current.time_zone);
  const languages = withValue(cultures, current.language, current.number_format);
  return (
    <SettingsSection
      title={organization ? 'Organization defaults' : 'Preferences'}
      description={
        organization
          ? 'Default language, formatting and document settings for everyone.'
          : 'These override the organization defaults. A value the server rejects is reported when you save.'
      }
      actions={
        canEdit && (
          <Button variant="primary" disabled={save.isPending} onClick={() => save.mutate()}>
            Save
          </Button>
        )
      }
    >
      <fieldset disabled={!canEdit}>
        {organization && <SettingRow id="default-currency" label="Default currency" hint="Used by new currency fields unless their workspace overrides it.">
          <CurrencyPicker id="default-currency" labelledBy="default-currency-label" value={current.default_currency} onChange={code => set('default_currency', code || 'USD')} disabled={!canEdit} />
        </SettingRow>}
        <SettingRow
          id="language"
          label="Language"
          hint={inherited.has('language') ? 'Using the organization default until you change it.' : undefined}
        >
          <Select id="language" value={current.language} onChange={(event) => set('language', event.target.value)}>
            {languages.map((code) => (
              <option key={code} value={code}>
                {cultureName(code, current.language)}
              </option>
            ))}
          </Select>
        </SettingRow>
        <SettingRow id="time-zone" label="Time zone">
          <Select id="time-zone" value={current.time_zone} onChange={(event) => set('time_zone', event.target.value)}>
            {zones.map((zone) => (
              <option key={zone} value={zone}>
                {zone}
              </option>
            ))}
          </Select>
        </SettingRow>
        <SettingRow id="date-format" label="Date format">
          <Select
            id="date-format"
            value={current.date_format}
            onChange={(event) => set('date_format', event.target.value)}
          >
            {withValue(dateFormats, current.date_format).map((pattern) => (
              <option key={pattern} value={pattern}>
                {pattern}
              </option>
            ))}
          </Select>
        </SettingRow>
        <SettingRow id="time-format" label="Time format">
          <Select
            id="time-format"
            value={current.time_format}
            onChange={(event) => set('time_format', event.target.value as Preferences['time_format'])}
          >
            <option value="24h">24-hour</option>
            <option value="12h">12-hour</option>
          </Select>
        </SettingRow>
        <SettingRow id="number-format" label="Numbers">
          <Select
            id="number-format"
            value={current.number_format}
            onChange={(event) => set('number_format', event.target.value)}
          >
            {languages.map((code) => (
              <option key={code} value={code}>
                {cultureName(code, current.language)}
              </option>
            ))}
          </Select>
        </SettingRow>
        <SettingRow id="theme" label="Theme">
          <Select
            id="theme"
            value={current.theme}
            onChange={(event) => set('theme', event.target.value as Preferences['theme'])}
          >
            <option value="system">System</option>
            <option value="light">Light</option>
            <option value="dark">Dark</option>
          </Select>
        </SettingRow>
        <SettingRow
          id="document-languages"
          label="Document languages"
          hint="Lowercase codes joined with +, such as eng or eng+deu."
        >
          <Input
            id="document-languages"
            value={current.document_languages}
            onChange={(event) => set('document_languages', event.target.value)}
          />
        </SettingRow>
      </fieldset>
    </SettingsSection>
  );
}
