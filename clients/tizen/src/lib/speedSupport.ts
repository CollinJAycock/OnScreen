// Whether this TV plays media at the speed it's asked for. Samsung's webview
// ignores playbackRate (measured on a 2022 Q80B, Tizen 6.5: <audio> and
// <video> both play an MP3 at 1x with the rate set to 1.5), which the player
// finds out a few seconds into a book (lib/player rateCheck) and then
// withdraws its speed control. Remembered here, so every later chapter and
// book opens with the control already gone and the note shown, instead of
// offering a speed that is then taken away.

const KEY = 'onscreen.speed_unsupported';

export function speedKnownUnsupported(storage: Pick<Storage, 'getItem'> | null = safeStorage()): boolean {
  try {
    return storage?.getItem(KEY) === '1';
  } catch {
    return false;
  }
}

export function rememberSpeedUnsupported(storage: Pick<Storage, 'setItem'> | null = safeStorage()): void {
  try {
    storage?.setItem(KEY, '1');
  } catch { /* storage full or blocked: found again next time */ }
}

function safeStorage(): Storage | null {
  try {
    return typeof localStorage === 'undefined' ? null : localStorage;
  } catch {
    return null;
  }
}
