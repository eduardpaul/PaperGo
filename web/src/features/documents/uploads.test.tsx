// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type * as Router from '@tanstack/react-router';
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { stringifyJSON } from '@/api/json';
import { UploadsProvider, useUploads, type UploadTarget } from './uploads';

const navigate = vi.fn();
vi.mock('@tanstack/react-router', async (importOriginal) => ({
  ...(await importOriginal<typeof Router>()),
  useNavigate: () => navigate,
}));

/** A stand-in for XMLHttpRequest whose responses the test decides. */
class FakeRequest {
  static sent: FakeRequest[] = [];
  upload: { onprogress?: (event: { lengthComputable: boolean; loaded: number; total: number }) => void } = {};
  onload?: () => void;
  onerror?: () => void;
  onabort?: () => void;
  status = 0;
  statusText = '';
  responseText = '';
  url = '';
  headers: Record<string, string> = {};
  open(_method: string, url: URL) {
    this.url = url.pathname;
  }
  setRequestHeader(key: string, value: string) {
    this.headers[key] = value;
  }
  send() {
    FakeRequest.sent.push(this);
  }
  abort() {
    this.onabort?.();
  }
  progress(loaded: number, total: number) {
    this.upload.onprogress?.({ lengthComputable: true, loaded, total });
  }
  respond(status: number, body: unknown) {
    this.status = status;
    this.responseText = stringifyJSON(body) ?? '';
    this.onload?.();
  }
}

afterEach(() => {
  cleanup();
  FakeRequest.sent = [];
  vi.unstubAllGlobals();
});

const target: UploadTarget = {
  url: '/v1/resources/lib/documents',
  label: 'Contracts',
  open: (itemId) => ({ to: '/w/$workspaceId/l/$listId', params: { workspaceId: 'w', listId: 'lib' }, search: { item: itemId } }),
};

function Uploader({ files, onDone }: { files: File[]; onDone: (count: number) => void }) {
  const { upload } = useUploads();
  return (
    <button type="button" onClick={() => void upload(files, target).then((results) => onDone(results.length))}>
      Start
    </button>
  );
}

it('runs two uploads at a time with progress, failures, retry and an Open link', async () => {
  vi.stubGlobal('XMLHttpRequest', FakeRequest);
  const files = ['a.pdf', 'b.pdf', 'c.pdf'].map((name) => new File(['%PDF-1'], name, { type: 'application/pdf' }));
  const onDone = vi.fn();
  render(
    <QueryClientProvider client={new QueryClient()}>
      <UploadsProvider>
        <Uploader files={files} onDone={onDone} />
      </UploadsProvider>
    </QueryClientProvider>,
  );
  fireEvent.click(screen.getByRole('button', { name: 'Start' }));
  await waitFor(() => expect(FakeRequest.sent).toHaveLength(2));
  expect(FakeRequest.sent[0].url).toBe('/v1/resources/lib/documents');
  expect(screen.getByText('Waiting · 6 B')).toBeTruthy();
  act(() => FakeRequest.sent[0].progress(3, 6));
  expect(screen.getByText('50% of 6 B to Contracts')).toBeTruthy();
  act(() =>
    FakeRequest.sent[0].respond(201, { item: { id: 'item-a', name: 'a.pdf' }, duplicates: [{ item_id: 'x', name: 'old.pdf' }] }),
  );
  await waitFor(() => expect(FakeRequest.sent).toHaveLength(3));
  act(() => FakeRequest.sent[1].respond(503, { code: 'unavailable', detail: 'server is busy' }));
  act(() => FakeRequest.sent[2].respond(201, { item: { id: 'item-c', name: 'c.pdf' } }));
  await waitFor(() => expect(onDone).toHaveBeenCalledWith(2));
  expect(screen.getByText('1 upload failed')).toBeTruthy();
  expect(screen.getByText('Server is busy')).toBeTruthy();
  expect(screen.getByText(/Already stored as old.pdf/)).toBeTruthy();
  fireEvent.click(screen.getAllByRole('button', { name: 'Open' })[0]);
  expect(navigate).toHaveBeenCalledWith({ to: '/w/$workspaceId/l/$listId', params: { workspaceId: 'w', listId: 'lib' }, search: { item: 'item-a' } });
  fireEvent.click(screen.getByRole('button', { name: /Retry/ }));
  await waitFor(() => expect(FakeRequest.sent).toHaveLength(4));
  act(() => FakeRequest.sent[3].respond(201, { item: { id: 'item-b', name: 'b.pdf' } }));
  await waitFor(() => expect(screen.getByText('3 uploads complete')).toBeTruthy());
  fireEvent.click(screen.getByRole('button', { name: 'Close uploads' }));
  expect(screen.queryByRole('region', { name: 'Uploads' })).toBeNull();
});

it('explains a taken file name and offers no retry for refused files', async () => {
  vi.stubGlobal('XMLHttpRequest', FakeRequest);
  render(
    <QueryClientProvider client={new QueryClient()}>
      <UploadsProvider>
        <Uploader files={[new File(['x'], 'same.pdf')]} onDone={vi.fn()} />
      </UploadsProvider>
    </QueryClientProvider>,
  );
  fireEvent.click(screen.getByRole('button', { name: 'Start' }));
  await waitFor(() => expect(FakeRequest.sent).toHaveLength(1));
  act(() => FakeRequest.sent[0].respond(409, { code: 'conflict', detail: 'resource conflicts with existing state' }));
  expect(await screen.findByText(/A file with this name is already here/)).toBeTruthy();
  expect(screen.queryByRole('button', { name: /Retry/ })).toBeNull();
});

it('cancels a running upload', async () => {
  vi.stubGlobal('XMLHttpRequest', FakeRequest);
  render(
    <QueryClientProvider client={new QueryClient()}>
      <UploadsProvider>
        <Uploader files={[new File(['x'], 'big.pdf')]} onDone={vi.fn()} />
      </UploadsProvider>
    </QueryClientProvider>,
  );
  fireEvent.click(screen.getByRole('button', { name: 'Start' }));
  await waitFor(() => expect(FakeRequest.sent).toHaveLength(1));
  fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
  await waitFor(() => expect(screen.getByText('Cancelled')).toBeTruthy());
});
