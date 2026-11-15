package tv.onscreen.android.ui.setup

import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.Button
import android.widget.TextView
import androidx.fragment.app.Fragment
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.lifecycleScope
import dagger.hilt.android.AndroidEntryPoint
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import tv.onscreen.android.R
import tv.onscreen.android.data.prefs.ServerPrefs
import tv.onscreen.android.data.repository.AuthRepository
import tv.onscreen.android.ui.MainActivity
import tv.onscreen.android.ui.NavigationDestination
import javax.inject.Inject

/**
 * Device-pairing sign-in screen. Shows a URL + 6-digit PIN; the
 * user opens the URL on their phone or laptop, signs in there
 * (with whatever auth provider their server supports — local /
 * LDAP / OIDC / OAuth / SAML — that's the whole point of doing
 * this on the web), and types the PIN. The TV polls the server in
 * the background and lands on Home once the user finishes.
 *
 * Why pairing instead of in-TV auth: TVs don't have ergonomic
 * browsers, OIDC/OAuth need a redirect dance, SAML needs an IdP
 * round-trip. By punting the auth flow to a real browser we get
 * full-fidelity support for every provider OnScreen knows about,
 * with one TV implementation. Plex / Disney+ / Netflix all do the
 * same thing for the same reason.
 *
 * Lifecycle:
 *   - onViewCreated kicks off the PIN-create call.
 *   - On success the polling loop runs at the server-suggested
 *     cadence (clamped 2-10 s) until either Done, Expired, or the
 *     fragment is torn down.
 *   - Done → persist tokens via AuthRepository → navigate to Home.
 *   - Expired → re-create a fresh code automatically (no user
 *     action needed beyond noticing the new PIN).
 *   - Failure → show "couldn't reach server, retrying..." in the
 *     status line and keep polling. Manual cancel via Back exits.
 *   - The poll runs on the view's scope, so it carries on while the app
 *     is stopped (every [STOPPED_POLL_MS]); MainActivity keeps this screen
 *     (not Login) when it routes back in, so the code on it survives HOME
 *     and the screensaver.
 *   - Not forever: it used to poll every 1-3 s and mint a new code every
 *     10 minutes for as long as the screen stayed up, the app stopped
 *     included. No code is minted while the app is stopped, and at most
 *     [MAX_CODES] per stay on screen (the [KEEP_SCREEN_ON_MS] window); then
 *     the screen says the code expired and waits. Coming back to it (HOME
 *     and back, the screensaver ending) starts a fresh code.
 */
@AndroidEntryPoint
class PairingFragment : Fragment() {

    @Inject lateinit var authRepo: AuthRepository
    @Inject lateinit var prefs: ServerPrefs

    private var pollJob: Job? = null

    /** The server the codes are for, once read (null until then). */
    private var cycleServer: String? = null

    /** Codes minted since this screen last came into view (onStart). */
    private var codesMinted = 0

    private lateinit var urlView: TextView
    private lateinit var pinView: TextView
    private lateinit var statusView: TextView
    private lateinit var backBtn: Button

    override fun onCreateView(inflater: LayoutInflater, container: ViewGroup?, saved: Bundle?): View {
        return inflater.inflate(R.layout.fragment_pairing, container, false)
    }

    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        super.onViewCreated(view, savedInstanceState)
        urlView = view.findViewById(R.id.pair_url)
        pinView = view.findViewById(R.id.pair_pin)
        statusView = view.findViewById(R.id.pair_status)
        backBtn = view.findViewById(R.id.pair_back)

        backBtn.setOnClickListener {
            (activity as? MainActivity)?.navigateTo(NavigationDestination.LOGIN)
        }
        backBtn.requestFocus()

        // No screensaver over the code while the user signs in on their
        // phone: some TVs start theirs after a minute without a key press,
        // hiding the code mid-typing. For a while only, so a code left on
        // screen doesn't keep the TV lit for good.
        view.keepScreenOn = true
        viewLifecycleOwner.lifecycleScope.launch {
            delay(KEEP_SCREEN_ON_MS)
            view.keepScreenOn = false
        }

        viewLifecycleOwner.lifecycleScope.launch {
            val server = prefs.serverUrl.first().orEmpty()
            // Show the URL up front so the user can start walking to
            // their phone while we fetch the PIN.
            urlView.text = "$server/pair"
            cycleServer = server
            startPairingCycle(server)
        }
    }

    override fun onStart() {
        super.onStart()
        // Back in view with no code running (the code ran out while the app
        // was stopped, or MAX_CODES were used up): a fresh one, and a fresh
        // allowance.
        codesMinted = 0
        val server = cycleServer ?: return
        if (view != null && pollJob?.isActive != true) startPairingCycle(server)
    }

    /** True while this screen is in view (the app not stopped). */
    private fun inView(): Boolean =
        viewLifecycleOwner.lifecycle.currentState.isAtLeast(Lifecycle.State.STARTED)

    /** No code running: the last one expired with nobody in view, or after
     *  [MAX_CODES]. onStart starts a new one. */
    private fun showIdle() {
        pinView.text = ""
        statusView.text = getString(R.string.pair_status_idle)
    }

    /** Allocate a fresh PIN + start the poll loop. Re-entered when
     *  the previous PIN expires so the user never sees a stale
     *  "Code expired" terminal state — they see a new code — while the
     *  screen is in view and [MAX_CODES] isn't reached. */
    private fun startPairingCycle(server: String, refreshed: Boolean = false) {
        pollJob?.cancel()
        pollJob = viewLifecycleOwner.lifecycleScope.launch {
            statusView.text = getString(R.string.pair_status_creating)
            val code = try {
                authRepo.startPairing()
            } catch (e: Exception) {
                statusView.text = getString(R.string.pair_status_create_failed, e.message ?: "")
                // Auto-retry after a beat so a transient network
                // hiccup doesn't strand the user on a dead screen — while
                // someone can see it (onStart retries otherwise).
                delay(5_000)
                if (inView()) startPairingCycle(server)
                return@launch
            }
            codesMinted++

            // startPairing() may have healed a stale cleartext origin to TLS
            // (AuthRepository.ensureTlsUpgradedOrigin), and urlView was drawn
            // from the PRE-heal prefs. Re-read so the address we tell the user
            // to type is the origin we actually talk to — a field report
            // showed "http://…/pair" on glass for a server that answers on
            // https. A browser survives the redirect, but the screen
            // shouldn't instruct users to type a URL the app itself refuses
            // to use.
            urlView.text = "${prefs.serverUrl.first().orEmpty()}/pair"
            pinView.text = code.pin
            // If we just rotated an expired code, say so loudly — otherwise the
            // displayed PIN silently changes under a user who's mid-typing the old
            // one on their phone, and their entry mysteriously fails.
            statusView.text = getString(
                if (refreshed) R.string.pair_status_refreshed else R.string.pair_status_waiting,
            )

            // Server suggests a cadence; clamp tighter than the
            // server-default 2 s so the fragment dismisses within
            // ~1 s of the user typing the PIN on their phone.
            // Earlier loop used 2-10 s and the fragment lingered
            // visibly after sign-in completed; users perceived
            // that as "I had to press a button to dismiss it."
            val pollSeconds = code.poll_after.coerceIn(1, 3)
            while (isActive) {
                when (val result = authRepo.pollPairing(code.device_token)) {
                    is AuthRepository.PollResult.Pending -> {
                        // Still pending — sleep then retry.
                    }
                    is AuthRepository.PollResult.Done -> {
                        authRepo.completePairing(result.pair)
                        (activity as? MainActivity)?.navigateTo(NavigationDestination.HOME)
                        return@launch
                    }
                    is AuthRepository.PollResult.Expired -> {
                        // Nobody in view, or the screen has been up through
                        // MAX_CODES codes: stop here rather than mint codes
                        // (and poll) for good. onStart starts a new one.
                        if (!inView() || codesMinted >= MAX_CODES) {
                            showIdle()
                            return@launch
                        }
                        // Cycle a new PIN — friendlier than a
                        // dead-end "code expired" terminal state.
                        // pollJob cancellation is automatic via
                        // the re-entrant call. refreshed=true so the
                        // new PIN is announced, not silently swapped.
                        startPairingCycle(server, refreshed = true)
                        return@launch
                    }
                    is AuthRepository.PollResult.Failure -> {
                        statusView.text = getString(R.string.pair_status_retry, result.reason)
                        // Keep polling — a network blip resolves
                        // itself; the user can hit Cancel to bail.
                    }
                }
                // Sleep AFTER the poll, not before, so the very
                // first poll fires immediately on entering the
                // loop. Catches the case where the user signed in
                // on the phone before the TV finished rendering
                // this fragment. Slower while the app is stopped: a code
                // claimed then (the screensaver came on mid-typing) waits
                // pairClaimTTL (5 min) on the server for this poll.
                delay(if (inView()) pollSeconds * 1000L else STOPPED_POLL_MS)
            }
        }
    }

    override fun onDestroyView() {
        super.onDestroyView()
        pollJob?.cancel()
        pollJob = null
    }

    private companion object {
        /** How long the pairing screen holds the screen on. */
        const val KEEP_SCREEN_ON_MS = 30 * 60 * 1000L

        /** Codes per stay on screen: each lives 10 minutes on the server
         *  (pairCodeTTL), so three cover the [KEEP_SCREEN_ON_MS] window. */
        const val MAX_CODES = 3

        /** Poll cadence while the app is stopped. */
        const val STOPPED_POLL_MS = 10_000L
    }
}
