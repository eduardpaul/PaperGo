// Monaco with TypeScript checking against the workflow SDK. The script
// editor imports this module lazily, so the editor and its workers load
// only when a script is open.
import * as monaco from 'monaco-editor/editor/editor.api';
import { ModuleKind, ModuleResolutionKind, ScriptTarget, typescriptDefaults } from 'monaco-editor/languages/features/typescript/register';
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

// Scripts run on ECMAScript and the runtime's globals only, with the SDK
// as @papergo/workflow.
typescriptDefaults.setCompilerOptions({
  target: ScriptTarget.ES2020,
  lib: ['es2022'],
  module: ModuleKind.ESNext,
  moduleResolution: ModuleResolutionKind.NodeJs,
  strict: true,
  noEmit: true,
  allowNonTsExtensions: true,
});
typescriptDefaults.setEagerModelSync(true);
typescriptDefaults.addExtraLib(declarations, 'file:///node_modules/@papergo/workflow/index.d.ts');

export { monaco };
