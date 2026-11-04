// The app runtime: dist/app-runtime.js, which the PaperGo page puts into an
// app's sandboxed frame before the app's compiled code. It supplies the
// modules apps import as globalThis.__papergoApp.modules, and start(init)
// renders the app's default export.

import * as React from "react";
import * as jsxRuntime from "react/jsx-runtime";
import { Component, type ErrorInfo, type ReactNode } from "react";
import { createRoot } from "react-dom/client";
import { Bridge, parentPort } from "./bridge.js";
import { appContext, RuntimeContext, type Init, type Runtime } from "./context.js";
import * as appModule from "./index.js";
import type { AppDefinition } from "./index.js";
import { styles } from "./kit.js";

class Boundary extends Component<{ children: ReactNode }, { error?: Error }> {
  state: { error?: Error } = {};
  static getDerivedStateFromError(error: Error) {
    return { error };
  }
  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error(error, info.componentStack);
  }
  render() {
    if (this.state.error) {
      return (
        <div role="alert">
          <h2>This app stopped</h2>
          <p className="pg-error">{this.state.error.message}</p>
        </div>
      );
    }
    return this.props.children;
  }
}

function Screen({ definition, ctx }: { definition: AppDefinition; ctx: Runtime["ctx"] }) {
  return <>{definition.render(ctx)}</>;
}

function fail(message: string): void {
  document.body.innerHTML = "";
  const p = document.createElement("p");
  p.className = "pg-error";
  p.textContent = message;
  document.body.append(p);
}

// The bridge connects as the runtime loads, before the app's code runs.
const bridge = new Bridge(parentPort());

/** Renders the app whose compiled code ran last, with what the page knows. */
function start(init: Init): void {
  document.documentElement.dataset.theme = init.theme;
  document.documentElement.lang = init.user.preferences.language || "en";
  const style = document.createElement("style");
  style.textContent = styles;
  document.head.append(style);
  const definition = (globalThis as { __papergoAppDefinition?: AppDefinition }).__papergoAppDefinition;
  if (!definition || typeof definition.render !== "function") {
    fail("This app's code did not load.");
    return;
  }
  const runtime: Runtime = { ctx: appContext(init, bridge), bridge };
  const root = document.createElement("div");
  document.body.append(root);
  createRoot(root).render(
    <RuntimeContext.Provider value={runtime}>
      <Boundary>
        <Screen definition={definition} ctx={runtime.ctx} />
      </Boundary>
    </RuntimeContext.Provider>,
  );
  bridge.ready();
}

(globalThis as Record<string, unknown>).__papergoApp = {
  modules: { react: React, "react/jsx-runtime": jsxRuntime, "@papergo/app": appModule },
  start,
};
