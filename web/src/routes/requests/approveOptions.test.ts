import type { ArrService, MediaRequest } from '$lib/api';
import {
  buildApproveBody,
  candidateInstances,
  createArrOptionsLoader,
  initialInstance,
  instanceLabel,
  pickProfileId,
  pickRootFolder,
  preferredFor,
} from './approveOptions';

vi.mock('$lib/api', () => ({ arrServicesApi: { list: vi.fn(), probe: vi.fn() } }));

function svc(over: Partial<ArrService> & Pick<ArrService, 'id' | 'name'>): ArrService {
  return {
    kind: 'radarr',
    base_url: 'http://radarr:7878',
    api_key_set: true,
    default_tags: [],
    is_default: false,
    enabled: true,
    created_at: '',
    updated_at: '',
    ...over,
  };
}

const radarr = svc({ id: 'r1', name: 'Radarr', is_default: true, default_quality_profile_id: 4, default_root_folder: '/movies' });
const radarr4k = svc({ id: 'r2', name: 'Radarr 4K', default_quality_profile_id: 7, default_root_folder: '/movies-4k' });
const radarrOff = svc({ id: 'r3', name: 'Radarr Old', enabled: false });
const sonarr = svc({ id: 's1', name: 'Sonarr', kind: 'sonarr', is_default: true });
const all = [radarr4k, radarrOff, sonarr, radarr];

type Req = Pick<MediaRequest, 'type' | 'requested_service_id'>;
const movie = (over: Partial<Req> = {}): Req => ({ type: 'movie', ...over });

describe('candidateInstances', () => {
  it('lists enabled instances of the right kind, default first', () => {
    expect(candidateInstances(movie(), all).map((s) => s.id)).toEqual(['r1', 'r2']);
    expect(candidateInstances({ type: 'show' }, all).map((s) => s.id)).toEqual(['s1']);
  });
});

describe('initialInstance', () => {
  it('uses the kind default when nothing was requested', () => {
    expect(initialInstance(movie(), all)).toEqual({ service: radarr, note: '' });
  });

  it('uses the requested instance when it is usable', () => {
    expect(initialInstance(movie({ requested_service_id: 'r2' }), all)).toEqual({ service: radarr4k, note: '' });
  });

  it('falls back with a note when the requested instance is unusable', () => {
    const off = initialInstance(movie({ requested_service_id: 'r3' }), all);
    expect(off.service).toBe(radarr);
    expect(off.note).toBe('The requested instance “Radarr Old” is disabled — using Radarr.');

    const wrongKind = initialInstance(movie({ requested_service_id: 's1' }), all);
    expect(wrongKind.service).toBe(radarr);
    expect(wrongKind.note).toMatch(/wrong kind/);

    const gone = initialInstance(movie({ requested_service_id: 'deleted' }), all);
    expect(gone.note).toBe('The requested instance no longer exists — using Radarr.');
  });

  it('takes the first enabled instance when none is default, and null when none exist', () => {
    expect(initialInstance(movie(), [radarr4k, radarrOff]).service).toBe(radarr4k);
    expect(initialInstance(movie(), [sonarr])).toEqual({ service: null, note: '' });
    expect(initialInstance(movie({ requested_service_id: 'x' }), []).note).toBe('The requested instance no longer exists.');
  });
});

describe('instanceLabel', () => {
  it('marks the default and the requester’s choice', () => {
    expect(instanceLabel(radarr, { requested_service_id: 'r2', username: 'alice' })).toBe('Radarr (default)');
    expect(instanceLabel(radarr4k, { requested_service_id: 'r2', username: 'alice' })).toBe('Radarr 4K — requested by alice');
    expect(instanceLabel(radarr4k, { requested_service_id: 'r2' })).toBe('Radarr 4K — requested by the requester');
  });
});

describe('preferredFor / pick*', () => {
  const profiles = [{ id: 4, name: 'HD-1080p' }, { id: 7, name: 'UHD' }, { id: 9, name: 'Any' }];
  const folders = [{ id: 1, path: '/movies' }, { id: 2, path: '/movies-4k/' }];

  it('prefers the request’s own values on the instance the dialog opened on', () => {
    const req = { quality_profile_id: 9, root_folder: '/movies-4k' };
    expect(preferredFor(req, radarr, true)).toEqual({ profileIds: [9, 4], rootFolders: ['/movies-4k', '/movies'] });
    // Switched instance: only its defaults.
    expect(preferredFor(req, radarr4k, false)).toEqual({ profileIds: [7], rootFolders: ['/movies-4k'] });
    expect(preferredFor({}, radarrOff, true)).toEqual({ profileIds: [], rootFolders: [] });
  });

  it('picks the first preferred value the instance actually has', () => {
    expect(pickProfileId(profiles, [99, 7, 4])).toBe(7);
    expect(pickRootFolder(folders, ['/nope', '/movies-4k'])).toBe('/movies-4k/'); // trailing slash ignored
  });

  it('auto-picks a lone option and otherwise leaves it to the admin', () => {
    expect(pickProfileId([{ id: 3, name: 'Only' }], [])).toBe(3);
    expect(pickProfileId(profiles, [])).toBeNull();
    expect(pickRootFolder([{ id: 1, path: '/only' }], ['/gone'])).toBe('/only');
    expect(pickRootFolder(folders, [])).toBeNull();
    expect(pickRootFolder([], ['/movies'])).toBeNull();
  });
});

describe('buildApproveBody', () => {
  it('sends only what is set', () => {
    expect(buildApproveBody({ serviceId: 'r2', profileId: 7, rootFolder: '/movies-4k' })).toEqual({
      service_id: 'r2', quality_profile_id: 7, root_folder: '/movies-4k',
    });
    expect(buildApproveBody({ serviceId: 'r2', profileId: null, rootFolder: null })).toEqual({ service_id: 'r2' });
    expect(buildApproveBody({ serviceId: null, profileId: null, rootFolder: '' })).toEqual({});
  });
});

describe('createArrOptionsLoader', () => {
  const probeResult = { status: 'ok', quality_profiles: [{ id: 4, name: 'HD' }], root_folders: [{ id: 1, path: '/movies' }], tags: [], language_profiles: [] };

  it('caches the instance list and each instance’s options', async () => {
    const api = { list: vi.fn().mockResolvedValue({ items: all, total: 4 }), probe: vi.fn().mockResolvedValue(probeResult) };
    const loader = createArrOptionsLoader(api);
    expect(await loader.services()).toEqual(all);
    await loader.services();
    expect(api.list).toHaveBeenCalledTimes(1);

    expect(await loader.options('r1')).toEqual({ quality_profiles: probeResult.quality_profiles, root_folders: probeResult.root_folders });
    await loader.options('r1');
    expect(api.probe).toHaveBeenCalledTimes(1);
    expect(api.probe).toHaveBeenCalledWith({ service_id: 'r1' });
    await loader.options('r2');
    expect(api.probe).toHaveBeenCalledTimes(2);

    await loader.options('r1', true);
    await loader.services(true);
    expect(api.probe).toHaveBeenCalledTimes(3);
    expect(api.list).toHaveBeenCalledTimes(2);
  });

  it('forgets failures so a retry goes back to the network', async () => {
    const api = {
      list: vi.fn().mockRejectedValueOnce(new Error('down')).mockResolvedValue({ items: [radarr], total: 1 }),
      probe: vi.fn().mockRejectedValueOnce(new Error('unreachable')).mockResolvedValue(probeResult),
    };
    const loader = createArrOptionsLoader(api);
    await expect(loader.services()).rejects.toThrow('down');
    expect(await loader.services()).toEqual([radarr]);
    await expect(loader.options('r1')).rejects.toThrow('unreachable');
    expect((await loader.options('r1')).quality_profiles).toHaveLength(1);
  });
});
