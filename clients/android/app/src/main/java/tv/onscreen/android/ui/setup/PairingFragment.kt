package tv.onscreen.android.ui.setup

import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.Button
import android.widget.TextView
import androidx.fragment.app.Fragment
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
 */
@AndroidEntryPoint
class PairingFragment : Fragment() {

    @Inject lateinit var authRepo: AuthRepository
    @Inject lateinit var prefs: ServerPrefs

    private var pollJob: Job? = null

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

        viewLifecycleOwner.lifecycleScope.launch {
            val server = prefs.serverUrl.first().orEmpty()
            // Show the URL up front so the user can start walking to
            // their phone while we fetch the PIN.
            urlView.text = "$server/pair"
            startPairingCycle(server)
        }
    }

    /** Allocate a fresh PIN + start the poll loop. Re-entered when
     *  the previous PIN expires so the user never sees a stale
     *  "Code expired" terminal state — they see a new code. */
    private fun startPairingCycle(server: String) {
        pollJob?.cancel()
        pollJob = viewLifecycleOwner.lifecycleScope.launch {
            statusView.text = getString(R.string.pair_status_creating)
            val code = try {
                authRepo.startPairing()
            } catch (e: Exception) {
                statusView.text = getString(R.string.pair_status_create_failed, e.message ?: "")
                // Auto-retry after a beat so a transient network
                // hiccup doesn't strand the user on a dead screen.
                delay(5_000)
                startPairingCycle(server)
                return@launch
            }

            pinView.text = code.pin
            statusView.text = getString(R.string.pair_status_waiting)

            // Server suggests a cadence; clamp to keep a misbehaving
            // server from pinning the radio at one-poll-per-tick or
            // letting the loop stall for minutes on end.
            val pollSeconds = code.poll_after.coerceIn(2, 10)
            while (isActive) {
                delay(pollSeconds * 1000L)
                when (val result = authRepo.pollPairing(code.device_token)) {
                    is AuthRepository.PollResult.Pending -> {
                        // Keep showing the waiting state.
                    }
                    is AuthRepository.PollResult.Done -> {
                        authRepo.completePairing(result.pair)
                        (activity as? MainActivity)?.navigateTo(NavigationDestination.HOME)
                        return@launch
                    }
                    is AuthRepository.PollResult.Expired -> {
                        // Cycle a new PIN — much friendlier than
                        // making the user click Retry. The cancellation
                        // is automatic via pollJob.cancel() in this
                        // re-entrant call.
                        startPairingCycle(server)
                        return@launch
                    }
                    is AuthRepository.PollResult.Failure -> {
                        statusView.text = getString(R.string.pair_status_retry, result.reason)
                        // Keep polling — a network blip resolves
                        // itself; the user can hit Cancel to bail.
                    }
                }
            }
        }
    }

    override fun onDestroyView() {
        super.onDestroyView()
        pollJob?.cancel()
        pollJob = null
    }
}
