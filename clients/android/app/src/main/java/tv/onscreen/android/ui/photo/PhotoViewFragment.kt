package tv.onscreen.android.ui.photo

import android.os.Bundle
import android.view.KeyEvent
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.ImageView
import androidx.fragment.app.Fragment
import androidx.lifecycle.lifecycleScope
import coil.imageLoader
import coil.request.ImageRequest
import dagger.hilt.android.AndroidEntryPoint
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import tv.onscreen.android.R
import tv.onscreen.android.data.prefs.ServerPrefs
import tv.onscreen.android.data.repository.ItemRepository
import javax.inject.Inject

/**
 * Full-screen photo viewer for the Android TV / Fire TV client.
 *
 * Routed to from HomeFragment / LibraryFragment / etc. when the
 * selected item's `type == "photo"`. Hits
 * `/api/v1/items/{id}/image?w=1920&h=1080&fit=contain` — a normal
 * API route, so Coil's singleton ImageLoader (configured in
 * OnScreenApp with our authed OkHttpClient) handles the Bearer
 * header automatically.
 *
 * D-pad navigation between sibling photos (left/right) is a
 * follow-up — needs the children-of-library endpoint plus a way
 * to compute the current index. For now, Back returns to the
 * previous fragment.
 */
@AndroidEntryPoint
class PhotoViewFragment : Fragment() {

    @Inject lateinit var prefs: ServerPrefs
    @Inject lateinit var itemRepo: ItemRepository

    private var imageView: ImageView? = null

    companion object {
        private const val ARG_ITEM_ID = "item_id"

        fun newInstance(itemId: String): PhotoViewFragment {
            return PhotoViewFragment().apply {
                arguments = Bundle().apply { putString(ARG_ITEM_ID, itemId) }
            }
        }
    }

    override fun onCreateView(
        inflater: LayoutInflater,
        container: ViewGroup?,
        savedInstanceState: Bundle?,
    ): View {
        val frame = ImageView(requireContext()).apply {
            layoutParams = ViewGroup.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.MATCH_PARENT,
            )
            scaleType = ImageView.ScaleType.FIT_CENTER
            setBackgroundColor(android.graphics.Color.BLACK)
            // Take focus so D-pad keyEvents reach onKeyDown below.
            isFocusable = true
            isFocusableInTouchMode = true
        }
        imageView = frame
        return frame
    }

    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        super.onViewCreated(view, savedInstanceState)
        val itemId = arguments?.getString(ARG_ITEM_ID) ?: return
        view.requestFocus()

        view.setOnKeyListener { _, keyCode, event ->
            if (event.action != KeyEvent.ACTION_DOWN) return@setOnKeyListener false
            when (keyCode) {
                KeyEvent.KEYCODE_BACK,
                KeyEvent.KEYCODE_ESCAPE -> {
                    parentFragmentManager.popBackStack()
                    true
                }
                else -> false
            }
        }

        viewLifecycleOwner.lifecycleScope.launch {
            val serverUrl = prefs.serverUrl.first() ?: return@launch
            val url = "$serverUrl/api/v1/items/$itemId/image?w=1920&h=1080&fit=contain"
            val img = imageView ?: return@launch
            val request = ImageRequest.Builder(requireContext())
                .data(url)
                .target(img)
                .build()
            requireContext().imageLoader.enqueue(request)
        }
    }

    override fun onDestroyView() {
        super.onDestroyView()
        imageView = null
    }
}
