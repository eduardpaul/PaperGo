import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { toast } from "sonner";
import { api } from "@/api/http";
import { useApiPages } from "@/api/pages";
import type { Resource, Workflow } from "@/api/types";
import { Button } from "@/components/ui/button";
import { Combobox } from "@/components/ui/combobox";
import { Alert } from "@/components/ui/feedback";
import { Input, Label, Textarea } from "@/components/ui/input";
import { Checkbox, Select } from "@/components/ui/select";
import {
  Sheet,
  SheetClose,
  SheetContent,
  SheetDescription,
  SheetTitle,
} from "@/components/ui/sheet";
import { QueryFeedback } from "@/features/admin/common";
import { formDefaults, normalizeForm, type FormSchema, type WorkflowDefinition } from "./model";
import { triggerLabel, workspaceTypesQuery } from "./presentation";
import { RunView } from "./run-details";
import { JsonInput, SchemaFields } from "./schema-form";
import { ScriptEditor } from "./script-editor";

/** The script a new workflow starts from. */
export const starterScript = `import { workflow } from "@papergo/workflow";

export default workflow({
  triggers: [{ type: "manual" }],
  async run(ctx) {
    ctx.log("started by", ctx.run.actor);
  },
});
`;

/**
 * Edits a workflow: its TypeScript script, the parameters the script reads
 * as ctx.params, and its use, beside a panel that saves the script and runs
 * it as a test on chosen items. The server bundles the script and reads its
 * triggers and forms when it is saved; a script that does not compile is
 * refused with the bundler's errors.
 */
export function WorkflowEditor({
  workspaceId,
  workflow: initial,
  canManage,
  onClose,
}: {
  workspaceId: string;
  workflow?: Workflow;
  canManage: boolean;
  onClose: () => void;
}) {
  const client = useQueryClient();
  const [workflow, setWorkflow] = useState(initial);
  const [name, setName] = useState(initial?.name ?? "");
  const [description, setDescription] = useState(initial?.description ?? "");
  const [automatic, setAutomatic] = useState(initial?.automatic_enabled ?? false);
  const [manual, setManual] = useState(initial?.manual_enabled ?? true);
  const [state, setState] = useState<Workflow["state"]>(initial?.state ?? "draft");
  const [source, setSource] = useState(initial?.source ?? starterScript);
  const [parameters, setParameters] = useState<Record<string, unknown>>(initial?.parameters ?? {});
  const body = { name: name.trim(), description, automatic_enabled: automatic, manual_enabled: manual, state, source, parameters };
  const saved = !!workflow && JSON.stringify(body) === JSON.stringify({
    name: workflow.name, description: workflow.description, automatic_enabled: workflow.automatic_enabled,
    manual_enabled: workflow.manual_enabled, state: workflow.state, source: workflow.source, parameters: workflow.parameters ?? {},
  });
  const save = useMutation({
    mutationFn: async () => {
      if (workflow && saved) return workflow;
      const result = await api<Workflow>(workflow ? `/v1/workflows/${workflow.id}` : `/v1/workspaces/${workspaceId}/workflows`, {
        method: workflow ? "PUT" : "POST",
        ifMatch: workflow?.version,
        body,
      });
      return result.data;
    },
    onSuccess: async (next) => {
      setWorkflow(next);
      await client.invalidateQueries({ queryKey: ["workflows", workspaceId] });
    },
  });
  const definition = workflow?.definition as WorkflowDefinition | undefined;
  const types = useQuery(workspaceTypesQuery(workspaceId, definition?.triggers));
  return (
    <Sheet open onOpenChange={(open) => !open && onClose()}>
      <SheetContent className="md:w-[min(1500px,96vw)]">
        <form
          className="flex min-h-0 flex-1 flex-col"
          onSubmit={(event) => {
            event.preventDefault();
            if (canManage) save.mutate(undefined, { onSuccess: (next) => toast.success(`Saved version ${next.current_version}`) });
          }}
        >
          <header className="flex items-center gap-2 border-b p-4">
            <div className="flex-1">
              <SheetTitle>{workflow ? `Workflow ${workflow.name}` : "New workflow"}</SheetTitle>
              <SheetDescription>
                A TypeScript script: its export declares when it runs, and run() calls the PaperGo API.
              </SheetDescription>
            </div>
            <SheetClose />
          </header>
          <div className="grid min-h-0 flex-1 overflow-auto lg:grid-cols-[minmax(0,3fr)_minmax(0,2fr)]">
            <fieldset disabled={!canManage} className="grid content-start gap-4 p-4">
              <Label htmlFor="workflow-name">Name</Label>
              <Input id="workflow-name" required maxLength={100} value={name} onChange={(event) => setName(event.target.value)} />
              <Label htmlFor="workflow-description">Description</Label>
              <Textarea id="workflow-description" value={description} onChange={(event) => setDescription(event.target.value)} />
              <label className="flex gap-2">
                <Checkbox checked={automatic} onChange={(event) => setAutomatic(event.target.checked)} />
                Run automatically on the script&apos;s triggers
              </label>
              <label className="flex gap-2">
                <Checkbox checked={manual} onChange={(event) => setManual(event.target.checked)} />
                Allow manual runs
              </label>
              <Label htmlFor="workflow-state">Publication state</Label>
              <Select id="workflow-state" value={state} onChange={(event) => setState(event.target.value as Workflow["state"])}>
                <option value="draft">Draft</option>
                <option value="published">Published</option>
                <option value="archived">Archived</option>
              </Select>
              <p className="text-xs text-muted">
                Drafts and archived workflows accept no new runs, except test runs. Automatic triggers on content name a library or list.
              </p>
              {workflow?.role && automatic && (
                <p className="text-sm">
                  Saving a published automatic workflow replaces the automatic provider for this role in its collection.
                </p>
              )}
              <Label htmlFor="workflow-source">Script</Label>
              <ScriptEditor id="workflow-source" value={source} onChange={setSource} readOnly={!canManage} />
              <p className="text-xs text-muted">
                Each API call, wait and sleep is a recorded step: a run that resumes replays them instead of repeating them.
                ctx.wait(operation) unloads the run until long work, such as OCR or a task, finishes.
              </p>
              <JsonInput label="Parameters" value={parameters} onChange={(value) => setParameters((value ?? {}) as Record<string, unknown>)} />
              <p className="text-xs text-muted">A JSON object the script reads as ctx.params, such as the IDs of the lists it works on.</p>
              {definition && (
                <section className="grid gap-1 rounded border p-3 text-sm">
                  <h3 className="font-semibold">Version {workflow?.current_version} runs</h3>
                  {definition.triggers.length === 0 && <p className="text-muted">Only when another workflow or an API call starts it.</p>}
                  <ul className="list-disc pl-5">
                    {definition.triggers.map((trigger, index) => (
                      <li key={index}>
                        {triggerLabel(trigger, types.data)}
                        {trigger.disabled && " · paused"}
                      </li>
                    ))}
                  </ul>
                  {definition.concurrency && <p className="text-muted">Concurrent starts on an item: {definition.concurrency}</p>}
                </section>
              )}
              <QueryFeedback query={save} />
            </fieldset>
            {canManage && <TestPanel workspaceId={workspaceId} workflow={workflow} saved={saved} save={() => save.mutateAsync()} />}
          </div>
          {canManage && (
            <footer className="flex justify-end gap-2 border-t p-4">
              <Button onClick={onClose}>Close</Button>
              <Button variant="primary" type="submit" disabled={save.isPending || saved}>
                Save
              </Button>
            </footer>
          )}
        </form>
      </SheetContent>
    </Sheet>
  );
}

/**
 * Saves the script when it changed and runs its current version as a test
 * on chosen items, showing the run's steps and log as they happen.
 */
function TestPanel({ workspaceId, workflow, saved, save }: { workspaceId: string; workflow?: Workflow; saved: boolean; save: () => Promise<Workflow> }) {
  const [q, setQ] = useState("");
  const [itemIds, setItemIds] = useState<string[]>([]);
  const [inputs, setInputs] = useState<Record<string, unknown>>({});
  const [runId, setRunId] = useState<string>();
  const items = useApiPages<Resource>(["resources", workspaceId, "test-items", q], "/v1/resources", { query: { workspace_id: workspaceId, q, surface: "head" } });
  const options = items.items.filter((item) => item.kind === "item").map((item) => ({ value: item.id, label: item.name }));
  const schema = ((workflow?.definition as WorkflowDefinition | undefined)?.input_schema ?? undefined) as FormSchema | undefined;
  const start = useMutation({
    mutationFn: async () => {
      const current = await save();
      const testSchema = (current.definition as WorkflowDefinition).input_schema;
      const values = testSchema ? normalizeForm(testSchema, { ...formDefaults(testSchema), ...inputs }) : undefined;
      return (await api<{ run_id: string }>(`/v1/workflows/${current.id}/test-runs`, { method: "POST", body: { item_ids: itemIds, inputs: values } })).data.run_id;
    },
    onSuccess: setRunId,
  });
  return (
    <section aria-label="Test" className="grid content-start gap-3 border-t p-4 lg:border-l lg:border-t-0">
      <h3 className="font-semibold">Test</h3>
      <p className="text-xs text-muted">
        Saves the script{saved ? "" : " (it has changes)"} and runs its current version now, draft or published, whatever its triggers. Test runs make real changes as the workflow&apos;s author and start no other workflow: use items meant for testing.
      </p>
      <Label id="test-items-label">Items</Label>
      <Combobox
        aria-labelledby="test-items-label"
        multiple
        selected={itemIds.map((id) => options.find((option) => option.value === id) ?? { value: id, label: id })}
        options={options}
        loading={items.isFetching}
        onSearch={setQ}
        onChange={(values) => setItemIds(values.map((option) => option.value))}
      />
      <p className="text-xs text-muted">None runs without an item; one runs on it; several run once on all of them, the first as the run&apos;s item.</p>
      {schema && <SchemaFields schema={schema} data={inputs} onChange={setInputs} workspaceId={workspaceId} />}
      <QueryFeedback query={start} />
      <div>
        <Button variant="primary" disabled={start.isPending} onClick={() => start.mutate()}>
          {saved ? "Run test" : "Save and run test"}
        </Button>
      </div>
      {!workflow && <Alert>Saving creates the workflow as you set it, a draft by default.</Alert>}
      {runId && <RunView key={runId} id={runId} interval={1000} />}
    </section>
  );
}
