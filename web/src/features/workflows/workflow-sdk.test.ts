import ts from 'typescript';
import { expect, it } from 'vitest';
import declarations from '@sdk/workflow.d.ts?raw';

// Monaco uses this declaration file with TypeScript's language service.
// Exercise completion and checking against that same artifact, without DOM mocks.
function languageService(source: string) {
  const files = new Map([
    ['/workflow.ts', source],
    ['/node_modules/@papergo/workflow/index.d.ts', declarations],
  ]);
  const options: ts.CompilerOptions = {
    strict: true, noEmit: true, target: ts.ScriptTarget.ES2020,
    lib: ['lib.es2022.d.ts'], module: ts.ModuleKind.ESNext,
    moduleResolution: ts.ModuleResolutionKind.Node10,
  };
  const readFile = (file: string) => files.get(file) ?? ts.sys.readFile(file);
  return ts.createLanguageService({
    getCompilationSettings: () => options,
    getScriptFileNames: () => [...files.keys()],
    getScriptVersion: () => '1',
    getScriptSnapshot: (file) => {
      const text = readFile(file);
      return text === undefined ? undefined : ts.ScriptSnapshot.fromString(text);
    },
    getCurrentDirectory: () => '/',
    getDefaultLibFileName: (settings) => ts.getDefaultLibFilePath(settings),
    fileExists: (file) => files.has(file) || ts.sys.fileExists(file),
    directoryExists: (dir) => [...files.keys()].some((file) => file.startsWith(`${dir}/`)) || ts.sys.directoryExists(dir),
    readFile,
  });
}

it('completes typed trigger snapshots in the editor SDK and narrows their fields', () => {
  const source = `import { workflow } from '@papergo/workflow';
export default workflow({ run(ctx) {
  if (ctx.trigger?.kind === 'file') {
    return { name: ctx.trigger.file.filename, item: ctx.trigger.item.name };
  }
  if (ctx.trigger?.kind === 'folder') return ctx.trigger.folder.name;
  if (ctx.trigger?.kind === 'item') return ctx.trigger.item.values?.title;
} });`;
  const service = languageService(source);
  try {
    expect(service.getSemanticDiagnostics('/workflow.ts').map((d) => ts.flattenDiagnosticMessageText(d.messageText, '\n'))).toEqual([]);
    const complete = (needle: string) => service.getCompletionsAtPosition('/workflow.ts', source.indexOf(needle) + needle.length, {})?.entries.map((entry) => entry.name);
    expect(complete('ctx.')).toContain('trigger');
    expect(complete('ctx.trigger.file.')).toEqual(expect.arrayContaining(['id', 'version', 'filename', 'content_type', 'size']));
    expect(complete('ctx.trigger.item.')).toEqual(expect.arrayContaining(['revision_id', 'name', 'values', 'values_omitted']));
    expect(complete('ctx.trigger.folder.')).toContain('name');
  } finally {
    service.dispose();
  }
});

it('completes typed manual form results, primary item and selected snapshots', () => {
  const source = `import { workflow } from '@papergo/workflow';
export default workflow<{ message: string; copies: number }>({ run(ctx, input) {
  if (ctx.trigger?.kind !== 'manual') return;
  const submitted: string = ctx.trigger.input.message;
  const original: number = input.copies;
  const primary = ctx.trigger.item?.name;
  return ctx.trigger.items.map(selected => ({ id: selected.id, name: selected.snapshot?.name, submitted, original, primary }));
} });`;
  const service = languageService(source);
  try {
    expect(service.getSemanticDiagnostics('/workflow.ts').map((d) => ts.flattenDiagnosticMessageText(d.messageText, '\n'))).toEqual([]);
    const complete = (needle: string) => service.getCompletionsAtPosition('/workflow.ts', source.indexOf(needle) + needle.length, {})?.entries.map((entry) => entry.name);
    expect(complete('ctx.trigger.')).toEqual(expect.arrayContaining(['kind', 'input', 'item', 'items']));
    expect(complete('ctx.trigger.input.')).toEqual(expect.arrayContaining(['message', 'copies']));
    expect(complete('selected.')).toEqual(expect.arrayContaining(['id', 'snapshot']));
    expect(complete('selected.snapshot?.')).toContain('name');
  } finally {
    service.dispose();
  }
});
