// @papergo/app: what apps are written against. An app's default export is
// app({ render, parameters }); render is a React component that receives the
// app's context. See docs/apps.md.

import type { ReactNode } from "react";
import type { AppContext } from "./context.js";

export type { AppContext, Init, OpenTarget, RequestOptions, Response, Role } from "./context.js";
export { RequestError } from "./context.js";
export type { Format, Preferences } from "./format.js";
export type { Aggregate, AggregateGroup, AggregateGroupBy, AggregateKey, AggregateQuery, Item, Loaded, Query, QueryOptions } from "./hooks.js";
export { useAggregates, useApp, useItem, useQuery } from "./hooks.js";
export type { Band, Column, Series } from "./kit.js";
export { BarChart, Button, Card, DateField, DateTimeField, Gauge, Grid, Legend, LineChart, Modal, Notice, NumberField, Stat, Table, TextField, Toolbar, narrowWidth, useNarrow } from "./kit.js";
export type { PaperGoClient } from "../client/paperGoClient.js";
export * as models from "../client/models/index.js";
export { json, plain } from "../values.js";

/** An app: its screen and the form of its parameters. */
export interface AppDefinition<P = Record<string, unknown>> {
  /**
   * The form of the app's parameters, a JSON Schema object
   * (docs/workflows.md#forms); pickers choose lists, content types or items.
   */
  parameters?: Record<string, unknown>;
  /** The app's screen, a React component of the app's context. */
  render: (ctx: AppContext<P>) => ReactNode;
}

/** Declares an app; make it the source's default export. */
export function app<P = Record<string, unknown>>(definition: AppDefinition<P>): AppDefinition<P> {
  return definition;
}
