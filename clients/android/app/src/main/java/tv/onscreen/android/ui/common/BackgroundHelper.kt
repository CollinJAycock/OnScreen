package tv.onscreen.android.ui.common

import android.app.Activity
import android.graphics.drawable.Drawable
import android.os.Handler
import android.os.Looper
import androidx.leanback.app.BackgroundManager
import coil.imageLoader
import coil.request.ImageRequest
import tv.onscreen.android.data.artworkUrl

/**
 * Loads fanart into the Leanback BackgroundManager with debouncing.
 * Attach to browse or detail fragments to show artwork behind content rows.
 */
class BackgroundHelper(private val activity: Activity, private val serverUrl: String) {

    private val backgroundManager = BackgroundManager.getInstance(activity).apply {
        if (!isAttached) attach(activity.window)
    }

    private val handler = Handler(Looper.getMainLooper())
    private var currentUrl: String? = null
    private val debounceMs = 300L

    fun setBackground(fanartPath: String?) {
        if (fanartPath.isNullOrEmpty()) {
            backgroundManager.drawable = null
            return
        }

        val url = artworkUrl(serverUrl, fanartPath, 1280)
        if (url == currentUrl) return
        currentUrl = url

        handler.removeCallbacksAndMessages(null)
        handler.postDelayed({
            loadImage(url)
        }, debounceMs)
    }

    fun clear() {
        handler.removeCallbacksAndMessages(null)
        backgroundManager.drawable = null
        currentUrl = null
    }

    private fun loadImage(url: String) {
        // Use the application-singleton ImageLoader (configured in
        // OnScreenApp with our authed OkHttpClient) instead of a
        // fresh ImageLoader(activity) — the latter would bypass the
        // Bearer-injecting AuthInterceptor and 401 on every fanart.
        val request = ImageRequest.Builder(activity)
            .data(url)
            .target(
                onSuccess = { drawable -> backgroundManager.drawable = drawable },
                onError = { backgroundManager.drawable = null },
            )
            .build()
        activity.imageLoader.enqueue(request)
    }
}
