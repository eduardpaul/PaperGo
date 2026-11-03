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
import { useTranslation } from '@/i18n';
import { offeredLanguages } from '@/i18n/languages';

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
  const { t, i18n } = useTranslation(['settings', 'common']);
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
      toast.success(t('preferences.saved'));
    },
  });

  function set<K extends (typeof fields)[number]>(key: K, value: Preferences[K]) {
    setDraft((currentDraft) => ({ ...currentDraft, [key]: value }));
  }

  if (!preferences.data) return null;
  const zones = withValue(timeZones(), current.time_zone);
  // The screen's languages by their own names; a locale set elsewhere (API, organization default) stays a choice.
  const translated = offeredLanguages();
  const languages = withValue(
    translated.map((language) => language.code),
    current.language,
  );
  const languageName = (code: string) =>
    translated.find((language) => language.code === code)?.name ?? cultureName(code, i18n.language);
  const numberCultures = withValue(cultures, current.number_format);
  const languageInherited = inherited.has('language');
  return (
    <SettingsSection
      title={organization ? t('preferences.organizationTitle') : t('preferences.title')}
      description={organization ? t('preferences.organizationDescription') : t('preferences.description')}
      actions={
        canEdit && (
          <Button variant="primary" disabled={save.isPending} onClick={() => save.mutate()}>
            {t('common:actions.save')}
          </Button>
        )
      }
    >
      <fieldset disabled={!canEdit}>
        {organization && <SettingRow id="default-currency" label={t('preferences.defaultCurrency')} hint={t('preferences.defaultCurrencyHint')}>
          <CurrencyPicker id="default-currency" labelledBy="default-currency-label" value={current.default_currency} onChange={code => set('default_currency', code || 'USD')} disabled={!canEdit} />
        </SettingRow>}
        <SettingRow
          id="language"
          label={t('preferences.language')}
          hint={languageInherited ? t('preferences.inherited') : undefined}
        >
          <Select id="language" value={current.language} onChange={(event) => set('language', event.target.value)}>
            {languages.map((code) => (
              <option key={code} value={code} lang={code}>
                {languageName(code)}
              </option>
            ))}
          </Select>
        </SettingRow>
        <SettingRow id="time-zone" label={t('preferences.timeZone')}>
          <Select id="time-zone" value={current.time_zone} onChange={(event) => set('time_zone', event.target.value)}>
            {zones.map((zone) => (
              <option key={zone} value={zone}>
                {zone}
              </option>
            ))}
          </Select>
        </SettingRow>
        <SettingRow id="date-format" label={t('preferences.dateFormat')}>
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
        <SettingRow id="time-format" label={t('preferences.timeFormat')}>
          <Select
            id="time-format"
            value={current.time_format}
            onChange={(event) => set('time_format', event.target.value as Preferences['time_format'])}
          >
            <option value="24h">{t('preferences.hours24')}</option>
            <option value="12h">{t('preferences.hours12')}</option>
          </Select>
        </SettingRow>
        <SettingRow id="number-format" label={t('preferences.numbers')}>
          <Select
            id="number-format"
            value={current.number_format}
            onChange={(event) => set('number_format', event.target.value)}
          >
            {numberCultures.map((code) => (
              <option key={code} value={code}>
                {cultureName(code, i18n.language)}
              </option>
            ))}
          </Select>
        </SettingRow>
        <SettingRow id="theme" label={t('preferences.theme')}>
          <Select
            id="theme"
            value={current.theme}
            onChange={(event) => set('theme', event.target.value as Preferences['theme'])}
          >
            <option value="system">{t('common:theme.system')}</option>
            <option value="light">{t('common:theme.light')}</option>
            <option value="dark">{t('common:theme.dark')}</option>
          </Select>
        </SettingRow>
        <SettingRow
          id="document-languages"
          label={t('preferences.documentLanguages')}
          hint={t('preferences.documentLanguagesHint')}
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
