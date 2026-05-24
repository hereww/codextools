import { invoke as tauriInvoke } from "@tauri-apps/api/core";
import { open as tauriOpen, type OpenDialogOptions } from "@tauri-apps/plugin-dialog";

type GoBackendConfig = {
  apiBase?: string;
};

declare global {
  interface Window {
    __CODEX_PLUS_GO_MANAGER__?: GoBackendConfig;
    __TAURI_INTERNALS__?: unknown;
  }
}

const goBackend = typeof window !== "undefined" ? window.__CODEX_PLUS_GO_MANAGER__ : undefined;

function hasTauriRuntime() {
  return typeof window !== "undefined" && "__TAURI_INTERNALS__" in window && !goBackend;
}

function goApiBase() {
  return (goBackend?.apiBase ?? "").replace(/\/$/, "");
}

export async function backendInvoke<T>(command: string, args?: Record<string, unknown>): Promise<T> {
  if (hasTauriRuntime()) {
    return tauriInvoke<T>(command, args);
  }

  const response = await fetch(`${goApiBase()}/api/commands/${encodeURIComponent(command)}`, {
    method: "POST",
    headers: {
      "content-type": "application/json",
    },
    body: JSON.stringify(args ?? {}),
  });
  if (!response.ok) {
    throw new Error(`Go manager command ${command} failed with HTTP ${response.status}`);
  }
  return (await response.json()) as T;
}

export async function openFileDialog(options: OpenDialogOptions): Promise<string | string[] | null> {
  if (hasTauriRuntime()) {
    return tauriOpen(options);
  }

  const response = await fetch(`${goApiBase()}/api/dialog/open`, {
    method: "POST",
    headers: {
      "content-type": "application/json",
    },
    body: JSON.stringify(options),
  });
  if (!response.ok) {
    throw new Error(`Go manager dialog failed with HTTP ${response.status}`);
  }
  return (await response.json()) as string | string[] | null;
}
