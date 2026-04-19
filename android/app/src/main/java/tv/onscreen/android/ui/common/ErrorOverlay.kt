package tv.onscreen.android.ui.common

import android.view.LayoutInflater
import android.view.View
import android.widget.Button
import android.widget.FrameLayout
import android.widget.TextView
import tv.onscreen.android.R

/**
 * Wraps a fragment's root view in a FrameLayout and overlays a retry UI on top.
 * Callers supply the original view returned from super.onCreateView(), and the
 * wrapper replaces it as the fragment's view.
 */
class ErrorOverlay private constructor(
    val root: FrameLayout,
    private val overlay: View,
    private val titleView: TextView,
    private val messageView: TextView,
    private val retryButton: Button,
) {
    fun show(message: String?, onRetry: () -> Unit) {
        messageView.text = message ?: ""
        messageView.visibility = if (message.isNullOrBlank()) View.GONE else View.VISIBLE
        retryButton.setOnClickListener {
            hide()
            onRetry()
        }
        overlay.visibility = View.VISIBLE
        retryButton.requestFocus()
    }

    fun hide() {
        overlay.visibility = View.GONE
    }

    companion object {
        /**
         * Wraps [inner] in a FrameLayout with an error overlay layered on top.
         * Returns the wrapper + overlay handle. Use [root] as the fragment's view.
         */
        fun wrap(inner: View): ErrorOverlay {
            val ctx = inner.context
            val wrapper = FrameLayout(ctx).apply {
                layoutParams = FrameLayout.LayoutParams(
                    FrameLayout.LayoutParams.MATCH_PARENT,
                    FrameLayout.LayoutParams.MATCH_PARENT,
                )
            }
            (inner.parent as? android.view.ViewGroup)?.removeView(inner)
            wrapper.addView(
                inner,
                FrameLayout.LayoutParams(
                    FrameLayout.LayoutParams.MATCH_PARENT,
                    FrameLayout.LayoutParams.MATCH_PARENT,
                ),
            )

            val overlay = LayoutInflater.from(ctx).inflate(R.layout.error_overlay, wrapper, false)
            wrapper.addView(overlay)

            return ErrorOverlay(
                root = wrapper,
                overlay = overlay,
                titleView = overlay.findViewById(R.id.error_title),
                messageView = overlay.findViewById(R.id.error_message),
                retryButton = overlay.findViewById(R.id.btn_retry),
            )
        }
    }
}
