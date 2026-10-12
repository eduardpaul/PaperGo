import { ApiError } from './http';
import { authorizationHeaders } from './auth';

export async function downloadFile(path: string, fallbackName: string) {
  const response = await fetch(path, { headers: authorizationHeaders(), credentials: 'same-origin' });
  if (!response.ok) {
    const text = await response.text();
    let code = 'error';
    let detail = text || response.statusText;
    try {
      const problem = JSON.parse(text) as { code?: string; detail?: string; title?: string };
      code = problem.code ?? code;
      detail = problem.detail ?? problem.title ?? detail;
    } catch {
      /* Non-JSON transport error. */
    }
    throw new ApiError(response.status, code, detail);
  }
  const disposition = response.headers.get('Content-Disposition');
  const match = /filename\*=UTF-8''([^;]+)|filename="([^"]+)"|filename=([^;]+)/i.exec(disposition ?? '');
  const raw = match?.[1] ?? match?.[2] ?? match?.[3];
  let name = raw?.trim() || fallbackName;
  try {
    name = decodeURIComponent(name);
  } catch {
    /* Use the received filename. */
  }
  saveFile(await response.blob(), name);
}

export function saveFile(blob: Blob, name: string) {
  const url = URL.createObjectURL(blob);
  const link = document.createElement('a');
  link.href = url;
  link.download = name;
  link.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
