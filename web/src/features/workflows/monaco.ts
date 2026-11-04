// Monaco with TypeScript checking against the workflow SDK and, for apps,
// @papergo/app with React's types. The script editor imports this module
// lazily, so the editor and its workers load only when a script is open.
import * as monaco from 'monaco-editor/editor/editor.api';
import { JsxEmit, ModuleKind, ModuleResolutionKind, ScriptTarget, typescriptDefaults } from 'monaco-editor/languages/features/typescript/register';
import 'monaco-editor/languages/definitions/typescript/register';
// The editor's features that suit scripts; editor.main would add every
// language and feature Monaco has.
import 'monaco-editor/editor/browser/coreCommands';
import 'monaco-editor/editor/common/standaloneStrings';
import 'monaco-editor/editor/standalone/browser/referenceSearch/standaloneReferenceSearch';
import 'monaco-editor/editor/contrib/anchorSelect/browser/anchorSelect';
import 'monaco-editor/editor/contrib/bracketMatching/browser/bracketMatching';
import 'monaco-editor/editor/contrib/caretOperations/browser/caretOperations';
import 'monaco-editor/editor/contrib/caretOperations/browser/transpose';
import 'monaco-editor/editor/contrib/clipboard/browser/clipboard';
import 'monaco-editor/editor/contrib/codeAction/browser/codeActionContributions';
import 'monaco-editor/editor/contrib/comment/browser/comment';
import 'monaco-editor/editor/contrib/contextmenu/browser/contextmenu';
import 'monaco-editor/editor/contrib/cursorUndo/browser/cursorUndo';
import 'monaco-editor/editor/contrib/find/browser/findController';
import 'monaco-editor/editor/contrib/folding/browser/folding';
import 'monaco-editor/editor/contrib/format/browser/formatActions';
import 'monaco-editor/editor/contrib/gotoError/browser/gotoError';
import 'monaco-editor/editor/contrib/gotoSymbol/browser/goToCommands';
import 'monaco-editor/editor/contrib/gotoSymbol/browser/link/goToDefinitionAtPosition';
import 'monaco-editor/editor/contrib/hover/browser/hoverContribution';
import 'monaco-editor/editor/contrib/indentation/browser/indentation';
import 'monaco-editor/editor/contrib/inlayHints/browser/inlayHintsContribution';
import 'monaco-editor/editor/contrib/linesOperations/browser/linesOperations';
import 'monaco-editor/editor/contrib/links/browser/links';
import 'monaco-editor/editor/contrib/multicursor/browser/multicursor';
import 'monaco-editor/editor/contrib/parameterHints/browser/parameterHints';
import 'monaco-editor/editor/contrib/readOnlyMessage/browser/contribution';
import 'monaco-editor/editor/contrib/rename/browser/rename';
import 'monaco-editor/editor/contrib/smartSelect/browser/smartSelect';
import 'monaco-editor/editor/contrib/snippet/browser/snippetController2';
import 'monaco-editor/editor/contrib/suggest/browser/suggestController';
import 'monaco-editor/editor/contrib/wordHighlighter/browser/wordHighlighter';
import 'monaco-editor/editor/contrib/wordOperations/browser/wordOperations';
import EditorWorker from 'monaco-editor/editor/editor.worker?worker';
import TypeScriptWorker from 'monaco-editor/languages/features/typescript/ts.worker?worker';
import declarations from '@sdk/workflow.d.ts?raw';

self.MonacoEnvironment = {
  getWorker: (_id, label) => (label === 'typescript' || label === 'javascript' ? new TypeScriptWorker() : new EditorWorker()),
};

// Workflow scripts run on ECMAScript and the runtime's globals only, with
// the SDK as @papergo/workflow. Apps, .tsx files, also have the DOM and
// React's automatic JSX.
typescriptDefaults.setCompilerOptions({
  target: ScriptTarget.ES2020,
  lib: ['es2022'],
  jsx: JsxEmit.ReactJSX,
  module: ModuleKind.ESNext,
  moduleResolution: ModuleResolutionKind.NodeJs,
  strict: true,
  noEmit: true,
  allowNonTsExtensions: true,
});
typescriptDefaults.setEagerModelSync(true);
typescriptDefaults.addExtraLib(declarations, 'file:///node_modules/@papergo/workflow/index.d.ts');

let appTypes: Promise<void> | undefined;

/** Adds @papergo/app, React's types and the DOM's, once, for the app editor. */
export function addAppTypes(): Promise<void> {
  appTypes ??= Promise.all([
    import('@sdk/app.d.ts?raw'),
    import('@react-types/index.d.ts?raw'),
    import('@react-types/global.d.ts?raw'),
    import('@react-types/jsx-runtime.d.ts?raw'),
    import('@csstype/index.d.ts?raw'),
  ]).then(([app, react, reactGlobal, jsxRuntime, csstype]) => {
    typescriptDefaults.addExtraLib(app.default, 'file:///node_modules/@papergo/app/index.d.ts');
    typescriptDefaults.addExtraLib(react.default, 'file:///node_modules/@types/react/index.d.ts');
    typescriptDefaults.addExtraLib(reactGlobal.default, 'file:///node_modules/@types/react/global.d.ts');
    typescriptDefaults.addExtraLib(jsxRuntime.default, 'file:///node_modules/@types/react/jsx-runtime.d.ts');
    typescriptDefaults.addExtraLib(csstype.default, 'file:///node_modules/csstype/index.d.ts');
  });
  return appTypes;
}

/**
 * Checks what is edited next as a workflow script, without the DOM, or as
 * an app, with it. Compiler options are Monaco's, not an editor's, so the
 * editor that opens sets them.
 */
export function checkAs(kind: 'workflow' | 'app'): void {
  const lib = kind === 'app' ? ['es2022', 'dom', 'dom.iterable'] : ['es2022'];
  const options = typescriptDefaults.getCompilerOptions();
  if (options.lib?.join() !== lib.join()) typescriptDefaults.setCompilerOptions({ ...options, lib });
}

export { monaco };
