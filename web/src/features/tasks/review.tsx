import { useQuery } from "@tanstack/react-query";
import { authorizationHeaders } from "@/api/auth";
import { downloadFile } from "@/api/files";
import { stagedFileQuery } from "@/api/queries";
import type { StagedFile, Task } from "@/api/types";
import { Alert, Skeleton } from "@/components/ui/feedback";
import { FileView } from "@/features/files/file-view";
import { useTranslation } from "@/i18n";
import type { FileRef } from "@/features/files/handlers";
import { useFormat } from "@/lib/preferences";
import { useBlobUrl } from "@/lib/use-blob-url";
import { problemMessage } from "@/lib/errors";

/** The staged file a task asks to review, rendered by its review type. */
export function Review({ task }: { task: Task }) {
  const { t } = useTranslation("tasks");
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
    <section className="grid gap-3" aria-label={t("review.label")}>
      {file.state !== "pending" && (
        <Alert tone="warning">{t(`review.${file.state}`)}</Alert>
      )}
      {file.state === "pending" && !file.fresh && (
        <Alert tone="warning">{t("review.stale")}</Alert>
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
  const { t } = useTranslation("tasks");
  const source = file.sources.find((s) => s.item_id === file.item_id);
  return (
    <div className="grid gap-3 lg:grid-cols-2">
      <Pane
        title={t("review.currentFile")}
        alt={t("review.currentFile")}
        path={`/v1/items/${file.item_id}/content${source ? `?blob_id=${source.blob_id}` : ""}`}
      />
      <Pane
        title={t("review.proposed", { filename: file.filename })}
        alt={t("review.proposedFile")}
        path={`/v1/staged-files/${file.id}/content`}
      />
    </div>
  );
}

/** A staged document, such as a PDF composed from photos, shown by the viewer's handler for its type. */
function Preview({ file }: { file: StagedFile }) {
  const path = `/v1/staged-files/${file.id}/content`;
  const staged: FileRef = { itemId: file.item_id, blobId: file.id, contentType: file.content_type, filename: file.filename, size: file.size, version: 0, earlier: true, detached: true };
  return (
    <FileView
      file={staged}
      source={{ url: path, headers: authorizationHeaders() }}
      onDownload={() => void downloadFile(path, file.filename)}
      className="h-[70vh]"
    />
  );
}

const metricKeys = [
  "source_width",
  "source_height",
  "width",
  "height",
  "source_bytes",
  "bytes",
  "savings_percent",
  "smallest_text_height",
  "scale",
  "pages",
  "text_regions",
] as const;
type MetricKey = (typeof metricKeys)[number];
const isMetric = (key: string): key is MetricKey => (metricKeys as readonly string[]).includes(key);

function Metrics({ metrics }: { metrics: Record<string, unknown> }) {
  const { t } = useTranslation("tasks");
  const format = useFormat();
  const entries = Object.entries(metrics ?? {}).filter(
    (entry): entry is [MetricKey, unknown] => isMetric(entry[0]) && entry[1] != null,
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
          <dt className="text-xs text-muted">{t(`review.metrics.${key}`)}</dt>
          <dd className="truncate">{show(key, value)}</dd>
        </div>
      ))}
    </dl>
  );
}
