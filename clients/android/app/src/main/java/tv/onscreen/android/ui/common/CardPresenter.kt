package tv.onscreen.android.ui.common

import android.animation.AnimatorSet
import android.animation.ObjectAnimator
import android.content.Context
import android.graphics.Color
import android.graphics.Outline
import android.graphics.Rect
import android.view.Gravity
import android.view.View
import android.view.ViewGroup
import android.view.ViewOutlineProvider
import android.widget.FrameLayout
import android.widget.ImageView
import android.widget.LinearLayout
import android.widget.ProgressBar
import android.widget.TextView
import androidx.leanback.widget.Presenter
import coil.load
import tv.onscreen.android.R
import tv.onscreen.android.data.artworkUrl
import tv.onscreen.android.data.normaliseScheme
import tv.onscreen.android.data.model.*
import tv.onscreen.android.data.model.MediaCollection

/**
 * Modernised TV card.
 *
 * Visual contract vs. the old presenter:
 *  - Rounded corners (12 dp) clipped via OutlineProvider on the poster
 *    frame so the artwork doesn't overflow the focus ring.
 *  - Focus indicator is a 3 dp accent stroke + lifted elevation + a
 *    light scale (1.05×, was 1.2×). The big-zoom feel of stock Leanback
 *    is what makes the app read as "old TV app" — keep just enough
 *    motion to make the active card pop.
 *  - Title is always visible. Unfocused: 60% alpha, regular weight.
 *    Focused: 100% alpha, bold. Eyes scan the row faster when names
 *    are readable at rest, and there's nowhere else for the title to
 *    surface in this layout.
 */
class CardPresenter(
    private val context: Context,
    private val serverUrl: String = "",
    /**
     * Secondary action for a card, fired on a long-press of OK/Enter (the
     * Android TV / Fire TV convention for a card's context menu). Null leaves
     * the card with its click action only. HomeFragment passes one for the
     * Continue Watching rows (Remove from Continue Watching).
     */
    private val onLongPress: ((Any) -> Unit)? = null,
) : Presenter() {

    companion object {
        // dp — converted at inflate time. These were raw PIXELS while every
        // text size inside the card is sp, so on tvdpi/hdpi panels the card
        // box shrank relative to its own title and the layout collapsed.
        //
        // 120x180dp = the exact 240x360px the card always was on the xhdpi
        // reference density, now scaling correctly on tvdpi/hdpi panels.
        private const val CARD_WIDTH_DP = 120
        private const val CARD_HEIGHT_DP = 180
        private const val FOCUS_SCALE = 1.05f
        private const val ANIM_DURATION = 180L
        private const val CARD_RADIUS_DP = 12f
        private const val UNFOCUSED_TITLE_ALPHA = 0.6f
        private const val FOCUSED_ELEVATION_DP = 12f
    }

    override fun onCreateViewHolder(parent: ViewGroup): ViewHolder {
        val density = context.resources.displayMetrics.density
        val cornerPx = CARD_RADIUS_DP * density
        val focusedElevPx = FOCUSED_ELEVATION_DP * density
        val cardWidth = (CARD_WIDTH_DP * density).toInt()
        val cardHeight = (CARD_HEIGHT_DP * density).toInt()

        val container = LinearLayout(context).apply {
            orientation = LinearLayout.VERTICAL
            isFocusable = true
            isFocusableInTouchMode = true
            setBackgroundColor(Color.TRANSPARENT)
            clipChildren = false
            clipToPadding = false
            layoutParams = ViewGroup.LayoutParams(cardWidth, ViewGroup.LayoutParams.WRAP_CONTENT)
        }

        // Poster + focus ring stack. The ring is the FrameLayout's
        // foreground, so it draws on top of the image when focused
        // (state_focused selector) and is transparent otherwise.
        val posterFrame = FrameLayout(context).apply {
            layoutParams = LinearLayout.LayoutParams(cardWidth, cardHeight)
            clipToOutline = true
            outlineProvider = object : ViewOutlineProvider() {
                override fun getOutline(view: View, outline: Outline) {
                    outline.setRoundRect(Rect(0, 0, view.width, view.height), cornerPx)
                }
            }
            // The focus RING (card_focus_state foreground) keys off state_focused,
            // but the focusable view is the parent container — without mirroring
            // its state the frame is never "focused", so the ring never painted
            // and the only focus feedback was the 1.05× scale (near-invisible →
            // "can't tell which card is selected", and D-pad moves looked like
            // nothing happened). Duplicate the parent's state so the ring lights up.
            isDuplicateParentStateEnabled = true
            foreground = context.getDrawable(R.drawable.card_focus_state)
            tag = "frame"
        }

        val imageView = ImageView(context).apply {
            layoutParams = FrameLayout.LayoutParams(
                FrameLayout.LayoutParams.MATCH_PARENT,
                FrameLayout.LayoutParams.MATCH_PARENT,
            )
            scaleType = ImageView.ScaleType.CENTER_CROP
            setBackgroundColor(context.getColor(R.color.bg_elevated))
            tag = "poster"
        }
        posterFrame.addView(imageView)

        // Watch-state overlays (v2.5): a check for fully-watched items, an
        // unwatched-episode count for shows, a progress bar for in-progress
        // videos. All GONE unless the bound item carries the fields, so cards
        // for older servers / music / photos render exactly as before.
        val badgeMargin = (6 * density).toInt()
        val watchedBadge = FrameLayout(context).apply {
            val size = (24 * density).toInt()
            layoutParams = FrameLayout.LayoutParams(size, size, Gravity.TOP or Gravity.END).apply {
                topMargin = badgeMargin
                marginEnd = badgeMargin
            }
            background = context.getDrawable(R.drawable.watched_badge_bg)
            importantForAccessibility = View.IMPORTANT_FOR_ACCESSIBILITY_NO
            visibility = View.GONE
            tag = "watched"
            addView(
                ImageView(context).apply {
                    val icon = (16 * density).toInt()
                    layoutParams = FrameLayout.LayoutParams(icon, icon, Gravity.CENTER)
                    setImageResource(R.drawable.ic_check)
                },
            )
        }
        val countBadge = TextView(context).apply {
            layoutParams = FrameLayout.LayoutParams(
                FrameLayout.LayoutParams.WRAP_CONTENT,
                FrameLayout.LayoutParams.WRAP_CONTENT,
                Gravity.TOP or Gravity.END,
            ).apply {
                topMargin = badgeMargin
                marginEnd = badgeMargin
            }
            background = context.getDrawable(R.drawable.badge_bg)
            minWidth = (22 * density).toInt()
            gravity = Gravity.CENTER
            setPadding((7 * density).toInt(), (2 * density).toInt(), (7 * density).toInt(), (2 * density).toInt())
            setTextColor(Color.WHITE)
            textSize = 11f
            setTypeface(typeface, android.graphics.Typeface.BOLD)
            importantForAccessibility = View.IMPORTANT_FOR_ACCESSIBILITY_NO
            visibility = View.GONE
            tag = "count"
        }
        val progressBar = ProgressBar(context, null, android.R.attr.progressBarStyleHorizontal).apply {
            layoutParams = FrameLayout.LayoutParams(
                FrameLayout.LayoutParams.MATCH_PARENT,
                (3 * density).toInt(),
                Gravity.BOTTOM,
            )
            progressDrawable = context.getDrawable(R.drawable.progress_bar_episode)
            max = 100
            importantForAccessibility = View.IMPORTANT_FOR_ACCESSIBILITY_NO
            visibility = View.GONE
            tag = "progress"
        }
        posterFrame.addView(watchedBadge)
        posterFrame.addView(countBadge)
        posterFrame.addView(progressBar)

        val titleView = TextView(context).apply {
            layoutParams = LinearLayout.LayoutParams(cardWidth, ViewGroup.LayoutParams.WRAP_CONTENT).apply {
                topMargin = (10 * density).toInt()
            }
            gravity = Gravity.CENTER_HORIZONTAL
            // Always reserve exactly two lines. Without minLines, a 1-line title
            // ("Chobits") yields a shorter card than a 2-line one ("Code Geass:
            // Lelouch of the…"); in a VerticalGrid those uneven heights stagger
            // the rows so titles drift into the gaps ("rows all off"). Fixed
            // two-line height ⇒ uniform card height ⇒ aligned rows.
            minLines = 2
            maxLines = 2
            // Truncate with an ellipsis instead of a hard mid-word clip on long
            // titles ("Code Geass: Lelouch of the Rebel…").
            ellipsize = android.text.TextUtils.TruncateAt.END
            setTextColor(context.getColor(R.color.text_primary))
            textSize = 13f
            alpha = UNFOCUSED_TITLE_ALPHA
            tag = "title"
        }

        // Second line for episode tiles (Next Up, recently-added episodes):
        // the show is the title, this is "S2 · E5 — Episode title". When it's
        // shown the title drops to one line, so the card keeps its two-line
        // text height and the row / grid stays aligned.
        val subtitleView = TextView(context).apply {
            layoutParams = LinearLayout.LayoutParams(cardWidth, ViewGroup.LayoutParams.WRAP_CONTENT)
            gravity = Gravity.CENTER_HORIZONTAL
            maxLines = 1
            ellipsize = android.text.TextUtils.TruncateAt.END
            setTextColor(context.getColor(R.color.text_secondary))
            textSize = 12f
            visibility = View.GONE
            tag = "subtitle"
        }

        container.addView(posterFrame)
        container.addView(titleView)
        container.addView(subtitleView)

        // Focus animation: small scale, elevation lift, title pop.
        // ObjectAnimator on `elevation` is the cheapest way to drive
        // the system shadow (no manual blur work).
        container.setOnFocusChangeListener { v, hasFocus ->
            val layout = v as? LinearLayout ?: return@setOnFocusChangeListener
            val title = layout.findViewWithTag<TextView>("title") ?: return@setOnFocusChangeListener
            val frame = layout.findViewWithTag<FrameLayout>("frame") ?: return@setOnFocusChangeListener

            val scale = if (hasFocus) FOCUS_SCALE else 1.0f
            val titleAlpha = if (hasFocus) 1f else UNFOCUSED_TITLE_ALPHA
            val elev = if (hasFocus) focusedElevPx else 0f

            AnimatorSet().apply {
                playTogether(
                    ObjectAnimator.ofFloat(layout, View.SCALE_X, scale),
                    ObjectAnimator.ofFloat(layout, View.SCALE_Y, scale),
                    ObjectAnimator.ofFloat(title, View.ALPHA, titleAlpha),
                    ObjectAnimator.ofFloat(frame, "elevation", elev),
                )
                duration = ANIM_DURATION
                start()
            }
        }

        return ViewHolder(container)
    }

    override fun onBindViewHolder(viewHolder: ViewHolder, item: Any) {
        val container = viewHolder.view as LinearLayout
        val imageView = container.findViewWithTag<ImageView>("poster")
        val titleView = container.findViewWithTag<TextView>("title")
        val subtitleView = container.findViewWithTag<TextView>("subtitle")
        val data = extractCardData(item) ?: return

        val episodeTitles = (item as? HubItem)?.let { WatchStateUi.episodeTileTitles(it) }
        if (episodeTitles != null) {
            titleView.text = episodeTitles.first
            titleView.minLines = 1
            titleView.maxLines = 1
            subtitleView.text = episodeTitles.second
            subtitleView.visibility = View.VISIBLE
        } else {
            titleView.text = data.title
            titleView.minLines = 2
            titleView.maxLines = 2
            subtitleView.text = null
            subtitleView.visibility = View.GONE
        }

        val badgeLabel = bindWatchBadge(container, item)
        container.contentDescription = listOfNotNull(
            titleView.text?.toString(),
            episodeTitles?.second,
            badgeLabel,
        ).joinToString(", ")

        val longPress = onLongPress
        if (longPress != null) {
            container.setOnLongClickListener { longPress(item); true }
        } else {
            container.setOnLongClickListener(null)
            container.isLongClickable = false
        }

        // Audiobooks (and some photos) come back without a
        // poster_path / thumb_path because the server stores their
        // image data in the source file itself rather than as an
        // /artwork/ asset. Fall back to the generic per-item image
        // endpoint, which is type-agnostic and re-encodes whatever
        // image the server has on hand.
        val url = when {
            data.posterPath != null && serverUrl.isNotEmpty() ->
                artworkUrl(serverUrl, data.posterPath)
            data.itemId != null && serverUrl.isNotEmpty() ->
                "${normaliseScheme(serverUrl)}/api/v1/items/${data.itemId}/image?w=500"
            else -> null
        }
        if (url != null) {
            imageView.load(url) {
                crossfade(true)
                placeholder(R.color.bg_elevated)
                error(R.color.bg_elevated)
            }
        } else {
            imageView.setImageDrawable(null)
            imageView.setBackgroundColor(context.getColor(R.color.bg_elevated))
        }
    }

    override fun onUnbindViewHolder(viewHolder: ViewHolder) {
        val container = viewHolder.view as LinearLayout
        val imageView = container.findViewWithTag<ImageView>("poster")
        imageView.setImageDrawable(null)
        container.setOnLongClickListener(null)
    }

    /** Show the watch overlay for [item] (see [WatchStateUi.cardBadge]) and
     *  return its spoken label, or null when the card has none. */
    private fun bindWatchBadge(container: LinearLayout, item: Any): String? {
        val watched = container.findViewWithTag<View>("watched")
        val count = container.findViewWithTag<TextView>("count")
        val progress = container.findViewWithTag<ProgressBar>("progress")
        val badge = when (item) {
            is MediaItem -> WatchStateUi.cardBadge(item)
            is HubItem -> WatchStateUi.cardBadge(item)
            else -> null
        }
        watched.visibility = if (badge is WatchStateUi.CardBadge.Watched) View.VISIBLE else View.GONE
        if (badge is WatchStateUi.CardBadge.Unwatched) {
            count.text = if (badge.count > 99) "99+" else badge.count.toString()
            count.visibility = View.VISIBLE
        } else {
            count.visibility = View.GONE
        }
        if (badge is WatchStateUi.CardBadge.Progress) {
            progress.progress = badge.pct
            progress.visibility = View.VISIBLE
        } else {
            progress.visibility = View.GONE
        }
        val res = context.resources
        return when (badge) {
            is WatchStateUi.CardBadge.Watched -> res.getString(R.string.watch_badge_watched)
            is WatchStateUi.CardBadge.Unwatched -> {
                val n = badge.count.coerceAtMost(Int.MAX_VALUE.toLong()).toInt()
                res.getQuantityString(R.plurals.watch_badge_unwatched, n, n)
            }
            is WatchStateUi.CardBadge.Progress -> res.getString(R.string.watch_badge_progress, badge.pct)
            null -> null
        }
    }

    private data class CardData(
        val title: String,
        val posterPath: String?,
        /** Item id used to build the per-item /image fallback when
         *  the server didn't expose an /artwork/ path (audiobooks,
         *  some photos). Null for non-item types like collections. */
        val itemId: String?,
    )

    private fun extractCardData(item: Any): CardData? = when (item) {
        is HubItem -> CardData(item.title, item.poster_path ?: item.thumb_path, item.id)
        is MediaItem -> CardData(item.title, item.poster_path, item.id)
        is ChildItem -> CardData(item.title, item.poster_path ?: item.thumb_path, item.id)
        is SearchResult -> CardData(item.title, item.poster_path ?: item.thumb_path, item.id)
        is MediaCollection -> CardData(item.name, item.poster_path, null)
        is CollectionItem -> CardData(item.title, item.poster_path, item.id)
        is FavoriteItem -> CardData(item.title, item.poster_path ?: item.thumb_path, item.id)
        is HistoryItem -> CardData(item.title, item.thumb_path, item.id)
        else -> null
    }
}
