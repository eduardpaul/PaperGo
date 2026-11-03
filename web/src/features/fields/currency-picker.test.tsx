// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { setLanguage } from '@/i18n';
import { CurrencyPicker } from './currency-picker';

afterEach(() => { cleanup(); vi.unstubAllGlobals(); });
it.each(['€', 'Euro', 'EUR'])('finds a currency by %s and selects its code', async search => {
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} });
  Element.prototype.scrollIntoView ??= () => {};
  const change = vi.fn();
  render(<CurrencyPicker value="USD" onChange={change} />);
  fireEvent.click(screen.getByRole('combobox'));
  fireEvent.change(screen.getByPlaceholderText('Search…'), { target: { value: search } });
  fireEvent.click(await screen.findByRole('option', { name: /EUR.*Euro/ }));
  expect(change).toHaveBeenCalledWith('EUR');
});
it('accepts a custom uppercase three-letter code', async () => {
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} });
  Element.prototype.scrollIntoView ??= () => {};
  const change = vi.fn();
  render(<CurrencyPicker value="USD" onChange={change} />);
  fireEvent.click(screen.getByRole('combobox'));
  fireEvent.change(screen.getByPlaceholderText('Search…'), { target: { value: 'tzs' } });
  fireEvent.click(await screen.findByRole('option', { name: /Add “tzs”/ }));
  expect(change).toHaveBeenCalledWith('TZS');
  expect(screen.getByRole('combobox').getAttribute('aria-expanded')).toBe('false');
});
it('names currencies in the UI language', async () => {
  await act(() => setLanguage('es'));
  try {
    render(<CurrencyPicker value="" onChange={vi.fn()} />);
    expect(screen.getByText('Elige una moneda…')).toBeTruthy();
    cleanup();
    render(<CurrencyPicker value="EUR" onChange={vi.fn()} />);
    expect(screen.getByText(/EUR · € · euro/)).toBeTruthy();
  } finally {
    await act(() => setLanguage('en'));
  }
});
