import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import Page from './+page.svelte';

const mockList = vi.hoisted(() => vi.fn());
const mockTest = vi.hoisted(() => vi.fn());

vi.mock('$lib/api', () => ({
  pluginApi: { list: mockList, create: vi.fn(), update: vi.fn(), del: vi.fn(), test: mockTest },
}));

vi.mock('$lib/stores/toast', () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn() },
}));

function plugin(role: 'notification' | 'metadata' | 'task') {
  return {
    id: `${role}-id`,
    name: `${role} plugin`,
    role,
    transport: 'http',
    endpoint_url: 'https://plugin.example.com/mcp',
    allowed_hosts: [],
    enabled: true,
    created_at: '2026-10-19T00:00:00Z',
    updated_at: '2026-10-19T00:00:00Z',
  };
}

beforeEach(() => {
  vi.clearAllMocks();
});

describe('plugins settings', () => {
  it('offers every role, none marked coming soon, with its contract', async () => {
    mockList.mockResolvedValue({ items: [] });
    render(Page);
    const [addButton] = await screen.findAllByText('Add Plugin');
    await fireEvent.click(addButton);

    const select = screen.getByLabelText('Role') as HTMLSelectElement;
    const options = Array.from(select.options);
    expect(options.map(o => o.value)).toEqual(['notification', 'metadata', 'task']);
    expect(options.every(o => !o.disabled)).toBe(true);
    expect(options.map(o => o.textContent).join(' ')).toMatch(/enrich_item.*run_task/);
    expect(screen.queryByText(/coming soon/i)).toBeNull();

    await fireEvent.change(select, { target: { value: 'metadata' } });
    expect(screen.getByTestId('role-contract').textContent).toMatch(/Locked fields are never changed/);

    await fireEvent.change(select, { target: { value: 'task' } });
    expect(screen.getByTestId('role-contract').textContent).toMatch(/Settings → Tasks/);
  });

  it('shows each plugin its tool and probes non-notification plugins', async () => {
    mockList.mockResolvedValue({ items: [plugin('metadata'), plugin('task')] });
    mockTest.mockResolvedValue(undefined);
    render(Page);

    await screen.findByText('metadata plugin');
    const contracts = Array.from(document.querySelectorAll('.p-contract')).map(el => el.textContent ?? '');
    expect(contracts[0]).toMatch(/Calls enrich_item/);
    expect(contracts[1]).toMatch(/Calls run_task/);
    expect(screen.getByRole('link', { name: 'Schedule' }).getAttribute('href')).toBe('/settings/tasks');

    const [testMetadata] = screen.getAllByTitle('Test');
    await fireEvent.click(testMetadata);
    await waitFor(() => expect(screen.getByText(/advertises enrich_item/)).toBeTruthy());
    expect(mockTest).toHaveBeenCalledWith('metadata-id');
  });
});
