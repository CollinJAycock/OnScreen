package tv.onscreen.mobile.ui.hub

import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import tv.onscreen.mobile.data.model.HubItem

/**
 * Should a hub row snap back to its start? Yes when a refresh put a
 * different item at its head — the title just played moves to index 0 of
 * Continue Watching, but a LazyRow keyed by id keeps its anchor on the old
 * first tile, leaving the new head off-screen to the left. No when the head
 * is unchanged (a row the user scrolled stays put) or the row was empty.
 */
internal fun rowHeadChanged(previousHeadId: String?, headId: String?): Boolean =
    previousHeadId != null && headId != null && previousHeadId != headId

/**
 * Scroll state for a hub row that jumps back to the start when the row's
 * first item changes (see [rowHeadChanged]). The last-seen head is saveable,
 * so the row coming back into view — or the home screen being returned to —
 * doesn't count as a change; only a refresh that re-ordered the row does.
 */
@Composable
internal fun rememberRowStateFollowingHead(items: List<HubItem>): LazyListState {
    val state = rememberLazyListState()
    val headId = items.firstOrNull()?.id
    var seenHeadId by rememberSaveable { mutableStateOf(headId) }
    LaunchedEffect(headId) {
        if (rowHeadChanged(seenHeadId, headId)) state.scrollToItem(0)
        seenHeadId = headId
    }
    return state
}
