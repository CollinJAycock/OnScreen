import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import type { ArrServiceHealth } from '$lib/api';
import ArrHealthPanel from './ArrHealthPanel.svelte';
import { formatBytes, isLowSpace, safeLink, summarizeHealth, usedPercent } from './health';

const mockHealth = vi.hoisted(() => vi.fn());

vi.mock('$lib/api', () => ({
  arrServicesApi: { health: mockHealth },
}));

const GB = 1024 ** 3;

function snapshot(over: Partial<ArrServiceHealth> = {}): ArrServiceHealth {
  return {
    reachable: true,
    version: '5.9.1',
    checks: [],
    disks: [{ path: '/movies', label: 'media', free_bytes: 500 * GB, total_bytes: 1000 * GB }],
    queue_count: 3,
    ...over,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
});

describe('health helpers', () => {
  it('summarises a healthy instance as OK', () => {
    expect(summarizeHealth(snapshot())).toEqual({ tone: 'ok', label: 'OK', hasDetails: true });
  });

  it('counts warnings and errors, errors winning the tone', () => {
    const s = summarizeHealth(
      snapshot({
        checks: [
          { type: 'warning', source: 'IndexerStatusCheck', message: 'Indexers unavailable' },
          { type: 'warning', source: 'ImportMechanismCheck', message: 'x' },
          { type: 'error', source: 'DownloadClientCheck', message: 'y' },
          { type: 'notice', source: 'UpdateCheck', message: 'z' },
        ],
      }),
    );
    expect(s.tone).toBe('error');
    expect(s.label).toBe('1 error · 2 warnings');
  });

  it('flags a disk under 10% free as a warning', () => {
    const s = summarizeHealth(
      snapshot({ disks: [{ path: '/movies', label: '', free_bytes: 5 * GB, total_bytes: 100 * GB }] }),
    );
    expect(s).toMatchObject({ tone: 'warn', label: 'low disk space' });
  });

  it('mentions notices only when nothing is wrong', () => {
    const s = summarizeHealth(snapshot({ checks: [{ type: 'notice', source: 'UpdateCheck', message: 'New version' }] }));
    expect(s).toMatchObject({ tone: 'ok', label: 'OK · 1 notice' });
  });

  it('reports an unreachable instance with its reason', () => {
    const s = summarizeHealth(snapshot({ reachable: false, error: 'the instance rejected the API key', disks: [] }));
    expect(s).toEqual({ tone: 'error', label: 'Unreachable — the instance rejected the API key', hasDetails: false });
  });

  it('computes low space and bar widths, tolerating unknown sizes', () => {
    expect(isLowSpace({ path: '/', label: '', free_bytes: 9, total_bytes: 100 })).toBe(true);
    expect(isLowSpace({ path: '/', label: '', free_bytes: 10, total_bytes: 100 })).toBe(false);
    expect(isLowSpace({ path: '/', label: '', free_bytes: 0, total_bytes: 0 })).toBe(false);
    expect(usedPercent({ path: '/', label: '', free_bytes: 25, total_bytes: 100 })).toBe(75);
    expect(usedPercent({ path: '/', label: '', free_bytes: 5, total_bytes: 0 })).toBe(0);
    expect(usedPercent({ path: '/', label: '', free_bytes: 500, total_bytes: 100 })).toBe(0);
  });

  it('formats bytes in binary units', () => {
    expect(formatBytes(0)).toBe('0 B');
    expect(formatBytes(512)).toBe('512 B');
    expect(formatBytes(1536)).toBe('1.5 KB');
    expect(formatBytes(3.5 * 1024 ** 4)).toBe('3.5 TB');
    expect(formatBytes(250 * GB)).toBe('250 GB');
  });

  it('only keeps http(s) wiki links', () => {
    expect(safeLink('https://wiki.servarr.com/x')).toBe('https://wiki.servarr.com/x');
    expect(safeLink('javascript:alert(1)')).toBeNull();
    expect(safeLink(undefined)).toBeNull();
  });
});

describe('ArrHealthPanel', () => {
  it('shows a compact OK line with version and queue size', async () => {
    mockHealth.mockResolvedValue(snapshot());
    render(ArrHealthPanel, { serviceId: 'svc-1' });
    await waitFor(() => expect(screen.getByText('OK')).toBeInTheDocument());
    expect(mockHealth).toHaveBeenCalledWith('svc-1');
    expect(screen.getByText('v5.9.1')).toBeInTheDocument();
    expect(screen.getByText('· 3 in queue')).toBeInTheDocument();
    // Collapsed until asked.
    expect(screen.queryByTestId('arr-disk')).toBeNull();
  });

  it('expands to the checks and disk bars, highlighting low space', async () => {
    mockHealth.mockResolvedValue(
      snapshot({
        checks: [
          {
            type: 'warning',
            source: 'IndexerStatusCheck',
            message: 'Indexers unavailable due to failures',
            wiki_url: 'https://wiki.servarr.com/radarr/system#indexers',
          },
          { type: 'error', source: 'Evil', message: 'bad link', wiki_url: 'javascript:alert(1)' },
        ],
        disks: [
          { path: '/movies', label: 'media', free_bytes: 500 * GB, total_bytes: 1000 * GB },
          { path: '/downloads', label: '', free_bytes: 20 * GB, total_bytes: 1000 * GB },
        ],
      }),
    );
    render(ArrHealthPanel, { serviceId: 'svc-1' });
    await waitFor(() => expect(screen.getByText('1 error · 1 warning · low disk space')).toBeInTheDocument());

    await fireEvent.click(screen.getByRole('button', { name: 'Details' }));
    expect(screen.getByText('Indexers unavailable due to failures')).toBeInTheDocument();
    const links = screen.getAllByRole('link', { name: 'Learn more' });
    expect(links).toHaveLength(1);
    expect(links[0]).toHaveAttribute('href', 'https://wiki.servarr.com/radarr/system#indexers');
    expect(links[0]).toHaveAttribute('rel', 'noopener noreferrer');

    const disks = screen.getAllByTestId('arr-disk');
    expect(disks).toHaveLength(2);
    expect(disks[0]).not.toHaveClass('low');
    expect(disks[1]).toHaveClass('low');
    expect(disks[1].textContent).toContain('20 GB free of 1000 GB — low');
    const meters = screen.getAllByRole('meter');
    expect(meters[0]).toHaveAttribute('aria-valuenow', '50');
    expect(meters[1]).toHaveAttribute('aria-valuenow', '98');

    await fireEvent.click(screen.getByRole('button', { name: 'Hide details' }));
    expect(screen.queryByTestId('arr-disk')).toBeNull();
  });

  it('shows an unreachable instance without details', async () => {
    mockHealth.mockResolvedValue(
      snapshot({ reachable: false, version: undefined, error: 'the instance could not be reached', disks: [], queue_count: null }),
    );
    render(ArrHealthPanel, { serviceId: 'svc-1' });
    await waitFor(() =>
      expect(screen.getByText('Unreachable — the instance could not be reached')).toBeInTheDocument(),
    );
    expect(screen.queryByRole('button', { name: 'Details' })).toBeNull();
    expect(screen.queryByText(/in queue/)).toBeNull();
  });

  it('surfaces a failed health request and can refresh', async () => {
    mockHealth.mockRejectedValueOnce(new Error('network down')).mockResolvedValueOnce(snapshot());
    render(ArrHealthPanel, { serviceId: 'svc-1' });
    await waitFor(() => expect(screen.getByText('Health check failed: network down')).toBeInTheDocument());
    await fireEvent.click(screen.getByRole('button', { name: 'Refresh health' }));
    await waitFor(() => expect(screen.getByText('OK')).toBeInTheDocument());
    expect(mockHealth).toHaveBeenCalledTimes(2);
  });

  it('does not probe a disabled instance', async () => {
    render(ArrHealthPanel, { serviceId: 'svc-1', enabled: false });
    expect(screen.getByText('Disabled — health not checked')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Refresh health' })).toBeNull();
    expect(mockHealth).not.toHaveBeenCalled();
  });
});
