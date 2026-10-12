import { describe, expect, it } from 'vitest';
import { createFormatter } from './format';

describe('createFormatter', () => {
  const moment = new Date('2026-03-29T00:30:00Z'); // 01:30 in Berlin (CET), 20:30 the day before in New York

  it('formats dates with the user pattern in the user time zone', () => {
    expect(createFormatter({ timeZone: 'Europe/Berlin', dateFormat: 'dd.MM.yyyy' }).date(moment)).toBe('29.03.2026');
    expect(createFormatter({ timeZone: 'America/New_York', dateFormat: 'M/d/yy' }).date(moment)).toBe('3/28/26');
  });

  it('formats times as 24h or 12h', () => {
    expect(createFormatter({ timeZone: 'Europe/Berlin', timeFormat: '24h' }).time(moment)).toBe('01:30');
    expect(createFormatter({ timeZone: 'America/New_York', timeFormat: '12h' }).time(moment)).toBe('8:30 PM');
  });

  it('keeps date-only values on their calendar day', () => {
    const format = createFormatter({ timeZone: 'Pacific/Honolulu', dateFormat: 'yyyy-MM-dd' });
    expect(format.date('2026-10-01')).toBe('2026-10-01');
    expect(format.dateTime('2026-10-01')).toBe('2026-10-01');
    expect(format.time('2026-10-01')).toBe('');
  });

  it('formats numbers and currencies in the number culture', () => {
    const format = createFormatter({ numberFormat: 'de-DE' });
    expect(format.number(1234.5)).toBe('1.234,5');
    expect(format.currency(12, 'EUR')).toMatch(/12,00\s€/);
  });

  it('formats exact numeric strings without rounding them through a JS number', () => {
    const format = createFormatter({ numberFormat: 'de-DE' });
    expect(format.number('9007199254740993')).toBe('9.007.199.254.740.993');
    expect(format.number('0.123456789123456789', { maximumFractionDigits: 28 })).toBe('0,123456789123456789');
  });

  it('falls back to defaults for invalid settings and empty values', () => {
    const format = createFormatter({ timeZone: 'Not/AZone', numberFormat: '' });
    expect(format.date(moment)).toMatch(/^\d{4}-\d{2}-\d{2}$/);
    expect(format.date(null)).toBe('');
    expect(format.fileSize(1536)).toBe('1.5 KB');
  });

  it('gives the hour and weekday in the user time zone', () => {
    const berlin = createFormatter({ timeZone: 'Europe/Berlin', language: 'en' });
    const newYork = createFormatter({ timeZone: 'America/New_York', language: 'de' });
    expect(berlin.hour(moment)).toBe(1);
    expect(berlin.weekday(moment)).toBe('Sunday');
    expect(newYork.hour(moment)).toBe(20);
    expect(newYork.weekday(moment)).toBe('Samstag');
  });

  it('describes relative times', () => {
    const format = createFormatter({ language: 'en' });
    expect(format.relative(new Date('2026-01-01T12:00:00Z'), new Date('2026-01-01T15:00:00Z'))).toBe('3 hours ago');
    expect(format.relative(new Date('2026-01-02T12:00:00Z'), new Date('2026-01-01T12:00:00Z'))).toBe('tomorrow');
  });
});
