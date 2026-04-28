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
import tv.onscreen.android.data.repository.AuthRepository
import tv.onscreen.android.ui.MainActivity
import tv.onscreen.android.ui.NavigationDestination
import javax.inject.Inject

@AndroidEntryPoint
class ServerSetupFragment : GuidedStepSupportFragment() {

    @Inject lateinit var authRepo: AuthRepository

    companion object {
        private const val ACTION_URL = 1L
        private const val ACTION_CONNECT = 2L
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
    }

    override fun onGuidedActionClicked(action: GuidedAction) {
        if (action.id != ACTION_CONNECT) return

        val urlAction = findActionById(ACTION_URL) ?: return
        val url = urlAction.description?.toString()?.trim() ?: return

        if (url.isEmpty()) {
            Toast.makeText(requireContext(), getString(R.string.error_connection), Toast.LENGTH_SHORT).show()
            return
        }
        // Guard against the previous-behaviour artifact: if a user
        // upgrades and somehow still sees the example URL pre-filled,
        // refuse to save it. The example points at a private LAN
        // address that won't resolve over Cloudflare / public DNS.
        if (url == getString(R.string.server_url_hint)) {
            Toast.makeText(requireContext(), getString(R.string.error_connection), Toast.LENGTH_SHORT).show()
            return
        }

        lifecycleScope.launch {
            val reachable = authRepo.checkServer(url)
            if (reachable) {
                (activity as? MainActivity)?.navigateTo(NavigationDestination.LOGIN)
            } else {
                Toast.makeText(requireContext(), getString(R.string.error_connection), Toast.LENGTH_SHORT).show()
            }
        }
    }
}
