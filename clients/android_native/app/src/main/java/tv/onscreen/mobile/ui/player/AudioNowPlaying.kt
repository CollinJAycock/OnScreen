package tv.onscreen.mobile.ui.player

import android.graphics.BitmapFactory
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.WindowInsetsSides
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.only
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawing
import androidx.compose.foundation.layout.windowInsetsPadding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Headphones
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.media3.common.C
import androidx.media3.common.MediaMetadata
import androidx.media3.common.Player
import androidx.media3.common.Tracks
import coil.compose.AsyncImage
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

/**
 * The player's page. Video, and audio carrying a picture track, fills the
 * screen with [surface] (the PlayerView). Audio gets a now-playing page:
 * the cover and what is playing above the controls ([surface], sized to
 * them), beside them in landscape.
 *
 * The audio page used to be the bare PlayerView, whose own artwork comes
 * only from art embedded in the file. Most libraries keep the cover as a
 * folder image instead (the scanner files it on the album or the book), so
 * the page was black with the controls in the middle.
 *
 * [surface] is called from this one place whatever the layout: the activity
 * handles rotation itself, and a PlayerView built afresh per layout left the
 * previous one registered on the player until the screen closed.
 *
 * The screen draws its top bar over everything: the audio page starts at
 * [topPadding], below it.
 */
@Composable
internal fun PlayerPage(
    audio: Boolean,
    landscape: Boolean,
    player: Player,
    artworkUrl: String?,
    artworkChecked: Boolean,
    fallbackTitle: String?,
    topPadding: Dp,
    surface: @Composable (Modifier) -> Unit,
) {
    val density = LocalDensity.current
    val bottomInset = with(density) { WindowInsets.safeDrawing.getBottom(density).toDp() }
    // The controller pads itself clear of the navigation bar, so it is laid
    // out that much taller.
    val controlsHeight = AUDIO_CONTROLS_HEIGHT + bottomInset
    Box(Modifier.fillMaxSize()) {
        if (audio) {
            NowPlayingInfo(
                player = player,
                artworkUrl = artworkUrl,
                artworkChecked = artworkChecked,
                fallbackTitle = fallbackTitle,
                landscape = landscape,
                modifier = if (landscape) {
                    Modifier
                        .align(Alignment.CenterStart)
                        .fillMaxWidth(COVER_WIDTH_FRACTION)
                        .fillMaxHeight()
                        .padding(top = topPadding)
                        .windowInsetsPadding(
                            WindowInsets.safeDrawing.only(WindowInsetsSides.Start + WindowInsetsSides.Bottom),
                        )
                        .padding(16.dp)
                } else {
                    Modifier.fillMaxSize().padding(top = topPadding, bottom = controlsHeight)
                },
            )
        }
        surface(
            when {
                !audio -> Modifier.fillMaxSize()
                // The full height: the top bar lies over the controller's
                // empty top, and the transport row and the seek bar get the
                // most room apart.
                landscape -> Modifier
                    .align(Alignment.CenterEnd)
                    .fillMaxWidth(1f - COVER_WIDTH_FRACTION)
                    .fillMaxHeight()
                else -> Modifier.align(Alignment.BottomCenter).fillMaxWidth().height(controlsHeight)
            },
        )
    }
}

/** Height the audio page gives the controls in portrait. Media3's controller
 *  centres its transport row (~100dp) and keeps the seek bar and bottom bar
 *  (~100dp) at its foot; the two clear each other only from ~300dp up. */
private val AUDIO_CONTROLS_HEIGHT = 316.dp

/** Share of a landscape screen's width the cover side takes. */
private const val COVER_WIDTH_FRACTION = 0.45f

/** The cover, and the title with the artist or author and the album or book
 *  under it. The service's queue names the item; the file's tags, read by
 *  the player, name the rest. */
@Composable
private fun NowPlayingInfo(
    player: Player,
    artworkUrl: String?,
    artworkChecked: Boolean,
    fallbackTitle: String?,
    landscape: Boolean,
    modifier: Modifier,
) {
    val metadata = rememberMediaMetadata(player)
    val title = metadata.title?.toString()?.takeIf { it.isNotBlank() } ?: fallbackTitle
    val subtitle = listOfNotNull(metadata.artist ?: metadata.albumArtist, metadata.albumTitle)
        .map { it.toString() }
        .filter { it.isNotBlank() && it != title }
        .distinct()
        .joinToString(" · ")
    if (landscape) {
        Column(
            modifier = modifier,
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.Center,
        ) {
            AudioCover(artworkUrl, artworkChecked, metadata.artworkData, Modifier.weight(1f, fill = false))
            NowPlayingText(title, subtitle, Modifier.fillMaxWidth().padding(top = 12.dp))
        }
    } else {
        Column(modifier) {
            Box(
                modifier = Modifier
                    .weight(1f)
                    .fillMaxWidth()
                    .windowInsetsPadding(WindowInsets.safeDrawing.only(WindowInsetsSides.Horizontal))
                    .padding(horizontal = 32.dp, vertical = 8.dp),
                contentAlignment = Alignment.Center,
            ) {
                AudioCover(artworkUrl, artworkChecked, metadata.artworkData, Modifier)
            }
            NowPlayingText(title, subtitle, Modifier.fillMaxWidth().padding(horizontal = 24.dp))
        }
    }
}

/**
 * The cover, as large a square as [modifier] leaves room for, the picture
 * fitted inside it (a book's cover is taller than it is wide): [url], else
 * [embedded] art from the file (a download played offline), else a
 * placeholder — but only once the lookup is [checked]. Until then the square
 * stays dark: a screen that follows the queue to the next track would
 * otherwise flash the placeholder before the cover.
 */
@Composable
private fun AudioCover(url: String?, checked: Boolean, embedded: ByteArray?, modifier: Modifier) {
    val square = modifier.aspectRatio(1f, matchHeightConstraintsFirst = true)
    var failed by remember(url) { mutableStateOf(false) }
    if (url != null && !failed) {
        AsyncImage(
            model = url,
            contentDescription = null,
            contentScale = ContentScale.Fit,
            onError = { failed = true },
            modifier = square,
        )
        return
    }
    val art = rememberEmbeddedArtwork(embedded)
    when {
        art != null -> Image(
            bitmap = art,
            contentDescription = null,
            contentScale = ContentScale.Fit,
            modifier = square,
        )
        !checked && !failed -> Box(square)
        else -> Box(
            modifier = square.clip(RoundedCornerShape(12.dp)).background(Color(0xFF1C1C1C)),
            contentAlignment = Alignment.Center,
        ) {
            Icon(
                Icons.Default.Headphones,
                contentDescription = null,
                tint = Color(0xFF5C5C5C),
                modifier = Modifier.fillMaxSize(0.35f),
            )
        }
    }
}

@Composable
private fun NowPlayingText(title: String?, subtitle: String, modifier: Modifier) {
    Column(modifier, horizontalAlignment = Alignment.CenterHorizontally) {
        if (title != null) {
            Text(
                text = title,
                color = Color.White,
                style = MaterialTheme.typography.titleLarge,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
                textAlign = TextAlign.Center,
            )
        }
        // Laid out even while empty: the artist arrives with the file's tags,
        // a moment after the title, and the cover shrank to make room.
        Text(
            text = subtitle.ifEmpty { " " },
            color = Color.White.copy(alpha = 0.7f),
            style = MaterialTheme.typography.bodyMedium,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            textAlign = TextAlign.Center,
            modifier = Modifier.padding(top = 4.dp),
        )
    }
}

/** [player]'s metadata, kept current as it changes (the next track, the
 *  file's tags once read). */
@Composable
private fun rememberMediaMetadata(player: Player): MediaMetadata {
    var metadata by remember(player) { mutableStateOf(player.mediaMetadata) }
    DisposableEffect(player) {
        val listener = object : Player.Listener {
            override fun onMediaMetadataChanged(mediaMetadata: MediaMetadata) {
                metadata = mediaMetadata
            }
        }
        player.addListener(listener)
        onDispose { player.removeListener(listener) }
    }
    return metadata
}

/** Art embedded in the file, decoded off the main thread and scaled down by
 *  powers of two to under twice [EMBEDDED_ART_MAX_PX]: a tag can carry a
 *  3000px scan. */
@Composable
private fun rememberEmbeddedArtwork(data: ByteArray?): ImageBitmap? =
    produceState<ImageBitmap?>(initialValue = null, data) {
        value = data?.let { bytes ->
            withContext(Dispatchers.Default) { decodeScaled(bytes, EMBEDDED_ART_MAX_PX) }
        }
    }.value

private const val EMBEDDED_ART_MAX_PX = 1024

private fun decodeScaled(bytes: ByteArray, maxPx: Int): ImageBitmap? {
    val bounds = BitmapFactory.Options().apply { inJustDecodeBounds = true }
    BitmapFactory.decodeByteArray(bytes, 0, bytes.size, bounds)
    if (bounds.outWidth <= 0 || bounds.outHeight <= 0) return null
    var sample = 1
    while (maxOf(bounds.outWidth, bounds.outHeight) / (sample * 2) >= maxPx) sample *= 2
    val options = BitmapFactory.Options().apply { inSampleSize = sample }
    return BitmapFactory.decodeByteArray(bytes, 0, bytes.size, options)?.asImageBitmap()
}

/** Whether [player] shows a picture: a selected video track, like an
 *  illustrated audiobook's slideshow. Art embedded in a file is metadata,
 *  not a track, and doesn't count. */
@Composable
internal fun rememberShowsPicture(player: Player): Boolean {
    var picture by remember(player) { mutableStateOf(player.showsPicture()) }
    DisposableEffect(player) {
        val listener = object : Player.Listener {
            override fun onTracksChanged(tracks: Tracks) {
                picture = player.showsPicture()
            }
        }
        player.addListener(listener)
        onDispose { player.removeListener(listener) }
    }
    return picture
}

private fun Player.showsPicture(): Boolean =
    isCommandAvailable(Player.COMMAND_GET_TRACKS) &&
        currentTracks.groups.any { it.type == C.TRACK_TYPE_VIDEO && it.isSelected }
