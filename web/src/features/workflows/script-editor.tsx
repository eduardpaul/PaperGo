import { Textarea } from "@/components/ui/input";

/** Edits a workflow script. */
export function ScriptEditor({ id, value, onChange, readOnly }: { id: string; value: string; onChange: (value: string) => void; readOnly?: boolean }) {
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
