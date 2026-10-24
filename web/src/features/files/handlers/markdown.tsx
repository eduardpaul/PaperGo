import { useMemo, useState, type ReactNode } from 'react';
import Markdown, { type Components } from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { cn } from '@/lib/utils';
import type { ViewerProps } from '../handlers';
import { TextDocument } from '../text-document';
import { RemoteImage, TrustedLink } from '../trust';

/** Heading ids people can link and the outline can jump to, unique within a document. */
function slugger() {
  const seen = new Map<string, number>();
  return (text: string) => {
    const base = text.toLowerCase().trim().replace(/[^\p{L}\p{N}\s-]/gu, '').replace(/\s+/g, '-') || 'section';
    const count = seen.get(base) ?? 0;
    seen.set(base, count + 1);
    return count ? `${base}-${count}` : base;
  };
}

function textOf(node: ReactNode): string {
  if (typeof node === 'string' || typeof node === 'number') return String(node);
  if (Array.isArray(node)) return node.map(textOf).join('');
  if (node && typeof node === 'object' && 'props' in node) return textOf((node.props as { children?: ReactNode }).children);
  return '';
}

/** The headings of a Markdown source, outside code fences, for the outline. */
function outline(source: string) {
  const slug = slugger();
  const headings: { depth: number; text: string; id: string }[] = [];
  let fence = false;
  for (const line of source.split('\n')) {
    if (/^\s*(```|~~~)/.test(line)) fence = !fence;
    const match = !fence && /^(#{1,3})\s+(.+?)\s*#*\s*$/.exec(line);
    if (!match) continue;
    const text = match[2].replace(/[*_`~]|\[([^\]]*)\]\([^)]*\)/g, '$1');
    headings.push({ depth: match[1].length, text, id: slug(text) });
  }
  return headings;
}

/**
 * Rendered Markdown, safe by construction: GitHub-flavoured syntax with
 * tables and task lists, raw HTML shown as text, links that follow the
 * organization's trusted hosts, and remote images that load only from
 * trusted hosts or when asked.
 */
function Rendered({ source, className }: { source: string; className?: string }) {
  const [images, setImages] = useState(false);
  const components = useMemo((): Components => {
    const slug = slugger();
    const heading = (Tag: 'h1' | 'h2' | 'h3') =>
      function Heading({ children }: { children?: ReactNode }) {
        return <Tag id={slug(textOf(children))}>{children}</Tag>;
      };
    return {
      a: ({ href, children }) => <TrustedLink href={href}>{children}</TrustedLink>,
      img: ({ src, alt }) => <RemoteImage src={typeof src === 'string' ? src : undefined} alt={alt} shown={images} onShow={setImages} />,
      h1: heading('h1'),
      h2: heading('h2'),
      h3: heading('h3'),
    };
    // A new render numbers headings from the start again.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [images, source]);
  return (
    <div className={cn('markdown mx-auto max-w-[72ch] px-6 py-8', className)}>
      <Markdown remarkPlugins={[remarkGfm]} skipHtml={false} components={components}>
        {source}
      </Markdown>
    </div>
  );
}

function Reader({ source }: { source: string }) {
  const headings = useMemo(() => outline(source), [source]);
  return (
    <div className="flex min-h-full">
      <Rendered source={source} className="min-w-0 flex-1" />
      {headings.length > 2 && (
        <nav aria-label="Outline" className="sticky top-0 hidden max-h-full w-60 shrink-0 self-start overflow-auto border-l px-4 py-8 text-[13px] xl:block">
          <p className="mb-2 text-xs font-medium text-muted uppercase">On this page</p>
          <ul className="grid gap-1">
            {headings.map((heading) => (
              <li key={heading.id} style={{ paddingLeft: (heading.depth - 1) * 12 }}>
                <a
                  href={`#${heading.id}`}
                  className="text-muted hover:text-foreground"
                  onClick={(event) => {
                    event.preventDefault();
                    document.getElementById(heading.id)?.scrollIntoView({ behavior: 'smooth', block: 'start' });
                  }}
                >
                  {heading.text}
                </a>
              </li>
            ))}
          </ul>
        </nav>
      )}
    </div>
  );
}

export default function MarkdownView(props: ViewerProps) {
  return <TextDocument {...props} language="markdown" render={(source) => <Reader source={source} />} preview={(source) => <Rendered source={source} className="py-4" />} />;
}
