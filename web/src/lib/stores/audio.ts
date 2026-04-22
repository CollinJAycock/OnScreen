import { writable, derived, get } from 'svelte/store';

export interface AudioTrack {
  id: string;          // media_item id (track)
  fileId: string;      // media_files id used for /media/stream/{fileId}
  title: string;
  durationMS?: number;
  index?: number;      // track number within the album
  album?: string;
  albumId?: string;
  artist?: string;
  artistId?: string;
  posterPath?: string; // album poster, used by the player chrome
}

export type RepeatMode = 'off' | 'one' | 'all';

interface AudioState {
  queue: AudioTrack[];
  index: number;       // -1 when queue is empty
  playing: boolean;
  positionMS: number;
  shuffle: boolean;
  repeat: RepeatMode;
  // shuffleOrder is the play order over queue indexes when shuffle is on.
  // Kept stable per shuffle activation so going Prev returns to the
  // previously-played track instead of jumping randomly again.
  shuffleOrder: number[];
  shufflePos: number;
}

const initial: AudioState = {
  queue: [],
  index: -1,
  playing: false,
  positionMS: 0,
  shuffle: false,
  repeat: 'off',
  shuffleOrder: [],
  shufflePos: -1
};

function fisherYates(n: number, startWith: number): number[] {
  const order = Array.from({ length: n }, (_, i) => i);
  // Move startWith to the front so the currently-playing track is index 0.
  const swap = order.indexOf(startWith);
  if (swap > 0) [order[0], order[swap]] = [order[swap], order[0]];
  for (let i = n - 1; i > 1; i--) {
    const j = 1 + Math.floor(Math.random() * i); // keep [0] pinned
    [order[i], order[j]] = [order[j], order[i]];
  }
  return order;
}

function createAudioStore() {
  const { subscribe, update, set } = writable<AudioState>(initial);

  return {
    subscribe,

    // Replace the queue and start from startIndex.
    play(queue: AudioTrack[], startIndex = 0) {
      const i = Math.max(0, Math.min(startIndex, queue.length - 1));
      update((s) => ({
        ...s,
        queue,
        index: i,
        positionMS: 0,
        playing: queue.length > 0,
        shuffleOrder: s.shuffle ? fisherYates(queue.length, i) : [],
        shufflePos: s.shuffle ? 0 : -1
      }));
    },

    // Append to queue without interrupting current playback.
    enqueue(tracks: AudioTrack[]) {
      update((s) => {
        if (s.queue.length === 0) {
          return {
            ...s,
            queue: tracks,
            index: 0,
            positionMS: 0,
            playing: tracks.length > 0,
            shuffleOrder: s.shuffle ? fisherYates(tracks.length, 0) : [],
            shufflePos: s.shuffle ? 0 : -1
          };
        }
        const queue = [...s.queue, ...tracks];
        const shuffleOrder = s.shuffle
          ? // Append new positions in random order after the current tail.
            [...s.shuffleOrder, ...fisherYates(tracks.length, 0).map((j) => j + s.queue.length)]
          : [];
        return { ...s, queue, shuffleOrder };
      });
    },

    togglePlay() {
      update((s) => (s.queue.length === 0 ? s : { ...s, playing: !s.playing }));
    },

    pause() {
      update((s) => ({ ...s, playing: false }));
    },

    resume() {
      update((s) => (s.queue.length === 0 ? s : { ...s, playing: true }));
    },

    next() {
      update((s) => advance(s, 1));
    },

    prev() {
      // Standard player UX: if past 3s into the track, restart it; otherwise
      // jump to the previous track. This avoids accidentally losing position.
      const s = get({ subscribe });
      if (s.positionMS > 3000) {
        update((s2) => ({ ...s2, positionMS: 0 }));
        return;
      }
      update((s2) => advance(s2, -1));
    },

    seek(ms: number) {
      update((s) => ({ ...s, positionMS: Math.max(0, ms) }));
    },

    // Reports current playback position from the audio element. Throttled
    // by the caller so we don't churn subscribers every frame.
    setPosition(ms: number) {
      update((s) => ({ ...s, positionMS: ms }));
    },

    toggleShuffle() {
      update((s) => {
        const shuffle = !s.shuffle;
        if (!shuffle) return { ...s, shuffle, shuffleOrder: [], shufflePos: -1 };
        const order = fisherYates(s.queue.length, Math.max(0, s.index));
        return { ...s, shuffle, shuffleOrder: order, shufflePos: 0 };
      });
    },

    cycleRepeat() {
      update((s) => ({ ...s, repeat: nextRepeat(s.repeat) }));
    },

    clear() {
      set(initial);
    }
  };
}

function nextRepeat(r: RepeatMode): RepeatMode {
  return r === 'off' ? 'all' : r === 'all' ? 'one' : 'off';
}

// advance moves index by +1 or -1 honoring shuffle and repeat modes. Returns
// a new state. Stops playback when the end of queue is reached and repeat=off.
function advance(s: AudioState, delta: 1 | -1): AudioState {
  if (s.queue.length === 0) return s;

  if (s.repeat === 'one') {
    // Repeat-one rewinds the current track regardless of direction.
    return { ...s, positionMS: 0, playing: true };
  }

  if (s.shuffle) {
    const nextPos = s.shufflePos + delta;
    if (nextPos >= 0 && nextPos < s.shuffleOrder.length) {
      return {
        ...s,
        shufflePos: nextPos,
        index: s.shuffleOrder[nextPos],
        positionMS: 0,
        playing: true
      };
    }
    if (s.repeat === 'all') {
      const wrapped = ((nextPos % s.queue.length) + s.queue.length) % s.queue.length;
      return {
        ...s,
        shufflePos: wrapped,
        index: s.shuffleOrder[wrapped],
        positionMS: 0,
        playing: true
      };
    }
    return { ...s, playing: false, positionMS: 0 };
  }

  const next = s.index + delta;
  if (next >= 0 && next < s.queue.length) {
    return { ...s, index: next, positionMS: 0, playing: true };
  }
  if (s.repeat === 'all') {
    const wrapped = ((next % s.queue.length) + s.queue.length) % s.queue.length;
    return { ...s, index: wrapped, positionMS: 0, playing: true };
  }
  return { ...s, playing: false, positionMS: 0 };
}

export const audio = createAudioStore();

export const currentTrack = derived(audio, ($a) =>
  $a.index >= 0 && $a.index < $a.queue.length ? $a.queue[$a.index] : null
);
