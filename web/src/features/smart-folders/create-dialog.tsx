import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { api } from '@/api/http';
import { numericInput } from '@/api/json';
import { keys, workspacesQuery } from '@/api/queries';
import type { SmartFolder } from '@/api/types';
import { FormDialog } from '@/components/form-dialog';
import { Input, Label, Textarea } from '@/components/ui/input';
import { Select } from '@/components/ui/select';

export function CreateSmartFolderDialog({ open, onClose, onCreated }: { open: boolean; onClose: () => void; onCreated: (folder: SmartFolder) => void }) {
  const queryClient = useQueryClient();
  const workspaces = useQuery(workspacesQuery);
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [personal, setPersonal] = useState(true);
  const [workspaceId, setWorkspaceId] = useState('');
  const [includeFolders, setIncludeFolders] = useState(false);
  const [field, setField] = useState('');
  const [op, setOp] = useState('eq');
  const [value, setValue] = useState('');
  const [valueType, setValueType] = useState('text');

  const create = useMutation({
    mutationFn: () =>
      api<SmartFolder>('/v1/smart-folders', {
        method: 'POST',
        body: {
          name: name.trim(),
          description: description.trim(),
          personal,
          workspace_id: personal ? undefined : workspaceId,
          definition: {
            include_folders: includeFolders,
            filter: field.trim() ? {
              field: field.trim(),
              op,
              value: valueType === 'number' ? numericInput(value) : valueType === 'boolean' ? value === 'true' : value.trim(),
            } : undefined,
          },
        },
      }).then((result) => result.data),
    onSuccess: async (folder) => {
      await queryClient.invalidateQueries({ queryKey: keys.smartFolders });
      onCreated(folder);
    },
  });

  const shared = workspaces.data?.filter((workspace) => !workspace.personal_owner_id) ?? [];
  return (
    <FormDialog
      open={open}
      title="New smart folder"
      description="A saved query. Personal folders stay on your account. Shared folders belong to one workspace."
      submitLabel="Create"
      pending={create.isPending}
      onClose={onClose}
      onSubmit={() => name.trim() && (personal || workspaceId) && create.mutate()}
    >
      <Label htmlFor="smart-name">Name</Label>
      <Input id="smart-name" value={name} onChange={(event) => setName(event.target.value)} required autoFocus />
      <Label htmlFor="smart-description">Description</Label>
      <Textarea id="smart-description" value={description} onChange={(event) => setDescription(event.target.value)} />
      <label className="flex items-center gap-2 text-[13px]">
        <input type="checkbox" checked={personal} onChange={(event) => setPersonal(event.target.checked)} />
        Personal
      </label>
      {!personal && (
        <>
          <Label htmlFor="smart-workspace">Workspace</Label>
          <Select id="smart-workspace" value={workspaceId} onChange={(event) => setWorkspaceId(event.target.value)} required>
            <option value="">Select a workspace…</option>
            {shared.map((workspace) => (
              <option key={workspace.id} value={workspace.id}>
                {workspace.name}
              </option>
            ))}
          </Select>
        </>
      )}
      <label className="flex items-center gap-2 text-[13px]">
        <input type="checkbox" checked={includeFolders} onChange={(event) => setIncludeFolders(event.target.checked)} />
        Include folders
      </label>
      <Label htmlFor="smart-field">Filter field</Label>
      <Input id="smart-field" value={field} onChange={(event) => setField(event.target.value)} placeholder="Optional indexed field key" />
      {field.trim() && (
        <>
          <Select value={op} onChange={(event) => setOp(event.target.value)} aria-label="Filter operator">
            {['eq', 'ne', 'gt', 'gte', 'lt', 'lte', 'contains'].map((operator) => (
              <option key={operator} value={operator}>
                {operator}
              </option>
            ))}
          </Select>
          <Label htmlFor="smart-value-type">Value type</Label>
          <Select id="smart-value-type" value={valueType} onChange={(event) => {
            setValueType(event.target.value);
            setValue(event.target.value === 'boolean' ? 'true' : '');
          }}>
            <option value="text">Text, date or reference</option>
            <option value="number">Number</option>
            <option value="boolean">Boolean</option>
          </Select>
          {valueType === 'boolean' ? (
            <Select value={value} onChange={(event) => setValue(event.target.value)} aria-label="Filter value">
              <option value="true">Yes</option>
              <option value="false">No</option>
            </Select>
          ) : (
            <Input value={value} onChange={(event) => setValue(event.target.value)} inputMode={valueType === 'number' ? 'decimal' : undefined} placeholder="Value" aria-label="Filter value" />
          )}
        </>
      )}
    </FormDialog>
  );
}
