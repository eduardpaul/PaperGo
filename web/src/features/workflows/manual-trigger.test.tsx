// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { useState } from 'react';
import { afterEach, expect, it } from 'vitest';
import { ManualTriggerFields } from './manual-trigger';
import { manualItemMode, type Trigger } from './model';

afterEach(cleanup);
it('authors standalone, workspace single-item and scoped multiple-item triggers', () => {
  let saved: Trigger = { type: 'manual' };
  function Editor() {
    const [trigger, setTrigger] = useState(saved);
    saved = trigger;
    return <ManualTriggerFields trigger={trigger} collections={[]} onChange={setTrigger} />;
  }
  render(<Editor />);
  expect(manualItemMode(saved)).toBe('none');
  expect(screen.queryByLabelText('Manual trigger collection')).toBeNull();
  fireEvent.click(screen.getByLabelText('Single Item'));
  expect(saved).toEqual({ type: 'manual', collection_id: undefined, selection: 'per_item' });
  expect(manualItemMode(saved)).toBe('single');
  fireEvent.click(screen.getByLabelText('Multiple Items'));
  expect(manualItemMode(saved)).toBe('multiple');
  expect(screen.getByText(/forEach/)).toBeTruthy();
  fireEvent.click(screen.getByLabelText('None (Standalone)'));
  expect(saved).toEqual({ type: 'manual' });
});
