package tv.onscreen.android.ui.detail

import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.FrameLayout
import android.widget.ImageView
import android.widget.ProgressBar
import android.widget.TextView
import androidx.recyclerview.widget.RecyclerView
import coil.load
import tv.onscreen.android.R
import tv.onscreen.android.data.artworkUrl
import tv.onscreen.android.data.model.ChildItem

class EpisodeAdapter(
    private val serverUrl: String,
    private val onClick: (ChildItem) -> Unit,
) : RecyclerView.Adapter<EpisodeAdapter.VH>() {

    private val items = mutableListOf<ChildItem>()

    fun submit(list: List<ChildItem>) {
        items.clear()
        items.addAll(list)
        notifyDataSetChanged()
    }

    override fun onCreateViewHolder(parent: ViewGroup, viewType: Int): VH {
        val view = LayoutInflater.from(parent.context)
            .inflate(R.layout.item_episode_card, parent, false)
        return VH(view)
    }

    override fun getItemCount(): Int = items.size

    override fun onBindViewHolder(holder: VH, position: Int) {
        val ep = items[position]
        holder.bind(ep, serverUrl, onClick)
    }

    class VH(view: View) : RecyclerView.ViewHolder(view) {
        private val thumb: ImageView = view.findViewById(R.id.ep_thumb)
        private val index: TextView = view.findViewById(R.id.ep_index)
        private val title: TextView = view.findViewById(R.id.ep_title)
        private val duration: TextView = view.findViewById(R.id.ep_duration)
        private val watchedBadge: FrameLayout = view.findViewById(R.id.ep_watched_badge)
        private val progress: ProgressBar = view.findViewById(R.id.ep_progress)

        fun bind(ep: ChildItem, serverUrl: String, onClick: (ChildItem) -> Unit) {
            index.text = ep.index?.let { "EPISODE $it" } ?: ""
            index.visibility = if (ep.index != null) View.VISIBLE else View.GONE
            title.text = ep.title
            duration.text = ep.duration_ms?.let { fmtDuration(it) } ?: ""
            duration.visibility = if (ep.duration_ms != null) View.VISIBLE else View.GONE

            val thumbPath = ep.thumb_path ?: ep.poster_path
            if (!thumbPath.isNullOrEmpty() && serverUrl.isNotEmpty()) {
                thumb.load(artworkUrl(serverUrl, thumbPath, 480)) {
                    crossfade(true)
                }
            } else {
                thumb.setImageDrawable(null)
            }

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
