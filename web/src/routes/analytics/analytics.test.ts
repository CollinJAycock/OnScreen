import { render, screen, waitFor } from '@testing-library/svelte';
import Page from './+page.svelte';

const mockGoto = vi.hoisted(() => vi.fn());
const mockGetUser = vi.hoisted(() => vi.fn());
const mockAnalytics = vi.hoisted(() => vi.fn());
const mockSessions = vi.hoisted(() => vi.fn());

vi.mock('$app/navigation', () => ({ goto: mockGoto }));
vi.mock('$lib/api', () => ({
  api: { getUser: mockGetUser },
  analyticsApi: { get: mockAnalytics },
  sessionsApi: { list: mockSessions },
}));

const emptyAnalytics = {
  range_days: 30,
  overview: {
    total_items: 42,
    total_size_bytes: 5 * 1024 * 1024 * 1024,
    total_plays: 7,
    total_watch_time_ms: 3600000,
    total_files: 99,
  },
  plays_by_day: [],
  bandwidth_by_day: [],
  video_codecs: [],
  containers: [],
  libraries: [],
  top_played: [],
  recent_plays: [],
  top_users: [],
  clients: [],
  plays_by_hour: [],
  completion: { plays: 0, completed: 0 },
  stream_types_by_day: [],
};

beforeEach(() => {
  vi.clearAllMocks();
  mockAnalytics.mockResolvedValue(emptyAnalytics);
  mockSessions.mockResolvedValue([]);
});

describe('Analytics page', () => {
  it('redirects non-authenticated users to /login', () => {
    mockGetUser.mockReturnValue(null);
    render(Page);
    expect(mockGoto).toHaveBeenCalledWith('/login');
  });

  it('redirects non-admins to /', () => {
    mockGetUser.mockReturnValue({ user_id: 'u1', is_admin: false });
    render(Page);
    expect(mockGoto).toHaveBeenCalledWith('/');
  });

  it('renders overview cards for admins', async () => {
    mockGetUser.mockReturnValue({ user_id: 'u1', is_admin: true });
    render(Page);

    await waitFor(() => {
      expect(screen.getByText('Analytics')).toBeTruthy();
    });
    await waitFor(() => {
      expect(screen.getByText('42')).toBeTruthy();
      expect(screen.getByText('Items')).toBeTruthy();
      expect(screen.getByText('Storage')).toBeTruthy();
      expect(screen.getByText('Plays')).toBeTruthy();
    });
  });

  it('shows a friendly error (not the raw exception) when the first load fails', async () => {
    mockGetUser.mockReturnValue({ user_id: 'u1', is_admin: true });
    mockAnalytics.mockRejectedValueOnce(new Error('analytics dead'));
    render(Page);

    await waitFor(() => {
      expect(screen.getByText(/couldn’t load analytics/i)).toBeTruthy();
    });
    expect(screen.queryByText('analytics dead')).toBeNull();
    // Retry affordance is offered.
    expect(screen.getByText('Try again')).toBeTruthy();
  });

  it('keeps showing loaded data when a later refresh fails', async () => {
    vi.useFakeTimers();
    try {
      mockGetUser.mockReturnValue({ user_id: 'u1', is_admin: true });
      render(Page);

      // First load succeeds.
      await vi.waitFor(() => {
        expect(screen.getByText('42')).toBeTruthy();
      });

      // Next 30s poll fails — the dashboard must stay, with a stale banner.
      mockAnalytics.mockRejectedValueOnce(new Error('blip'));
      await vi.advanceTimersByTimeAsync(30000);

      expect(screen.getByText('42')).toBeTruthy();
      expect(screen.getByText(/couldn’t refresh/i)).toBeTruthy();
      expect(screen.queryByText('blip')).toBeNull();
    } finally {
      vi.useRealTimers();
    }
  });

  it('renders Now Playing section when sessions are active', async () => {
    mockGetUser.mockReturnValue({ user_id: 'u1', is_admin: true });
    mockSessions.mockResolvedValueOnce([
      {
        id: 's1',
        user_id: 'u1',
        title: 'Interstellar',
        year: 2014,
        decision: 'transcode',
        position_ms: 1000,
        duration_ms: 10000,
        poster_path: null,
        client_name: 'Chrome',
        bitrate_kbps: 8000,
      },
    ]);

    render(Page);
    await waitFor(() => {
      expect(screen.getByText('Interstellar')).toBeTruthy();
      expect(screen.getByText(/Transcoding/i)).toBeTruthy();
    });
  });

  it('does not render Now Playing when no sessions', async () => {
    mockGetUser.mockReturnValue({ user_id: 'u1', is_admin: true });
    render(Page);

    await waitFor(() => expect(screen.getByText('Analytics')).toBeTruthy());
    expect(screen.queryByText(/Now playing/i)).toBeNull();
  });
});
