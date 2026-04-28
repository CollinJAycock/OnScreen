package tv.onscreen.android.ui.notifications

import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.TextView
import androidx.recyclerview.widget.RecyclerView
import tv.onscreen.android.R
import tv.onscreen.android.data.model.NotificationItem
import java.time.Duration
import java.time.Instant

class NotificationAdapter(
    private val onClick: (NotificationItem) -> Unit,
) : RecyclerView.Adapter<NotificationAdapter.VH>() {

    private val items = mutableListOf<NotificationItem>()

    fun submit(list: List<NotificationItem>) {
        items.clear()
        items.addAll(list)
        notifyDataSetChanged()
    }

    override fun onCreateViewHolder(parent: ViewGroup, viewType: Int): VH {
        val view = LayoutInflater.from(parent.context)
            .inflate(R.layout.item_notification, parent, false)
        return VH(view)
    }

    override fun getItemCount(): Int = items.size

    override fun onBindViewHolder(holder: VH, position: Int) {
        holder.bind(items[position], onClick)
    }

    class VH(view: View) : RecyclerView.ViewHolder(view) {
        private val dot: View = view.findViewById(R.id.notif_unread_dot)
        private val title: TextView = view.findViewById(R.id.notif_title)
        private val body: TextView = view.findViewById(R.id.notif_body)
        private val time: TextView = view.findViewById(R.id.notif_time)

        fun bind(item: NotificationItem, onClick: (NotificationItem) -> Unit) {
            dot.visibility = if (item.read) View.INVISIBLE else View.VISIBLE
            title.text = item.title
            body.text = item.body
            body.visibility = if (item.body.isBlank()) View.GONE else View.VISIBLE
            time.text = relativeTime(item.created_at)
            itemView.setOnClickListener { onClick(item) }
        }

        // Server sends created_at as UnixMilli (int64); previous code
        // tried Instant.parse on what it assumed was an ISO string and
        // silently returned empty for every notification.
        private fun relativeTime(unixMs: Long): String {
            if (unixMs <= 0) return ""
            val instant = Instant.ofEpochMilli(unixMs)
            val diff = Duration.between(instant, Instant.now())
            val mins = diff.toMinutes()
            return when {
                mins < 1 -> "just now"
                mins < 60 -> "${mins}m ago"
                mins < 1440 -> "${mins / 60}h ago"
                else -> "${mins / 1440}d ago"
            }
        }
    }
}
