import { useMemo, useState } from 'react';
import { Combobox, type ComboboxOption } from '@/components/ui/combobox';
import { useTranslation } from '@/i18n';

const codes = ['USD', 'EUR', 'GBP', 'JPY', 'CAD', 'AUD', 'CHF', 'CNY', 'BRL', 'INR', 'MXN', 'KRW', 'SGD', 'HKD', 'NZD', 'SEK', 'NOK', 'DKK', 'PLN', 'CZK', 'ZAR', 'AED', 'SAR', 'TRY'];

/** The common currencies, named in the UI language. */
function currencyOptions(language: string): { options: ComboboxOption[]; name: (code: string) => string } {
  const names = new Intl.DisplayNames([language, 'en'], { type: 'currency' });
  const name = (code: string) => names.of(code) ?? code;
  const options = codes.map(code => {
    const symbol = new Intl.NumberFormat(language, { style: 'currency', currency: code, currencyDisplay: 'narrowSymbol' }).formatToParts(0).find(part => part.type === 'currency')?.value ?? code;
    return { value: code, label: `${code} · ${symbol} · ${name(code)}` };
  });
  return { options, name };
}

export function CurrencyPicker({ value, onChange, id, labelledBy, placeholder, disabled }: { value: string; onChange: (code: string) => void; id?: string; labelledBy?: string; placeholder?: string; disabled?: boolean }) {
  const { t, i18n } = useTranslation('fields');
  const { options, name } = useMemo(() => currencyOptions(i18n.language), [i18n.language]);
  const [search, setSearch] = useState('');
  const custom = search.trim().toUpperCase();
  const selected = value ? options.find(option => option.value === value) ?? { value, label: `${value} · ${name(value)}` } : undefined;
  return <Combobox id={id} aria-labelledby={labelledBy} disabled={disabled} placeholder={placeholder ?? t('currency.placeholder')} selected={selected ? [selected] : []} options={options.filter(option => option.label.toLocaleLowerCase(i18n.language).includes(search.toLocaleLowerCase(i18n.language)))} onSearch={setSearch} onChange={chosen => onChange(chosen[0]?.value ?? '')} onCreate={/^[A-Z]{3}$/.test(custom) && !options.some(option => option.value === custom) ? () => onChange(custom) : undefined} />;
}
