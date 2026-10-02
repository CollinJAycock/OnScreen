<script lang="ts">
  import '../app.css';
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/state';
  import { focusManager } from '$lib/focus/manager';
  import { api, type NotificationEvent, type SignOutReason } from '$lib/api';
  import { closeApp, rootBackHandler } from '$lib/appExit';
  import { registerTizenKeys } from '$lib/focus/keys';
  import { avplay } from '$lib/player/avplay';
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
  };
  let booted = $state(false);

  // Return that no screen took (lib/appExit): the previous page, or on a
  // first screen (Home, Setup, Sign in) closing the app, back to Smart Hub,
  // as Samsung's checklist asks.
  const onRootBack = rootBackHandler({
    routeId: () => page.route.id,
    goBack: () => goBack(),
    exit: () => closeApp(),
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
  // AVPlay's decoder stays bound across a trip to the background unless it
  // is released, and comes back black or stale: it is torn down on hide
  // (the live position kept) and re-opened there on show. True only when
  // something was playing, so other visibility flaps restore nothing.
  let suspendedForBackground = false;
  function onVisibilityChange() {
    if (document.hidden) {
      hiddenAt = Date.now();
      suspendedForBackground = avplay.suspend();
      return;
    }
    if (suspendedForBackground) {
      suspendedForBackground = false;
      avplay.restore();
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
    // The media keys, colour keys and channel rocker reach the app only
    // once asked for.
    registerTizenKeys();
    focusManager.init();
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
</main>

<style>
  .tv-root {
    width: 1920px;
    height: 1080px;
    position: relative;
    overflow: hidden;
    background: var(--bg-primary);
    color: var(--text-primary);
  }

  @media (max-width: 1919px) {
    .tv-root {
      width: 100vw;
      height: 100vh;
      transform-origin: top left;
    }
  }
</style>
