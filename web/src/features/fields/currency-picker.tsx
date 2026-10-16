import { useState } from 'react';
import { Combobox, type ComboboxOption } from '@/components/ui/combobox';

const names = new Intl.DisplayNames(['en'], { type: 'currency' });
export const currencyOptions: ComboboxOption[] = ['USD', 'EUR', 'GBP', 'JPY', 'CAD', 'AUD', 'CHF', 'CNY', 'BRL', 'INR', 'MXN', 'KRW', 'SGD', 'HKD', 'NZD', 'SEK', 'NOK', 'DKK', 'PLN', 'CZK', 'ZAR', 'AED', 'SAR', 'TRY'].map(code => {
  const symbol = new Intl.NumberFormat('en', { style: 'currency', currency: code, currencyDisplay: 'narrowSymbol' }).formatToParts(0).find(part => part.type === 'currency')?.value ?? code;
  return { value: code, label: `${code} · ${symbol} · ${names.of(code) ?? code}` };
});

export function CurrencyPicker({ value, onChange, id, labelledBy, placeholder = 'Choose a currency…', disabled }: { value: string; onChange: (code: string) => void; id?: string; labelledBy?: string; placeholder?: string; disabled?: boolean }) {
  const [search, setSearch] = useState('');
  const custom = search.trim().toUpperCase();
  const selected = value ? currencyOptions.find(option => option.value === value) ?? { value, label: `${value} · ${names.of(value) ?? value}` } : undefined;
  return <Combobox id={id} aria-labelledby={labelledBy} disabled={disabled} placeholder={placeholder} selected={selected ? [selected] : []} options={currencyOptions.filter(option => option.label.toLocaleLowerCase('en').includes(search.toLocaleLowerCase('en')))} onSearch={setSearch} onChange={options => onChange(options[0]?.value ?? '')} onCreate={/^[A-Z]{3}$/.test(custom) && !currencyOptions.some(option => option.value === custom) ? () => onChange(custom) : undefined} />;
}
