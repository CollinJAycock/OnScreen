package tv.onscreen.android.ui.search

import android.content.Intent
import android.content.pm.PackageManager
import android.speech.RecognizerIntent

/**
 * Whether the Search screen offers voice input (Leanback's microphone orb).
 *
 * Only where it can work: a speech recognizer activity is installed. Many
 * Fire TVs have none (voice there is Alexa, on the remote's own button), so
 * on Amazon's test device the orb started nothing and the Appstore rejected
 * the build ("Microphone button does not respond"). Devices that have one,
 * Fire TV or Google TV, keep the orb; elsewhere it is hidden and the
 * keyboard is the way in.
 */
internal object VoiceSearch {

    /** The intent the orb starts: free-form speech, results back to us. */
    fun recognizeIntent(): Intent =
        Intent(RecognizerIntent.ACTION_RECOGNIZE_SPEECH).apply {
            putExtra(RecognizerIntent.EXTRA_LANGUAGE_MODEL, RecognizerIntent.LANGUAGE_MODEL_FREE_FORM)
        }

    /** An activity takes [recognizeIntent]. Needs the <queries> entry in the
     *  manifest: from Android 11 the lookup sees no other app without it. */
    fun recognizerInstalled(pm: PackageManager): Boolean =
        try {
            pm.queryIntentActivities(recognizeIntent(), 0).isNotEmpty()
        } catch (e: RuntimeException) {
            false
        }
}
