package tv.onscreen.mobile.ui.player

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.VolumeDown
import androidx.compose.material.icons.automirrored.filled.VolumeOff
import androidx.compose.material.icons.automirrored.filled.VolumeUp
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.Slider
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.unit.dp
import androidx.media3.common.Player
import kotlin.math.roundToInt

/**
 * The app's own volume, for the Quest build: a Quest panel has no volume keys
 * of its own, and Meta asks media apps for a mute that silences the app only.
 * It is the player's volume (Player.volume), so the system volume and other
 * apps are untouched. Kept for the life of the process: the next title, and
 * the new player a session switch builds, play at the same level.
 */
internal object AppVolume {
    var level: Float = 1f
    var muted: Boolean = false

    fun effective(): Float = if (muted) 0f else level

    fun icon(level: Float, muted: Boolean): ImageVector = when {
        muted || level == 0f -> Icons.AutoMirrored.Filled.VolumeOff
        level < 0.5f -> Icons.AutoMirrored.Filled.VolumeDown
        else -> Icons.AutoMirrored.Filled.VolumeUp
    }
}

/** The player toolbar's volume button: a slider with mute beside it. */
@Composable
internal fun AppVolumeButton(player: Player) {
    var open by remember { mutableStateOf(false) }
    var level by remember { mutableFloatStateOf(AppVolume.level) }
    var muted by remember { mutableStateOf(AppVolume.muted) }
    // A new player (the next title, a session switch) starts at full volume.
    LaunchedEffect(player) { player.volume = AppVolume.effective() }

    fun apply() {
        AppVolume.level = level
        AppVolume.muted = muted
        player.volume = AppVolume.effective()
    }

    Box {
        IconButton(onClick = { open = true }) {
            Icon(AppVolume.icon(level, muted), contentDescription = "Volume", tint = Color.White)
        }
        DropdownMenu(expanded = open, onDismissRequest = { open = false }) {
            Row(
                verticalAlignment = Alignment.CenterVertically,
                modifier = Modifier.padding(horizontal = 8.dp).width(320.dp),
            ) {
                IconButton(onClick = {
                    muted = !muted
                    apply()
                }) {
                    Icon(
                        AppVolume.icon(level, muted),
                        contentDescription = if (muted) "Unmute" else "Mute",
                    )
                }
                Slider(
                    value = level,
                    onValueChange = {
                        level = it
                        // Turning it up is unmuting.
                        if (it > 0f) muted = false
                        apply()
                    },
                    modifier = Modifier.weight(1f),
                )
                Text(
                    if (muted) "Muted" else "${(level * 100).roundToInt()}%",
                    modifier = Modifier.padding(start = 8.dp).width(56.dp),
                )
            }
        }
    }
}
