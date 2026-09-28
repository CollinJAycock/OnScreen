import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import type { ArrService, MediaRequest } from '$lib/api';
import type { ArrInstanceOptions, ArrOptionsLoader } from './approveOptions';
import ApproveDialog from './ApproveDialog.svelte';

vi.mock('$lib/api', () => ({ arrServicesApi: { list: vi.fn(), probe: vi.fn() } }));

function svc(over: Partial<ArrService> & Pick<ArrService, 'id' | 'name'>): ArrService {
  return {
    kind: 'radarr', base_url: 'http://radarr:7878', api_key_set: true, default_tags: [],
    is_default: false, enabled: true, created_at: '', updated_at: '', ...over,
  };
}

const radarr = svc({ id: 'r1', name: 'Radarr', is_default: true, default_quality_profile_id: 4, default_root_folder: '/movies' });
const radarr4k = svc({ id: 'r2', name: 'Radarr 4K', default_quality_profile_id: 7, default_root_folder: '/movies-4k' });
const sonarr = svc({ id: 's1', name: 'Sonarr', kind: 'sonarr', is_default: true, default_quality_profile_id: 2, default_root_folder: '/tv' });

const OPTIONS: Record<string, ArrInstanceOptions> = {
  r1: { quality_profiles: [{ id: 4, name: 'HD-1080p' }, { id: 5, name: 'Any' }], root_folders: [{ id: 1, path: '/movies', free_space: 2 * 1024 ** 4 }] },
  r2: { quality_profiles: [{ id: 7, name: 'Ultra-HD' }, { id: 8, name: 'Remux' }], root_folders: [{ id: 1, path: '/movies-4k' }, { id: 2, path: '/archive' }] },
  s1: { quality_profiles: [{ id: 2, name: 'WEB-1080p' }], root_folders: [{ id: 1, path: '/tv' }] },
};

function request(over: Partial<MediaRequest> = {}): MediaRequest {
  return {
    id: 'q1', user_id: 'u1', type: 'movie', tmdb_id: 1, title: 'Dune', year: 2021,
    status: 'pending', auto_approved: false, created_at: '', updated_at: '', username: 'alice', ...over,
  };
}

function loaderWith(services: ArrService[] = [radarr, radarr4k, sonarr]) {
  const loader = {
    services: vi.fn().mockResolvedValue(services),
    options: vi.fn((id: string) => Promise.resolve(OPTIONS[id])),
  };
  return loader as typeof loader & ArrOptionsLoader;
}

function setup(req: MediaRequest, loader = loaderWith(), busy = false) {
  const onapprove = vi.fn();
  const oncancel = vi.fn();
  const r = render(ApproveDialog, { request: req, loader, busy, onapprove, oncancel });
  return { ...r, loader, onapprove, oncancel };
}

const select = (label: string | RegExp) => screen.getByLabelText(label) as HTMLSelectElement;
const approveBtn = () => screen.getByRole('button', { name: 'Approve' }) as HTMLButtonElement;

async function ready() {
  await waitFor(() => expect(approveBtn().disabled).toBe(false));
}

beforeEach(() => vi.clearAllMocks());

describe('ApproveDialog', () => {
  it('is a labelled modal dialog naming the requester', async () => {
    setup(request());
    const dialog = screen.getByRole('dialog');
    expect(dialog.getAttribute('aria-modal')).toBe('true');
    expect(dialog.getAttribute('aria-labelledby')).toBe('approve-title');
    expect(screen.getByRole('heading').textContent).toContain('Approve “Dune”');
    expect(dialog.textContent).toContain('Requested by alice');
    await ready();
    await waitFor(() => expect(document.activeElement).toBe(dialog));
  });

  it('pre-fills the default instance with its default profile and folder', async () => {
    const { loader, onapprove } = setup(request());
    await ready();
    expect(select('Radarr instance').value).toBe('r1');
    // Only the matching kind, default first.
    expect([...select('Radarr instance').options].map((o) => o.textContent)).toEqual(['Radarr (default)', 'Radarr 4K']);
    expect(loader.options).toHaveBeenCalledWith('r1', false);
    expect(select('Quality profile').selectedOptions[0].textContent).toBe('HD-1080p');
    expect(select('Root folder').selectedOptions[0].textContent).toBe('/movies — 2.0 TB free');

    await fireEvent.click(approveBtn());
    expect(onapprove).toHaveBeenCalledWith({ service_id: 'r1', quality_profile_id: 4, root_folder: '/movies' });
  });

  it('pre-selects the requester’s instance and labels it', async () => {
    const { onapprove } = setup(request({ requested_service_id: 'r2' }));
    await ready();
    expect(select(/Radarr instance/).value).toBe('r2');
    expect(select(/Radarr instance/).selectedOptions[0].textContent).toBe('Radarr 4K — requested by alice');
    expect(screen.getByText('Requested by alice', { selector: '.tag' })).toBeTruthy();
    await fireEvent.click(approveBtn());
    expect(onapprove).toHaveBeenCalledWith({ service_id: 'r2', quality_profile_id: 7, root_folder: '/movies-4k' });
  });

  it('uses the request’s own profile and folder on the instance it opened on', async () => {
    const { onapprove } = setup(request({ requested_service_id: 'r2', quality_profile_id: 8, root_folder: '/archive' }));
    await ready();
    await fireEvent.click(approveBtn());
    expect(onapprove).toHaveBeenCalledWith({ service_id: 'r2', quality_profile_id: 8, root_folder: '/archive' });
  });

  it('reloads profiles and folders when the instance changes', async () => {
    const { loader, onapprove } = setup(request());
    await ready();
    const inst = select(/Radarr instance/);
    inst.value = 'r2';
    await fireEvent.change(inst);
    await waitFor(() => expect(loader.options).toHaveBeenCalledWith('r2', false));
    await waitFor(() => expect(select('Quality profile').selectedOptions[0]?.textContent).toBe('Ultra-HD'));
    expect([...select('Quality profile').options].map((o) => o.textContent)).toEqual(['Ultra-HD', 'Remux']);
    expect(select('Root folder').value).toBe('/movies-4k');

    // Admin overrides the profile.
    const qp = select('Quality profile');
    qp.value = '8';
    await fireEvent.change(qp);
    await fireEvent.click(approveBtn());
    expect(onapprove).toHaveBeenCalledWith({ service_id: 'r2', quality_profile_id: 8, root_folder: '/movies-4k' });
  });

  it('shows a loading state until the instance options arrive', async () => {
    let resolve: (v: ArrInstanceOptions) => void = () => {};
    const loader = loaderWith();
    loader.options.mockImplementationOnce(() => new Promise<ArrInstanceOptions>((res) => (resolve = res)));
    const { onapprove } = setup(request(), loader);
    await waitFor(() => expect(loader.options).toHaveBeenCalled());
    expect(approveBtn().disabled).toBe(true);
    expect(select('Quality profile').disabled).toBe(true);
    expect(select('Quality profile').textContent).toContain('Loading…');
    expect(screen.getByRole('dialog').getAttribute('aria-busy')).toBe('true');

    // Enter does nothing while loading.
    await fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Enter' });
    expect(onapprove).not.toHaveBeenCalled();

    resolve(OPTIONS.r1);
    await ready();
  });

  it('makes the admin choose when there is no default to pre-fill', async () => {
    const bare = svc({ id: 'r1', name: 'Radarr', is_default: true });
    const { onapprove } = setup(request(), loaderWith([bare]));
    await waitFor(() => expect(select('Quality profile').disabled).toBe(false));
    expect(approveBtn().disabled).toBe(true);
    expect(select('Quality profile').selectedOptions[0].textContent).toBe('Choose a profile…');

    const qp = select('Quality profile');
    qp.value = '5';
    await fireEvent.change(qp);
    // Root folder had a single option, so it was pre-picked.
    await ready();
    await fireEvent.click(approveBtn());
    expect(onapprove).toHaveBeenCalledWith({ service_id: 'r1', quality_profile_id: 5, root_folder: '/movies' });
  });

  it('falls back to the saved defaults when the options can’t be loaded, with a retry', async () => {
    const loader = loaderWith();
    loader.options.mockRejectedValueOnce(new Error('could not reach arr instance'));
    const { onapprove } = setup(request(), loader);
    await waitFor(() => expect(screen.getByRole('alert').textContent).toContain('could not reach arr instance'));
    expect(screen.getByText(/saved defaults/)).toBeTruthy();
    await fireEvent.click(approveBtn());
    expect(onapprove).toHaveBeenCalledWith({ service_id: 'r1', quality_profile_id: 4, root_folder: '/movies' });

    await fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    await waitFor(() => expect(loader.options).toHaveBeenLastCalledWith('r1', true));
    await waitFor(() => expect(screen.queryByRole('alert')).toBeNull());
    expect(select('Quality profile').value).toBe('4');
  });

  it('explains when no instance of the kind is enabled', async () => {
    const { onapprove } = setup(request({ type: 'show', title: 'Severance' }), loaderWith([radarr]));
    await waitFor(() => expect(screen.getByRole('alert').textContent).toContain('No enabled Sonarr instance'));
    expect(screen.getByRole('link', { name: 'Set one up' }).getAttribute('href')).toBe('/settings/arr-services');
    expect(approveBtn().disabled).toBe(true);
    expect((screen.getByRole('button', { name: 'Approve with defaults' }) as HTMLButtonElement).disabled).toBe(true);
    await fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Enter' });
    expect(onapprove).not.toHaveBeenCalled();
  });

  it('shows the requested seasons for a show', async () => {
    setup(request({ type: 'show', title: 'Severance', seasons: [1, 2, 3, 5] }));
    await ready();
    expect(screen.getByRole('dialog').textContent).toContain('Seasons 1–3, 5');
    expect(select('Sonarr instance').value).toBe('s1');
  });

  it('“Approve with defaults” sends an empty body', async () => {
    const { onapprove } = setup(request());
    await fireEvent.click(screen.getByRole('button', { name: 'Approve with defaults' }));
    expect(onapprove).toHaveBeenCalledWith({});
  });

  it('Enter approves, Esc cancels', async () => {
    const { onapprove, oncancel } = setup(request());
    await ready();
    await fireEvent.keyDown(select('Quality profile'), { key: 'Enter' });
    expect(onapprove).toHaveBeenCalledWith({ service_id: 'r1', quality_profile_id: 4, root_folder: '/movies' });

    await fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' });
    expect(oncancel).toHaveBeenCalledTimes(1);
  });

  it('leaves Enter on a button to the button', async () => {
    const { onapprove, oncancel } = setup(request());
    await ready();
    await fireEvent.keyDown(screen.getByRole('button', { name: 'Cancel' }), { key: 'Enter' });
    expect(onapprove).not.toHaveBeenCalled();
    expect(oncancel).not.toHaveBeenCalled();
  });

  it('cancels on an overlay click but not a click inside', async () => {
    const { oncancel } = setup(request());
    await fireEvent.click(screen.getByRole('dialog'));
    expect(oncancel).not.toHaveBeenCalled();
    await fireEvent.click(screen.getByRole('presentation'));
    expect(oncancel).toHaveBeenCalledTimes(1);
  });

  it('locks while the approval is in flight', async () => {
    const { onapprove, oncancel } = setup(request(), loaderWith(), true);
    await waitFor(() => expect(select(/Radarr instance/).value).toBe('r1'));
    expect(screen.getByRole('button', { name: 'Approving…' }).hasAttribute('disabled')).toBe(true);
    expect((screen.getByRole('button', { name: 'Cancel' }) as HTMLButtonElement).disabled).toBe(true);
    await fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' });
    await fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Enter' });
    expect(oncancel).not.toHaveBeenCalled();
    expect(onapprove).not.toHaveBeenCalled();
  });

  it('keeps Tab inside the dialog', async () => {
    setup(request());
    await ready();
    const buttons = screen.getAllByRole('button');
    const last = buttons[buttons.length - 1];
    last.focus();
    await fireEvent.keyDown(last, { key: 'Tab' });
    expect(document.activeElement).toBe(select(/Radarr instance/));
    await fireEvent.keyDown(select(/Radarr instance/), { key: 'Tab', shiftKey: true });
    expect(document.activeElement).toBe(last);
  });
});
