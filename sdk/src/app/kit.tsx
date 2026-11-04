// A small kit in PaperGo's theme, for apps: tiles, charts, a table, buttons
// and fields. Charts are plain SVG, since apps load nothing from the network.

import { useId, type ReactNode } from "react";

/** The kit's stylesheet; the runtime adds it to the app's document. */
export const styles = `
:root { color-scheme: light; --pg-bg: #ffffff; --pg-surface: #f7f7f8; --pg-border: #e4e4e7; --pg-text: #18181b; --pg-muted: #71717a;
  --pg-primary: #2563eb; --pg-primary-text: #ffffff; --pg-danger: #dc2626; --pg-warning: #d97706; --pg-success: #16a34a;
  --pg-series-1: #2563eb; --pg-series-2: #d97706; --pg-series-3: #16a34a; --pg-radius: 10px;
  --pg-band-warm: rgba(220, 38, 38, .10); --pg-band-cool: rgba(37, 99, 235, .08); --pg-band-neutral: rgba(113, 113, 122, .08); }
:root[data-theme="dark"] { color-scheme: dark; --pg-bg: #09090b; --pg-surface: #18181b; --pg-border: #27272a; --pg-text: #fafafa; --pg-muted: #a1a1aa;
  --pg-primary: #3b82f6; --pg-series-1: #60a5fa; --pg-series-2: #fbbf24; --pg-series-3: #4ade80; }
* { box-sizing: border-box; }
html, body { margin: 0; background: var(--pg-bg); color: var(--pg-text); font: 14px/1.45 system-ui, -apple-system, "Segoe UI", Roboto, sans-serif; }
body { padding: 16px; }
h1, h2, h3 { margin: 0 0 8px; line-height: 1.2; }
h1 { font-size: 20px; } h2 { font-size: 16px; } h3 { font-size: 14px; }
.pg-card { background: var(--pg-surface); border: 1px solid var(--pg-border); border-radius: var(--pg-radius); padding: 14px 16px; min-width: 0; }
.pg-card > .pg-card-head { display: flex; align-items: center; justify-content: space-between; gap: 8px; flex-wrap: wrap; margin-bottom: 8px; }
.pg-card-title { font-weight: 600; }
.pg-legend { display: flex; gap: 6px; flex-wrap: wrap; }
.pg-pill { display: inline-flex; align-items: center; gap: 6px; font-size: 12px; color: var(--pg-muted); border: 1px solid var(--pg-border); border-radius: 999px; padding: 2px 10px; }
.pg-pill-dot { width: 10px; height: 10px; border-radius: 3px; }
.pg-toolbar { display: flex; gap: 8px; flex-wrap: wrap; justify-content: flex-end; margin-bottom: 12px; }
.pg-modal-back { position: fixed; inset: 0; background: rgba(0, 0, 0, .35); display: flex; align-items: flex-start; justify-content: center; padding: 48px 16px; z-index: 10; }
.pg-modal { background: var(--pg-bg); color: var(--pg-text); border: 1px solid var(--pg-border); border-radius: var(--pg-radius); width: 100%; max-width: 440px; padding: 16px 18px; box-shadow: 0 10px 40px rgba(0, 0, 0, .25); }
.pg-modal-head { display: flex; justify-content: space-between; align-items: center; margin-bottom: 12px; }
.pg-modal-actions { display: flex; justify-content: flex-end; gap: 8px; margin-top: 12px; }
.pg-close { background: none; border: 0; font-size: 20px; line-height: 1; cursor: pointer; color: var(--pg-muted); }
.pg-field-hint { color: var(--pg-success); font-size: 13px; }
.pg-grid { display: grid; gap: 12px; grid-template-columns: repeat(auto-fit, minmax(var(--pg-min, 180px), 1fr)); margin-bottom: 12px; }
.pg-stat-label { color: var(--pg-muted); font-size: 12px; text-transform: uppercase; letter-spacing: .04em; }
.pg-stat-value { font-size: 26px; font-weight: 650; margin-top: 2px; font-variant-numeric: tabular-nums; }
.pg-stat-hint { color: var(--pg-muted); font-size: 12px; margin-top: 2px; }
.pg-tone-warning .pg-stat-value { color: var(--pg-warning); } .pg-tone-danger .pg-stat-value { color: var(--pg-danger); } .pg-tone-success .pg-stat-value { color: var(--pg-success); }
.pg-button { font: inherit; border-radius: 8px; border: 1px solid var(--pg-border); background: var(--pg-bg); color: var(--pg-text); padding: 8px 14px; cursor: pointer; min-height: 40px; }
.pg-button:disabled { opacity: .55; cursor: default; }
.pg-button-primary { background: var(--pg-primary); border-color: var(--pg-primary); color: var(--pg-primary-text); }
.pg-button-danger { color: var(--pg-danger); }
.pg-field { display: flex; flex-direction: column; gap: 4px; margin-bottom: 10px; }
.pg-field label { font-size: 12px; color: var(--pg-muted); }
.pg-field-row { display: flex; align-items: center; gap: 8px; }
.pg-field input { font: inherit; font-size: 18px; padding: 8px 10px; border-radius: 8px; border: 1px solid var(--pg-border); background: var(--pg-bg); color: var(--pg-text); width: 100%; min-height: 44px; }
.pg-field-unit { color: var(--pg-muted); }
.pg-field-error { color: var(--pg-danger); font-size: 12px; }
.pg-notice { border-radius: var(--pg-radius); padding: 10px 14px; border: 1px solid var(--pg-border); display: flex; gap: 12px; align-items: center; justify-content: space-between; margin-bottom: 12px; }
.pg-notice-warning { border-color: var(--pg-warning); } .pg-notice-danger { border-color: var(--pg-danger); } .pg-notice-success { border-color: var(--pg-success); }
.pg-notice-title { font-weight: 600; }
.pg-table { width: 100%; border-collapse: collapse; font-variant-numeric: tabular-nums; }
.pg-table th, .pg-table td { text-align: left; padding: 6px 8px; border-bottom: 1px solid var(--pg-border); }
.pg-table th { color: var(--pg-muted); font-weight: 500; font-size: 12px; }
.pg-table .pg-right { text-align: right; }
.pg-table tr.pg-clickable { cursor: pointer; } .pg-table tr.pg-clickable:hover td { background: var(--pg-bg); }
.pg-chart { width: 100%; height: auto; display: block; }
.pg-chart text { fill: var(--pg-muted); font-size: 11px; }
.pg-chart .pg-axis { stroke: var(--pg-border); }
.pg-empty { color: var(--pg-muted); padding: 24px 0; text-align: center; }
.pg-error { color: var(--pg-danger); white-space: pre-wrap; }
`;

type Tone = "default" | "warning" | "danger" | "success";

/** A surface with an optional title, and actions or a legend on its right. */
export function Card({ title, actions, children }: { title?: ReactNode; actions?: ReactNode; children?: ReactNode }) {
  return (
    <section className="pg-card">
      {(title !== undefined || actions !== undefined) && (
        <div className="pg-card-head">
          <div className="pg-card-title">{title}</div>
          {actions}
        </div>
      )}
      {children}
    </section>
  );
}

/** Coloured pills that name what a chart's colours mean. */
export function Legend({ items }: { items: { label: ReactNode; color: string }[] }) {
  return (
    <div className="pg-legend">
      {items.map((item, i) => (
        <span key={i} className="pg-pill">
          <span className="pg-pill-dot" style={{ background: item.color }} />
          {item.label}
        </span>
      ))}
    </div>
  );
}

/** Buttons on the right, above what follows. */
export function Toolbar({ children }: { children?: ReactNode }) {
  return <div className="pg-toolbar">{children}</div>;
}

/** A dialog over the app; Escape and the backdrop close it. */
export function Modal({ open, title, onClose, children, actions }: { open: boolean; title: ReactNode; onClose: () => void; children?: ReactNode; actions?: ReactNode }) {
  if (!open) return null;
  return (
    <div className="pg-modal-back" onClick={onClose} onKeyDown={(e) => e.key === "Escape" && onClose()}>
      <div className="pg-modal" role="dialog" aria-modal="true" onClick={(e) => e.stopPropagation()}>
        <div className="pg-modal-head">
          <h2 style={{ margin: 0 }}>{title}</h2>
          <button type="button" className="pg-close" aria-label="Close" onClick={onClose}>
            ×
          </button>
        </div>
        {children}
        {actions !== undefined && <div className="pg-modal-actions">{actions}</div>}
      </div>
    </div>
  );
}

/** Columns that wrap on narrow screens; min is a column's narrowest width in pixels. */
export function Grid({ min = 180, children }: { min?: number; children?: ReactNode }) {
  return (
    <div className="pg-grid" style={{ ["--pg-min" as string]: `${min}px` }}>
      {children}
    </div>
  );
}

/** A figure with its label and an optional hint below it. */
export function Stat({ label, value, hint, tone = "default" }: { label: ReactNode; value: ReactNode; hint?: ReactNode; tone?: Tone }) {
  return (
    <div className={`pg-card pg-tone-${tone}`}>
      <div className="pg-stat-label">{label}</div>
      <div className="pg-stat-value">{value}</div>
      {hint !== undefined && <div className="pg-stat-hint">{hint}</div>}
    </div>
  );
}

export function Button({ children, onClick, variant = "default", disabled, type = "button" }: { children?: ReactNode; onClick?: () => void; variant?: "default" | "primary" | "danger"; disabled?: boolean; type?: "button" | "submit" }) {
  return (
    <button type={type} className={`pg-button pg-button-${variant}`} onClick={onClick} disabled={disabled}>
      {children}
    </button>
  );
}

/** A message with an optional action, such as an open task. */
export function Notice({ title, children, tone = "default", action }: { title?: ReactNode; children?: ReactNode; tone?: Tone; action?: { label: ReactNode; onClick: () => void } }) {
  return (
    <div className={`pg-notice pg-notice-${tone}`} role="status">
      <div>
        {title !== undefined && <div className="pg-notice-title">{title}</div>}
        {children}
      </div>
      {action && <Button onClick={action.onClick}>{action.label}</Button>}
    </div>
  );
}

interface FieldProps {
  label: ReactNode;
  value: string;
  onChange: (value: string) => void;
  error?: ReactNode;
  required?: boolean;
}

function Field({ label, error, children }: { label: ReactNode; error?: ReactNode; children: (id: string) => ReactNode }) {
  const id = useId();
  return (
    <div className="pg-field">
      <label htmlFor={id}>{label}</label>
      {children(id)}
      {error !== undefined && error !== null && error !== false && <div className="pg-field-error">{error}</div>}
    </div>
  );
}

/**
 * A decimal typed as text, with a phone's numeric keyboard; read it with
 * ctx.format.parseDecimal, which knows the viewer's decimal separator.
 */
export function NumberField({ label, value, onChange, unit, error, required, hint, autoFocus }: FieldProps & { unit?: ReactNode; hint?: ReactNode; autoFocus?: boolean }) {
  return (
    <Field label={label} error={error}>
      {(id) => (
        <>
          <div className="pg-field-row">
            <input id={id} type="text" inputMode="decimal" autoComplete="off" autoFocus={autoFocus} value={value} required={required} onChange={(e) => onChange(e.target.value)} />
            {unit !== undefined && <span className="pg-field-unit">{unit}</span>}
          </div>
          {hint !== undefined && hint !== null && hint !== false && <div className="pg-field-hint">{hint}</div>}
        </>
      )}
    </Field>
  );
}

export function TextField({ label, value, onChange, error, required, maxLength }: FieldProps & { maxLength?: number }) {
  return (
    <Field label={label} error={error}>
      {(id) => <input id={id} type="text" value={value} required={required} maxLength={maxLength} onChange={(e) => onChange(e.target.value)} />}
    </Field>
  );
}

function localInput(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

const pad2 = (n: number) => String(n).padStart(2, "0");

/**
 * A date; value and onChange use ISO timestamps. Today keeps the current
 * time, so entries of one day stay in the order they were made; another
 * day is noon.
 */
export function DateField({ label, value, onChange, error, required }: FieldProps) {
  const d = new Date(value);
  const local = Number.isNaN(d.getTime()) ? "" : `${d.getFullYear()}-${pad2(d.getMonth() + 1)}-${pad2(d.getDate())}`;
  return (
    <Field label={label} error={error}>
      {(id) => (
        <input
          id={id}
          type="date"
          value={local}
          required={required}
          onChange={(e) => {
            const [y, m, day] = e.target.value.split("-").map(Number);
            if (!y) return onChange("");
            const now = new Date();
            const today = now.getFullYear() === y && now.getMonth() + 1 === m && now.getDate() === day;
            onChange((today ? now : new Date(y, m - 1, day, 12, 0)).toISOString());
          }}
        />
      )}
    </Field>
  );
}

/** A date and time; value and onChange use ISO timestamps. */
export function DateTimeField({ label, value, onChange, error, required }: FieldProps) {
  return (
    <Field label={label} error={error}>
      {(id) => (
        <input
          id={id}
          type="datetime-local"
          value={value ? localInput(value) : ""}
          required={required}
          onChange={(e) => {
            const d = new Date(e.target.value);
            onChange(Number.isNaN(d.getTime()) ? "" : d.toISOString());
          }}
        />
      )}
    </Field>
  );
}

export interface Column<R> {
  key: string;
  label: ReactNode;
  align?: "left" | "right";
  render?: (row: R) => ReactNode;
}

export function Table<R extends Record<string, unknown>>({ columns, rows, onRowClick, empty = "Nothing yet" }: { columns: Column<R>[]; rows: R[]; onRowClick?: (row: R) => void; empty?: ReactNode }) {
  if (rows.length === 0) return <div className="pg-empty">{empty}</div>;
  return (
    <table className="pg-table">
      <thead>
        <tr>
          {columns.map((c) => (
            <th key={c.key} className={c.align === "right" ? "pg-right" : undefined}>
              {c.label}
            </th>
          ))}
        </tr>
      </thead>
      <tbody>
        {rows.map((row, i) => (
          <tr key={String(row.id ?? i)} className={onRowClick ? "pg-clickable" : undefined} onClick={onRowClick ? () => onRowClick(row) : undefined}>
            {columns.map((c) => (
              <td key={c.key} className={c.align === "right" ? "pg-right" : undefined}>
                {c.render ? c.render(row) : String(row[c.key] ?? "")}
              </td>
            ))}
          </tr>
        ))}
      </tbody>
    </table>
  );
}

const width = 640;
const pad = { top: 12, right: 12, bottom: 28, left: 48 };

function ticks(min: number, max: number, count = 4): number[] {
  if (min === max) return [min];
  const step = (max - min) / count;
  return Array.from({ length: count + 1 }, (_, i) => min + step * i);
}

export interface Series {
  name: string;
  points: { x: number | Date; y: number }[];
}

/** A shaded stretch of a chart's x axis, such as a season. */
export interface Band {
  from: number | Date;
  to: number | Date;
  tone: "warm" | "cool" | "neutral";
  label?: string;
}

/**
 * Lines over a numeric or time axis, with optional markers on the x axis,
 * such as purchases on a level chart, and shaded bands behind them.
 */
export function LineChart({ series, markers = [], bands = [], references = [], xTicks, height = 220, formatX = (x) => new Date(x).toLocaleDateString(), formatTick = formatX, formatY = (y) => String(Math.round(y)), yMin, yMax }: { series: Series[]; markers?: { x: number | Date; label?: string }[]; bands?: Band[]; references?: { y: number; label?: string }[]; xTicks?: (number | Date)[]; height?: number; formatX?: (x: number) => string; formatTick?: (x: number) => string; formatY?: (y: number) => string; yMin?: number; yMax?: number }) {
  const points = series.flatMap((s) => s.points.map((p) => ({ x: +p.x, y: p.y })));
  if (points.length === 0) return <div className="pg-empty">No data yet</div>;
  const xs = points.map((p) => p.x).concat(markers.map((m) => +m.x));
  const ys = points.map((p) => p.y);
  const [x0, x1] = [Math.min(...xs), Math.max(...xs)];
  const [y0, y1] = [yMin ?? Math.min(0, ...ys), yMax ?? Math.max(...ys)];
  const sx = (x: number) => pad.left + (x1 === x0 ? 0.5 : (x - x0) / (x1 - x0)) * (width - pad.left - pad.right);
  const sy = (y: number) => height - pad.bottom - (y1 === y0 ? 0.5 : (y - y0) / (y1 - y0)) * (height - pad.top - pad.bottom);
  return (
    <svg className="pg-chart" viewBox={`0 0 ${width} ${height}`} role="img">
      {bands.map((b, i) => {
        const from = sx(Math.max(+b.from, x0));
        const to = sx(Math.min(+b.to, x1));
        if (to <= from) return null;
        return (
          <rect key={i} x={from} width={to - from} y={pad.top} height={height - pad.top - pad.bottom} fill={`var(--pg-band-${b.tone})`}>
            {b.label && <title>{b.label}</title>}
          </rect>
        );
      })}
      {ticks(y0, y1).map((t) => (
        <g key={t}>
          <line className="pg-axis" x1={pad.left} x2={width - pad.right} y1={sy(t)} y2={sy(t)} />
          <text x={pad.left - 6} y={sy(t) + 4} textAnchor="end">
            {formatY(t)}
          </text>
        </g>
      ))}
      {xTicks ? (
        xTicks
          .map((t) => +t)
          .filter((t) => t >= x0 && t <= x1)
          .map((t) => (
            <g key={t}>
              <line className="pg-axis" x1={sx(t)} x2={sx(t)} y1={height - pad.bottom} y2={height - pad.bottom + 4} />
              <text x={sx(t)} y={height - 8} textAnchor={sx(t) < pad.left + 24 ? "start" : sx(t) > width - pad.right - 24 ? "end" : "middle"}>
                {formatTick(t)}
              </text>
            </g>
          ))
      ) : (
        <>
          <text x={pad.left} y={height - 8}>
            {formatX(x0)}
          </text>
          <text x={width - pad.right} y={height - 8} textAnchor="end">
            {formatX(x1)}
          </text>
        </>
      )}
      {references.map((r, i) => (
        <g key={`ref${i}`}>
          <line x1={pad.left} x2={width - pad.right} y1={sy(r.y)} y2={sy(r.y)} stroke="var(--pg-danger)" strokeDasharray="6 4" />
          {r.label && (
            <text x={width - pad.right - 4} y={sy(r.y) - 4} textAnchor="end" style={{ fill: "var(--pg-danger)" }}>
              {r.label}
            </text>
          )}
        </g>
      ))}
      {markers.map((m, i) => (
        <line key={i} x1={sx(+m.x)} x2={sx(+m.x)} y1={pad.top} y2={height - pad.bottom} stroke="var(--pg-series-2)" strokeDasharray="4 3">
          {m.label && <title>{m.label}</title>}
        </line>
      ))}
      {series.map((s, i) => {
        const color = `var(--pg-series-${(i % 3) + 1})`;
        const sorted = [...s.points].sort((a, b) => +a.x - +b.x);
        return (
          <g key={s.name}>
            <polyline fill="none" stroke={color} strokeWidth={2} points={sorted.map((p) => `${sx(+p.x)},${sy(p.y)}`).join(" ")} />
            {sorted.map((p, j) => (
              <circle key={j} cx={sx(+p.x)} cy={sy(p.y)} r={3} fill={color}>
                <title>{`${s.name}: ${formatX(+p.x)} · ${formatY(p.y)}`}</title>
              </circle>
            ))}
          </g>
        );
      })}
    </svg>
  );
}

/**
 * How full something is, as a half-circle arc: value of max, its label and
 * an optional hint. The arc turns amber below warn and red below danger,
 * fractions of max. A bare gauge has no card around it, to sit in one.
 */
export function Gauge({ value, max = 100, label, hint, text, warn = 0.3, danger = 0.15, bare }: { value: number; max?: number; label?: ReactNode; hint?: ReactNode; text?: ReactNode; warn?: number; danger?: number; bare?: boolean }) {
  const share = Math.min(Math.max(max > 0 ? value / max : 0, 0), 1);
  const color = share < danger ? "var(--pg-danger)" : share < warn ? "var(--pg-warning)" : "var(--pg-success)";
  const r = 80;
  const arc = (to: number) => {
    const angle = Math.PI * (1 - to);
    return `M ${100 - r} 100 A ${r} ${r} 0 0 1 ${100 + r * Math.cos(angle)} ${100 - r * Math.sin(angle)}`;
  };
  return (
    <div className={bare ? undefined : "pg-card"} style={{ textAlign: "center" }}>
      {label !== undefined && <div className="pg-stat-label">{label}</div>}
      <svg viewBox="0 0 200 112" style={{ width: "100%", maxWidth: 260 }} role="img">
        <path d={arc(1)} fill="none" stroke="var(--pg-border)" strokeWidth={16} strokeLinecap="round" />
        {share > 0 && <path d={arc(share)} fill="none" stroke={color} strokeWidth={16} strokeLinecap="round" />}
        <text x={100} y={96} textAnchor="middle" style={{ fontSize: 30, fontWeight: 650, fill: "var(--pg-text)" }}>
          {text ?? `${Math.round(share * 100)} %`}
        </text>
      </svg>
      {hint !== undefined && <div className="pg-stat-hint">{hint}</div>}
    </div>
  );
}

/** Bars, one per label, such as a month. */
export function BarChart({ data, height = 200, formatY = (y) => String(Math.round(y)) }: { data: { label: string; value: number }[]; height?: number; formatY?: (y: number) => string }) {
  if (data.length === 0) return <div className="pg-empty">No data yet</div>;
  const max = Math.max(0, ...data.map((d) => d.value));
  const y1 = max === 0 ? 1 : max;
  const band = (width - pad.left - pad.right) / data.length;
  const sy = (y: number) => height - pad.bottom - (y / y1) * (height - pad.top - pad.bottom);
  const every = Math.max(1, Math.ceil(data.length / 12));
  return (
    <svg className="pg-chart" viewBox={`0 0 ${width} ${height}`} role="img">
      {ticks(0, y1).map((t) => (
        <g key={t}>
          <line className="pg-axis" x1={pad.left} x2={width - pad.right} y1={sy(t)} y2={sy(t)} />
          <text x={pad.left - 6} y={sy(t) + 4} textAnchor="end">
            {formatY(t)}
          </text>
        </g>
      ))}
      {data.map((d, i) => (
        <g key={d.label}>
          <rect x={pad.left + i * band + band * 0.15} width={band * 0.7} y={sy(Math.max(0, d.value))} height={Math.max(0, sy(0) - sy(Math.max(0, d.value)))} fill="var(--pg-series-1)" rx={3}>
            <title>{`${d.label}: ${formatY(d.value)}`}</title>
          </rect>
          {i % every === 0 && (
            <text x={pad.left + i * band + band / 2} y={height - 8} textAnchor="middle">
              {d.label}
            </text>
          )}
        </g>
      ))}
    </svg>
  );
}
