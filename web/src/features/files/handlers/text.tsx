import { WrapText } from 'lucide-react';
import { useState } from 'react';
import { Button } from '@/components/ui/button';
import type { ViewerProps } from '../handlers';
import { TextDocument } from '../text-document';

const wrapKey = 'papergo.viewer.wrap';

/** Plain text with line numbers, or wrapped to the window without them. */
function Lines({ text }: { text: string }) {
  const [wrap, setWrap] = useState(() => {
    try {
      return localStorage.getItem(wrapKey) === 'on';
    } catch {
      return false;
    }
  });
  const lines = text.split('\n');
  if (lines.at(-1) === '') lines.pop();
  return (
    <div className="flex h-full flex-col">
      <div className="flex shrink-0 justify-end border-b px-2 py-1">
        <Button
          size="sm"
          variant={wrap ? 'secondary' : 'ghost'}
          aria-pressed={wrap}
          onClick={() => {
            setWrap(!wrap);
            try {
              localStorage.setItem(wrapKey, wrap ? 'off' : 'on');
            } catch {
              /* The choice holds for this visit. */
            }
          }}
        >
          <WrapText /> Wrap lines
        </Button>
      </div>
      <div className="flex min-h-0 flex-1 overflow-auto font-mono text-[13px] leading-6">
        {!wrap && (
          <pre aria-hidden className="sticky left-0 shrink-0 border-r bg-surface-muted px-3 py-3 text-right text-muted select-none">
            {lines.map((_line, index) => index + 1).join('\n')}
          </pre>
        )}
        <pre className={wrap ? 'min-w-0 flex-1 px-4 py-3 break-words whitespace-pre-wrap' : 'px-4 py-3'}>{text}</pre>
      </div>
    </div>
  );
}

export default function TextView(props: ViewerProps) {
  return <TextDocument {...props} language="plaintext" render={(text) => <Lines text={text} />} />;
}
