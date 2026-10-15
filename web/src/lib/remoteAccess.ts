// Settings ▸ Remote access: presentation helpers for the port-mapping status.
import type { RemoteAccessStatus } from '$lib/api';

/** A short label for the mapping state. */
export function remoteAccessStateLabel(s: Pick<RemoteAccessStatus, 'state'>): string {
  switch (s.state) {
    case 'disabled':
      return 'Off';
    case 'loopback_only':
      return 'Not possible — the server listens on loopback only';
    case 'standby':
      return 'Waiting for the mapping node';
    case 'no_gateway':
      return 'No router found';
    case 'mapped':
      return 'Port mapped';
    case 'error':
      return 'Router refused or failed';
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
export function externalPortError(v: number | null | undefined): string {
  if (v === null || v === undefined || Number.isNaN(v) || v === 0) return '';
  if (!Number.isInteger(v) || v < 1 || v > 65535) return 'Use a port from 1 to 65535, or leave it empty for the listen port.';
  return '';
}
