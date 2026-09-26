import { render, screen, waitFor } from '@testing-library/svelte';
import Page from './+page.svelte';
import {
  NOT_REPORTED_NOTE, fmtShare, normalizeStreamDay, reportedShares, resolveTotals,
  segmentWidth, sharePercents, streamBreakdown,
} from './stream-types';

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

// Local-timezone "YYYY-MM-DD" for today — the page's chart window keys.
function todayKey(): string {
  const d = new Date();
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
}

function summaryValues(container: HTMLElement): string[] {
  return Array.from(container.querySelectorAll('.stream-stat-value')).map(e => e.textContent ?? '');
}

describe('Analytics page — stream types', () => {
  beforeEach(() => {
    mockGetUser.mockReturnValue({ user_id: 'u1', is_admin: true });
  });

  it('shows shares of reported plays, the not-reported count and the per-client panel', async () => {
    mockAnalytics.mockResolvedValue({
      ...emptyAnalytics,
      stream_types_by_day: [
        { date: todayKey(), direct_play: 6, direct_stream: 3, transcode: 1, unknown: 90, direct: 9 },
      ],
      stream_totals: { direct_play: 6, direct_stream: 3, transcode: 1, unknown: 90 },
      stream_types_by_client: [
        { client: 'Fire TV', direct_play: 0, direct_stream: 2, transcode: 1, unknown: 90, total: 93 },
        { client: 'Chrome', direct_play: 6, direct_stream: 1, transcode: 0, unknown: 0, total: 7 },
      ],
    });
    const { container } = render(Page);

    await waitFor(() => expect(screen.getByText('Stream types by client')).toBeTruthy());
    // 6 / 3 / 1 of 10 reported plays — the 90 not-reported don't dilute it.
    expect(summaryValues(container)).toEqual(['60%', '30%', '10%']);
    expect(screen.getByText('90 not reported')).toBeTruthy();
    expect(screen.getByText(NOT_REPORTED_NOTE)).toBeTruthy();
    expect(screen.getAllByText('Not reported').length).toBeGreaterThan(0);
    expect(screen.queryByText('Unknown')).toBeNull();

    // Day tooltip carries the full four-way split.
    expect(container.querySelector(
      '.bar-col.stacked[title$=": 6 direct play, 3 direct stream, 1 transcode, 90 not reported"]',
    )).toBeTruthy();
    // Four day segments, in stack order top-down.
    const kinds = ['unknown', 'transcode', 'direct-stream', 'direct-play'];
    const segs = container.querySelectorAll('.bar-col.stacked .bar-seg');
    expect(Array.from(segs).map(s => kinds.find(k => s.classList.contains(k)))).toEqual(kinds);

    // Per-client rows: label, total, and only the non-zero segments.
    expect(screen.getByText('Fire TV')).toBeTruthy();
    expect(screen.getByText('93')).toBeTruthy();
    const fireTv = container.querySelector('.hbar-track.stacked[aria-label^="Fire TV:"]');
    expect(fireTv?.querySelectorAll('.hbar-seg').length).toBe(3);
    expect(container.querySelector('.hbar-seg[title="Chrome: 6 direct play (86%)"]')).toBeTruthy();
  });

  it('falls back gracefully when the server predates the new fields', async () => {
    // Older server: combined `direct` only, no stream_totals / by-client.
    mockAnalytics.mockResolvedValue({
      ...emptyAnalytics,
      stream_types_by_day: [{ date: todayKey(), direct: 3, transcode: 1, unknown: 4 }],
    });
    const { container } = render(Page);

    await waitFor(() => expect(summaryValues(container)).toEqual(['75%', '0%', '25%']));
    expect(screen.getByText('4 not reported')).toBeTruthy();
    expect(container.querySelector(
      '.bar-col.stacked[title$=": 3 direct play, 0 direct stream, 1 transcode, 4 not reported"]',
    )).toBeTruthy();
    // No per-client data to show — hidden, not an empty panel.
    expect(screen.queryByText('Stream types by client')).toBeNull();
    expect(container.textContent).not.toMatch(/NaN/);
  });

  it('handles an empty range without NaN', async () => {
    mockAnalytics.mockResolvedValue({
      ...emptyAnalytics,
      stream_totals: { direct_play: 0, direct_stream: 0, transcode: 0, unknown: 0 },
      stream_types_by_client: [],
    });
    const { container } = render(Page);

    await waitFor(() => expect(screen.getByText('Stream types by client')).toBeTruthy());
    expect(summaryValues(container)).toEqual(['—', '—', '—']);
    expect(screen.getByText('0 not reported')).toBeTruthy();
    expect(screen.getByText('No plays recorded in this range yet')).toBeTruthy();
    const clientPanel = screen.getByText('Stream types by client').closest('section');
    expect(clientPanel?.textContent).toContain('No plays recorded yet');
    expect(screen.queryByText(NOT_REPORTED_NOTE)).toBeNull();
    expect(container.textContent).not.toMatch(/NaN/);
  });
});

describe('stream-type maths', () => {
  it('sharePercents rounds to whole percentages that sum to exactly 100', () => {
    expect(sharePercents([1, 1, 1])).toEqual([34, 33, 33]);
    expect(sharePercents([2, 1])).toEqual([67, 33]);
    expect(sharePercents([0, 0, 5])).toEqual([0, 0, 100]);
    for (const parts of [[7, 11, 13], [1, 998, 1], [3, 3, 3, 3, 3, 3, 1]]) {
      expect(sharePercents(parts)!.reduce((s, v) => s + v, 0)).toBe(100);
    }
  });

  it('sharePercents is null (not NaN) when there is nothing to share', () => {
    expect(sharePercents([0, 0, 0])).toBeNull();
    expect(sharePercents([])).toBeNull();
    expect(sharePercents([NaN, 5])).toEqual([0, 100]);
  });

  it('reportedShares excludes not-reported plays from the denominator', () => {
    expect(reportedShares({ direct_play: 6, direct_stream: 3, transcode: 1, unknown: 90 }))
      .toEqual({ reported: 10, direct_play: 60, direct_stream: 30, transcode: 10 });
    expect(reportedShares({ direct_play: 0, direct_stream: 0, transcode: 0, unknown: 12 })).toBeNull();
  });

  it('fmtShare shows — for no data and <1% for a sliver that rounds away', () => {
    const s = reportedShares({ direct_play: 999, direct_stream: 0, transcode: 1, unknown: 0 })!;
    expect(s.direct_play).toBe(100);
    expect(fmtShare(s.transcode, 1)).toBe('<1%');
    expect(fmtShare(s.direct_stream, 0)).toBe('0%');
    expect(fmtShare(null, 0)).toBe('—');
    expect(fmtShare(undefined, 3)).toBe('—');
    expect(fmtShare(42, 5)).toBe('42%');
  });

  it('normalizeStreamDay maps a pre-split row\'s combined direct onto direct play', () => {
    expect(normalizeStreamDay({ date: '2026-09-01', direct: 5, transcode: 2, unknown: 1 }))
      .toEqual({ date: '2026-09-01', direct_play: 5, direct_stream: 0, transcode: 2, unknown: 1 });
    // A split row never double-counts the backward-compat `direct`.
    expect(normalizeStreamDay({ date: '2026-09-01', direct_play: 3, direct_stream: 2, direct: 5, transcode: 0, unknown: 0 }))
      .toEqual({ date: '2026-09-01', direct_play: 3, direct_stream: 2, transcode: 0, unknown: 0 });
  });

  it('resolveTotals prefers stream_totals and falls back to summing days', () => {
    const days = [
      { date: 'a', direct_play: 1, direct_stream: 2, transcode: 3, unknown: 4 },
      { date: 'b', direct_play: 1, direct_stream: 0, transcode: 0, unknown: 1 },
    ];
    expect(resolveTotals(undefined, days))
      .toEqual({ direct_play: 2, direct_stream: 2, transcode: 3, unknown: 5 });
    expect(resolveTotals({ direct_play: 9, direct_stream: 0, transcode: 1, unknown: 0 }, days))
      .toEqual({ direct_play: 9, direct_stream: 0, transcode: 1, unknown: 0 });
  });

  it('segmentWidth and streamBreakdown are safe on empty bars', () => {
    expect(segmentWidth(1, 0)).toBe(0);
    expect(segmentWidth(1, 4)).toBe(25);
    expect(streamBreakdown({ direct_play: 0, direct_stream: 0, transcode: 0, unknown: 0 }))
      .toBe('0 direct play, 0 direct stream, 0 transcode, 0 not reported');
  });
});
