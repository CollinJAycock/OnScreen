package tv.onscreen.mobile.ui.player

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Cast
import androidx.compose.material.icons.filled.Pause
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material.icons.filled.Stop
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Slider
import androidx.compose.material3.SliderDefaults
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.dp
import tv.onscreen.mobile.cast.CastPlaybackController

/**
 * The remote control shown over the (paused) local player while this item
 * plays on a Chromecast: where it is in the content, play / pause, a seek bar
 * and Stop. Seeks go through [CastPlaybackController.seekTo], which seeks the
 * receiver within its session or starts a new session where it can't.
 */
@Composable
internal fun CastControlsPanel(
    state: CastPlaybackController.State,
    onTogglePause: () -> Unit,
    onSeek: (Long) -> Unit,
    onStop: () -> Unit,
    modifier: Modifier = Modifier,
) {
    // The thumb follows the finger while dragging, the receiver otherwise.
    var dragging by remember { mutableStateOf<Float?>(null) }
    val duration = state.durationMs.takeIf { it > 0 }
    Column(
        modifier = modifier
            .widthIn(max = 520.dp)
            .fillMaxWidth()
            .background(Color(0xE0101010), RoundedCornerShape(12.dp))
            .padding(horizontal = 16.dp, vertical = 12.dp),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Icon(Icons.Default.Cast, contentDescription = null, tint = Color.White)
            Spacer(Modifier.size(8.dp))
            Text(
                text = state.deviceName?.let { "Casting to $it" } ?: "Casting",
                color = Color.White,
                style = MaterialTheme.typography.titleSmall,
                modifier = Modifier.weight(1f),
            )
            if (state.starting || state.buffering) {
                CircularProgressIndicator(modifier = Modifier.size(18.dp), strokeWidth = 2.dp, color = Color.White)
            }
        }
        if (duration != null) {
            val fraction = dragging ?: (state.positionMs.toFloat() / duration).coerceIn(0f, 1f)
            Slider(
                value = fraction,
                onValueChange = { dragging = it },
                onValueChangeFinished = {
                    dragging?.let { onSeek((it * duration).toLong()) }
                    dragging = null
                },
                enabled = state.loaded,
                colors = SliderDefaults.colors(
                    thumbColor = Color.White,
                    activeTrackColor = Color.White,
                    inactiveTrackColor = Color(0x66FFFFFF),
                ),
            )
        }
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.SpaceBetween) {
            val shown = dragging?.let { d -> duration?.let { (d * it).toLong() } } ?: state.positionMs
            Text(
                text = formatCastMs(shown) + (duration?.let { " / " + formatCastMs(it) } ?: ""),
                color = Color.White,
                style = MaterialTheme.typography.labelMedium,
                modifier = Modifier.weight(1f),
            )
            IconButton(onClick = onTogglePause, enabled = state.loaded) {
                Icon(
                    if (state.playing) Icons.Default.Pause else Icons.Default.PlayArrow,
                    contentDescription = if (state.playing) "Pause" else "Play",
                    tint = Color.White,
                )
            }
            IconButton(onClick = onStop) {
                Icon(Icons.Default.Stop, contentDescription = "Stop casting", tint = Color.White)
            }
        }
        if (state.subtitlesDropped) {
            Text(
                text = "Subtitles cast only when starting from the beginning.",
                color = Color(0xCCFFFFFF),
                style = MaterialTheme.typography.bodySmall,
            )
        }
    }
}

/** A cast refusal or failure (too many streams, Dolby Vision, a damaged
 *  file, a receiver that couldn't play it). */
@Composable
internal fun CastNoticeDialog(notice: CastPlaybackController.Notice, onDismiss: () -> Unit) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(notice.title ?: "Can't cast") },
        text = { Text(notice.message) },
        confirmButton = { TextButton(onClick = onDismiss) { Text("OK") } },
    )
}

private fun formatCastMs(ms: Long): String {
    val total = (ms.coerceAtLeast(0) / 1000)
    val h = total / 3600
    val m = (total % 3600) / 60
    val s = total % 60
    return if (h > 0) "%d:%02d:%02d".format(h, m, s) else "%d:%02d".format(m, s)
}
