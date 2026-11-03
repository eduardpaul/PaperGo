import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useNavigate } from '@tanstack/react-router';
import { useState, type FormEvent } from 'react';
import { api } from '@/api/http';
import { keys, templatesQuery } from '@/api/queries';
import type { Resource } from '@/api/types';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Alert, Skeleton } from '@/components/ui/feedback';
import { Input, Label, Textarea } from '@/components/ui/input';
import { CollectionIcon } from '@/features/lists/collection-icon';
import { useTranslation } from '@/i18n';
import { problemMessage } from '@/lib/errors';
import { cn } from '@/lib/utils';

interface Choice {
  key: string;
  template?: string;
  kind: 'list' | 'library';
  name: string;
  description: string;
}


/** A new list or library, blank or from a template; opens it once created. */
export function NewCollectionDialog({
  workspaceId,
  open,
  onOpenChange,
}: {
  workspaceId: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useTranslation(['workspaces', 'common']);
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const templates = useQuery({ ...templatesQuery, enabled: open });
  const choices: Choice[] = [
    { key: 'blank-list', kind: 'list', name: t('newCollection.blankList'), description: t('newCollection.blankListHint') },
    { key: 'blank-library', kind: 'library', name: t('newCollection.blankLibrary'), description: t('newCollection.blankLibraryHint') },
    ...(templates.data ?? []).map((template) => ({
      key: template.key,
      template: template.key,
      kind: template.kind,
      name: template.name,
      description: template.description,
    })),
  ];
  const [choiceKey, setChoice] = useState('blank-list');
  const choice = choices.find((entry) => entry.key === choiceKey) ?? choices[0];
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const create = useMutation({
    meta: { silent: true },
    mutationFn: () =>
      api<Resource>(`/v1/resources/${workspaceId}/children`, {
        method: 'POST',
        body: choice.template
          ? { name: name.trim(), description: description.trim(), template_key: choice.template, keywords: [], values: {} }
          : { kind: choice.kind, name: name.trim(), description: description.trim(), keywords: [], values: {} },
      }).then((result) => result.data),
    onSuccess: async (collection) => {
      await queryClient.invalidateQueries({ queryKey: keys.resources });
      onOpenChange(false);
      void navigate({ to: '/w/$workspaceId/l/$listId', params: { workspaceId, listId: collection.id } });
    },
  });
  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (name.trim()) create.mutate();
  };
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[90vh] max-w-2xl overflow-auto">
        <form onSubmit={submit}>
          <DialogHeader>
            <DialogTitle>{t('newCollection.title')}</DialogTitle>
            <DialogDescription>{t('newCollection.description')}</DialogDescription>
          </DialogHeader>
          <div className="flex flex-col gap-4 px-5 pb-5">
            {create.isError && <Alert>{problemMessage(create.error)}</Alert>}
            <div role="radiogroup" aria-label={t('newCollection.startFrom')} className="grid gap-2 sm:grid-cols-2">
              {choices.map((entry) => (
                <button
                  key={entry.key}
                  type="button"
                  role="radio"
                  aria-checked={choice.key === entry.key}
                  onClick={() => {
                    // Suggest the template's name unless the person typed their own.
                    if (!name.trim() || (choice.template && name === choice.name)) {
                      setName(entry.template ? entry.name : '');
                    }
                    setChoice(entry.key);
                  }}
                  className={cn(
                    'flex flex-col items-start gap-1 rounded-lg border p-3 text-left hover:border-accent/50',
                    choice.key === entry.key && 'border-accent bg-accent-soft/40',
                  )}
                >
                  <span className="flex items-center gap-2 text-[13px] font-semibold">
                    <CollectionIcon collection={{ kind: entry.kind, key: entry.template }} className="size-4 text-accent" />
                    {entry.name}
                  </span>
                  <span className="line-clamp-2 text-xs text-muted">
                    {entry.description || (entry.kind === 'library' ? t('newCollection.library') : t('newCollection.list'))}
                  </span>
                </button>
              ))}
              {templates.isPending && <Skeleton className="h-16" />}
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="collection-name">{t('common:fields.name')}</Label>
              <Input
                id="collection-name"
                required
                maxLength={200}
                autoFocus
                value={name}
                onChange={(event) => setName(event.target.value)}
              />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="collection-description">{t('common:fields.description')}</Label>
              <Textarea
                id="collection-description"
                rows={2}
                value={description}
                onChange={(event) => setDescription(event.target.value)}
              />
            </div>
          </div>
          <DialogFooter>
            <Button onClick={() => onOpenChange(false)}>{t('common:actions.cancel')}</Button>
            <Button type="submit" variant="primary" disabled={!name.trim() || create.isPending}>
              {create.isPending ? t('common:status.creating') : choice.kind === 'library' ? t('newCollection.createLibrary') : t('newCollection.createList')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
