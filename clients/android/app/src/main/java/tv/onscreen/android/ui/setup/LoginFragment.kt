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
class LoginFragment : GuidedStepSupportFragment() {

    @Inject lateinit var authRepo: AuthRepository

    companion object {
        private const val ACTION_USERNAME = 1L
        private const val ACTION_PASSWORD = 2L
        private const val ACTION_SIGN_IN = 3L
        private const val ACTION_PAIR_DEVICE = 4L
        private const val ACTION_CHANGE_SERVER = 5L
    }

    override fun onCreateGuidance(savedInstanceState: Bundle?): GuidanceStylist.Guidance {
        return GuidanceStylist.Guidance(
            getString(R.string.login_title),
            getString(R.string.login_description),
            getString(R.string.app_name),
            null,
        )
    }

    override fun onCreateActions(actions: MutableList<GuidedAction>, savedInstanceState: Bundle?) {
        actions.add(
            GuidedAction.Builder(requireContext())
                .id(ACTION_USERNAME)
                .title(getString(R.string.username))
                .descriptionEditable(true)
                .descriptionEditInputType(android.text.InputType.TYPE_CLASS_TEXT)
                .build()
        )
        actions.add(
            GuidedAction.Builder(requireContext())
                .id(ACTION_PASSWORD)
                .title(getString(R.string.password))
                .descriptionEditable(true)
                .descriptionEditInputType(
                    android.text.InputType.TYPE_CLASS_TEXT or android.text.InputType.TYPE_TEXT_VARIATION_PASSWORD
                )
                .build()
        )
        actions.add(
            GuidedAction.Builder(requireContext())
                .id(ACTION_SIGN_IN)
                .title(getString(R.string.sign_in))
                .build()
        )
        // Pair-with-another-device path. Lets the user complete a
        // full OIDC / OAuth / SAML / LDAP / local sign-in flow on
        // their phone or laptop where there's a real browser, then
        // hands the resulting tokens back to the TV. The
        // username+password fields above remain for direct local /
        // LDAP sign-in (the only types that work cleanly with a TV
        // remote).
        actions.add(
            GuidedAction.Builder(requireContext())
                .id(ACTION_PAIR_DEVICE)
                .title(getString(R.string.pair_with_device))
                .build()
        )
        actions.add(
            GuidedAction.Builder(requireContext())
                .id(ACTION_CHANGE_SERVER)
                .title(getString(R.string.change_server))
                .build()
        )
    }

    override fun onGuidedActionEditedAndProceed(action: GuidedAction): Long {
        // Promote the in-progress edit text to the committed description so a
        // direct jump to Sign In still picks it up.
        val edited = action.editDescription?.toString().orEmpty()
        if (edited.isNotEmpty()) action.description = edited
        return GuidedAction.ACTION_ID_NEXT
    }

    private fun fieldText(id: Long): String {
        val a = findActionById(id) ?: return ""
        val desc = a.description?.toString().orEmpty()
        if (desc.isNotEmpty()) return desc.trim()
        return a.editDescription?.toString()?.trim().orEmpty()
    }

    override fun onGuidedActionClicked(action: GuidedAction) {
        if (action.id == ACTION_CHANGE_SERVER) {
            (activity as? MainActivity)?.navigateTo(NavigationDestination.SERVER_SETUP)
            return
        }
        if (action.id == ACTION_PAIR_DEVICE) {
            (activity as? MainActivity)?.navigateTo(NavigationDestination.PAIRING)
            return
        }
        if (action.id != ACTION_SIGN_IN) return

        val username = fieldText(ACTION_USERNAME)
        val password = fieldText(ACTION_PASSWORD)

        if (username.isEmpty() || password.isEmpty()) {
            Toast.makeText(
                requireContext(),
                "Enter a username and password",
                Toast.LENGTH_SHORT,
            ).show()
            return
        }

        lifecycleScope.launch {
            try {
                authRepo.login(username, password)
                (activity as? MainActivity)?.navigateTo(NavigationDestination.HOME)
            } catch (e: Exception) {
                Toast.makeText(
                    requireContext(),
                    getString(R.string.error_login) + ": " + (e.message ?: "Unknown error"),
                    Toast.LENGTH_LONG,
                ).show()
            }
        }
    }
}
