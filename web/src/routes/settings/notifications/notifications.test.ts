import { fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import Page from './+page.svelte';
import {
  agentStatus,
  configFor,
  createPayload,
  defaultEvents,
  destinationSummary,
  emptyForm,
  formFromAgent,
  parseRecipients,
  secretField,
  updatePayload,
  validateForm,
} from './notifications';
import type { NotificationAgent, NotificationAgentEvent } from '$lib/api';

const mockList = vi.hoisted(() => vi.fn());
const mockEvents = vi.hoisted(() => vi.fn());
const mockCreate = vi.hoisted(() => vi.fn());
const mockUpdate = vi.hoisted(() => vi.fn());
const mockDel = vi.hoisted(() => vi.fn());
const mockTest = vi.hoisted(() => vi.fn());
const toastSuccess = vi.hoisted(() => vi.fn());
const toastError = vi.hoisted(() => vi.fn());

vi.mock('$lib/api', () => ({
  notificationAgentApi: {
    list: mockList,
    events: mockEvents,
    create: mockCreate,
    update: mockUpdate,
    del: mockDel,
    test: mockTest,
  },
}));

vi.mock('$lib/stores/toast', () => ({
  toast: { success: toastSuccess, error: toastError, info: vi.fn() },
}));

const CHAT = ['discord', 'telegram', 'ntfy', 'gotify'] as const;
const ALL = [...CHAT, 'email'] as const;

const CATALOG: NotificationAgentEvent[] = [
  { key: 'request_pending', label: 'New request', description: 'Waiting for approval.', group: 'requests', default_for: [...ALL] },
  { key: 'request_available', label: 'Request available', description: 'Ready to watch.', group: 'requests', default_for: [...CHAT] },
  { key: 'new_content', label: 'New content', description: 'Batched.', group: 'library', default_for: [] },
  { key: 'task_failed', label: 'Scheduled task failed', description: 'A task failed.', group: 'server', default_for: [...ALL] },
];

const HOUR = 3_600_000;
const ago = (ms: number) => new Date(Date.now() - ms).toISOString();

function agent(p: Partial<NotificationAgent> = {}): NotificationAgent {
  return {
    id: 'a1',
    kind: 'ntfy',
    name: 'Phones',
    enabled: true,
    events: ['request_pending', 'task_failed'],
    config: { server_url: 'https://ntfy.example.com', topic: 'house' },
    secret_configured: true,
    allow_private_network: false,
    last_success_at: ago(2 * HOUR),
    last_error: null,
    last_error_at: null,
    created_at: ago(48 * HOUR),
    updated_at: ago(48 * HOUR),
    ...p,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  mockList.mockResolvedValue({ items: [agent()], total: 1 });
  mockEvents.mockResolvedValue(CATALOG);
  mockCreate.mockResolvedValue(agent({ id: 'new' }));
  mockUpdate.mockResolvedValue(agent());
  mockDel.mockResolvedValue(undefined);
  mockTest.mockResolvedValue({ ok: true });
});

describe('notification agent helpers', () => {
  it('defaults events per kind: new content always opt-in, email only admin events', () => {
    expect(defaultEvents('discord', CATALOG)).toEqual(['request_pending', 'request_available', 'task_failed']);
    expect(defaultEvents('email', CATALOG)).toEqual(['request_pending', 'task_failed']);
    const f = emptyForm('ntfy', CATALOG);
    expect(f.serverUrl).toBe('https://ntfy.sh');
    expect(f.name).toBe('ntfy');
    expect(f.events).not.toContain('new_content');
  });

  it('describes each kind’s write-only secret', () => {
    expect(secretField('discord')?.label).toBe('Webhook URL');
    expect(secretField('telegram')?.required).toBe(true);
    expect(secretField('ntfy')?.required).toBe(false);
    expect(secretField('email')).toBeNull();
  });

  it('sends only the kind’s config and never a blank secret', () => {
    const f = { ...emptyForm('telegram', CATALOG), chatId: ' -100 ', serverUrl: 'ignored', secret: '  ' };
    expect(configFor(f)).toEqual({ chat_id: '-100' });
    const body = createPayload(f);
    expect(body.secret).toBeUndefined();
    expect(body.allow_private_network).toBeUndefined();
    expect(createPayload({ ...f, secret: ' 1:abc ' }).secret).toBe('1:abc');
    expect(parseRecipients('a@x.com, b@y.org;\nc@z.net ')).toEqual(['a@x.com', 'b@y.org', 'c@z.net']);
  });

  it('keeps the stored secret on edit unless a new one is typed or it is cleared', () => {
    const f = formFromAgent(agent());
    expect(f.secret).toBe('');
    expect(updatePayload(f)).not.toHaveProperty('secret');
    expect(updatePayload({ ...f, secret: 'tk_new' }).secret).toBe('tk_new');
    expect(updatePayload({ ...f, clearSecret: true }).clear_secret).toBe(true);
    expect(updatePayload(f).allow_private_network).toBe(false);
  });

  it('validates required fields and the secret-follows-endpoint rule', () => {
    expect(validateForm({ ...emptyForm('discord', CATALOG) }, null)).toBe('Webhook URL is required');
    expect(validateForm({ ...emptyForm('discord', CATALOG), events: [], secret: 'x' }, null)).toBe('Select at least one event');
    expect(validateForm({ ...emptyForm('ntfy', CATALOG) }, null)).toBe('Topic is required');
    expect(validateForm({ ...emptyForm('email', CATALOG) }, null)).toBe('Add at least one recipient');
    const a = agent();
    const moved = { ...formFromAgent(a), serverUrl: 'https://elsewhere.example.net' };
    expect(validateForm(moved, a)).toMatch(/Re-enter the access token/);
    expect(validateForm({ ...moved, secret: 'tk' }, a)).toBeNull();
    expect(validateForm({ ...formFromAgent(a), serverUrl: 'https://NTFY.example.com/' }, a)).toBeNull();
    // A configured required secret doesn't need retyping.
    const g = agent({ kind: 'gotify', config: { server_url: 'https://g.example.com' } });
    expect(validateForm(formFromAgent(g), g)).toBeNull();
  });

  it('reports the newer of last success / last error', () => {
    const now = Date.now();
    expect(agentStatus(agent(), now)).toEqual({ tone: 'ok', text: 'Last delivered 2 hours ago' });
    const failed = agentStatus(agent({ last_error: 'HTTP 401: Unauthorized', last_error_at: ago(HOUR) }), now);
    expect(failed.tone).toBe('error');
    expect(failed.detail).toBe('HTTP 401: Unauthorized');
    expect(agentStatus(agent({ last_error: 'old', last_error_at: ago(3 * HOUR) }), now).tone).toBe('ok');
    expect(agentStatus(agent({ last_success_at: null }), now).text).toBe('Nothing sent yet');
    expect(agentStatus(agent({ enabled: false }), now).tone).toBe('disabled');
  });

  it('summarises destinations without secrets', () => {
    expect(destinationSummary(agent())).toBe('https://ntfy.example.com/house');
    expect(destinationSummary(agent({ kind: 'discord', config: {} }))).toBe('Discord webhook configured');
    expect(destinationSummary(agent({ kind: 'email', config: { recipients: ['a@x.com', 'b@x.com'] } }))).toBe('a@x.com, b@x.com');
  });
});

describe('Notifications settings page', () => {
  it('lists agents with status, destination and event labels', async () => {
    mockList.mockResolvedValue({
      items: [agent(), agent({ id: 'a2', kind: 'discord', name: 'Family', config: {}, last_error: 'HTTP 404: Unknown Webhook', last_error_at: ago(HOUR) })],
      total: 2,
    });
    render(Page);
    const rows = await screen.findAllByTestId('agent-row');
    expect(rows).toHaveLength(2);
    expect(within(rows[0]).getByText('Phones')).toBeTruthy();
    expect(within(rows[0]).getByText('https://ntfy.example.com/house')).toBeTruthy();
    expect(within(rows[0]).getByText('Scheduled task failed')).toBeTruthy();
    expect(within(rows[0]).getByText(/Last delivered/)).toBeTruthy();
    expect(within(rows[1]).getByText('HTTP 404: Unknown Webhook')).toBeTruthy();
    expect(within(rows[1]).getByText(/Last delivery failed/)).toBeTruthy();
  });

  it('adds a Discord agent with default events and a write-only webhook URL', async () => {
    mockList.mockResolvedValue({ items: [], total: 0 });
    render(Page);
    await fireEvent.click(await screen.findByRole('button', { name: '+ Discord' }));
    const secret = screen.getByLabelText(/Webhook URL/) as HTMLInputElement;
    expect(secret.type).toBe('password');
    expect((screen.getByLabelText(/New content/) as HTMLInputElement).checked).toBe(false);
    expect((screen.getByLabelText(/Request available/) as HTMLInputElement).checked).toBe(true);
    // Discord can't target the private network — no toggle offered.
    expect(screen.queryByLabelText(/Allow private network/)).toBeNull();

    await fireEvent.click(screen.getByRole('button', { name: 'Add agent' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Webhook URL is required');
    expect(mockCreate).not.toHaveBeenCalled();

    await fireEvent.input(secret, { target: { value: 'https://discord.com/api/webhooks/1/abc' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Add agent' }));
    await waitFor(() => expect(mockCreate).toHaveBeenCalledTimes(1));
    expect(mockCreate.mock.calls[0][0]).toEqual({
      kind: 'discord',
      name: 'Discord',
      enabled: true,
      events: ['request_pending', 'request_available', 'task_failed'],
      config: {},
      secret: 'https://discord.com/api/webhooks/1/abc',
    });
    await waitFor(() => expect(toastSuccess).toHaveBeenCalled());
  });

  it('edits without resending the secret and shows server validation errors', async () => {
    render(Page);
    const row = (await screen.findAllByTestId('agent-row'))[0];
    await fireEvent.click(within(row).getByRole('button', { name: 'Edit' }));
    const secret = screen.getByLabelText(/Access token/) as HTMLInputElement;
    expect(secret.value).toBe('');
    expect(secret.placeholder).toBe('Configured — leave blank to keep');
    expect(screen.getByLabelText(/Allow private network/)).toBeTruthy();

    mockUpdate.mockRejectedValueOnce(new Error('the server host resolves to a private address'));
    await fireEvent.click(screen.getByRole('button', { name: 'Save changes' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('resolves to a private address');
    const body = mockUpdate.mock.calls[0][1];
    expect(mockUpdate.mock.calls[0][0]).toBe('a1');
    expect(body).not.toHaveProperty('secret');
    expect(body.config).toEqual({ server_url: 'https://ntfy.example.com', topic: 'house' });
  });

  it('refuses to move a server URL without the token before calling the API', async () => {
    render(Page);
    const row = (await screen.findAllByTestId('agent-row'))[0];
    await fireEvent.click(within(row).getByRole('button', { name: 'Edit' }));
    await fireEvent.input(screen.getByLabelText('Server URL'), { target: { value: 'https://evil.example.net' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Save changes' }));
    expect(await screen.findByRole('alert')).toHaveTextContent(/Re-enter the access token/);
    expect(mockUpdate).not.toHaveBeenCalled();
  });

  it('sends a test and shows the inline result', async () => {
    mockTest.mockResolvedValueOnce({ ok: false, error: 'HTTP 403: forbidden' });
    render(Page);
    const row = (await screen.findAllByTestId('agent-row'))[0];
    await fireEvent.click(within(row).getByRole('button', { name: 'Send test' }));
    await waitFor(() => expect(mockTest).toHaveBeenCalledWith('a1'));
    expect(await within(row).findByRole('status')).toHaveTextContent('HTTP 403: forbidden');

    await fireEvent.click(within(row).getByRole('button', { name: 'Send test' }));
    await waitFor(() => expect(within(row).getByRole('status')).toHaveTextContent('Test message sent'));
  });

  it('deletes after confirmation', async () => {
    render(Page);
    const row = (await screen.findAllByTestId('agent-row'))[0];
    await fireEvent.click(within(row).getByRole('button', { name: 'Delete' }));
    const dialog = screen.getByRole('dialog');
    expect(dialog).toHaveTextContent('Delete “Phones”?');
    expect(mockDel).not.toHaveBeenCalled();
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Delete' }));
    await waitFor(() => expect(mockDel).toHaveBeenCalledWith('a1'));
  });

  it('toggles enabled from the list', async () => {
    render(Page);
    const row = (await screen.findAllByTestId('agent-row'))[0];
    await fireEvent.click(within(row).getByLabelText('Enabled'));
    await waitFor(() => expect(mockUpdate).toHaveBeenCalledWith('a1', { enabled: false }));
  });

  it('shows a load error', async () => {
    mockList.mockRejectedValueOnce(new Error('forbidden'));
    render(Page);
    expect(await screen.findByText('forbidden')).toBeTruthy();
  });
});
