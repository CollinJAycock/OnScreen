// The approve-with-options dialog's decisions: which Radarr / Sonarr instance
// to pre-select, which quality profile and root folder to pre-fill, and the
// body POST /admin/requests/{id}/approve gets. Mirrors the server's fallback
// order (internal/requests: override → requested_service_id → the kind's
// default; profile/folder: override → the request's own → the instance's
// default) so "pre-filled" means "what approving without the dialog would do".
//
// Also a small per-page cache for the instance list and each instance's
// profiles / folders, so a second Approve on the same page opens instantly.

import {
  arrServicesApi,
  type ApproveRequestBody,
  type ArrProbeResult,
  type ArrService,
  type ArrServiceKind,
  type MediaRequest,
} from '$lib/api';

export type ArrInstanceOptions = Pick<ArrProbeResult, 'quality_profiles' | 'root_folders'>;

export function arrKindFor(type: MediaRequest['type']): ArrServiceKind {
  return type === 'show' ? 'sonarr' : 'radarr';
}

export function arrKindLabel(kind: ArrServiceKind): string {
  return kind === 'sonarr' ? 'Sonarr' : 'Radarr';
}

/** Enabled instances that can take this request, default first. */
export function candidateInstances(req: Pick<MediaRequest, 'type'>, services: readonly ArrService[]): ArrService[] {
  const kind = arrKindFor(req.type);
  return services
    .filter((s) => s.kind === kind && s.enabled)
    .sort((a, b) => Number(b.is_default) - Number(a.is_default) || a.name.localeCompare(b.name));
}

export interface InitialInstance {
  service: ArrService | null;
  /** Shown under the instance picker when the requester's choice can't be used. */
  note: string;
}

/**
 * The instance to pre-select: the requester's preference when it is still
 * usable, else the kind's default, else the first enabled instance.
 */
export function initialInstance(
  req: Pick<MediaRequest, 'type' | 'requested_service_id'>,
  services: readonly ArrService[],
): InitialInstance {
  const candidates = candidateInstances(req, services);
  const fallback = candidates.find((s) => s.is_default) ?? candidates[0] ?? null;
  if (!req.requested_service_id) return { service: fallback, note: '' };

  const requested = services.find((s) => s.id === req.requested_service_id);
  if (requested && candidates.includes(requested)) return { service: requested, note: '' };

  let why = 'The requested instance no longer exists';
  if (requested && requested.kind !== arrKindFor(req.type)) why = `The requested instance “${requested.name}” is the wrong kind`;
  else if (requested && !requested.enabled) why = `The requested instance “${requested.name}” is disabled`;
  return { service: fallback, note: fallback ? `${why} — using ${fallback.name}.` : `${why}.` };
}

/** Option text for the instance picker. */
export function instanceLabel(s: ArrService, req: Pick<MediaRequest, 'requested_service_id' | 'username'>): string {
  let label = s.name;
  if (s.is_default) label += ' (default)';
  if (s.id === req.requested_service_id) label += ` — requested by ${req.username || 'the requester'}`;
  return label;
}

export interface Preferred {
  profileIds: number[];
  rootFolders: string[];
}

/**
 * Profile / folder preferences for an instance, best first. The request's own
 * values (picked by the requester for the instance they asked for) only apply
 * to the instance the dialog opened on — switching to another instance uses
 * that instance's defaults, since profile ids aren't shared across instances.
 */
export function preferredFor(
  req: Pick<MediaRequest, 'quality_profile_id' | 'root_folder'>,
  svc: ArrService,
  isInitialInstance: boolean,
): Preferred {
  const profileIds: number[] = [];
  const rootFolders: string[] = [];
  if (isInitialInstance) {
    if (req.quality_profile_id) profileIds.push(req.quality_profile_id);
    if (req.root_folder) rootFolders.push(req.root_folder);
  }
  if (svc.default_quality_profile_id) profileIds.push(svc.default_quality_profile_id);
  if (svc.default_root_folder) rootFolders.push(svc.default_root_folder);
  return { profileIds, rootFolders };
}

/** First preferred profile the instance actually has; the only profile when
 *  there's exactly one; otherwise null (the admin has to pick). */
export function pickProfileId(profiles: ArrInstanceOptions['quality_profiles'], preferred: readonly number[]): number | null {
  for (const id of preferred) if (profiles.some((p) => p.id === id)) return id;
  return profiles.length === 1 ? profiles[0].id : null;
}

/** Same rule for root folders (matched by path, ignoring a trailing slash). */
export function pickRootFolder(folders: ArrInstanceOptions['root_folders'], preferred: readonly string[]): string | null {
  const norm = (p: string) => p.replace(/[\\/]+$/, '');
  for (const want of preferred) {
    const hit = folders.find((f) => norm(f.path) === norm(want));
    if (hit) return hit.path;
  }
  return folders.length === 1 ? folders[0].path : null;
}

/** The approve body for the dialog's selections. Unset fields are left out
 *  so the server applies its own fallback. */
export function buildApproveBody(sel: {
  serviceId: string | null;
  profileId: number | null;
  rootFolder: string | null;
}): ApproveRequestBody {
  const body: ApproveRequestBody = {};
  if (sel.serviceId) body.service_id = sel.serviceId;
  if (sel.profileId != null) body.quality_profile_id = sel.profileId;
  if (sel.rootFolder) body.root_folder = sel.rootFolder;
  return body;
}

export interface ArrOptionsLoader {
  services(force?: boolean): Promise<ArrService[]>;
  options(serviceId: string, force?: boolean): Promise<ArrInstanceOptions>;
}

type ArrApi = Pick<typeof arrServicesApi, 'list' | 'probe'>;

/**
 * Memoises the instance list and each instance's profiles / folders for the
 * page's lifetime. A failed load is forgotten so Retry goes back to the
 * network; `force` skips the cache.
 */
export function createArrOptionsLoader(apiImpl?: ArrApi): ArrOptionsLoader {
  let services: Promise<ArrService[]> | null = null;
  const options = new Map<string, Promise<ArrInstanceOptions>>();
  // Resolved per call, not at construction: the page builds the loader up
  // front, and nothing should touch the admin API until an admin needs it.
  const arr = () => apiImpl ?? arrServicesApi;

  return {
    services(force = false) {
      if (!services || force) {
        const p = Promise.resolve()
          .then(() => arr().list())
          .then((res) => res.items ?? []);
        services = p;
        p.catch(() => {
          if (services === p) services = null;
        });
      }
      return services;
    },
    options(serviceId, force = false) {
      let p = force ? undefined : options.get(serviceId);
      if (!p) {
        const next = Promise.resolve()
          .then(() => arr().probe({ service_id: serviceId }))
          .then((r) => ({
            quality_profiles: r.quality_profiles ?? [],
            root_folders: r.root_folders ?? [],
          }));
        options.set(serviceId, next);
        next.catch(() => {
          if (options.get(serviceId) === next) options.delete(serviceId);
        });
        p = next;
      }
      return p;
    },
  };
}
