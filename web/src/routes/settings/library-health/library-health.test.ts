import { fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import Page from './+page.svelte';
import {
  canRegrab,
  displayTitle,
  regrabErrorMessage,
  regrabResolutionNote,
  regrabSummary,
} from './library-health';
import type { AdminMediaIssue, LibraryHealth, RegrabResult } from '$lib/api';

const mockHealth = vi.hoisted(() => vi.fn());
const mockAdminList = vi.hoisted(() => vi.fn());
const mockClose = vi.hoisted(() => vi.fn());
const mockRegrab = vi.hoisted(() => vi.fn());
const toastSuccess = vi.hoisted(() => vi.fn());
const toastError = vi.hoisted(() => vi.fn());

vi.mock('$lib/api', () => ({
  assetUrl: (p: string) => p,
  issuesApi: { libraryHealth: mockHealth, adminList: mockAdminList, close: mockClose, regrab: mockRegrab },
}));

vi.mock('$lib/stores/toast', () => ({
  toast: { success: toastSuccess, error: toastError },
}));

const DAY = 86_400_000;
const ago = (ms: number) => new Date(Date.now() - ms).toISOString();

function issueRow(p: Partial<AdminMediaIssue> = {}): AdminMediaIssue {
  return {
    id: 'iss-1', item_id: 'item-1', kind: 'video', status: 'open', created_at: ago(2 * DAY),
    library_id: 'lib', item_type: 'movie', item_title: 'Heat', item_year: 1995,
    reporter_id: 'u1', reporter_username: 'alice', note: 'green frames', file_name: 'Heat.1995.mkv',
    ...p,
  };
}

const HEALTH: LibraryHealth = {
  open_issues: 1,
  damaged_total: 1,
  unmatched_count: 3,
  missing_art_count: 4,
  damaged_files: [{
    file_id: 'f-1', item_id: 'item-2', library_id: 'lib', item_type: 'episode', title: 'Harvest',
    show_title: 'Andor', season_number: 2, episode_number: 5, path_basename: 'Andor.S02E05.mkv',
    integrity_detail: 'decode stopped at 00:00:02', checked_at: ago(DAY + 60_000),
  }],
};

const REGRAB: RegrabResult = {
  service_id: 's', service_name: 'HD', service_kind: 'radarr', command: 'MoviesSearch', command_id: 9,
  arr_id: 12, title: 'Heat', blocklisted: true, blocklisted_release: 'Heat.1995.BAD', has_file: false,
};

beforeEach(() => {
  vi.clearAllMocks();
  mockHealth.mockResolvedValue(HEALTH);
  mockAdminList.mockResolvedValue({ items: [issueRow()], total: 1 });
  mockClose.mockResolvedValue({});
  mockRegrab.mockResolvedValue(REGRAB);
});

describe('library-health helpers', () => {
  it('titles episodes, seasons and movies', () => {
    expect(displayTitle({ item_type: 'episode', title: 'Harvest', show_title: 'Andor', season_number: 2, episode_number: 5 }))
      .toBe('Andor — S02E05 · Harvest');
    expect(displayTitle({ item_type: 'season', title: 'Season 2', show_title: 'Andor' })).toBe('Andor — Season 2');
    expect(displayTitle({ item_type: 'movie', title: 'Heat', year: 1995 })).toBe('Heat (1995)');
    expect(displayTitle({ item_type: 'episode', title: 'Loose' })).toBe('Loose');
  });

  it('only offers re-grab for *arr-managed types', () => {
    for (const t of ['movie', 'show', 'season', 'episode']) expect(canRegrab(t)).toBe(true);
    for (const t of ['track', 'photo', 'audiobook', 'home_video']) expect(canRegrab(t)).toBe(false);
  });

  it('summarises a re-grab, warning when the app still has a file', () => {
    expect(regrabSummary(REGRAB)).toBe('Radarr (HD) is searching for Heat. Blocklisted Heat.1995.BAD.');
    const withFile = regrabSummary({ ...REGRAB, service_kind: 'sonarr', blocklisted: false, has_file: true });
    expect(withFile).toContain('Sonarr (HD) is searching for Heat.');
    expect(withFile).toContain('only an upgrade will be grabbed');
    expect(regrabResolutionNote(REGRAB)).toBe('Blocklisted the bad release and requested a new download.');
    expect(regrabResolutionNote({ ...REGRAB, blocklisted: false }, '  Should land tonight. ')).toBe(
      'Requested a new download. Should land tonight.',
    );
  });

  it('explains re-grab failures', () => {
    expect(regrabErrorMessage({ status: 409, code: 'NOT_MANAGED', message: 'no Radarr instance manages this title' }))
      .toBe('Not managed by Radarr/Sonarr: no Radarr instance manages this title');
    expect(regrabErrorMessage({ status: 502, code: 'ARR_UNAVAILABLE', message: 'could not reach' })).toBe('could not reach');
    expect(regrabErrorMessage(new Error('boom'))).toBe('Re-grab failed: boom');
  });
});

describe('Library health page', () => {
  it('shows counts with links, open reports and damaged files', async () => {
    render(Page);
    const reports = await screen.findByRole('list', { name: 'Problem reports' });
    const row = within(reports).getByText('Heat (1995)').closest('li')!;
    expect(row.textContent).toContain("Video won't play or looks wrong");
    expect(row.textContent).toContain('by alice');
    expect(row.textContent).toContain('2 days ago');
    expect(row.textContent).toContain('“green frames”');
    expect(row.textContent).toContain('Heat.1995.mkv');
    expect(mockAdminList).toHaveBeenCalledWith({ status: 'open', limit: 50 });

    await waitFor(() => expect(screen.getByText('unmatched →').closest('a')?.getAttribute('href')).toBe('/settings/unmatched'));
    expect(screen.getByText('missing art →').closest('a')?.getAttribute('href')).toBe('/settings/missing-art');

    const damaged = screen.getByRole('list', { name: 'Damaged files' });
    expect(damaged.textContent).toContain('Andor — S02E05 · Harvest');
    expect(damaged.textContent).toContain('Andor.S02E05.mkv');
    expect(damaged.textContent).toContain('decode stopped at 00:00:02');
  });

  it('resolves with a note and drops the row from the open list', async () => {
    render(Page);
    const input = await screen.findByLabelText('Note to the reporter');
    await fireEvent.input(input, { target: { value: ' Replaced the file ' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Resolve' }));
    await waitFor(() => expect(mockClose).toHaveBeenCalledWith('iss-1', 'resolved', 'Replaced the file'));
    await waitFor(() => expect(screen.queryByRole('list', { name: 'Problem reports' })).toBeNull());
    expect(screen.getByText('No open reports. Nothing to do here.')).toBeTruthy();
    expect(toastSuccess).toHaveBeenCalled();
  });

  it('dismisses without a note', async () => {
    render(Page);
    await fireEvent.click(await screen.findByRole('button', { name: 'Dismiss' }));
    await waitFor(() => expect(mockClose).toHaveBeenCalledWith('iss-1', 'dismissed', undefined));
  });

  it('re-grabs with the blocklist option, then resolves with a note', async () => {
    render(Page);
    const reports = await screen.findByRole('list', { name: 'Problem reports' });
    await fireEvent.click(within(reports).getByRole('checkbox', { name: 'Blocklist this release' }));
    await fireEvent.click(within(reports).getByRole('button', { name: 'Re-grab' }));
    await waitFor(() => expect(mockRegrab).toHaveBeenCalledWith('item-1', true));
    await waitFor(() =>
      expect(mockClose).toHaveBeenCalledWith('iss-1', 'resolved', 'Blocklisted the bad release and requested a new download.'),
    );
    expect(toastSuccess).toHaveBeenCalledWith('Radarr (HD) is searching for Heat. Blocklisted Heat.1995.BAD.');
  });

  it('keeps the report open when the re-grab fails', async () => {
    mockRegrab.mockRejectedValue(Object.assign(new Error('no Radarr instance manages this title'), { status: 409, code: 'NOT_MANAGED' }));
    render(Page);
    const reports = await screen.findByRole('list', { name: 'Problem reports' });
    await fireEvent.click(within(reports).getByRole('button', { name: 'Re-grab' }));
    await waitFor(() => expect(toastError).toHaveBeenCalledWith('Not managed by Radarr/Sonarr: no Radarr instance manages this title'));
    expect(mockClose).not.toHaveBeenCalled();
    expect(screen.getByRole('list', { name: 'Problem reports' })).toBeTruthy();
  });

  it('re-grabs a damaged file without blocklisting by default', async () => {
    render(Page);
    const damaged = await screen.findByRole('list', { name: 'Damaged files' });
    await fireEvent.click(within(damaged).getByRole('button', { name: 'Re-grab' }));
    await waitFor(() => expect(mockRegrab).toHaveBeenCalledWith('item-2', false));
    expect(await within(damaged).findByText('Re-grab requested')).toBeTruthy();
  });

  it('switches the status filter', async () => {
    mockAdminList
      .mockResolvedValueOnce({ items: [issueRow()], total: 1 })
      .mockResolvedValueOnce({
        items: [issueRow({ id: 'iss-2', status: 'resolved', resolved_at: ago(DAY + 60_000), resolved_by_username: 'root', resolution_note: 'Fixed' })],
        total: 1,
      });
    render(Page);
    await screen.findByRole('list', { name: 'Problem reports' });
    await fireEvent.click(screen.getByRole('tab', { name: 'Resolved' }));
    await waitFor(() => expect(mockAdminList).toHaveBeenLastCalledWith({ status: 'resolved', limit: 50 }));
    const reports = await screen.findByRole('list', { name: 'Problem reports' });
    await waitFor(() => expect(reports.textContent).toContain('Resolved by root yesterday: Fixed'));
    // Closed reports have no actions.
    expect(within(reports).queryByRole('button', { name: 'Resolve' })).toBeNull();
  });
});
