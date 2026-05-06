package tv.onscreen.mobile.cast

import android.content.Context
import com.google.android.gms.cast.CastMediaControlIntent
import com.google.android.gms.cast.framework.CastOptions
import com.google.android.gms.cast.framework.OptionsProvider
import com.google.android.gms.cast.framework.SessionProvider

/**
 * Cast SDK options provider. The Cast framework reflects on the
 * `OPTIONS_PROVIDER_CLASS_NAME` manifest meta-data key and instantiates
 * this class once at app start to learn (a) which receiver app to talk
 * to and (b) any additional session providers (none — we only use the
 * built-in CastSession).
 *
 * v1 uses Google's Default Media Receiver — pre-published, supports
 * MP4 + HLS direct play, no Cast Developer Console registration required.
 * If we ever need a custom receiver (mid-stream subtitle styling,
 * custom UI, etc.) we'd register an app, get an ID, and swap it in here.
 */
class CastOptionsProvider : OptionsProvider {
    override fun getCastOptions(context: Context): CastOptions {
        return CastOptions.Builder()
            .setReceiverApplicationId(CastMediaControlIntent.DEFAULT_MEDIA_RECEIVER_APPLICATION_ID)
            .build()
    }

    override fun getAdditionalSessionProviders(context: Context): MutableList<SessionProvider>? = null
}
