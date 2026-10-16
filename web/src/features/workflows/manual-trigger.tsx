import { useId } from 'react';
import type { Resource } from '@/api/types';
import { Select } from '@/components/ui/select';
import { manualItemMode, type Trigger } from './model';

export function ManualTriggerFields({ trigger, collections, onChange }: { trigger: Trigger; collections: Resource[]; onChange: (trigger: Trigger) => void }) {
  const mode = manualItemMode(trigger);
  const name = useId();
  return <>
    <fieldset className="grid gap-2 text-sm">
      <legend className="mb-2 font-medium">Item association</legend>
      {([['none', 'None (Standalone)'], ['single', 'Single Item'], ['multiple', 'Multiple Items']] as const).map(([value, label]) =>
        <label key={value} className="flex items-center gap-2">
          <input type="radio" name={name} checked={mode === value} onChange={() => onChange({
            type: 'manual',
            ...(value === 'none' ? {} : { collection_id: trigger.collection_id, selection: value === 'multiple' ? 'selection' : 'per_item' }),
          })} />{label}
        </label>,
      )}
    </fieldset>
    {mode !== 'none' && <Select aria-label="Manual trigger collection" value={trigger.collection_id ?? ''} onChange={(event) => onChange({ ...trigger, collection_id: event.target.value || undefined })}>
      <option value="">Any collection in workspace</option>
      {collections.map((collection) => <option key={collection.id} value={collection.id}>{collection.name}</option>)}
    </Select>}
    {mode === 'multiple' && <p className="text-xs text-muted">Use {'{run:items}'} for the ordered selection and a forEach step to process each item. One run receives all selected items.</p>}
  </>;
}
