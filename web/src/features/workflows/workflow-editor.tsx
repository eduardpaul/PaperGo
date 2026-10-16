import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api } from "@/api/http";
import { collectionChildren, fieldsQuery } from "@/api/queries";
import type { Workflow } from "@/api/types";
import { Button } from "@/components/ui/button";
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
import {
  FilterEditor,
  normalizeFilter,
  systemFields,
} from "@/features/lists/filter-editor";
import { problemMessage } from "@/lib/errors";
import { activityOutputPaths, formDefaults, nodePorts, normalizeForm, renameNode } from "./model";
import type {
  Activity,
  FlowNode,
  Trigger,
  WorkflowCatalog,
  WorkflowDefinition,
} from "./model";
import { JsonInput, SchemaFields } from "./schema-form";

const fileSources = ["upload", "webdav", "restore", "workflow", "reindex"];
// A file.added trigger without sources ignores files that workflows write.
const defaultFileSources = ["upload", "webdav", "restore"];
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
  const [enabled, setEnabled] = useState(workflow?.enabled ?? true);
  const [definition, setDefinition] = useState<WorkflowDefinition>(
    (workflow?.definition as WorkflowDefinition) ?? {
      triggers: [{ type: "manual" }],
      flow: { start: "end", nodes: { end: { activity: "end" } } },
    },
  );
  const [asJSON, setAsJSON] = useState(false);
  const catalog = useQuery({
    queryKey: ["workflow-catalog"],
    queryFn: async () =>
      (await api<WorkflowCatalog>("/v1/workflow-catalog")).data,
  });
  const collections = useQuery({
    queryKey: ["resources", workspaceId, "collections"],
    queryFn: () => collectionChildren(workspaceId),
  });
  const collectionId = definition.triggers?.find(
    (trigger) => trigger.collection_id,
  )?.collection_id;
  const fields = useQuery({
    ...fieldsQuery(collectionId ?? ""),
    enabled: !!collectionId,
  });
  const queryFields = [
    ...systemFields,
    ...(fields.data ?? []).filter((field) => field.indexed),
  ];
  const save = useMutation({
    mutationFn: () => {
      const normalized: WorkflowDefinition = {
        ...definition,
        input_schema: definition.triggers.some(
          (trigger) => trigger.type === "manual",
        )
          ? definition.input_schema
          : undefined,
        condition: normalizeFilter(definition.condition, queryFields),
        flow: {
          ...definition.flow,
          nodes: Object.fromEntries(
            Object.entries(definition.flow.nodes).map(([name, node]) => {
              const activity = catalog.data?.activities.find(
                (activity) => activity.key === node.activity,
              );
              return [
                name,
                {
                  ...node,
                  inputs: activity
                    ? (normalizeForm(
                        activity.input_schema,
                        node.inputs ?? {},
                        true,
                      ) as Record<string, unknown>)
                    : node.inputs,
                },
              ];
            }),
          ),
        },
      };
      return api(
        workflow
          ? `/v1/workflows/${workflow.id}`
          : `/v1/workspaces/${workspaceId}/workflows`,
        {
          method: workflow ? "PUT" : "POST",
          ifMatch: workflow?.version,
          body: {
            name: name.trim(),
            description,
            enabled,
            definition: normalized,
          },
        },
      );
    },
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: ["workflows", workspaceId] });
      onClose();
    },
  });
  const changeTrigger = (index: number, trigger: Trigger) =>
    setDefinition((current) => ({
      ...current,
      triggers: current.triggers.map((entry, i) =>
        i === index ? trigger : entry,
      ),
    }));
  return (
    <Sheet open onOpenChange={(open) => !open && onClose()}>
      <SheetContent className="md:w-[min(850px,94vw)]">
        <form
          className="flex min-h-0 flex-1 flex-col"
          onSubmit={(event) => {
            event.preventDefault();
            if (canManage) save.mutate();
          }}
        >
          <header className="flex items-center gap-2 border-b p-4">
            <div className="flex-1">
              <SheetTitle>
                {workflow ? `Workflow ${workflow.name}` : "New workflow"}
              </SheetTitle>
              <SheetDescription>
                Triggers, conditions and connected activity steps.
              </SheetDescription>
            </div>
            <Button
              aria-pressed={asJSON}
              onClick={() => setAsJSON((value) => !value)}
            >
              JSON
            </Button>
            <SheetClose />
          </header>
          <fieldset
            disabled={!canManage}
            className="grid min-h-0 flex-1 gap-4 overflow-auto p-4"
          >
            <Label htmlFor="workflow-name">Name</Label>
            <Input
              id="workflow-name"
              required
              maxLength={100}
              value={name}
              onChange={(event) => setName(event.target.value)}
            />
            <Label htmlFor="workflow-description">Description</Label>
            <Textarea
              id="workflow-description"
              value={description}
              onChange={(event) => setDescription(event.target.value)}
            />
            <label className="flex gap-2">
              <Checkbox
                checked={enabled}
                onChange={(event) => setEnabled(event.target.checked)}
              />
              Enabled
            </label>
            <QueryFeedback query={catalog} />
            {asJSON ? (
              <JsonInput
                key="definition"
                label="Workflow definition"
                value={definition}
                onChange={(value) => {
                  const next = value as WorkflowDefinition;
                  if (
                    !next ||
                    !Array.isArray(next.triggers) ||
                    !next.flow ||
                    next.triggers.some(
                      (trigger) => !trigger || typeof trigger.type !== "string",
                    ) ||
                    typeof next.flow.start !== "string" ||
                    !next.flow.nodes ||
                    Object.values(next.flow.nodes).some(
                      (node) => !node || typeof node.activity !== "string",
                    )
                  )
                    throw new Error(
                      "A workflow needs triggers and a flow with named activity steps.",
                    );
                  setDefinition(next);
                }}
              />
            ) : (
              <>
                <section className="grid gap-3 rounded border p-3">
                  <h3 className="font-semibold">When</h3>
                  {definition.triggers.map((trigger, index) => (
                    <div key={index} className="grid gap-2 rounded border p-3">
                      <Select
                        aria-label={`Trigger ${index + 1}`}
                        value={
                          trigger.type.startsWith("wf.")
                            ? "workflow-event"
                            : trigger.type
                        }
                        onChange={(event) =>
                          changeTrigger(index, {
                            type:
                              event.target.value === "workflow-event"
                                ? "wf.workflow.completed"
                                : event.target.value,
                            collection_id: event.target.value.startsWith(
                              "term.",
                            )
                              ? undefined
                              : trigger.collection_id,
                          })
                        }
                      >
                        {catalog.data?.triggers
                          .filter((trigger) => !trigger.type.startsWith("wf."))
                          .map((trigger) => (
                            <option key={trigger.type} value={trigger.type}>
                              {trigger.type}
                            </option>
                          ))}
                        <option value="workflow-event">
                          Another workflow event
                        </option>
                      </Select>
                      {trigger.type.startsWith("wf.") && (
                        <Input
                          aria-label="Workflow event"
                          value={trigger.type}
                          onChange={(event) =>
                            changeTrigger(index, {
                              ...trigger,
                              type: event.target.value,
                            })
                          }
                          placeholder="wf.workflow-key.completed"
                        />
                      )}
                      {!trigger.type.startsWith("term.") && (
                        <Select
                          aria-label={`Trigger collection ${index + 1}`}
                          value={trigger.collection_id ?? ""}
                          onChange={(event) =>
                            changeTrigger(index, {
                              ...trigger,
                              collection_id: event.target.value || undefined,
                              content_type_id: undefined,
                              surface: undefined,
                              selection: undefined,
                            })
                          }
                        >
                          <option value="">Whole workspace</option>
                          {collections.data?.map((collection) => (
                            <option key={collection.id} value={collection.id}>
                              {collection.name}
                            </option>
                          ))}
                        </Select>
                      )}
                      {trigger.collection_id &&
                        (trigger.type.startsWith("item.") ||
                          trigger.type.startsWith("wf.") ||
                          trigger.type === "file.added") && (
                          <Input
                            aria-label="Trigger content type"
                            value={trigger.content_type_id ?? ""}
                            onChange={(event) =>
                              changeTrigger(index, {
                                ...trigger,
                                content_type_id:
                                  event.target.value || undefined,
                              })
                            }
                            placeholder="Optional content type id"
                          />
                        )}
                      {trigger.type === "schedule" && (
                        <>
                          <Input
                            aria-label="Cron schedule"
                            required
                            placeholder="0 8 * * 1-5"
                            value={trigger.cron ?? ""}
                            onChange={(event) =>
                              changeTrigger(index, {
                                ...trigger,
                                cron: event.target.value,
                              })
                            }
                          />
                          <Input
                            aria-label="Schedule time zone"
                            placeholder="UTC"
                            value={trigger.time_zone ?? ""}
                            onChange={(event) =>
                              changeTrigger(index, {
                                ...trigger,
                                time_zone: event.target.value || undefined,
                              })
                            }
                          />
                          {trigger.collection_id && (
                            <Select
                              aria-label="Schedule surface"
                              value={trigger.surface ?? "head"}
                              onChange={(event) =>
                                changeTrigger(index, {
                                  ...trigger,
                                  surface: event.target.value,
                                })
                              }
                            >
                              <option value="head">Current draft</option>
                              <option value="published">Published</option>
                            </Select>
                          )}
                        </>
                      )}
                      {trigger.type === "manual" && trigger.collection_id && (
                        <Select
                          aria-label="Manual selection mode"
                          value={trigger.selection ?? "per_item"}
                          onChange={(event) =>
                            changeTrigger(index, {
                              ...trigger,
                              selection: event.target.value,
                            })
                          }
                        >
                          <option value="per_item">One run per item</option>
                          <option value="selection">
                            One run for the ordered selection
                          </option>
                        </Select>
                      )}
                      {trigger.type === "file.added" && (
                        <fieldset className="flex flex-wrap gap-3 text-sm">
                          <legend className="sr-only">File sources</legend>
                          {fileSources.map((source) => {
                            const chosen =
                              trigger.sources ?? defaultFileSources;
                            return (
                              <label
                                key={source}
                                className="flex items-center gap-1"
                              >
                                <Checkbox
                                  aria-label={`File source ${source}`}
                                  checked={chosen.includes(source)}
                                  onChange={(event) => {
                                    const next = event.target.checked
                                      ? [...chosen, source]
                                      : chosen.filter((s) => s !== source);
                                    changeTrigger(index, {
                                      ...trigger,
                                      sources: next.length ? next : undefined,
                                    });
                                  }}
                                />
                                {source}
                              </label>
                            );
                          })}
                        </fieldset>
                      )}
                      {trigger.type === "file.added" && (
                        <Input
                          aria-label="Media types"
                          placeholder="Optional media types, such as application/pdf, image/*"
                          defaultValue={(trigger.media_types ?? []).join(", ")}
                          onBlur={(event) => {
                            const types = event.target.value
                              .split(",")
                              .map((type) => type.trim().toLowerCase())
                              .filter(Boolean);
                            changeTrigger(index, {
                              ...trigger,
                              media_types: types.length ? types : undefined,
                            });
                          }}
                        />
                      )}
                      {trigger.type.startsWith("task.") && (
                        <Input
                          aria-label="Task key"
                          placeholder="Optional task key, such as legal_review"
                          value={trigger.task_key ?? ""}
                          onChange={(event) =>
                            changeTrigger(index, {
                              ...trigger,
                              task_key: event.target.value || undefined,
                            })
                          }
                        />
                      )}
                      {[
                        "item.created",
                        "item.updated",
                        "item.published",
                      ].includes(trigger.type) && (
                        <>
                          <Input
                            aria-label="Trigger terms"
                            value={(trigger.terms ?? []).join(", ")}
                            placeholder="Optional term ids, comma separated"
                            onChange={(event) =>
                              changeTrigger(index, {
                                ...trigger,
                                terms: event.target.value
                                  .split(",")
                                  .map((id) => id.trim())
                                  .filter(Boolean),
                                term_change: undefined,
                              })
                            }
                          />
                          {!!trigger.terms?.length && (
                            <Select
                              aria-label="Term change"
                              value={trigger.term_change ?? "present"}
                              onChange={(event) =>
                                changeTrigger(index, {
                                  ...trigger,
                                  term_change: event.target.value,
                                })
                              }
                            >
                              <option value="present">Present</option>
                              <option value="added">Added</option>
                              <option value="removed">Removed</option>
                            </Select>
                          )}
                        </>
                      )}
                      <Button
                        onClick={() =>
                          setDefinition((current) => ({
                            ...current,
                            triggers: current.triggers.filter(
                              (_, i) => i !== index,
                            ),
                          }))
                        }
                      >
                        Remove trigger
                      </Button>
                    </div>
                  ))}
                  <Button
                    disabled={definition.triggers.length >= 20}
                    onClick={() =>
                      setDefinition((current) => ({
                        ...current,
                        triggers: [
                          ...current.triggers,
                          { type: "item.updated" },
                        ],
                      }))
                    }
                  >
                    Add trigger
                  </Button>
                </section>
                <section className="grid gap-2">
                  <h3 className="font-semibold">
                    While a run on the same item is active
                  </h3>
                  <Select
                    aria-label="Concurrency"
                    value={definition.concurrency ?? "parallel"}
                    onChange={(event) =>
                      setDefinition((current) => ({
                        ...current,
                        concurrency:
                          event.target.value === "parallel"
                            ? undefined
                            : event.target.value,
                      }))
                    }
                  >
                    <option value="parallel">Start another run</option>
                    <option value="skip">Skip the new run</option>
                    <option value="replace">Cancel the active run</option>
                    <option value="coalesce">
                      Run once more after the active run
                    </option>
                  </Select>
                </section>
                <section className="grid gap-2">
                  <h3 className="font-semibold">Only when</h3>
                  <FilterEditor
                    filter={definition.condition}
                    fields={queryFields}
                    onChange={(condition) =>
                      setDefinition((current) => ({ ...current, condition }))
                    }
                  />
                </section>
                {definition.triggers.some(
                  (trigger) => trigger.type === "manual",
                ) && (
                  <section className="grid gap-2">
                    <h3 className="font-semibold">Launch form schema</h3>
                    <JsonInput
                      label="Workflow input schema"
                      value={
                        definition.input_schema ?? {
                          type: "object",
                          properties: {},
                        }
                      }
                      onChange={(input_schema) =>
                        setDefinition((current) => ({
                          ...current,
                          input_schema:
                            input_schema as WorkflowDefinition["input_schema"],
                        }))
                      }
                    />
                  </section>
                )}
                <section className="grid gap-2">
                  <h3 className="font-semibold">Variables</h3>
                  <JsonInput
                    label="Workflow variables"
                    value={definition.variables ?? {}}
                    onChange={(variables) =>
                      setDefinition((current) => ({
                        ...current,
                        variables: variables as Record<string, unknown>,
                      }))
                    }
                  />
                </section>
                <section className="grid gap-2">
                  <h3 className="font-semibold">Outputs</h3>
                  <p className="text-sm text-muted">
                    What a completed run returns to the workflows that follow
                    it, as data.outputs of its completed event.
                  </p>
                  <JsonInput
                    label="Workflow output schema"
                    value={
                      definition.output_schema ?? {
                        type: "object",
                        properties: {},
                      }
                    }
                    onChange={(output_schema) =>
                      setDefinition((current) => ({
                        ...current,
                        output_schema: isEmptyForm(output_schema)
                          ? undefined
                          : (output_schema as WorkflowDefinition["output_schema"]),
                      }))
                    }
                  />
                  <JsonInput
                    label="Workflow outputs"
                    value={definition.outputs ?? {}}
                    onChange={(outputs) =>
                      setDefinition((current) => {
                        const values = outputs as Record<string, unknown>;
                        return {
                          ...current,
                          outputs: Object.keys(values ?? {}).length
                            ? values
                            : undefined,
                        };
                      })
                    }
                  />
                </section>
                {catalog.data && (
                  <FlowEditor
                    key={asJSON ? "json" : "visual"}
                    definition={definition}
                    activities={catalog.data.activities}
                    workspaceId={workspaceId}
                    onChange={setDefinition}
                  />
                )}
              </>
            )}
            {save.error && <Alert>{problemMessage(save.error)}</Alert>}
          </fieldset>
          <footer className="flex justify-end gap-2 border-t p-4">
            <Button onClick={onClose}>Close</Button>
            {canManage && (
              <Button type="submit" variant="primary" disabled={save.isPending}>
                Save workflow
              </Button>
            )}
          </footer>
        </form>
      </SheetContent>
    </Sheet>
  );
}

/** An object form without properties declares nothing. */
function isEmptyForm(value: unknown): boolean {
  const form = value as
    { type?: string; properties?: Record<string, unknown> } | undefined;
  return (
    !form ||
    (form.type === "object" &&
      Object.keys(form.properties ?? {}).length === 0 &&
      Object.keys(form).length <= 2)
  );
}

function FlowEditor({
  definition,
  activities,
  workspaceId,
  onChange,
}: {
  definition: WorkflowDefinition;
  activities: Activity[];
  workspaceId: string;
  onChange: (definition: WorkflowDefinition) => void;
}) {
  const [selected, setSelected] = useState(definition.flow.start);
  const [newName, setNewName] = useState(definition.flow.start);
  const [error, setError] = useState("");
  const nodes = definition.flow.nodes;
  const node = nodes[selected];
  const activity = activities.find(
    (activity) => activity.key === node?.activity,
  );
  const changeNode = (patch: Partial<FlowNode>) =>
    onChange({
      ...definition,
      flow: {
        ...definition.flow,
        nodes: { ...nodes, [selected]: { ...node, ...patch } },
      },
    });
  return (
    <section className="grid gap-3 rounded border p-3">
      <h3 className="font-semibold">Steps and branches</h3>
      <Select
        aria-label="First step"
        value={definition.flow.start}
        onChange={(event) =>
          onChange({
            ...definition,
            flow: { ...definition.flow, start: event.target.value },
          })
        }
      >
        {Object.keys(nodes).map((name) => (
          <option key={name} value={name}>
            {name}
          </option>
        ))}
      </Select>
      <div className="flex flex-wrap gap-2">
        {Object.entries(nodes).map(([name, node]) => (
          <button
            key={name}
            type="button"
            className={`rounded border p-3 text-left ${name === selected ? "border-accent bg-accent-soft" : "bg-surface"}`}
            onClick={() => {
              setSelected(name);
              setNewName(name);
            }}
          >
            <span className="block font-medium">{name}</span>
            <span className="text-xs text-muted">{node.activity}</span>
            <span className="block text-xs">
              {Object.entries(node.next ?? {})
                .map(([port, target]) => `${port} → ${target}`)
                .join(" · ")}
            </span>
          </button>
        ))}
      </div>
      <Button
        disabled={Object.keys(nodes).length >= 100}
        onClick={() => {
          let name = `Step ${Object.keys(nodes).length + 1}`;
          while (nodes[name]) name += "_";
          const port = nodePorts(activity, node?.inputs)[0] ?? "done";
          const target = activity?.terminal ? selected : node?.next?.[port];
          const next: FlowNode = {
            activity: "item.get",
            inputs: {},
            next: target ? { done: target } : undefined,
          };
          const updated = Object.fromEntries(
            Object.entries(nodes).map(([id, current]) => [
              id,
              activity?.terminal
                ? {
                    ...current,
                    next: Object.fromEntries(
                      Object.entries(current.next ?? {}).map(
                        ([port, target]) => [
                          port,
                          target === selected ? name : target,
                        ],
                      ),
                    ),
                  }
                : id === selected
                  ? { ...current, next: { ...current.next, [port]: name } }
                  : current,
            ]),
          );
          onChange({
            ...definition,
            flow: {
              start:
                activity?.terminal && definition.flow.start === selected
                  ? name
                  : definition.flow.start || name,
              nodes: { ...updated, [name]: next },
            },
          });
          setSelected(name);
          setNewName(name);
        }}
      >
        Add step
      </Button>
      {node && (
        <div key={selected} className="grid gap-3 rounded border p-3">
          <div className="flex gap-2">
            <Input
              aria-label="Step name"
              value={newName || selected}
              onChange={(event) => setNewName(event.target.value)}
            />
            <Button
              onClick={() => {
                try {
                  onChange(
                    renameNode(definition, selected, newName || selected),
                  );
                  setSelected(newName.trim() || selected);
                  setError("");
                } catch (error) {
                  setError(problemMessage(error));
                }
              }}
            >
              Rename
            </Button>
            <Button
              variant="danger"
              disabled={Object.keys(nodes).length <= 1}
              onClick={() => {
                const remaining = Object.fromEntries(
                  Object.entries(nodes)
                    .filter(([name]) => name !== selected)
                    .map(([name, current]) => [
                      name,
                      {
                        ...current,
                        next: Object.fromEntries(
                          Object.entries(current.next ?? {}).filter(
                            ([, target]) => target !== selected,
                          ),
                        ),
                      },
                    ]),
                );
                const first = Object.keys(remaining)[0];
                onChange({
                  ...definition,
                  flow: {
                    start:
                      definition.flow.start === selected
                        ? first
                        : definition.flow.start,
                    nodes: remaining,
                  },
                });
                setSelected(first);
                setNewName(first);
              }}
            >
              Delete step
            </Button>
          </div>
          <Select
            aria-label="Activity"
            value={node.activity}
            onChange={(event) => {
              const activity = activities.find(
                (activity) => activity.key === event.target.value,
              )!;
              const inputs = formDefaults(activity.input_schema);
              const ports = ["done", "error", ...nodePorts(activity, inputs)];
              changeNode({
                activity: activity.key,
                inputs,
                next: activity.terminal
                  ? undefined
                  : Object.fromEntries(
                      Object.entries(node.next ?? {}).filter(([port]) =>
                        ports.includes(port),
                      ),
                    ),
              });
            }}
          >
            {activities.map((activity) => (
              <option key={activity.key} value={activity.key}>
                {activity.key}
              </option>
            ))}
          </Select>
          <p className="text-xs text-muted">{activity?.description}</p>
          {activity && (
            <SchemaFields
              key={node.activity}
              schema={activity.input_schema}
              data={node.inputs ?? {}}
              onChange={(inputs) => changeNode({ inputs })}
              workspaceId={workspaceId}
              tokens
            />
          )}
          {activityOutputPaths(activity, node.inputs).length > 0 && (
            <details className="text-xs">
              <summary>Output tokens</summary>
              {activityOutputPaths(activity, node.inputs).map(path => <code key={path} className="block">{`{step:${selected}.${path}}`}</code>)}
            </details>
          )}
          {!activity?.terminal &&
            [
              ...new Set([
                "done",
                "error",
                ...nodePorts(activity, node.inputs),
              ]),
            ].map((port) => (
              <label
                key={port}
                className="grid grid-cols-[5rem_1fr] items-center gap-2"
              >
                {port}
                <Select
                  aria-label={`${port} branch`}
                  value={node.next?.[port] ?? ""}
                  onChange={(event) => {
                    const next = { ...node.next };
                    if (event.target.value) next[port] = event.target.value;
                    else delete next[port];
                    changeNode({ next });
                  }}
                >
                  <option value="">End run</option>
                  {Object.keys(nodes).map((name) => (
                    <option key={name} value={name}>
                      {name}
                    </option>
                  ))}
                </Select>
              </label>
            ))}
          <label className="flex gap-2">
            <Checkbox
              checked={!!node.retry}
              onChange={(event) =>
                changeNode({
                  retry: event.target.checked
                    ? { attempts: 3, delay: "1m" }
                    : undefined,
                })
              }
            />
            Retry failed step
          </label>
          {node.retry && (
            <div className="flex gap-2">
              <Input
                aria-label="Retry attempts"
                type="number"
                min={1}
                max={10}
                value={node.retry.attempts}
                onChange={(event) =>
                  changeNode({
                    retry: {
                      ...node.retry!,
                      attempts: Number(event.target.value),
                    },
                  })
                }
              />
              <Input
                aria-label="Retry delay"
                value={node.retry.delay ?? ""}
                placeholder="1m"
                onChange={(event) =>
                  changeNode({
                    retry: {
                      ...node.retry!,
                      delay: event.target.value || undefined,
                    },
                  })
                }
              />
            </div>
          )}
        </div>
      )}
      {error && <Alert>{error}</Alert>}
    </section>
  );
}
