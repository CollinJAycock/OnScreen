package tv.onscreen.android.ui.photo

import android.graphics.Color
import android.os.Bundle
import android.util.TypedValue
import android.view.Gravity
import android.view.KeyEvent
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.FrameLayout
import android.widget.ImageView
import android.widget.TextView
import androidx.fragment.app.Fragment
import androidx.lifecycle.lifecycleScope
import coil.imageLoader
import coil.request.ImageRequest
import dagger.hilt.android.AndroidEntryPoint
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import tv.onscreen.android.data.prefs.ServerPrefs
import tv.onscreen.android.data.repository.ItemRepository
import javax.inject.Inject

/**
 * Full-screen photo viewer for the Android TV / Fire TV client.
 *
 * Navigation model:
 *   - Hits `/api/v1/items/{id}/image?w=1920&h=1080&fit=contain` —
 *     a normal API route, so Coil's singleton ImageLoader (the one
 *     OnScreenApp configures with our authed OkHttp) picks up the
 *     Bearer header automatically.
 *   - When a sibling list + start index are passed, D-pad left /
 *     right cycle through them with wrap-around. The fragment
 *     stays mounted; only the photo URL + position label change.
 *   - When called without siblings (e.g., from a hub card with no
 *     surrounding context), left / right are no-ops. Back exits
 *     the fragment in either case.
 *
 * The position counter ("3 / 47") is hidden when there are no
 * siblings — single-photo viewing doesn't need chrome.
 */
@AndroidEntryPoint
class PhotoViewFragment : Fragment() {

    @Inject lateinit var prefs: ServerPrefs
    @Inject lateinit var itemRepo: ItemRepository

    private var imageView: ImageView? = null
    private var positionLabel: TextView? = null

    private var serverUrl: String = ""
    private var siblingIds: List<String> = emptyList()
    private var currentIndex: Int = 0

    companion object {
        private const val ARG_ITEM_ID = "item_id"
        private const val ARG_SIBLING_IDS = "sibling_ids"
        private const val ARG_START_INDEX = "start_index"

        fun newInstance(itemId: String): PhotoViewFragment =
            newInstance(itemId, emptyList(), 0)

        /** Pre-pass the sibling photos so D-pad left/right cycles
         *  through them without an extra fetch per click.
         *  `siblingIds[startIndex]` MUST equal `itemId` — the caller
         *  is responsible for keeping those in sync. */
        fun newInstance(
            itemId: String,
            siblingIds: List<String>,
            startIndex: Int,
        ): PhotoViewFragment {
            return PhotoViewFragment().apply {
                arguments = Bundle().apply {
                    putString(ARG_ITEM_ID, itemId)
                    putStringArrayList(ARG_SIBLING_IDS, ArrayList(siblingIds))
                    putInt(ARG_START_INDEX, startIndex)
                }
            }
        }
    }

    override fun onCreateView(
        inflater: LayoutInflater,
        container: ViewGroup?,
        savedInstanceState: Bundle?,
    ): View {
        val frame = FrameLayout(requireContext()).apply {
            layoutParams = ViewGroup.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.MATCH_PARENT,
            )
            setBackgroundColor(Color.BLACK)
            isFocusable = true
            isFocusableInTouchMode = true
        }

        val img = ImageView(requireContext()).apply {
            layoutParams = FrameLayout.LayoutParams(
                FrameLayout.LayoutParams.MATCH_PARENT,
                FrameLayout.LayoutParams.MATCH_PARENT,
            )
            scaleType = ImageView.ScaleType.FIT_CENTER
        }
        imageView = img
        frame.addView(img)

        // "3 / 47" position counter, bottom-right. Translucent
        // background so it stays readable over a bright photo.
        val label = TextView(requireContext()).apply {
            val lp = FrameLayout.LayoutParams(
                FrameLayout.LayoutParams.WRAP_CONTENT,
                FrameLayout.LayoutParams.WRAP_CONTENT,
            ).apply {
                gravity = Gravity.BOTTOM or Gravity.END
                marginEnd = 60
                bottomMargin = 60
            }
            layoutParams = lp
            setTextSize(TypedValue.COMPLEX_UNIT_SP, 16f)
            setTextColor(Color.WHITE)
            setBackgroundColor(0x88000000.toInt())
            setPadding(28, 14, 28, 14)
            visibility = View.GONE
        }
        positionLabel = label
        frame.addView(label)

        return frame
    }

    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        super.onViewCreated(view, savedInstanceState)
        val args = arguments ?: return
        val initialId = args.getString(ARG_ITEM_ID) ?: return
        siblingIds = args.getStringArrayList(ARG_SIBLING_IDS).orEmpty()
        currentIndex = args.getInt(ARG_START_INDEX, 0)

        view.requestFocus()
        view.setOnKeyListener { _, keyCode, event ->
            if (event.action != KeyEvent.ACTION_DOWN) return@setOnKeyListener false
            when (keyCode) {
                KeyEvent.KEYCODE_BACK,
                KeyEvent.KEYCODE_ESCAPE -> {
                    parentFragmentManager.popBackStack()
                    true
                }
                KeyEvent.KEYCODE_DPAD_LEFT,
                KeyEvent.KEYCODE_MEDIA_PREVIOUS -> {
                    advance(-1); true
                }
                KeyEvent.KEYCODE_DPAD_RIGHT,
                KeyEvent.KEYCODE_MEDIA_NEXT -> {
                    advance(1); true
                }
                else -> false
            }
        }

        viewLifecycleOwner.lifecycleScope.launch {
            serverUrl = prefs.serverUrl.first().orEmpty()
            renderCurrent(initialId)
        }
    }

    /** Wrap-around D-pad nav. Wrapping (rather than stopping at
     *  edges) feels less surprising on a TV — the user holds the
     *  remote and the photo just keeps cycling, instead of the
     *  click going dead at album boundaries. */
    private fun advance(delta: Int) {
        if (siblingIds.size < 2) return
        val n = siblingIds.size
        val next = ((currentIndex + delta) % n + n) % n
        currentIndex = next
        renderCurrent(siblingIds[next])
    }

    private fun renderCurrent(itemId: String) {
        val img = imageView ?: return
        if (serverUrl.isEmpty()) return
        val url = "$serverUrl/api/v1/items/$itemId/image?w=1920&h=1080&fit=contain"
        val request = ImageRequest.Builder(requireContext())
            .data(url)
            .target(img)
            .build()
        requireContext().imageLoader.enqueue(request)

        val label = positionLabel ?: return
        if (siblingIds.size >= 2) {
            label.text = "${currentIndex + 1} / ${siblingIds.size}"
            label.visibility = View.VISIBLE
        } else {
            label.visibility = View.GONE
        }
    }

    override fun onDestroyView() {
        super.onDestroyView()
        imageView = null
        positionLabel = null
    }
}
