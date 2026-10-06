<script lang="ts">
  import '../app.css';
  import { onMount, tick } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/state';
  import { focusManager } from '$lib/focus/manager';
  import { api, type NotificationEvent, type SignOutReason } from '$lib/api';
  import { closeApp, isEntryRoute, rootBackHandler } from '$lib/appExit';
  import ExitPopup from '$lib/components/ExitPopup.svelte';
  import { clientName } from '$lib/device';
  import {
    PLAYBACK_TRANSFER_EVENT,
    events,
    isTransferForMe,
    parseTransfer,
    shouldRedialAfterHidden,
    streamWanted,
  } from '$lib/events';
  import { forgetHistory, goBack, playItem, resetStack } from '$lib/nav';
  import { ensureNavGates } from '$lib/navGates';
  import { forgetSearch } from '$lib/searchMemory';
  import { ensureFreshAssetToken, type AssetTokenDeps } from '$lib/assetToken';
  import { startGamepadKeys } from '$lib/gamepad';
  import { listenToShell } from '$lib/shell';

  let { children } = $props();

  // The first screen waits for a stale asset token to be renewed (lib/
  // assetToken): every poster on it carries that token, and an expired one
  // (a TV signed in more than a day ago) left them all broken. Capped, and
  // immediate when the token is fresh.
  const assetTokenDeps: AssetTokenDeps = {
    accessToken: () => api.getToken(),
    assetToken: () => api.getAssetToken(),
    issuedAt: () => api.getAssetTokenIssuedAt(),
    now: () => Date.now(),
    refresh: () => api.refreshTokensOutcome(),
    online: () => navigator.onLine,
  };
  let booted = $state(false);

  // Back that no screen took (lib/appExit): the previous page, or on a
  // first screen (Home, Setup, Sign in) the exit popup. The popup puts the
  // ring back where it was when cancelled.
  let exitOpen = $state(false);
  let exitReturn: HTMLElement | null = null;

  const onRootBack = rootBackHandler({
    routeId: () => page.route.id,
    goBack: () => goBack(),
    openExitPopup: () => {
      if (exitOpen) return;
      exitReturn = focusManager.currentElement();
      exitOpen = true;
    },
  });

  async function cancelExit() {
    exitOpen = false;
    const back = exitReturn;
    exitReturn = null;
    await tick();
    // What the page put the ring on behind the popup (a Back restore of the
    // hub that finished while it was up) when nothing had it before.
    const behind = focusManager.takeBehindModal();
    if (back && document.body.contains(back)) focusManager.focus(back);
    else if (behind) focusManager.focus(behind);
    // Nothing had the ring when the popup opened: the page is still placing
    // it (the hub restoring after a Back). refocus() would put it on the
    // Home pill and end that restore (RestoreGuard); the D-pad brings the
    // ring up meanwhile.
    else if (!back) return;
    else focusManager.refocus();
  }

  // Exit: the shell closes the app (lib/appExit). Where nothing does (a
  // desktop browser) the popup goes as Cancel would, rather than stay up.
  function exitApp() {
    closeApp();
    void cancelExit();
  }

  // Off the first screens (a "play on this TV" opening the player under
  // it), the popup goes: Back there is the page's again.
  $effect(() => {
    if (exitOpen && !isEntryRoute(page.route.id)) {
      exitOpen = false;
      exitReturn = null;
    }
  });

  // The app's one event stream ($lib/events) follows the route: open on every
  // signed-in screen, closed on login / setup / pairing, where sign-out,
  // forgetting the server and every Unauthorized in the app end up. It used
  // to be the player's, so "play on this TV" sent to a TV on the hub (the
  // usual case) went nowhere.
  $effect(() => {
    if (streamWanted(page.route.id, !!api.getToken())) events.start();
    else events.stop();
  });

  // "Play on this TV" from another of the user's devices (POST
  // /playback/transfer). The event reaches every app of the user; this TV
  // acts only when it is the one named (isTransferForMe). Like Android's
  // showPlayback: the back stack starts over at the hub, and the item plays
  // from the sender's position, from anywhere in the app. An open player
  // gives way to it (another item's route remounts the player, and its
  // cleanup reports and ends the old session; the same item gets a fresh
  // player through playerEpoch). Not into a TV in the background (Home, or
  // another app up): it would start a stream nobody sees, as Android's
  // stopped activity drops it too.
  function onTransfer(evt: NotificationEvent) {
    if (document.hidden || !isTransferForMe(evt, clientName())) return;
    const t = parseTransfer(evt);
    if (!t) return;
    resetStack('#/hub');
    playItem(t.itemId, t.positionMs, { push: false });
  }

  // Back from standby or another app after a while, or the network back:
  // the connection from before may be half-open and silent for good, so
  // dial a fresh one (see REDIAL_AFTER_HIDDEN_MS). The top-nav pills are
  // asked again too when their answers failed or have aged (a page's own
  // bar asks only when it mounts).
  let hiddenAt = 0;
  function onVisibilityChange() {
    if (document.hidden) {
      hiddenAt = Date.now();
      return;
    }
    if (hiddenAt && shouldRedialAfterHidden(Date.now() - hiddenAt)) events.restart();
    hiddenAt = 0;
    // A day in the background outlives the asset token: renewed now, so the
    // next images load (the event stream's reconnect would only renew it
    // after a failed dial).
    void ensureFreshAssetToken(assetTokenDeps);
    void ensureNavGates();
  }
  function onOnline() {
    events.restart();
    void ensureNavGates();
  }

  // A refresh came back as the server's verdict that this sign-in is dead
  // (revoked, expired, signed out everywhere): to sign-in, where every
  // Unauthorized in the app goes. Whoever found out: the stream's reconnect
  // on a TV idle on the hub, or a heartbeat mid-film, whose failures the
  // player otherwise swallows while the film plays on unsaved. Not over a
  // sign-in made since (its tokens replaced the dead ones), nor from a
  // signed-out screen.
  function onSessionRejected() {
    if (!api.getToken() && streamWanted(page.route.id, true)) goto('#/login');
  }

  // The stored sign-in was cleared, however: close the stream at once,
  // before the route even changes, and drop what belonged to that user
  // (Back history, focus notes, the last search; after Change server those
  // would also point into the old server). Which top-nav pills apply is
  // dropped by lib/navGates itself, on the same signal.
  function onSignedOut(reason: SignOutReason) {
    events.stop();
    forgetHistory();
    forgetSearch();
    if (reason === 'rejected') onSessionRejected();
  }

  onMount(() => {
    void ensureFreshAssetToken(assetTokenDeps).finally(() => (booted = true));
    focusManager.init();
    // A controller through the Gamepad API (a PC); a console delivers its
    // own gamepad key events and this stands down (lib/gamepad).
    startGamepadKeys();
    // The shell's messages: B as a system Back request (lib/shell).
    listenToShell();
    const offRootBack = focusManager.setRootBack(onRootBack);
    const offTransfer = events.subscribe(PLAYBACK_TRANSFER_EVENT, onTransfer);
    const offSignedOut = api.onSignedOut(onSignedOut);
    const offRejected = events.onRejected(onSessionRejected);
    document.addEventListener('visibilitychange', onVisibilityChange);
    window.addEventListener('online', onOnline);
    return () => {
      offRootBack();
      offTransfer();
      offSignedOut();
      offRejected();
      document.removeEventListener('visibilitychange', onVisibilityChange);
      window.removeEventListener('online', onOnline);
      events.stop();
      focusManager.destroy();
    };
  });
</script>

<main class="tv-root">
  {#if booted}
    {@render children()}
  {/if}
  {#if exitOpen}
    <ExitPopup onexit={exitApp} oncancel={() => void cancelExit()} />
  {/if}
</main>

<style>
  /* Always 1920x1080, scaled to the view and centred (app.html sets the
     variables). Fixed-position overlays inside it scale with it, since a
     transformed box is their containing block. */
  .tv-root {
    width: 1920px;
    height: 1080px;
    position: absolute;
    left: 0;
    top: 0;
    overflow: hidden;
    transform-origin: top left;
    transform: translate(var(--tv-x, 0px), var(--tv-y, 0px)) scale(var(--tv-scale, 1));
    background: var(--bg-primary);
    color: var(--text-primary);
  }
</style>
