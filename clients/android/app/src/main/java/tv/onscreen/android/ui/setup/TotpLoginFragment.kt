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

/**
 * Second step of a two-factor login on TV. LoginFragment pushes this
 * once /auth/login reports totp_required, passing the challenge token.
 * The user enters their authenticator code (or a recovery code) and we
 * exchange it for a real session via /auth/totp/verify.
 */
@AndroidEntryPoint
class TotpLoginFragment : GuidedStepSupportFragment() {

    @Inject lateinit var authRepo: AuthRepository

    companion object {
        private const val ARG_CHALLENGE = "challenge_token"
        private const val ACTION_CODE = 1L
        private const val ACTION_VERIFY = 2L

        fun create(challengeToken: String): TotpLoginFragment {
            return TotpLoginFragment().apply {
                arguments = Bundle().apply { putString(ARG_CHALLENGE, challengeToken) }
            }
        }
    }

    private val challengeToken: String
        get() = arguments?.getString(ARG_CHALLENGE).orEmpty()

    override fun onCreateGuidance(savedInstanceState: Bundle?): GuidanceStylist.Guidance {
        return GuidanceStylist.Guidance(
            getString(R.string.totp_title),
            getString(R.string.totp_description),
            getString(R.string.app_name),
            null,
        )
    }

    override fun onCreateActions(actions: MutableList<GuidedAction>, savedInstanceState: Bundle?) {
        actions.add(
            GuidedAction.Builder(requireContext())
                .id(ACTION_CODE)
                .title(getString(R.string.totp_code))
                .descriptionEditable(true)
                // Masked in BOTH modes — see the note in LoginFragment. This
                // field also accepts a recovery code, which unlike a 30-second
                // TOTP is a long-lived secret, so leaving it rendered on the TV
                // is the same exposure as the password.
                .descriptionEditInputType(
                    android.text.InputType.TYPE_CLASS_TEXT or android.text.InputType.TYPE_TEXT_VARIATION_PASSWORD
                )
                .descriptionInputType(
                    android.text.InputType.TYPE_CLASS_TEXT or android.text.InputType.TYPE_TEXT_VARIATION_PASSWORD
                )
                .build()
        )
        actions.add(
            GuidedAction.Builder(requireContext())
                .id(ACTION_VERIFY)
                .title(getString(R.string.verify))
                .build()
        )
    }

    override fun onGuidedActionEditedAndProceed(action: GuidedAction): Long {
        val edited = action.editDescription?.toString().orEmpty()
        if (edited.isNotEmpty()) action.description = edited
        return GuidedAction.ACTION_ID_NEXT
    }

    private fun codeText(): String {
        val a = findActionById(ACTION_CODE) ?: return ""
        val desc = a.description?.toString().orEmpty()
        if (desc.isNotEmpty()) return desc.trim()
        return a.editDescription?.toString()?.trim().orEmpty()
    }

    override fun onGuidedActionClicked(action: GuidedAction) {
        if (action.id != ACTION_VERIFY) return
        val code = codeText()
        if (code.isEmpty()) {
            Toast.makeText(requireContext(), "Enter your code", Toast.LENGTH_SHORT).show()
            return
        }
        lifecycleScope.launch {
            try {
                authRepo.verifyTotp(challengeToken, code)
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
