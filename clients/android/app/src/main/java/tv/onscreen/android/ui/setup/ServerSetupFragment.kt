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
        return GuidanceStylist.Guidance(
            getString(R.string.server_url_title),
            getString(R.string.server_url_description),
            getString(R.string.app_name),
            null,
        )
    }

    override fun onCreateActions(actions: MutableList<GuidedAction>, savedInstanceState: Bundle?) {
        actions.add(
            GuidedAction.Builder(requireContext())
                .id(ACTION_URL)
                .title(getString(R.string.server_url_title))
                .description(getString(R.string.server_url_hint))
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
