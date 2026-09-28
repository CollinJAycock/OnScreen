// Pure helpers for Settings → Notifications (outbound notification agents).
// Kept out of the component so the form logic — defaults per kind, the
// write-only secret, what a PATCH carries — is unit-testable.
import type {
  NotificationAgent,
  NotificationAgentConfig,
  NotificationAgentCreateInput,
  NotificationAgentEvent,
  NotificationAgentKind,
  NotificationAgentUpdateInput,
} from '$lib/api';
import { relativeAge } from '$lib/reportProblem';

export interface KindInfo {
  kind: NotificationAgentKind;
  label: string;
  blurb: string;
}

export const KINDS: KindInfo[] = [
  { kind: 'discord', label: 'Discord', blurb: 'Post to a channel through a webhook.' },
  { kind: 'telegram', label: 'Telegram', blurb: 'Message a group or channel through your bot.' },
  { kind: 'ntfy', label: 'ntfy', blurb: 'Push to phones via ntfy.sh or your own ntfy server.' },
  { kind: 'gotify', label: 'Gotify', blurb: 'Push to your self-hosted Gotify server.' },
  { kind: 'email', label: 'Email', blurb: 'Email a list of addresses using the server’s SMTP settings.' },
];

export function kindLabel(kind: NotificationAgentKind): string {
  return KINDS.find((k) => k.kind === kind)?.label ?? kind;
}

/** Kinds that may point at a server on the LAN (allow_private_network). */
export function selfHostable(kind: NotificationAgentKind): boolean {
  return kind === 'ntfy' || kind === 'gotify';
}

export interface SecretField {
  label: string;
  placeholder: string;
  hint: string;
  /** Required when creating (an optional secret can be left out). */
  required: boolean;
}

/** The kind's write-only credential field, or null when it has none. */
export function secretField(kind: NotificationAgentKind): SecretField | null {
  switch (kind) {
    case 'discord':
      return {
        label: 'Webhook URL',
        placeholder: 'https://discord.com/api/webhooks/…',
        hint: 'Channel settings → Integrations → Webhooks → Copy Webhook URL. Treated as a secret.',
        required: true,
      };
    case 'telegram':
      return {
        label: 'Bot token',
        placeholder: '123456789:AA…',
        hint: 'From @BotFather. Add the bot to your group first.',
        required: true,
      };
    case 'ntfy':
      return {
        label: 'Access token',
        placeholder: 'tk_…',
        hint: 'Only needed when the topic is protected.',
        required: false,
      };
    case 'gotify':
      return {
        label: 'App token',
        placeholder: 'Application token',
        hint: 'Gotify → Apps → create an application and copy its token.',
        required: true,
      };
    default:
      return null;
  }
}

export interface AgentForm {
  kind: NotificationAgentKind;
  name: string;
  enabled: boolean;
  events: string[];
  /** A NEW secret; blank keeps the stored one when editing. */
  secret: string;
  clearSecret: boolean;
  chatId: string;
  serverUrl: string;
  topic: string;
  /** Comma / newline separated addresses. */
  recipients: string;
  allowPrivateNetwork: boolean;
}

/** Events a new agent of this kind starts subscribed to. */
export function defaultEvents(kind: NotificationAgentKind, catalog: NotificationAgentEvent[]): string[] {
  return catalog.filter((e) => e.default_for.includes(kind)).map((e) => e.key);
}

export function emptyForm(kind: NotificationAgentKind, catalog: NotificationAgentEvent[]): AgentForm {
  return {
    kind,
    name: kindLabel(kind),
    enabled: true,
    events: defaultEvents(kind, catalog),
    secret: '',
    clearSecret: false,
    chatId: '',
    serverUrl: kind === 'ntfy' ? 'https://ntfy.sh' : '',
    topic: '',
    recipients: '',
    allowPrivateNetwork: false,
  };
}

export function formFromAgent(a: NotificationAgent): AgentForm {
  return {
    kind: a.kind,
    name: a.name,
    enabled: a.enabled,
    events: [...a.events],
    secret: '',
    clearSecret: false,
    chatId: a.config.chat_id ?? '',
    serverUrl: a.config.server_url ?? '',
    topic: a.config.topic ?? '',
    recipients: (a.config.recipients ?? []).join(', '),
    allowPrivateNetwork: a.allow_private_network,
  };
}

export function parseRecipients(s: string): string[] {
  return s
    .split(/[\s,;]+/)
    .map((r) => r.trim())
    .filter((r) => r !== '');
}

/** The non-secret config for the form's kind (other kinds' fields dropped). */
export function configFor(f: AgentForm): NotificationAgentConfig {
  switch (f.kind) {
    case 'telegram':
      return { chat_id: f.chatId.trim() };
    case 'ntfy':
      return { server_url: f.serverUrl.trim(), topic: f.topic.trim() };
    case 'gotify':
      return { server_url: f.serverUrl.trim() };
    case 'email':
      return { recipients: parseRecipients(f.recipients) };
    default:
      return {};
  }
}

export function createPayload(f: AgentForm): NotificationAgentCreateInput {
  const body: NotificationAgentCreateInput = {
    kind: f.kind,
    name: f.name.trim(),
    enabled: f.enabled,
    events: [...f.events],
    config: configFor(f),
  };
  if (f.secret.trim()) body.secret = f.secret.trim();
  if (selfHostable(f.kind)) body.allow_private_network = f.allowPrivateNetwork;
  return body;
}

/** A PATCH body: the secret only when a new one was typed (blank keeps it). */
export function updatePayload(f: AgentForm): NotificationAgentUpdateInput {
  const body: NotificationAgentUpdateInput = {
    name: f.name.trim(),
    enabled: f.enabled,
    events: [...f.events],
    config: configFor(f),
  };
  if (f.secret.trim()) body.secret = f.secret.trim();
  else if (f.clearSecret) body.clear_secret = true;
  if (selfHostable(f.kind)) body.allow_private_network = f.allowPrivateNetwork;
  return body;
}

function sameServer(a: string, b: string): boolean {
  const norm = (s: string) => s.trim().replace(/\/+$/, '').toLowerCase();
  return norm(a) === norm(b);
}

/**
 * Client-side checks mirroring the server's, for an immediate message. The
 * server stays authoritative (it also resolves hosts and checks formats).
 */
export function validateForm(f: AgentForm, editing: NotificationAgent | null): string | null {
  if (!f.name.trim()) return 'Name is required';
  if (f.events.length === 0) return 'Select at least one event';
  const secret = secretField(f.kind);
  const hasSecret = f.secret.trim() !== '' || (editing?.secret_configured === true && !f.clearSecret);
  if (secret?.required && !hasSecret) return `${secret.label} is required`;
  switch (f.kind) {
    case 'telegram':
      if (!f.chatId.trim()) return 'Chat ID is required';
      break;
    case 'ntfy':
      if (!f.serverUrl.trim()) return 'Server URL is required';
      if (!f.topic.trim()) return 'Topic is required';
      break;
    case 'gotify':
      if (!f.serverUrl.trim()) return 'Server URL is required';
      break;
    case 'email':
      if (parseRecipients(f.recipients).length === 0) return 'Add at least one recipient';
      break;
  }
  // Secrets don't follow endpoints: moving a self-hosted agent to another
  // server needs the token typed again (the server answers 422 otherwise).
  if (
    editing &&
    selfHostable(f.kind) &&
    editing.secret_configured &&
    !f.secret.trim() &&
    !f.clearSecret &&
    !sameServer(editing.config.server_url ?? '', f.serverUrl)
  ) {
    return `Re-enter the ${secret?.label.toLowerCase() ?? 'token'} when changing the server URL`;
  }
  return null;
}

export type StatusTone = 'ok' | 'error' | 'idle' | 'disabled';

export interface AgentStatus {
  tone: StatusTone;
  text: string;
  detail?: string;
}

/** Delivery status for the list: the newer of last success / last error. */
export function agentStatus(a: NotificationAgent, now: number = Date.now()): AgentStatus {
  const ok = a.last_success_at ? Date.parse(a.last_success_at) : NaN;
  const bad = a.last_error_at ? Date.parse(a.last_error_at) : NaN;
  let s: AgentStatus;
  if (!Number.isNaN(bad) && (Number.isNaN(ok) || bad > ok)) {
    s = { tone: 'error', text: `Last delivery failed ${relativeAge(a.last_error_at!, now)}`, detail: a.last_error ?? undefined };
  } else if (!Number.isNaN(ok)) {
    s = { tone: 'ok', text: `Last delivered ${relativeAge(a.last_success_at!, now)}` };
  } else {
    s = { tone: 'idle', text: 'Nothing sent yet' };
  }
  if (!a.enabled) s = { ...s, tone: 'disabled', text: `Disabled · ${s.text}` };
  return s;
}

/** One line saying where the agent sends — never the secret. */
export function destinationSummary(a: NotificationAgent): string {
  switch (a.kind) {
    case 'discord':
      return a.secret_configured ? 'Discord webhook configured' : 'Discord webhook missing';
    case 'telegram':
      return `Chat ${a.config.chat_id ?? '?'}`;
    case 'ntfy':
      return `${a.config.server_url ?? ''}/${a.config.topic ?? ''}`;
    case 'gotify':
      return a.config.server_url ?? '';
    case 'email':
      return (a.config.recipients ?? []).join(', ');
  }
  return '';
}

export const EVENT_GROUPS: { group: NotificationAgentEvent['group']; label: string }[] = [
  { group: 'requests', label: 'Requests' },
  { group: 'library', label: 'Library' },
  { group: 'server', label: 'Server' },
];

export function eventsInGroup(catalog: NotificationAgentEvent[], group: NotificationAgentEvent['group']): NotificationAgentEvent[] {
  return catalog.filter((e) => e.group === group);
}

export function eventLabel(catalog: NotificationAgentEvent[], key: string): string {
  return catalog.find((e) => e.key === key)?.label ?? key;
}

export function toggleEvent(events: string[], key: string): string[] {
  return events.includes(key) ? events.filter((e) => e !== key) : [...events, key];
}
