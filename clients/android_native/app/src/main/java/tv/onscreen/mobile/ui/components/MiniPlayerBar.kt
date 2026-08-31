package tv.onscreen.mobile.ui.components

import android.content.ComponentName
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.MusicNote
import androidx.compose.material.icons.filled.Pause
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.core.content.ContextCompat
import androidx.media3.common.Player
import androidx.media3.session.MediaController
import androidx.media3.session.SessionToken
import tv.onscreen.mobile.playback.PlaybackService

/**
 * Compact now-playing bar for the main navigation shell.
 *
 * Background audio deliberately survives leaving the player screen — but
 * before this bar existed there was NO in-app surface to pause, stop, or
 * reopen it afterwards: BACK from now-playing left the queue running with
 * the notification as the only control, and the E2E suite showed even
 * KEYCODE_MEDIA_STOP being ignored. The bar renders only while the
 * background service holds a current item, and only on shell destinations
 * (AppNav hides it on immersive routes and on the player itself).
 *
 * Tap → reopen the full player for the current item. The ✕ stops playback
 * via controller.stop(); PlaybackService reacts to STATE_IDLE by publishing
 * the terminal progress report and tearing itself down, so the notification
 * doesn't linger as a paused ghost.
 */
@Composable
fun MiniPlayerBar(onOpen: (String) -> Unit) {
    val context = LocalContext.current
    var controller by remember { mutableStateOf<MediaController?>(null) }
    var mediaId by remember { mutableStateOf<String?>(null) }
    var title by remember { mutableStateOf("") }
    var playing by remember { mutableStateOf(false) }

    DisposableEffect(Unit) {
        val token = SessionToken(context, ComponentName(context, PlaybackService::class.java))
        val future = MediaController.Builder(context, token).buildAsync()
        val listener = object : Player.Listener {
            override fun onEvents(player: Player, events: Player.Events) {
                mediaId = player.currentMediaItem?.mediaId
                title = player.mediaMetadata.title?.toString().orEmpty()
                playing = player.isPlaying
            }
        }
        future.addListener({
            val c = runCatching { future.get() }.getOrNull() ?: return@addListener
            controller = c
            c.addListener(listener)
            // Seed from current state — onEvents only fires on changes, and
            // audio may already be playing when the shell (re)composes.
            mediaId = c.currentMediaItem?.mediaId
            title = c.mediaMetadata.title?.toString().orEmpty()
            playing = c.isPlaying
        }, ContextCompat.getMainExecutor(context))
        onDispose {
            controller?.removeListener(listener)
            MediaController.releaseFuture(future)
            controller = null
        }
    }

    val id = mediaId ?: return
    Surface(
        tonalElevation = 3.dp,
        modifier = Modifier
            .fillMaxWidth()
            .navigationBarsPadding(),
    ) {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            modifier = Modifier
                .fillMaxWidth()
                .clickable { onOpen(id) }
                .padding(horizontal = 12.dp, vertical = 6.dp),
        ) {
            Icon(
                Icons.Default.MusicNote,
                contentDescription = null,
                tint = MaterialTheme.colorScheme.primary,
            )
            Text(
                title.ifEmpty { "Now playing" },
                style = MaterialTheme.typography.bodyMedium,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier
                    .weight(1f)
                    .padding(horizontal = 12.dp),
            )
            IconButton(onClick = {
                controller?.let { if (it.isPlaying) it.pause() else it.play() }
            }) {
                Icon(
                    if (playing) Icons.Default.Pause else Icons.Default.PlayArrow,
                    contentDescription = if (playing) "Pause" else "Play",
                )
            }
            IconButton(onClick = {
                // stop() FIRST — the service's STATE_IDLE handler publishes
                // the terminal progress from currentMediaItem, so the item
                // must still exist at that moment. THEN clear, which drops
                // currentMediaItem and hides this bar (verified on device:
                // stop() alone left the bar lingering in a stopped state).
                controller?.run {
                    stop()
                    clearMediaItems()
                }
            }) {
                Icon(Icons.Default.Close, contentDescription = "Stop")
            }
        }
    }
}
