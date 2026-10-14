import { useQuery } from "@tanstack/react-query";
import { useEffect, useMemo } from "react";
import { fetchBlob } from "@/api/files";
import { stagedFileQuery } from "@/api/queries";
import type { StagedFile, Task } from "@/api/types";
import { Alert, Skeleton } from "@/components/ui/feedback";
import { useFormat } from "@/lib/preferences";
import { problemMessage } from "@/lib/errors";

/** An object URL of an authenticated file, revoked when it is no longer shown. */
export function useBlobUrl(path: string | undefined) {
  const blob = useQuery({
    queryKey: ["blob", path],
    queryFn: () => fetchBlob(path!),
    enabled: !!path,
    staleTime: Infinity,
  });
  const url = useMemo(
    () => (blob.data ? URL.createObjectURL(blob.data) : undefined),
    [blob.data],
  );
  useEffect(
    () => () => {
      if (url) URL.revokeObjectURL(url);
    },
    [url],
  );
  return { url, error: blob.error, loading: blob.isLoading };
}

/** The staged file a task asks to review, rendered by its review type. */
export function Review({ task }: { task: Task }) {
  const review = task.review;
  const staged = useQuery({
    ...stagedFileQuery(review?.staged_file_id ?? ""),
    enabled: !!review,
  });
  if (!review) return null;
  if (staged.error) return <Alert>{problemMessage(staged.error)}</Alert>;
  if (!staged.data) return <Skeleton className="h-64" />;
  const file = staged.data;
  return (
    <section className="grid gap-3" aria-label="Review">
      {file.state !== "pending" && (
        <Alert tone="warning">This file was {file.state}.</Alert>
      )}
      {file.state === "pending" && !file.fresh && (
        <Alert tone="warning">
          A file this was made from changed after the review was requested. It
          can no longer be accepted; only a rejection is possible.
        </Alert>
      )}
      {review.type === "file.compare" ? (
        <Compare file={file} />
      ) : (
        <Preview file={file} />
      )}
      <Metrics metrics={file.metrics} />
    </section>
  );
}

function Pane({
  title,
  path,
  alt,
}: {
  title: string;
  path: string;
  alt: string;
}) {
  const { url, error } = useBlobUrl(path);
  return (
    <figure className="grid min-w-0 gap-1">
      <figcaption className="text-xs font-medium text-muted">
        {title}
      </figcaption>
      {error ? (
        <Alert>{problemMessage(error)}</Alert>
      ) : !url ? (
        <Skeleton className="h-64" />
      ) : (
        // Native size, one image pixel per CSS pixel, so small text is judged as stored.
        <div className="max-h-[70vh] overflow-auto rounded-md border bg-surface-muted">
          <img
            src={url}
            alt={alt}
            className="max-w-none"
            style={{ imageRendering: "auto" }}
          />
        </div>
      )}
    </figure>
  );
}

/** The item's current file beside the staged image, both at 100%. */
function Compare({ file }: { file: StagedFile }) {
  const source = file.sources.find((s) => s.item_id === file.item_id);
  return (
    <div className="grid gap-3 lg:grid-cols-2">
      <Pane
        title="Current file"
        alt="Current file"
        path={`/v1/items/${file.item_id}/content${source ? `?blob_id=${source.blob_id}` : ""}`}
      />
      <Pane
        title={`Proposed: ${file.filename}`}
        alt="Proposed file"
        path={`/v1/staged-files/${file.id}/content`}
      />
    </div>
  );
}

/** A staged document, such as a PDF composed from photos. */
function Preview({ file }: { file: StagedFile }) {
  const { url, error } = useBlobUrl(`/v1/staged-files/${file.id}/content`);
  if (error) return <Alert>{problemMessage(error)}</Alert>;
  if (!url) return <Skeleton className="h-[70vh]" />;
  if (file.content_type.startsWith("image/")) {
    return (
      <img
        src={url}
        alt={file.filename}
        className="max-w-full rounded-md border"
      />
    );
  }
  return (
    <iframe
      title={file.filename}
      src={url}
      className="h-[70vh] w-full rounded-md border"
    />
  );
}

const labels: Record<string, string> = {
  source_width: "Original width",
  source_height: "Original height",
  width: "Width",
  height: "Height",
  source_bytes: "Original size",
  bytes: "Size",
  savings_percent: "Savings",
  smallest_text_height: "Smallest text height",
  scale: "Scale",
  pages: "Pages",
  text_regions: "Text regions",
};

function Metrics({ metrics }: { metrics: Record<string, unknown> }) {
  const format = useFormat();
  const entries = Object.entries(metrics ?? {}).filter(
    ([key, value]) => labels[key] && value != null,
  );
  if (entries.length === 0) return null;
  const show = (key: string, value: unknown) => {
    if (typeof value !== "number") return String(value);
    if (key.endsWith("bytes")) return format.fileSize(value);
    if (key === "savings_percent")
      return `${format.number(value, { maximumFractionDigits: 1 })} %`;
    if (key === "scale")
      return format.number(value, { maximumFractionDigits: 3 });
    if (key.endsWith("height") || key.endsWith("width"))
      return `${format.number(value, { maximumFractionDigits: 1 })} px`;
    return format.number(value);
  };
  return (
    <dl className="grid grid-cols-2 gap-x-4 gap-y-1 text-[13px] sm:grid-cols-4">
      {entries.map(([key, value]) => (
        <div key={key} className="min-w-0">
          <dt className="text-xs text-muted">{labels[key]}</dt>
          <dd className="truncate">{show(key, value)}</dd>
        </div>
      ))}
    </dl>
  );
}
