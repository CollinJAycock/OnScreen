package tv.onscreen.mobile.ui.item

import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.filled.CheckCircle
import androidx.compose.material.icons.filled.DoneAll
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material.icons.filled.RemoveDone
import androidx.compose.material.icons.outlined.RadioButtonUnchecked
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.FilterChip
import androidx.compose.material3.FilterChipDefaults
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import tv.onscreen.mobile.R
import tv.onscreen.mobile.data.model.ChildItem
import tv.onscreen.mobile.data.model.UpNextMode
import tv.onscreen.mobile.i18n.UiText
import tv.onscreen.mobile.i18n.asString
import tv.onscreen.mobile.ui.watch.episodeNumberLabel
import tv.onscreen.mobile.ui.watch.markAllOptions
import tv.onscreen.mobile.ui.watch.progressFraction
import tv.onscreen.mobile.ui.watch.upNextLabel

/*
 * Watch-state pieces of the item detail page, driven by [ItemWatchViewModel].
 * ItemDetailScreen places them: [LeafWatchedToggle] under a movie / episode's
 * Play row, [ContainerWatchHeader] in place of Play on a show / season, and
 * [watchEpisodeSection] as the show / season body.
 */

/** Movie / episode: "Mark watched" ⇄ "Watched" toggle. */
@Composable
internal fun LeafWatchedToggle(
    watched: Boolean,
    busy: Boolean,
    onToggle: () -> Unit,
) {
    FilterChip(
        selected = watched,
        enabled = !busy,
        onClick = onToggle,
        label = {
            Text(stringResource(if (watched) R.string.watch_watched else R.string.watch_mark_watched))
        },
        leadingIcon = {
            Icon(
                if (watched) Icons.Default.Check else Icons.Outlined.RadioButtonUnchecked,
                contentDescription = null,
                modifier = Modifier.size(FilterChipDefaults.IconSize),
            )
        },
    )
}

/**
 * Show / season header: the up-next primary button ("Resume S3 · E4",
 * "Play S3 · E5", "Play S1 · E1", "Watch again") and the "Mark all …"
 * buttons. When the server has no up-next (older build, call failed) Play
 * hands the container id to the player, which resolves the episode itself.
 */
@OptIn(ExperimentalLayoutApi::class)
@Composable
internal fun ContainerWatchHeader(
    ui: ItemWatchUi,
    itemId: String,
    itemType: String,
    itemTitle: String,
    onPlay: (String) -> Unit,
    onMarkAll: (Boolean) -> Unit,
) {
    val upNext = ui.upNext
    val label = upNextLabel(upNext)
    val episode = upNext?.episode
    Column {
        when {
            upNext?.mode == UpNextMode.NONE -> Text(
                stringResource(R.string.watch_no_episodes_yet),
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            label != null && episode != null -> {
                Button(onClick = { onPlay(episode.id) }) {
                    Icon(Icons.Default.PlayArrow, contentDescription = null)
                    Spacer(Modifier.width(6.dp))
                    Text(label.asString())
                }
                if (episode.title.isNotBlank()) {
                    Spacer(Modifier.height(4.dp))
                    Text(
                        episode.title,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                }
            }
            else -> Button(onClick = { onPlay(itemId) }, enabled = ui.upNextLoaded) {
                Icon(Icons.Default.PlayArrow, contentDescription = null)
                Spacer(Modifier.width(6.dp))
                Text(stringResource(R.string.play))
            }
        }

        // Mark all — only once up-next has answered, so the buttons don't
        // flicker from "both" to the one that applies.
        if (ui.upNextLoaded) {
            val opts = markAllOptions(upNext)
            if (opts.watched || opts.unwatched) {
                var confirmUnwatch by remember { mutableStateOf(false) }
                Spacer(Modifier.height(8.dp))
                FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    if (opts.watched) {
                        OutlinedButton(onClick = { onMarkAll(true) }, enabled = !ui.markBusy) {
                            Icon(Icons.Default.DoneAll, contentDescription = null)
                            Spacer(Modifier.width(6.dp))
                            Text(stringResource(R.string.watch_mark_all_watched))
                        }
                    }
                    if (opts.unwatched) {
                        OutlinedButton(
                            onClick = {
                                // Whole-show unwatched wipes every mark and
                                // resume point in the show — confirm first.
                                if (itemType == "show") confirmUnwatch = true else onMarkAll(false)
                            },
                            enabled = !ui.markBusy,
                        ) {
                            Icon(Icons.Default.RemoveDone, contentDescription = null)
                            Spacer(Modifier.width(6.dp))
                            Text(stringResource(R.string.watch_mark_all_unwatched))
                        }
                    }
                }
                if (confirmUnwatch) {
                    AlertDialog(
                        onDismissRequest = { confirmUnwatch = false },
                        title = { Text(stringResource(R.string.watch_mark_all_unwatched_title)) },
                        text = { Text(stringResource(R.string.watch_mark_all_unwatched_body, itemTitle)) },
                        confirmButton = {
                            TextButton(onClick = {
                                confirmUnwatch = false
                                onMarkAll(false)
                            }) { Text(stringResource(R.string.watch_mark_unwatched)) }
                        },
                        dismissButton = {
                            TextButton(onClick = { confirmUnwatch = false }) { Text(stringResource(R.string.cancel)) }
                        },
                    )
                }
            }
        }
    }
}

/**
 * Show / season body: season chips (show only; opens on the up-next
 * season) and the selected season's episodes, each with a watched toggle
 * (trailing button or long-press) and a resume bar.
 */
internal fun LazyListScope.watchEpisodeSection(
    ui: ItemWatchUi,
    onSelectSeason: (String) -> Unit,
    onOpenEpisode: (String) -> Unit,
    onToggleEpisode: (ChildItem) -> Unit,
    onMarkSeason: (seasonId: String, watched: Boolean) -> Unit,
    onRetry: () -> Unit,
) {
    if (ui.seasons.isNotEmpty()) {
        item(key = "watch-seasons") {
            Column {
                Spacer(Modifier.height(8.dp))
                Text(
                    stringResource(R.string.item_seasons),
                    style = MaterialTheme.typography.titleMedium,
                    modifier = Modifier.padding(horizontal = 16.dp),
                )
                Spacer(Modifier.height(4.dp))
                // The selected (up-next) season can sit far right on a long
                // show — S17 of 17 opened showing S11-S14. Jump it into view
                // when the row first appears, then follow selection changes
                // only when the chip isn't already fully visible. The flag is
                // saveable so scrolling the page away and back doesn't yank a
                // chip row the user scrolled by hand.
                val chipState = rememberLazyListState()
                var chipPositioned by rememberSaveable { mutableStateOf(false) }
                val selectedIndex = ui.selectedSeasonIndex
                LaunchedEffect(selectedIndex) {
                    if (selectedIndex < 0) return@LaunchedEffect
                    if (!chipPositioned) {
                        chipState.scrollToItem(selectedIndex)
                        chipPositioned = true
                    } else if (!chipState.isFullyVisible(selectedIndex)) {
                        chipState.animateScrollToItem(selectedIndex)
                    }
                }
                LazyRow(
                    state = chipState,
                    contentPadding = PaddingValues(horizontal = 16.dp),
                    horizontalArrangement = Arrangement.spacedBy(8.dp),
                ) {
                    items(ui.seasons, key = { it.id }) { season ->
                        FilterChip(
                            selected = season.id == ui.selectedSeasonId,
                            onClick = { onSelectSeason(season.id) },
                            label = { Text(seasonLabel(season).asString()) },
                        )
                    }
                }
            }
        }
    }
    item(key = "watch-episodes-header") {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            modifier = Modifier
                .fillMaxWidth()
                .padding(start = 16.dp, end = 4.dp, top = 8.dp),
        ) {
            Text(
                stringResource(R.string.item_episodes),
                style = MaterialTheme.typography.titleMedium,
                modifier = Modifier.weight(1f),
            )
            // Show page: per-season marks behind ⋮ (the header's Mark all
            // is the whole show; a season page's header already covers it).
            val season = ui.seasons.firstOrNull { it.id == ui.selectedSeasonId }
            if (season != null && ui.selectedEpisodes.isNotEmpty()) {
                var menuOpen by remember { mutableStateOf(false) }
                val seasonName = seasonLabel(season).asString()
                Box {
                    IconButton(onClick = { menuOpen = true }, enabled = !ui.markBusy) {
                        Icon(
                            Icons.Default.MoreVert,
                            contentDescription = stringResource(R.string.watch_season_options, seasonName),
                        )
                    }
                    DropdownMenu(expanded = menuOpen, onDismissRequest = { menuOpen = false }) {
                        if (ui.selectedEpisodes.any { !it.watched }) {
                            DropdownMenuItem(
                                text = { Text(stringResource(R.string.watch_mark_season_watched, seasonName)) },
                                leadingIcon = { Icon(Icons.Default.DoneAll, contentDescription = null) },
                                onClick = { menuOpen = false; onMarkSeason(season.id, true) },
                            )
                        }
                        if (ui.selectedEpisodes.any { it.watched || it.view_offset_ms > 0 }) {
                            DropdownMenuItem(
                                text = { Text(stringResource(R.string.watch_mark_season_unwatched, seasonName)) },
                                leadingIcon = { Icon(Icons.Default.RemoveDone, contentDescription = null) },
                                onClick = { menuOpen = false; onMarkSeason(season.id, false) },
                            )
                        }
                    }
                }
            }
        }
    }
    val selected = ui.selectedSeasonId
    when {
        ui.loadingContainer || (selected != null && selected in ui.loadingEpisodes && ui.selectedEpisodes.isEmpty()) ->
            item(key = "watch-episodes-loading") {
                Box(Modifier.fillMaxWidth().padding(24.dp), contentAlignment = Alignment.Center) {
                    CircularProgressIndicator()
                }
            }
        ui.containerError != null -> item(key = "watch-episodes-error") {
            Column(Modifier.padding(horizontal = 16.dp)) {
                Text(
                    stringResource(R.string.watch_episodes_load_failed),
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                TextButton(onClick = onRetry) { Text(stringResource(R.string.try_again)) }
            }
        }
        ui.selectedEpisodes.isEmpty() -> item(key = "watch-episodes-empty") {
            Text(
                stringResource(R.string.watch_no_episodes),
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(horizontal = 16.dp),
            )
        }
        else -> items(ui.selectedEpisodes, key = { "ep-${it.id}" }) { ep ->
            EpisodeRow(
                episode = ep,
                busy = ep.id in ui.episodeBusy,
                onOpen = { onOpenEpisode(ep.id) },
                onToggle = { onToggleEpisode(ep) },
            )
        }
    }
}

@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun EpisodeRow(
    episode: ChildItem,
    busy: Boolean,
    onOpen: () -> Unit,
    onToggle: () -> Unit,
) {
    val toggleLabel = stringResource(
        if (episode.watched) R.string.watch_mark_unwatched else R.string.watch_mark_watched,
    )
    val progress = if (episode.watched) 0f else progressFraction(episode.view_offset_ms, episode.duration_ms)
    Row(
        verticalAlignment = Alignment.CenterVertically,
        modifier = Modifier
            .fillMaxWidth()
            .combinedClickable(
                onClick = onOpen,
                onLongClickLabel = toggleLabel,
                onLongClick = { if (!busy) onToggle() },
            )
            .padding(start = 16.dp, end = 4.dp, top = 4.dp, bottom = 4.dp),
    ) {
        if (episode.index != null) {
            // "1–2." for a multi-episode file.
            Text(
                "${episodeNumberLabel(episode.index, episode.index_end)}.",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                // At least the old column, wider for a range ("12–13.").
                modifier = Modifier.widthIn(min = 40.dp).padding(end = 4.dp),
            )
        }
        Column(modifier = Modifier.weight(1f)) {
            Text(
                episode.title,
                style = MaterialTheme.typography.bodyLarge,
                color = if (episode.watched) MaterialTheme.colorScheme.onSurfaceVariant
                    else MaterialTheme.colorScheme.onSurface,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
            val minutes = (episode.duration_ms ?: 0L) / 60_000L
            if (minutes > 0) {
                Text(
                    stringResource(R.string.watch_minutes, minutes),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            if (progress > 0f) {
                Spacer(Modifier.height(4.dp))
                LinearProgressIndicator(
                    progress = { progress },
                    modifier = Modifier.fillMaxWidth(0.6f).height(3.dp),
                )
            }
        }
        IconButton(onClick = onToggle, enabled = !busy) {
            if (episode.watched) {
                Icon(
                    Icons.Default.CheckCircle,
                    contentDescription = stringResource(R.string.watch_episode_watched_action, episode.title),
                    tint = MaterialTheme.colorScheme.primary,
                )
            } else {
                Icon(
                    Icons.Outlined.RadioButtonUnchecked,
                    contentDescription = stringResource(R.string.watch_episode_unwatched_action, episode.title),
                    tint = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
    }
}

/** Is item [index] laid out entirely inside the row's viewport? */
private fun LazyListState.isFullyVisible(index: Int): Boolean {
    val info = layoutInfo
    val item = info.visibleItemsInfo.firstOrNull { it.index == index } ?: return false
    return item.offset >= info.viewportStartOffset &&
        item.offset + item.size <= info.viewportEndOffset
}

/** A season chip's name: the server's title, else "Specials" / "Season 2". */
internal fun seasonLabel(season: ChildItem): UiText = when {
    season.title.isNotBlank() -> UiText.raw(season.title)
    season.index == 0 -> UiText.res(R.string.watch_specials)
    season.index != null -> UiText.res(R.string.watch_season_n, season.index)
    else -> UiText.res(R.string.watch_season)
}
