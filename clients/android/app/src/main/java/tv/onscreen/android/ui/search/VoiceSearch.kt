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
 * keyboard is the way in. A recognizer that resolves but then fails to
 * start or returns without listening takes the orb away too
 * ([provenUnavailable]).
 */
internal object VoiceSearch {

    /** The intent the orb starts: free-form speech, results back to us. */
    fun recognizeIntent(): Intent =
        Intent(RecognizerIntent.ACTION_RECOGNIZE_SPEECH).apply {
            putExtra(RecognizerIntent.EXTRA_LANGUAGE_MODEL, RecognizerIntent.LANGUAGE_MODEL_FREE_FORM)
        }

    /** What came back from the recognizer. */
    sealed interface Outcome {
        data class Query(val text: String) : Outcome
        /** Back before anyone could have spoken: a recognizer that resolves
         *  but doesn't listen. Said out loud, or the orb looks dead. */
        object Unavailable : Outcome
        /** Listened, heard nothing usable. */
        object NothingHeard : Outcome
        /** The viewer backed out. */
        object Cancelled : Outcome
        /** The recognizer reported an error (network, server, audio, its
         *  own client): said out loud, but the orb stays (it can recover). */
        object Failed : Outcome
    }

    /** RecognizerIntent's result codes past RESULT_FIRST_USER: NO_MATCH (1),
     *  then CLIENT_ERROR, SERVER_ERROR, NETWORK_ERROR, AUDIO_ERROR (2-5). */
    private const val RESULT_NO_MATCH = RecognizerIntent.RESULT_NO_MATCH
    private const val RESULT_OK = -1 // Activity.RESULT_OK

    /** An empty "success" quicker than this: nobody had time to speak, so
     *  the recognizer never listened. */
    const val INSTANT_RETURN_MS = 1500L

    /** A cancel quicker than this is the recognizer's, not the viewer's: a
     *  person can't see the prompt and press BACK that fast. Slower cancels
     *  are the viewer changing their mind on a recognizer that works. */
    const val INSTANT_CANCEL_MS = 300L

    /** [resultCode] as onActivityResult got it. Only a plain cancel is ever
     *  silent: a recognizer's own error codes (no match, network, server,
     *  audio) used to read as a cancel, and the press looked dead. */
    fun outcome(resultCode: Int, matches: List<String>?, elapsedMs: Long): Outcome {
        val ok = resultCode == RESULT_OK
        val text = matches?.firstOrNull { it.isNotBlank() }?.trim()
        return when {
            ok && text != null -> Outcome.Query(text)
            ok && elapsedMs < INSTANT_RETURN_MS -> Outcome.Unavailable
            ok -> Outcome.NothingHeard
            resultCode == RESULT_NO_MATCH -> Outcome.NothingHeard
            resultCode > RESULT_NO_MATCH -> Outcome.Failed
            elapsedMs < INSTANT_CANCEL_MS -> Outcome.Unavailable
            else -> Outcome.Cancelled
        }
    }

    /** Set once a recognizer has failed to start or returned without
     *  listening. It holds for the rest of the process, so a reopened
     *  Search shows no orb rather than one already known not to work. */
    @Volatile var provenUnavailable = false

    /** An activity takes [recognizeIntent]. Needs the <queries> entry in the
     *  manifest: from Android 11 the lookup sees no other app without it. */
    fun recognizerInstalled(pm: PackageManager): Boolean =
        try {
            pm.queryIntentActivities(recognizeIntent(), 0).isNotEmpty()
        } catch (e: RuntimeException) {
            false
        }
}
