import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api } from "@/api/http";
import type { Workflow } from "@/api/types";
import { Button } from "@/components/ui/button";
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
import type { WorkflowDefinition } from "./model";
import { triggerLabel } from "./presentation";
import { JsonInput } from "./schema-form";

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
 * as ctx.params, and its use. The server bundles the script and reads its
 * triggers and forms when it is saved; a script that does not compile is
 * refused with the bundler's errors.
 */
export function WorkflowEditor({
  workspaceId,
  workflow,
  canManage,
  onClose,
}: {
  workspaceId: string;
  workflow?: Workflow;
  canManage: boolean;
  onClose: () => void;
}) {
  const client = useQueryClient();
  const [name, setName] = useState(workflow?.name ?? "");
  const [description, setDescription] = useState(workflow?.description ?? "");
  const [automatic, setAutomatic] = useState(workflow?.automatic_enabled ?? false);
  const [manual, setManual] = useState(workflow?.manual_enabled ?? true);
  const [state, setState] = useState<Workflow["state"]>(workflow?.state ?? "draft");
  const [source, setSource] = useState(workflow?.source ?? starterScript);
  const [parameters, setParameters] = useState<Record<string, unknown>>(workflow?.parameters ?? {});
  const save = useMutation({
    mutationFn: () =>
      api<Workflow>(workflow ? `/v1/workflows/${workflow.id}` : `/v1/workspaces/${workspaceId}/workflows`, {
        method: workflow ? "PUT" : "POST",
        ifMatch: workflow?.version,
        body: { name: name.trim(), description, automatic_enabled: automatic, manual_enabled: manual, state, source, parameters },
      }),
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: ["workflows", workspaceId] });
      onClose();
    },
  });
  const definition = workflow?.definition as WorkflowDefinition | undefined;
  return (
    <Sheet open onOpenChange={(open) => !open && onClose()}>
      <SheetContent className="md:w-[min(950px,94vw)]">
        <form
          className="flex min-h-0 flex-1 flex-col"
          onSubmit={(event) => {
            event.preventDefault();
            if (canManage) save.mutate();
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
          <fieldset disabled={!canManage} className="grid min-h-0 flex-1 gap-4 overflow-auto p-4">
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
              Drafts and archived workflows accept no new runs. Automatic triggers on content name a library or list.
            </p>
            {workflow?.role && automatic && (
              <p className="text-sm">
                Saving a published automatic workflow replaces the automatic provider for this role in its collection.
              </p>
            )}
            <Label htmlFor="workflow-source">Script</Label>
            <Textarea
              id="workflow-source"
              required
              spellCheck={false}
              className="min-h-96 font-mono text-xs"
              value={source}
              onChange={(event) => setSource(event.target.value)}
            />
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
                      {triggerLabel(trigger)}
                      {trigger.disabled && " · paused"}
                    </li>
                  ))}
                </ul>
                {definition.concurrency && <p className="text-muted">Concurrent starts on an item: {definition.concurrency}</p>}
              </section>
            )}
            <QueryFeedback query={save} />
          </fieldset>
          {canManage && (
            <footer className="flex justify-end gap-2 border-t p-4">
              <Button onClick={onClose}>Cancel</Button>
              <Button variant="primary" type="submit" disabled={save.isPending}>
                Save
              </Button>
            </footer>
          )}
        </form>
      </SheetContent>
    </Sheet>
  );
}
