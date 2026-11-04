import type * as Api from "monaco-editor/editor/editor.api";
import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { Textarea } from "@/components/ui/input";
import { useTranslation } from "@/i18n";

type Monaco = typeof Api;

/**
 * Edits a workflow script, or an app's TSX source, in Monaco, which
 * completes and checks it against the workflow SDK or @papergo/app as
 * TypeScript. A plain text area stands in while Monaco loads, and stays if
 * it cannot. Monaco starts from value and then owns the text, reporting each
 * change: replacing the script means a new key.
 */
export function ScriptEditor({ id, value, onChange, readOnly, kind = "workflow" }: { id: string; value: string; onChange: (value: string) => void; readOnly?: boolean; kind?: "workflow" | "app" }) {
  const { t } = useTranslation("workflows");
  const [monaco, setMonaco] = useState<Monaco>();
  const host = useRef<HTMLDivElement>(null);
  const editor = useRef<Api.editor.IStandaloneCodeEditor>(undefined);
  // Changes can arrive before a render passes them back, so value is read
  // only when Monaco starts.
  const latest = useRef({ value, onChange });
  useLayoutEffect(() => {
    latest.current = { value, onChange };
  });

  useEffect(() => {
    let live = true;
    import("./monaco")
      .then(async (module) => {
        if (kind === "app") await module.addAppTypes();
        module.checkAs(kind);
        if (live) setMonaco(module.monaco);
      })
      .catch(() => undefined);
    return () => {
      live = false;
    };
  }, [kind]);

  useEffect(() => {
    if (!monaco || !host.current) return;
    const model = monaco.editor.createModel(latest.current.value, "typescript", monaco.Uri.parse(`file:///${id}.${kind === "app" ? "tsx" : "ts"}`));
    const created = monaco.editor.create(host.current, {
      model,
      automaticLayout: true,
      fixedOverflowWidgets: true,
      fontSize: 12,
      minimap: { enabled: false },
      scrollBeyondLastLine: false,
      tabSize: 2,
      theme: dark() ? "vs-dark" : "vs",
    });
    editor.current = created;
    const changes = created.onDidChangeModelContent(() => latest.current.onChange(created.getValue()));
    // The app's theme toggles the dark class on the root element.
    const theme = new MutationObserver(() => monaco.editor.setTheme(dark() ? "vs-dark" : "vs"));
    theme.observe(document.documentElement, { attributes: true, attributeFilter: ["class"] });
    return () => {
      theme.disconnect();
      changes.dispose();
      created.dispose();
      model.dispose();
      editor.current = undefined;
    };
  }, [monaco, id, kind]);

  useEffect(() => {
    editor.current?.updateOptions({ readOnly });
  }, [readOnly, monaco]);

  useEffect(() => {
    editor.current?.updateOptions({ ariaLabel: t("editor.script") });
  }, [t, monaco, id]);

  if (!monaco) {
    return (
      <Textarea
        id={id}
        required
        spellCheck={false}
        readOnly={readOnly}
        className="min-h-96 font-mono text-xs"
        value={value}
        onChange={(event) => onChange(event.target.value)}
      />
    );
  }
  return <div id={id} ref={host} className="h-[32rem] overflow-hidden rounded border" />;
}

function dark() {
  return document.documentElement.classList.contains("dark");
}
