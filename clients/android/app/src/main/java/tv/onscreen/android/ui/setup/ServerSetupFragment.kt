package tv.onscreen.android.ui.setup

import android.os.Bundle
import android.widget.Toast
import androidx.leanback.app.GuidedStepSupportFragment
import androidx.leanback.widget.GuidanceStylist
import androidx.leanback.widget.GuidedAction
import androidx.lifecycle.lifecycleScope
import dagger.hilt.android.AndroidEntryPoint
import kotlinx.coroutines.launch
import tv.onscreen.android.R
import kotlinx.coroutines.flow.first
import tv.onscreen.android.data.prefs.ServerPrefs
import tv.onscreen.android.data.repository.AuthRepository
import tv.onscreen.android.ui.MainActivity
import tv.onscreen.android.ui.NavigationDestination
import javax.inject.Inject

@AndroidEntryPoint
class ServerSetupFragment : GuidedStepSupportFragment() {

    @Inject lateinit var authRepo: AuthRepository

    @Inject lateinit var prefs: ServerPrefs

    companion object {
        private const val ACTION_URL = 1L
        private const val ACTION_CONNECT = 2L
        private const val ACTION_CANCEL = 3L
    }

    override fun onCreateGuidance(savedInstanceState: Bundle?): GuidanceStylist.Guidance {
        // Show the example URL in the guidance text rather than as
        // the action's default value (see comment in onCreateActions).
        val description = getString(R.string.server_url_description) +
            "\n\n" + getString(R.string.server_url_example, getString(R.string.server_url_hint))
        return GuidanceStylist.Guidance(
            getString(R.string.server_url_title),
            description,
            getString(R.string.app_name),
            null,
        )
    }

    override fun onCreateActions(actions: MutableList<GuidedAction>, savedInstanceState: Bundle?) {
        actions.add(
            GuidedAction.Builder(requireContext())
                .id(ACTION_URL)
                .title(getString(R.string.server_url_title))
                // Leave description blank. Earlier versions used the
                // example URL ("http://192.168.1.100:7070") as the
                // description and treated it as a placeholder hint —
                // but Leanback's GuidedAction has no real placeholder
                // concept, so the example string was the LITERAL
                // default value. A user tapping Connect without
                // editing ended up pointing the app at that
                // non-existent LAN host. The example now lives in
                // the guidance description instead, where it's
                // informational rather than load-bearing.
                .descriptionEditable(true)
                .descriptionEditInputType(
                    android.text.InputType.TYPE_CLASS_TEXT or android.text.InputType.TYPE_TEXT_VARIATION_URI
                )
                .build()
        )
        actions.add(
            GuidedAction.Builder(requireContext())
                .id(ACTION_CONNECT)
                .title(getString(R.string.connect))
                .build()
        )
        // Escape hatch + prefill for an install that ALREADY has a server:
        // reached from Login's "Change server", this screen used to arrive
        // empty with no way back — BACK exited the app, making a mistaken
        // click a full re-type of the URL. Prefill the current URL (editing
        // beats re-typing on a D-pad) and offer an explicit Cancel.
        lifecycleScope.launch {
            val stored = prefs.serverUrl.first()
            if (!stored.isNullOrBlank()) {
                findActionById(ACTION_URL)?.let { a ->
                    if (a.description.isNullOrEmpty()) {
                        a.description = stored
                        notifyActionChanged(findActionPositionById(ACTION_URL))
                    }
                }
                if (findActionById(ACTION_CANCEL) == null) {
                    actions.add(
                        GuidedAction.Builder(requireContext())
                            .id(ACTION_CANCEL)
                            .title(getString(R.string.cancel))
                            .build()
                    )
                    setActionsDiffCallback(null)
                    setActions(actions)
                }
            }
        }
    }

    /** True while a connect probe is in flight. Guards against the second press
     *  a user makes when nothing on screen acknowledged the first. */
    private var connecting = false

    override fun onGuidedActionClicked(action: GuidedAction) {
        if (action.id == ACTION_CANCEL) {
            // Only offered when a server is already configured — return to
            // the sign-in screen for it.
            (activity as? MainActivity)?.navigateTo(NavigationDestination.LOGIN)
            return
        }
        if (action.id != ACTION_CONNECT) return
        if (connecting) return

        val urlAction = findActionById(ACTION_URL) ?: return
        val url = urlAction.description?.toString()?.trim() ?: return

        // These are INPUT problems, not connection problems. Both used to show
        // "Could not connect to server", which sent the user off checking their
        // network for a mistake that was in the text field.
        if (url.isEmpty()) {
            Toast.makeText(requireContext(), getString(R.string.error_url_required), Toast.LENGTH_LONG).show()
            return
        }
        // Guard against the previous-behaviour artifact: if a user
        // upgrades and somehow still sees the example URL pre-filled,
        // refuse to save it. The example points at a private LAN
        // address that won't resolve over Cloudflare / public DNS.
        if (url == getString(R.string.server_url_hint)) {
            Toast.makeText(requireContext(), getString(R.string.error_url_is_example), Toast.LENGTH_LONG).show()
            return
        }

        // The probe can take up to the OkHttp connect timeout on a typo'd LAN
        // address, and nothing on screen used to change for the whole of it —
        // the single most likely moment a first-time user concludes the app or
        // their remote is broken.
        setConnecting(true)
        lifecycleScope.launch {
            val reachable = authRepo.checkServer(url)
            setConnecting(false)
            if (reachable) {
                (activity as? MainActivity)?.navigateTo(NavigationDestination.LOGIN)
            } else {
                Toast.makeText(requireContext(), getString(R.string.error_connection), Toast.LENGTH_LONG).show()
            }
        }
    }

    private fun setConnecting(busy: Boolean) {
        connecting = busy
        val a = findActionById(ACTION_CONNECT) ?: return
        a.title = getString(if (busy) R.string.connecting else R.string.connect)
        // isEnabled alone. Do NOT also clear isFocusable: the stylist maps
        // that to setFocusable(false) on the row that currently HOLDS focus,
        // and View.setFlags then calls clearFocus(refocus = false) — the
        // parent is explicitly told not to hand focus anywhere. The whole
        // screen lost its focus ring for the duration of the request, and a
        // user pressing UP/DOWN mid-request got nothing. isEnabled already
        // dims the row and already blocks a re-press, and the busy flag
        // guards the handler regardless.
        a.isEnabled = !busy
        notifyActionChanged(findActionPositionById(ACTION_CONNECT))
    }
}
