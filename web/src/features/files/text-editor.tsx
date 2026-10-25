import type * as Api from 'monaco-editor/editor/editor.api';
import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { cn } from '@/lib/utils';

type Monaco = typeof Api;

/** Monaco is poor on touch screens; phones and tablets edit in a text area. */
function touch() {
  return typeof window !== 'undefined' && window.matchMedia?.('(pointer: coarse)').matches;
}

/**
 * Edits text in Monaco, with Markdown highlighting for Markdown files, or in
 * a plain text area on touch screens and while Monaco loads. The editor starts
 * from value and reports each change; Ctrl or ⌘ S saves. Monaco keeps its own
 * text: value replaces it only when revision changes (and differs), so keys typed while
 * React renders an earlier change are never undone.
 */
export function TextEditor({ id, value, revision, language, label, onChange, onSave, className }: { id: string; value: string; revision: number | string; language: 'markdown' | 'plaintext'; label: string; onChange: (value: string) => void; onSave: () => void; className?: string }) {
  const [monaco, setMonaco] = useState<Monaco>();
  const host = useRef<HTMLDivElement>(null);
  const editor = useRef<Api.editor.IStandaloneCodeEditor>(undefined);
  const latest = useRef({ value, onChange, onSave });
  useLayoutEffect(() => {
    latest.current = { value, onChange, onSave };
  });

  useEffect(() => {
    if (touch()) return;
    let live = true;
    import('./monaco-text').then((module) => live && setMonaco(module.monaco), () => undefined);
    return () => {
      live = false;
    };
  }, []);

  useEffect(() => {
    if (!monaco || !host.current) return;
    const model = monaco.editor.createModel(latest.current.value, language);
    const created = monaco.editor.create(host.current, {
      model,
      ariaLabel: label,
      automaticLayout: true,
      fixedOverflowWidgets: true,
      fontSize: 13,
      lineNumbers: language === 'plaintext' ? 'on' : 'off',
      minimap: { enabled: false },
      scrollBeyondLastLine: false,
      wordWrap: 'on',
      tabSize: 2,
      padding: { top: 12, bottom: 12 },
      theme: dark() ? 'vs-dark' : 'vs',
    });
    editor.current = created;
    created.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS, () => latest.current.onSave());
    created.focus();
    const changes = created.onDidChangeModelContent(() => latest.current.onChange(created.getValue()));
    const theme = new MutationObserver(() => monaco.editor.setTheme(dark() ? 'vs-dark' : 'vs'));
    theme.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] });
    return () => {
      theme.disconnect();
      changes.dispose();
      created.dispose();
      model.dispose();
      editor.current = undefined;
    };
  }, [monaco, language, label]);

  // A text from outside, such as their version after a conflict, replaces the editor's.
  useEffect(() => {
    const current = editor.current;
    if (current && current.getValue() !== latest.current.value) current.setValue(latest.current.value);
  }, [revision]);

  if (!monaco) {
    return (
      <textarea
        id={id}
        aria-label={label}
        spellCheck={language === 'markdown'}
        className={cn('h-full w-full resize-none bg-surface p-4 font-mono text-[13px] leading-relaxed outline-none', className)}
        value={value}
        onChange={(event) => onChange(event.target.value)}
        onKeyDown={(event) => {
          if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 's') {
            event.preventDefault();
            onSave();
          }
        }}
      />
    );
  }
  return <div id={id} ref={host} className={cn('h-full w-full overflow-hidden', className)} />;
}

function dark() {
  return document.documentElement.classList.contains('dark');
}
