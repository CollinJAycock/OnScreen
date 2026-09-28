// How the bell-icon panel presents each notification type: an icon/colour
// "kind" and where clicking it goes. Types mirror internal/notification/types.go.

export type NotificationKind =
  | 'new_content'
  | 'scan_complete'
  | 'request_waiting' // request_created: yours is queued for an admin
  | 'request_pending' // request_pending: (admins) one is waiting for you
  | 'request_approved'
  | 'request_available'
  | 'request_problem' // request_declined / request_failed
  | 'issue_reported' // (admins) a user reported a problem with an item
  | 'issue_resolved' // your problem report was resolved / closed
  | 'other';

export function notificationKind(type: string | null | undefined): NotificationKind {
  switch (type) {
    case 'new_content':
      return 'new_content';
    case 'scan_complete':
      return 'scan_complete';
    case 'request_created':
      return 'request_waiting';
    case 'request_pending':
      return 'request_pending';
    case 'request_approved':
      return 'request_approved';
    case 'request_available':
    case 'request_season_available': // one season of a TV request landed
      return 'request_available';
    case 'request_declined':
    case 'request_failed':
      return 'request_problem';
    case 'issue_reported':
      return 'issue_reported';
    case 'issue_resolved':
      return 'issue_resolved';
    default:
      // A future request_* type still belongs on the Requests page.
      return type?.startsWith('request_') ? 'request_waiting' : 'other';
  }
}

/** Where a click on the notification goes, or null for nowhere. A fulfilled
 *  request opens the title it became; every other request notice opens the
 *  Requests page; anything else with an item opens that item. */
export function notificationHref(n: { type: string; item_id?: string | null }): string | null {
  if ((n.type === 'request_available' || n.type === 'request_season_available') && n.item_id) return `/watch/${n.item_id}`;
  if (n.type?.startsWith('request_')) return '/requests';
  // A new problem report opens the admin queue; a closed one opens the item.
  if (n.type === 'issue_reported') return '/settings/library-health';
  if (n.item_id) return `/watch/${n.item_id}`;
  return null;
}

// 20×20 solid icon paths (fill-rule evenodd).
export const NOTIFICATION_ICONS: Record<NotificationKind, string> = {
  new_content:
    'M10 18a8 8 0 100-16 8 8 0 000 16zm.75-11.25a.75.75 0 00-1.5 0v2.5h-2.5a.75.75 0 000 1.5h2.5v2.5a.75.75 0 001.5 0v-2.5h2.5a.75.75 0 000-1.5h-2.5v-2.5z',
  scan_complete:
    'M16.704 4.153a.75.75 0 01.143 1.052l-8 10.5a.75.75 0 01-1.127.075l-4.5-4.5a.75.75 0 011.06-1.06l3.894 3.893 7.48-9.817a.75.75 0 011.05-.143z',
  request_waiting:
    'M10 18a8 8 0 100-16 8 8 0 000 16zm.75-13a.75.75 0 00-1.5 0v5c0 .414.336.75.75.75h3a.75.75 0 000-1.5h-2.25V5z',
  request_pending:
    'M10 18a8 8 0 100-16 8 8 0 000 16zM10 7a1 1 0 011 1v2h2a1 1 0 110 2h-2v2a1 1 0 11-2 0v-2H7a1 1 0 110-2h2V8a1 1 0 011-1z',
  request_approved:
    'M10 18a8 8 0 100-16 8 8 0 000 16zm3.857-9.809a.75.75 0 00-1.214-.882l-3.483 4.79-1.88-1.88a.75.75 0 10-1.06 1.061l2.5 2.5a.75.75 0 001.137-.089l4-5.5z',
  request_available:
    'M2 10a8 8 0 1116 0 8 8 0 01-16 0zm6.39-2.908a.75.75 0 01.766.027l3.5 2.25a.75.75 0 010 1.262l-3.5 2.25A.75.75 0 018 12.25v-4.5a.75.75 0 01.39-.658z',
  request_problem:
    'M10 18a8 8 0 100-16 8 8 0 000 16zM8.28 7.22a.75.75 0 00-1.06 1.06L8.94 10l-1.72 1.72a.75.75 0 101.06 1.06L10 11.06l1.72 1.72a.75.75 0 101.06-1.06L11.06 10l1.72-1.72a.75.75 0 00-1.06-1.06L10 8.94 8.28 7.22z',
  issue_reported:
    'M8.485 2.495c.673-1.167 2.357-1.167 3.03 0l6.28 10.875c.673 1.167-.17 2.625-1.516 2.625H3.72c-1.347 0-2.189-1.458-1.515-2.625L8.485 2.495zM10 5a.75.75 0 01.75.75v3.5a.75.75 0 01-1.5 0v-3.5A.75.75 0 0110 5zm0 9a1 1 0 100-2 1 1 0 000 2z',
  issue_resolved:
    'M16.704 4.153a.75.75 0 01.143 1.052l-8 10.5a.75.75 0 01-1.127.075l-4.5-4.5a.75.75 0 011.06-1.06l3.894 3.893 7.48-9.817a.75.75 0 011.05-.143z',
  other:
    'M18 10a8 8 0 11-16 0 8 8 0 0116 0zm-7-4a1 1 0 11-2 0 1 1 0 012 0zM9 9a.75.75 0 000 1.5h.253a.25.25 0 01.244.304l-.459 2.066A1.75 1.75 0 0010.747 15H11a.75.75 0 000-1.5h-.253a.25.25 0 01-.244-.304l.459-2.066A1.75 1.75 0 009.253 9H9z',
};
