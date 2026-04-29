package tv.onscreen.android.ui.photo

import android.graphics.Color
import android.os.Bundle
import android.util.Log
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
import coil.request.ErrorResult
import coil.request.ImageRequest
import coil.request.SuccessResult
import dagger.hilt.android.AndroidEntryPoint
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import tv.onscreen.android.data.prefs.ServerPrefs
import tv.onscreen.android.data.repository.ItemRepository
import tv.onscreen.android.data.repository.LibraryRepository
import tv.onscreen.android.ui.KeyEventHandler
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
class PhotoViewFragment : Fragment(), KeyEventHandler {

    @Inject lateinit var prefs: ServerPrefs
    @Inject lateinit var itemRepo: ItemRepository
    @Inject lateinit var libraryRepo: LibraryRepository

    private var imageView: ImageView? = null
    private var positionLabel: TextView? = null
    private var statusLabel: TextView? = null

    private var serverUrl: String = ""
    private var siblingIds: List<String> = emptyList()
    private var currentIndex: Int = 0

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

        viewLifecycleOwner.lifecycleScope.launch {
            serverUrl = prefs.serverUrl.first().orEmpty()
            renderCurrent(initialId)

            // Auto-resolve siblings when the caller didn't supply
            // them (Search / Home / Favorites / History don't have
            // a natural sibling row to hand over). Fetch the photo
            // to find its parent album, then list the album's photo
            // children so D-pad nav still works regardless of entry
            // point. Best-effort — failures leave siblings empty
            // and the viewer stays single-photo.
            if (siblingIds.isEmpty()) {
                try {
                    val detail = itemRepo.getItem(initialId)
                    Log.d(TAG, "resolve siblings parent=${detail.parent_id} library=${detail.library_id}")
                    val photos = mutableListOf<String>()

                    // First try the parent album's children. Photos
                    // taken from albums hand back the right scoped
                    // set (the album the user was browsing).
                    val parent = detail.parent_id
                    if (!parent.isNullOrEmpty()) {
                        photos += itemRepo.getChildren(parent)
                            .filter { it.type == "photo" }
                            .map { it.id }
                    }

                    // Fallback: list everything in the library. Some
                    // entry points (Search, Favorites, History, Home
                    // hub) surface a photo with no parent album, or a
                    // photo whose album isn't where the user wants
                    // to scroll.
                    //
                    // Pagination runs concurrently (one async per
                    // page after the first) instead of sequentially.
                    // A 4 100-item library used to take ~5 s through
                    // the LAN-default 200-per-page loop — long enough
                    // that a user pressing D-pad left/right hit the
                    // empty-siblings no-op before resolve completed
                    // and concluded nav was broken. Parallel fetches
                    // collapse that to roughly one page time.
                    if (photos.size < 2) {
                        photos.clear()
                        val pageSize = 200
                        val (firstPage, total) = libraryRepo.getItems(
                            detail.library_id,
                            limit = pageSize,
                            offset = 0,
                        )
                        // Order matters — siblings are rendered in the
                        // same order as the library's item list, so
                        // append pages by their offset rather than in
                        // arrival order.
                        val pagesByOffset = sortedMapOf<Int, List<String>>()
                        pagesByOffset[0] = firstPage
                            .filter { it.type == "photo" }.map { it.id }

                        if (total > pageSize) {
                            val remainingOffsets = (pageSize until total step pageSize).toList()
                            val deferred = remainingOffsets.map { off ->
                                async {
                                    val (page, _) = libraryRepo.getItems(
                                        detail.library_id,
                                        limit = pageSize,
                                        offset = off,
                                    )
                                    off to page.filter { it.type == "photo" }.map { it.id }
                                }
                            }
                            for ((off, ids) in deferred.awaitAll()) {
                                pagesByOffset[off] = ids
                            }
                        }
                        for (ids in pagesByOffset.values) photos += ids
                    }

                    Log.d(TAG, "resolved ${photos.size} siblings")
                    if (photos.size >= 2) {
                        siblingIds = photos
                        currentIndex = photos.indexOf(initialId).coerceAtLeast(0)
                        renderCurrent(siblingIds[currentIndex])
                    }
                } catch (e: Exception) {
                    Log.d(TAG, "sibling resolve failed: ${e.message}")
                }
            }
        }
    }

    /**
     * Receives D-pad / Back / media-key events forwarded from
     * MainActivity.dispatchKeyEvent. The standard
     * View.OnKeyListener path doesn't work here: Leanback's
     * fragment container claims focus during the transition and
     * the FrameLayout never gets a chance, so D-pad keys are
     * silently consumed by the previous (still-focused) grid.
     *
     * Routing through the activity bypasses focus entirely — the
     * activity's dispatchKeyEvent fires before the view tree, and
     * we only consume keys that map to known photo-viewer
     * commands so other fragments are unaffected.
     */
    override fun onActivityKeyEvent(event: KeyEvent): Boolean {
        Log.d(TAG, "key code=${event.keyCode} siblings=${siblingIds.size} index=$currentIndex")
        return when (event.keyCode) {
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

    companion object {
        private const val TAG = "PhotoViewFragment"
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
