import { describe, expect, it } from 'vitest';
import { parseJSON, stringifyJSON } from '@/api/json';
import { nodePorts, normalizeForm, renameNode } from './model';
import type { WorkflowDefinition } from './model';

describe('workflow editing', () => {
  it('renames graph edges and nested step tokens without rounding stored numbers', () => {
    const definition = parseJSON(
      '{"triggers":[{"type":"manual"}],"variables":{"amount":9007199254740993},"flow":{"start":"Read","nodes":{"Read":{"activity":"item.get","next":{"done":"Update"}},"Update":{"activity":"item.update","inputs":{"values":{"title":"{step:Read.name}","other":"{step:Reader.name}"}},"next":{"error":"Read"}}}}}',
    ) as WorkflowDefinition;
    const renamed = renameNode(definition, 'Read', 'Get');
    expect(renamed.flow.start).toBe('Get');
    expect(renamed.flow.nodes.Update.next?.error).toBe('Get');
    expect(renamed.flow.nodes.Update.inputs?.values).toEqual({ title: '{step:Get.name}', other: '{step:Reader.name}' });
    expect(stringifyJSON(renamed)).toContain('9007199254740993');
    expect(definition.flow.start).toBe('Read');
    expect(() => renameNode(definition, 'Read', 'Update')).toThrow('already has');
  });

  it('normalizes typed nested launch values and preserves activity tokens', () => {
    const schema = {
      type: 'object',
      properties: {
        entries: { type: 'array', items: { type: 'object', properties: { amount: { type: 'integer' } } } },
        token: { type: 'number' },
      },
    };
    expect(
      stringifyJSON(
        normalizeForm(schema, { entries: [{ amount: '9007199254740993' }], token: '{input:amount}' }, true),
      ),
    ).toBe('{"entries":[{"amount":9007199254740993}],"token":"{input:amount}"}');
    expect(() => normalizeForm(schema, { entries: [{ amount: '1.5' }] })).toThrow();
  });
});

describe('node ports', () => {
  const task = { key: 'task', kind: 'flow', description: '', ports: [], ports_from: 'outcomes' as const, input_schema: { type: 'object' } };
  it('follows a task node outcomes, done by default and none without waiting', () => {
    expect(nodePorts(task, { outcomes: [{ key: 'correct' }, { key: 'fix' }] })).toEqual(['correct', 'fix']);
    expect(nodePorts(task, {})).toEqual(['done']);
    expect(nodePorts(task, { wait: false, outcomes: [{ key: 'x' }] })).toEqual([]);
    expect(nodePorts({ ...task, ports_from: undefined, ports: ['true', 'false'] }, {})).toEqual(['true', 'false']);
  });
});
