import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import ApiKeys from './ApiKeys.svelte';
import { expiryFromDays, expiryLabel, scopeLabel, sortKeys, validKeyName } from '$lib/apiKeys';
import type { ApiKey } from '$lib/api';

const mockListMine = vi.hoisted(() => vi.fn());
const mockCreate = vi.hoisted(() => vi.fn());
const mockRevoke = vi.hoisted(() => vi.fn());
const mockAdminList = vi.hoisted(() => vi.fn());
const mockAdminRevoke = vi.hoisted(() => vi.fn());
const mockConfirm = vi.hoisted(() => vi.fn());

vi.mock('$lib/api', () => ({
  apiKeyApi: {
    listMine: mockListMine,
    create: mockCreate,
    revoke: mockRevoke,
    adminList: mockAdminList,
    adminRevoke: mockAdminRevoke,
  },
}));
vi.mock('$lib/native', () => ({ confirmAction: mockConfirm }));
vi.mock('$lib/stores/toast', () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

const key = (over: Partial<ApiKey> = {}): ApiKey => ({
  id: 'k1',
  name: 'Home Assistant',
  prefix: 'osk_AbCd1234',
  scope: 'read_only',
  created_at: new Date(Date.now() - 3600_000).toISOString(),
  last_used_at: null,
  expires_at: null,
  expired: false,
  ...over,
});

beforeEach(() => {
  vi.clearAllMocks();
  mockConfirm.mockResolvedValue(true);
  mockRevoke.mockResolvedValue(undefined);
  mockAdminRevoke.mockResolvedValue(undefined);
  // happy-dom's <dialog> may lack the modal API.
  const proto = HTMLDialogElement.prototype as HTMLDialogElement & { showModal?: () => void };
  if (!proto.showModal) proto.showModal = function (this: HTMLDialogElement) { this.setAttribute('open', ''); };
  if (!proto.close) proto.close = function (this: HTMLDialogElement) { this.removeAttribute('open'); };
});

describe('ApiKeys', () => {
  it('lists keys without ever showing a full key', async () => {
    mockListMine.mockResolvedValue([key()]);
    render(ApiKeys);
    expect(await screen.findByText('Home Assistant')).toBeTruthy();
    expect(screen.getByText('Read-only')).toBeTruthy();
    expect(screen.getByText('osk_AbCd1234…')).toBeTruthy();
    expect(screen.getByText('Never used')).toBeTruthy();
  });

  it('creates a key and shows it once', async () => {
    mockListMine.mockResolvedValueOnce([]).mockResolvedValue([key({ scope: 'full' })]);
    mockCreate.mockResolvedValue({ ...key({ scope: 'full' }), key: 'osk_' + 'x'.repeat(43) });
    render(ApiKeys);
    await fireEvent.click(await screen.findByRole('button', { name: 'Create API key' }));
    await fireEvent.input(screen.getByLabelText('Name'), { target: { value: '  Kometa ' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Create key' }));

    await waitFor(() => expect(mockCreate).toHaveBeenCalledWith('Kometa', 'full', null));
    expect(await screen.findByLabelText('New API key')).toBeTruthy();
    expect(screen.getByLabelText('New API key').textContent).toBe('osk_' + 'x'.repeat(43));
    expect(screen.getByText(/only time the key is shown/i)).toBeTruthy();
  });

  it('refuses a blank name without calling the server', async () => {
    mockListMine.mockResolvedValue([]);
    render(ApiKeys);
    await fireEvent.click(await screen.findByRole('button', { name: 'Create API key' }));
    await fireEvent.input(screen.getByLabelText('Name'), { target: { value: '   ' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Create key' }));
    expect(await screen.findByText(/Give the key a name/)).toBeTruthy();
    expect(mockCreate).not.toHaveBeenCalled();
  });

  it('revokes after confirmation', async () => {
    mockListMine.mockResolvedValue([key()]);
    render(ApiKeys);
    await fireEvent.click(await screen.findByRole('button', { name: 'Revoke' }));
    await waitFor(() => expect(mockRevoke).toHaveBeenCalledWith('k1'));
    await waitFor(() => expect(screen.queryByText('Home Assistant')).toBeNull());
  });

  it('admin mode lists everyone, names owners, revokes through the admin route and offers no create', async () => {
    mockAdminList.mockResolvedValue([key({ username: 'alice', user_id: 'u-a' })]);
    render(ApiKeys, { admin: true });
    expect(await screen.findByText('alice')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Create API key' })).toBeNull();
    await fireEvent.click(screen.getByRole('button', { name: 'Revoke' }));
    await waitFor(() => expect(mockAdminRevoke).toHaveBeenCalledWith('k1'));
    expect(mockRevoke).not.toHaveBeenCalled();
    expect(mockListMine).not.toHaveBeenCalled();
  });
});

describe('apiKeys helpers', () => {
  it('maps expiry choices to timestamps', () => {
    const now = Date.parse('2026-10-14T00:00:00Z');
    expect(expiryFromDays(0, now)).toBeNull();
    expect(expiryFromDays(30, now)).toBe('2026-11-13T00:00:00.000Z');
  });

  it('labels scope and expiry', () => {
    expect(scopeLabel('full')).toBe('Full access');
    expect(scopeLabel('read_only')).toBe('Read-only');
    expect(expiryLabel({ expires_at: null, expired: false })).toBe('No expiry');
    expect(expiryLabel({ expires_at: '2020-01-01T00:00:00Z', expired: true })).toBe('Expired');
    expect(expiryLabel({ expires_at: '2030-01-01T00:00:00Z', expired: false })).toMatch(/^Expires /);
  });

  it('sorts newest first and validates names', () => {
    const a = key({ id: 'a', created_at: '2026-01-01T00:00:00Z' });
    const b = key({ id: 'b', created_at: '2026-02-01T00:00:00Z' });
    expect(sortKeys([a, b]).map((k) => k.id)).toEqual(['b', 'a']);
    expect(validKeyName('  ')).toBe(false);
    expect(validKeyName('x'.repeat(100))).toBe(true);
    expect(validKeyName('x'.repeat(101))).toBe(false);
  });
});
