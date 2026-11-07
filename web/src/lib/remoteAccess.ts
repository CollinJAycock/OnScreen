// Settings ▸ Remote access: presentation helpers for the port-mapping status.
// Wording comes from the i18n catalog; the page passes $i18n as tx so the
// text re-renders when the language changes (it defaults to the current one).
import { get } from 'svelte/store';
import type { RemoteAccessStatus } from '$lib/api';
import { i18n, type Translator } from '$lib/i18n/index.svelte';

/** A short label for the mapping state. */
export function remoteAccessStateLabel(s: Pick<RemoteAccessStatus, 'state'>, tx: Translator = get(i18n)): string {
  switch (s.state) {
    case 'disabled':
      return tx('player.off');
    case 'loopback_only':
      return tx('lib.remoteAccess.loopbackOnly');
    case 'standby':
      return tx('lib.remoteAccess.standby');
    case 'no_gateway':
      return tx('lib.remoteAccess.noGateway');
    case 'mapped':
      return tx('lib.remoteAccess.mapped');
    case 'error':
      return tx('lib.remoteAccess.error');
    default:
      return s.state;
  }
}

/** The tone of the state chip. */
export function remoteAccessTone(s: Pick<RemoteAccessStatus, 'state'>): 'ok' | 'warn' | 'off' {
  if (s.state === 'mapped') return 'ok';
  if (s.state === 'disabled' || s.state === 'standby') return 'off';
  return 'warn';
}

/** "UPnP IGD", "PCP", "NAT-PMP". */
export function remoteAccessMethodLabel(m: RemoteAccessStatus['method']): string {
  switch (m) {
    case 'upnp':
      return 'UPnP IGD';
    case 'pcp':
      return 'PCP';
    case 'nat-pmp':
      return 'NAT-PMP';
    default:
      return '';
  }
}

/** "203.0.113.7:7070", or '' while unknown. */
export function publicAddress(s: Pick<RemoteAccessStatus, 'external_ip' | 'external_port'>): string {
  if (!s.external_ip || !s.external_port) return '';
  const host = s.external_ip.includes(':') ? `[${s.external_ip}]` : s.external_ip;
  return `${host}:${s.external_port}`;
}

/** Validation for the external-port field: '' when fine (0/empty = the listen port). */
export function externalPortError(v: number | null | undefined, tx: Translator = get(i18n)): string {
  if (v === null || v === undefined || Number.isNaN(v) || v === 0) return '';
  if (!Number.isInteger(v) || v < 1 || v > 65535) return tx('lib.remoteAccess.portError');
  return '';
}
