import { describe, expect, it } from 'vitest';
import {
  externalPortError,
  publicAddress,
  remoteAccessMethodLabel,
  remoteAccessStateLabel,
  remoteAccessTone,
} from './remoteAccess';

describe('remote access helpers', () => {
  it('labels and tones each state', () => {
    expect(remoteAccessStateLabel({ state: 'mapped' })).toBe('Port mapped');
    expect(remoteAccessTone({ state: 'mapped' })).toBe('ok');
    expect(remoteAccessTone({ state: 'disabled' })).toBe('off');
    expect(remoteAccessTone({ state: 'no_gateway' })).toBe('warn');
    expect(remoteAccessTone({ state: 'error' })).toBe('warn');
  });

  it('names the mapping protocol', () => {
    expect(remoteAccessMethodLabel('upnp')).toBe('UPnP IGD');
    expect(remoteAccessMethodLabel('nat-pmp')).toBe('NAT-PMP');
    expect(remoteAccessMethodLabel(undefined)).toBe('');
  });

  it('formats the public address', () => {
    expect(publicAddress({ external_ip: '203.0.113.7', external_port: 7070 })).toBe('203.0.113.7:7070');
    expect(publicAddress({ external_ip: '2001:db8::1', external_port: 443 })).toBe('[2001:db8::1]:443');
    expect(publicAddress({ external_port: 7070 })).toBe('');
  });

  it('validates the external port', () => {
    expect(externalPortError(0)).toBe('');
    expect(externalPortError(undefined)).toBe('');
    expect(externalPortError(32400)).toBe('');
    expect(externalPortError(70000)).not.toBe('');
    expect(externalPortError(-1)).not.toBe('');
    expect(externalPortError(1.5)).not.toBe('');
  });
});
