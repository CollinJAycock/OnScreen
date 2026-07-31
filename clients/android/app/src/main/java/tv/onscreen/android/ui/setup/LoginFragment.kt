package tv.onscreen.android.ui.setup

import android.os.Bundle
import android.util.Log
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
                // descriptionInputType governs the row OUT of edit mode, and
                // Leanback defaults it to plain text. Setting only the Edit
                // variant masked the field while the IME was up and then
                // repainted the password in cleartext, in TV-sized type, the
                // moment the field was committed or escaped — where it stayed
                // until the fragment was replaced, i.e. through a failed-login
                // toast and through the 2FA push. A TV is a shared display;
                // anyone in the room, any screen recorder and `adb screencap`
                // all captured it.
                .descriptionInputType(
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
        if (signingIn) return

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

        setSigningIn(true)
        lifecycleScope.launch {
            try {
                val pair = authRepo.login(username, password)
                setSigningIn(false)
                if (pair.totp_required) {
                    // Password OK, second factor owed — push the code step.
                    GuidedStepSupportFragment.add(
                        parentFragmentManager,
                        TotpLoginFragment.create(pair.login_challenge_token.orEmpty()),
                    )
                    return@launch
                }
                (activity as? MainActivity)?.navigateTo(NavigationDestination.HOME)
            } catch (e: Exception) {
                setSigningIn(false)
                // The raw exception text used to be appended here — an OkHttp
                // stack message read at ten feet tells a user nothing they can
                // act on. Kept in logcat for support, off the TV.
                Log.w("LoginFragment", "login failed", e)
                Toast.makeText(
                    requireContext(),
                    getString(R.string.error_login),
                    Toast.LENGTH_LONG,
                ).show()
            }
        }
    }

    /** True while a sign-in is in flight. Nothing on screen used to change when
     *  the user pressed Sign In, so the natural "did that register?" second
     *  press fired a duplicate login. */
    private var signingIn = false

    private fun setSigningIn(busy: Boolean) {
        signingIn = busy
        if (!isAdded) return
        val a = findActionById(ACTION_SIGN_IN) ?: return
        a.title = getString(if (busy) R.string.signing_in else R.string.sign_in)
        a.isEnabled = !busy
        a.isFocusable = !busy
        notifyActionChanged(findActionPositionById(ACTION_SIGN_IN))
    }

}
