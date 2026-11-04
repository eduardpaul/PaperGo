import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import { useState } from 'react';
import { toast } from 'sonner';
import { api } from '@/api/http';
import { Button } from '@/components/ui/button';
import { Input, Label, Textarea } from '@/components/ui/input';
import { Select } from '@/components/ui/select';
import { Sheet, SheetClose, SheetContent, SheetDescription, SheetTitle } from '@/components/ui/sheet';
import { QueryFeedback } from '@/features/admin/common';
import type { FormSchema } from '@/features/workflows/model';
import { SchemaFields } from '@/features/workflows/schema-form';
import { ScriptEditor } from '@/features/workflows/script-editor';
import { useTranslation } from '@/i18n';
import { appKeys, type App, type AppState } from './api';

/** The source a new app starts from: a live table of a chosen list's items. */
export const starterApp = `import { app, useQuery, Card, Table } from "@papergo/app";

export default app<{ list: string }>({
  parameters: {
    type: "object",
    required: ["list"],
    properties: { list: { type: "string", title: "List", "x-papergo": { kind: "collection" } } },
  },
  render(ctx) {
    const items = useQuery(ctx.params.list, {}, { live: true });
    return (
      <Card title={ctx.app.name}>
        <Table columns={[{ key: "name", label: "Name" }]} rows={items.data} onRowClick={(item) => ctx.open({ itemId: item.id })} />
      </Card>
    );
  },
});
`;

/**
 * Edits an app: its name, description and state, its TSX source checked
 * against @papergo/app, and its parameters, chosen with the form the saved
 * source declares. The server compiles the source when it is saved and
 * refuses one that does not compile, with the reason.
 */
export function AppEditor({ workspaceId, app: initial, onClose }: { workspaceId: string; app?: App; onClose: () => void }) {
  const { t } = useTranslation(['apps', 'common']);
  const client = useQueryClient();
  const [app, setApp] = useState(initial);
  const [name, setName] = useState(initial?.name ?? '');
  const [description, setDescription] = useState(initial?.description ?? '');
  const [state, setState] = useState<AppState>(initial?.state ?? 'draft');
  const [source, setSource] = useState(initial?.source ?? starterApp);
  const [parameters, setParameters] = useState<Record<string, unknown>>(initial?.parameters ?? {});
  const body = { name: name.trim(), description, state, source, parameters };
  const saved = !!app && JSON.stringify(body) === JSON.stringify({ name: app.name, description: app.description, state: app.state, source: app.source, parameters: app.parameters });
  const save = useMutation({
    mutationFn: async () => (await api<App>(app ? `/v1/apps/${app.id}` : `/v1/workspaces/${workspaceId}/apps`, { method: app ? 'PUT' : 'POST', ifMatch: app?.version, body })).data,
    onSuccess: async (next) => {
      setApp(next);
      setParameters(next.parameters);
      toast.success(t('editor.saved', { version: next.current_version }));
      await client.invalidateQueries({ queryKey: appKeys.lists });
      await client.invalidateQueries({ queryKey: appKeys.one(next.id) });
    },
  });
  const schema = app?.parameters_schema as FormSchema | undefined;
  const hasParameters = !!schema && Object.keys(schema.properties ?? {}).length > 0;
  return (
    <Sheet open onOpenChange={(open) => !open && onClose()}>
      <SheetContent className="md:w-[min(1500px,96vw)]">
        <form
          className="flex min-h-0 flex-1 flex-col"
          onSubmit={(event) => {
            event.preventDefault();
            save.mutate();
          }}
        >
          <header className="flex items-center gap-2 border-b p-4">
            <div className="flex-1">
              <SheetTitle>{app ? t('editor.title', { name: app.name }) : t('editor.newTitle')}</SheetTitle>
              <SheetDescription>{t('editor.description')}</SheetDescription>
            </div>
            <SheetClose />
          </header>
          <div className="grid min-h-0 flex-1 overflow-auto lg:grid-cols-[minmax(0,3fr)_minmax(0,2fr)]">
            <div className="grid content-start gap-3 p-4">
              <Label htmlFor="app-source">{t('editor.source')}</Label>
              <ScriptEditor id="app-source" kind="app" value={source} onChange={setSource} />
              <p className="text-xs text-muted">{t('editor.sourceHint')}</p>
            </div>
            <div className="grid content-start gap-3 border-t p-4 lg:border-t-0 lg:border-l">
              <Label htmlFor="app-name">{t('common:fields.name')}</Label>
              <Input id="app-name" required maxLength={200} value={name} onChange={(event) => setName(event.target.value)} />
              <Label htmlFor="app-description">{t('common:fields.description')}</Label>
              <Textarea id="app-description" maxLength={2000} value={description} onChange={(event) => setDescription(event.target.value)} />
              <Label htmlFor="app-state">{t('editor.state')}</Label>
              <Select id="app-state" value={state} onChange={(event) => setState(event.target.value as AppState)}>
                <option value="draft">{t('state.draft')}</option>
                <option value="published">{t('state.published')}</option>
              </Select>
              <p className="text-xs text-muted">{t('editor.stateHint')}</p>
              <h3 className="mt-2 text-sm font-semibold">{t('editor.parameters')}</h3>
              {hasParameters ? (
                <SchemaFields schema={schema} data={parameters} onChange={setParameters} workspaceId={workspaceId} />
              ) : (
                <p className="text-xs text-muted">{app ? t('editor.noParameters') : t('editor.parametersAfterSave')}</p>
              )}
              <QueryFeedback query={save} />
            </div>
          </div>
          <footer className="flex flex-wrap justify-end gap-2 border-t p-4">
            {app && (
              <Button asChild>
                <Link to="/w/$workspaceId/a/$appId" params={{ workspaceId, appId: app.id }}>
                  {t('actions.open')}
                </Link>
              </Button>
            )}
            <Button onClick={onClose}>{t('common:actions.close')}</Button>
            <Button variant="primary" type="submit" disabled={save.isPending || saved}>
              {t('common:actions.save')}
            </Button>
          </footer>
        </form>
      </SheetContent>
    </Sheet>
  );
}
