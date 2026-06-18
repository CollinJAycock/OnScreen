package tv.onscreen.android.ui.detail

import android.graphics.drawable.ColorDrawable
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.FrameLayout
import android.widget.ImageView
import android.widget.ProgressBar
import android.widget.TextView
import androidx.core.content.ContextCompat
import androidx.recyclerview.widget.DiffUtil
import androidx.recyclerview.widget.RecyclerView
import coil.load
import tv.onscreen.android.R
import tv.onscreen.android.data.artworkUrl
import tv.onscreen.android.data.model.ChildItem

class EpisodeAdapter(
    private val serverUrl: String,
    // Artwork for the parent container (show / season / artist / album) used
    // as a fallback when a child has no still/poster of its own — e.g. demo
    // shows whose episodes were never given per-episode stills. Beats a blank
    // tile; mirrors Plex/Jellyfin. Null when the parent itself has no art.
    private val fallbackArtPath: String? = null,
    private val onClick: (ChildItem) -> Unit,
) : RecyclerView.Adapter<EpisodeAdapter.VH>() {

    private val items = mutableListOf<ChildItem>()

    fun submit(list: List<ChildItem>) {
        // DiffUtil over notifyDataSetChanged: only the rows that actually
        // changed (e.g. a watched/progress update after returning from
        // playback) rebind, which keeps D-pad focus stable and avoids
        // re-decoding every thumbnail on a full refresh.
        val diff = DiffUtil.calculateDiff(object : DiffUtil.Callback() {
            override fun getOldListSize(): Int = items.size
            override fun getNewListSize(): Int = list.size
            override fun areItemsTheSame(oldPos: Int, newPos: Int): Boolean =
                items[oldPos].id == list[newPos].id
            // ChildItem is a data class, so structural equality covers
            // every rendered field (watched, view_offset_ms, title, …).
            override fun areContentsTheSame(oldPos: Int, newPos: Int): Boolean =
                items[oldPos] == list[newPos]
        })
        items.clear()
        items.addAll(list)
        diff.dispatchUpdatesTo(this)
    }

    override fun onCreateViewHolder(parent: ViewGroup, viewType: Int): VH {
        val view = LayoutInflater.from(parent.context)
            .inflate(R.layout.item_episode_card, parent, false)
        return VH(view)
    }

    override fun getItemCount(): Int = items.size

    override fun onBindViewHolder(holder: VH, position: Int) {
        val ep = items[position]
        holder.bind(ep, serverUrl, fallbackArtPath, onClick)
    }

    class VH(view: View) : RecyclerView.ViewHolder(view) {
        private val thumb: ImageView = view.findViewById(R.id.ep_thumb)
        private val index: TextView = view.findViewById(R.id.ep_index)
        private val title: TextView = view.findViewById(R.id.ep_title)
        private val duration: TextView = view.findViewById(R.id.ep_duration)
        private val watchedBadge: FrameLayout = view.findViewById(R.id.ep_watched_badge)
        private val progress: ProgressBar = view.findViewById(R.id.ep_progress)

        fun bind(ep: ChildItem, serverUrl: String, fallbackArtPath: String?, onClick: (ChildItem) -> Unit) {
            val ctx = itemView.context
            index.text = ep.index?.let { n ->
                // Label by the child's own type — a music album's children are
                // "TRACK n", an audiobook's are "CHAPTER n", a book series' are
                // "BOOK n" — not always "EPISODE n". Localised via resources.
                when (ep.type) {
                    "track", "music_video" -> ctx.getString(R.string.label_track_n, n)
                    "audiobook_chapter" -> ctx.getString(R.string.label_chapter_n, n)
                    "book" -> ctx.getString(R.string.label_book_n, n)
                    else -> ctx.getString(R.string.label_episode_n, n)
                }
            } ?: ""
            index.visibility = if (ep.index != null) View.VISIBLE else View.GONE
            title.text = ep.title
            duration.text = ep.duration_ms?.let { fmtDuration(it) } ?: ""
            duration.visibility = if (ep.duration_ms != null) View.VISIBLE else View.GONE

            // Coalesce on blank, not just null: the API returns "" (not null)
            // for missing art, so a plain ?: would let an empty thumb_path
            // shadow a present poster_path. Fall through own still → own
            // poster → parent art; only a truly art-less item lands on the
            // neutral elevated tile (matching the movie-card treatment).
            val artPath = ep.thumb_path?.takeIf { it.isNotBlank() }
                ?: ep.poster_path?.takeIf { it.isNotBlank() }
                ?: fallbackArtPath?.takeIf { it.isNotBlank() }
            if (artPath != null && serverUrl.isNotEmpty()) {
                thumb.load(artworkUrl(serverUrl, artPath, 480)) {
                    crossfade(true)
                    placeholder(R.color.bg_elevated)
                    error(R.color.bg_elevated)
                }
            } else {
                thumb.setImageDrawable(ColorDrawable(ContextCompat.getColor(ctx, R.color.bg_elevated)))
            }

            // Watched treatment: dim the thumbnail + dull the title row
            // so the user can see at a glance which episodes they've
            // already finished, even without focusing on the small
            // corner check badge. Matches Plex / Jellyfin convention.
            // The badge itself stays full-opacity (overlaid on the
            // dimmed thumb) so it remains the obvious affordance.
            thumb.alpha = if (ep.watched) 0.45f else 1f
            title.alpha = if (ep.watched) 0.6f else 1f
            index.alpha = if (ep.watched) 0.6f else 1f
            duration.alpha = if (ep.watched) 0.6f else 1f
            watchedBadge.visibility = if (ep.watched) View.VISIBLE else View.GONE
            val dur = ep.duration_ms ?: 0L
            val shouldShowProgress = !ep.watched && ep.view_offset_ms > 0 && dur > 0
            if (shouldShowProgress) {
                progress.progress = ((ep.view_offset_ms * 100) / dur).toInt().coerceIn(0, 100)
                progress.visibility = View.VISIBLE
            } else {
                progress.visibility = View.GONE
            }

            itemView.setOnClickListener { onClick(ep) }
        }

        private fun fmtDuration(ms: Long): String {
            val totalMin = ms / 60_000
            return if (totalMin >= 60) "${totalMin / 60}h ${totalMin % 60}m"
            else "${totalMin}m"
        }
    }
}
