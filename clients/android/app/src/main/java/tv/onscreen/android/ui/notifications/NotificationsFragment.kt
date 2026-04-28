package tv.onscreen.android.ui.notifications

import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.Button
import android.widget.TextView
import androidx.fragment.app.Fragment
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.lifecycleScope
import androidx.recyclerview.widget.LinearLayoutManager
import androidx.recyclerview.widget.RecyclerView
import dagger.hilt.android.AndroidEntryPoint
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.launch
import tv.onscreen.android.R
import tv.onscreen.android.ui.detail.DetailFragment

@AndroidEntryPoint
class NotificationsFragment : Fragment() {

    private lateinit var viewModel: NotificationsViewModel
    private lateinit var adapter: NotificationAdapter

    override fun onCreateView(inflater: LayoutInflater, container: ViewGroup?, savedInstanceState: Bundle?): View =
        inflater.inflate(R.layout.fragment_notifications, container, false)

    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        super.onViewCreated(view, savedInstanceState)
        viewModel = ViewModelProvider(this)[NotificationsViewModel::class.java]

        val list = view.findViewById<RecyclerView>(R.id.notifications_list)
        val empty = view.findViewById<TextView>(R.id.notifications_empty)
        val markAll = view.findViewById<Button>(R.id.btn_mark_all_read)

        adapter = NotificationAdapter { notif ->
            if (!notif.read) viewModel.markRead(notif.id)
            val target = notif.item_id
            if (!target.isNullOrEmpty()) {
                parentFragmentManager.beginTransaction()
                    .replace(R.id.main_container, DetailFragment.newInstance(target))
                    .addToBackStack(null)
                    .commit()
            }
        }
        list.layoutManager = LinearLayoutManager(requireContext())
        list.adapter = adapter

        markAll.setOnClickListener { viewModel.markAllRead() }

        viewLifecycleOwner.lifecycleScope.launch {
            viewModel.uiState.collectLatest { state ->
                adapter.submit(state.items)
                val isEmpty = !state.isLoading && state.items.isEmpty()
                empty.visibility = if (isEmpty) View.VISIBLE else View.GONE
                markAll.visibility = if (state.unreadCount > 0) View.VISIBLE else View.GONE
            }
        }
    }

    override fun onResume() {
        super.onResume()
        viewModel.load()
    }
}
