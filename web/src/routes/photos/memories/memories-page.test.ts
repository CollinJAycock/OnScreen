import { fireEvent, render, screen } from '@testing-library/svelte';
import { readable } from 'svelte/store';

const mockGoto = vi.hoisted(() => vi.fn());
const mockMemories = vi.hoisted(() => vi.fn());

vi.mock('$app/navigation', () => ({ goto: mockGoto }));
vi.mock('$app/stores', () => ({
  page: readable({ url: new URL('http://localhost/photos/memories?date=2026-10-14'), params: {} }),
}));
vi.mock('$lib/api', () => ({
  memoriesApi: { get: mockMemories },
  assetUrl: (p: string) => p,
}));

import Page from './+page.svelte';
import { loadMemorySelection } from '$lib/photos';

const photo = (id: string, poster = `2023/${id}.jpg`) => ({
  id, library_id: 'lib-1', title: id, poster_path: poster, taken_at: '2023-10-14T10:00:00Z', updated_at: '1',
});

beforeEach(() => {
  vi.clearAllMocks();
  sessionStorage.clear();
  localStorage.setItem('onscreen_user', JSON.stringify({ id: '1', username: 'u' }));
  mockMemories.mockResolvedValue({
    date: '2026-10-14',
    groups: [
      { kind: 'on_this_day', year: 2023, years_ago: 3, title: '3 years ago today', total: 2,
        cover: photo('a'), items: [photo('a'), photo('b', '2023/b.CR3')] },
    ],
  });
});

describe('Memories page', () => {
  it('shows the memories of the requested day', async () => {
    render(Page);
    expect(await screen.findByText('3 years ago today')).toBeTruthy();
    expect(mockMemories).toHaveBeenCalledWith('2026-10-14');
    const imgs = document.querySelectorAll('.tile img');
    expect(imgs[0].getAttribute('src')).toBe('/artwork/2023/a.jpg?v=1&w=300');
    // A RAW photo goes through the server render.
    expect(imgs[1].getAttribute('src')).toBe('/api/v1/items/b/image?w=300&v=1');
  });

  it('plays a memory as a slideshow in the viewer', async () => {
    render(Page);
    await fireEvent.click(await screen.findByRole('button', { name: /Play/ }));
    expect(mockGoto).toHaveBeenCalledWith('/photos/a?memory=2026-10-14-0&slideshow=1');
    expect(loadMemorySelection('2026-10-14-0')).toEqual(['a', 'b']);
  });

  it('says so when there is nothing for the day', async () => {
    mockMemories.mockResolvedValue({ date: '2026-10-14', groups: [] });
    render(Page);
    expect(await screen.findByText('No memories for this day')).toBeTruthy();
  });
});
