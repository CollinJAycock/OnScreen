import {
  adminStopText,
  isPlaybackStoppedError,
  isStopForPlayer,
  parsePlaybackStop,
  probePlaybackStopped,
  type PlaybackStopEvent,
} from './playback-stop';

describe('parsePlaybackStop', () => {
  it('maps the SSE payload', () => {
    expect(parsePlaybackStop({
      item_id: 'i1', session_id: 's1', client_name: 'Web — Chrome on Windows', decision: 'remux', message: 'bye',
    })).toEqual({ itemId: 'i1', sessionId: 's1', clientName: 'Web — Chrome on Windows', decision: 'remux', message: 'bye' });
  });

  it('drops empty optional fields', () => {
    expect(parsePlaybackStop({ item_id: 'i1', session_id: '', message: '' })).toEqual({
      itemId: 'i1', sessionId: undefined, clientName: undefined, decision: undefined, message: undefined,
    });
  });

  it('rejects unusable payloads', () => {
    for (const bad of [null, undefined, 'x', 42, {}, { item_id: '' }, { item_id: 7 }]) {
      expect(parsePlaybackStop(bad)).toBeNull();
    }
  });
});

describe('isStopForPlayer', () => {
  const me = { itemId: 'i1', sessionId: 'sess-1', clientName: 'Web — Chrome on Windows' };
  const ev = (e: Partial<PlaybackStopEvent>): PlaybackStopEvent => ({ itemId: 'i1', ...e });

  it('ignores other items and players with nothing loaded', () => {
    expect(isStopForPlayer(ev({ itemId: 'i2' }), me)).toBe(false);
    expect(isStopForPlayer(ev({}), { ...me, itemId: null })).toBe(false);
  });

  it('an untargeted stop hits every player of the item', () => {
    expect(isStopForPlayer(ev({}), me)).toBe(true);
    expect(isStopForPlayer(ev({}), { ...me, sessionId: null })).toBe(true);
  });

  it('a session stop hits the session owner, or the named client', () => {
    expect(isStopForPlayer(ev({ sessionId: 'sess-1' }), me)).toBe(true);
    expect(isStopForPlayer(ev({ sessionId: 'sess-2' }), me)).toBe(false);
    expect(isStopForPlayer(ev({ sessionId: 'sess-2', clientName: 'Web — Chrome on Windows' }), me)).toBe(true);
    expect(isStopForPlayer(ev({ sessionId: 'sess-2', clientName: 'Living Room TV' }), me)).toBe(false);
  });

  it('a direct-play stop hits the client that reported the name', () => {
    expect(isStopForPlayer(ev({ clientName: 'Web — Chrome on Windows' }), { ...me, sessionId: null })).toBe(true);
    expect(isStopForPlayer(ev({ clientName: 'Living Room TV' }), { ...me, sessionId: null })).toBe(false);
  });
});

describe('adminStopText', () => {
  it('matches the server 403 wording', () => {
    expect(adminStopText('Server maintenance')).toBe('Playback was stopped by the server admin: Server maintenance');
    expect(adminStopText('  ')).toBe('Playback was stopped by the server admin.');
    expect(adminStopText()).toBe('Playback was stopped by the server admin.');
  });
});

describe('isPlaybackStoppedError', () => {
  it('matches the refused-stream code only', () => {
    expect(isPlaybackStoppedError(Object.assign(new Error('x'), { code: 'PLAYBACK_STOPPED' }))).toBe(true);
    for (const other of [null, undefined, 'PLAYBACK_STOPPED', new Error('x'), { code: 'PARENTAL_LIMIT' }]) {
      expect(isPlaybackStoppedError(other)).toBe(false);
    }
  });
});

describe('probePlaybackStopped', () => {
  afterEach(() => vi.unstubAllGlobals());

  const answer = (status: number, body: unknown) =>
    vi.fn(async () => ({ status, json: async () => body }));

  it("returns the server's sentence for a 403 PLAYBACK_STOPPED", async () => {
    const f = answer(403, { error: { code: 'PLAYBACK_STOPPED', message: 'Playback was stopped by the server admin: bye' } });
    vi.stubGlobal('fetch', f);
    await expect(probePlaybackStopped('/media/stream/f1')).resolves.toBe('Playback was stopped by the server admin: bye');
    expect(f).toHaveBeenCalledWith('/media/stream/f1', { headers: { Range: 'bytes=0-0' } });
  });

  it('falls back to the default sentence when the 403 has no message', async () => {
    vi.stubGlobal('fetch', answer(403, { error: { code: 'PLAYBACK_STOPPED' } }));
    await expect(probePlaybackStopped('/s')).resolves.toBe('Playback was stopped by the server admin.');
  });

  it('is null for anything that is not an admin stop', async () => {
    for (const f of [
      answer(206, null),
      answer(403, { error: { code: 'PARENTAL_LIMIT', message: 'x' } }),
      answer(403, null),
      vi.fn(async () => ({ status: 403, json: async () => { throw new SyntaxError('not json'); } })),
      vi.fn(async () => { throw new TypeError('Failed to fetch'); }),
    ]) {
      vi.stubGlobal('fetch', f);
      await expect(probePlaybackStopped('/s')).resolves.toBeNull();
    }
  });
});
