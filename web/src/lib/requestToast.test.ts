import { createdRequestToast, wasAutoApproved } from './requestToast';
import { setLocale } from '$lib/i18n/index.svelte';

describe('createdRequestToast', () => {
  it('reports over-quota requests as info', () => {
    const t = createdRequestToast({ id: 'r', status: 'pending', over_quota: true } as never, 'Show');
    expect(t.kind).toBe('info');
    expect(t.text).toBe("You've reached your limit — this request needs admin approval: Show");
  });

  it('says whether it was approved automatically', () => {
    expect(createdRequestToast({ id: 'r', status: 'downloading', auto_approved: true } as never, 'Show')).toEqual({
      kind: 'success',
      text: "Approved automatically — it's on its way: Show",
    });
    expect(createdRequestToast({ id: 'r', status: 'pending' } as never, 'Show').text).toBe(
      'Requested: Show — awaiting admin approval',
    );
    expect(wasAutoApproved({ status: 'approved', auto_approved: false })).toBe(true);
  });
});

describe('translations', () => {
  it('follows the interface language', async () => {
    await setLocale('es');
    try {
      expect(createdRequestToast({ id: 'r', status: 'pending' } as never, 'Show').text).toBe(
        'Pedido: Show. Esperando la aprobación de un administrador',
      );
    } finally {
      await setLocale('en');
    }
  });
});
