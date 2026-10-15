package tv.onscreen.mobile.ui.item

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Shape
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.async
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.receiveAsFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import retrofit2.HttpException
import tv.onscreen.mobile.data.artworkUrl
import tv.onscreen.mobile.data.model.ArtistAppearance
import tv.onscreen.mobile.data.model.ItemDetail
import tv.onscreen.mobile.data.model.SimilarArtist
import tv.onscreen.mobile.data.repository.MusicRepository
import tv.onscreen.mobile.playback.InstantMixQueue
import javax.inject.Inject

/** An album another artist released that this one appears on, from the
 *  appears-on tracks (which the server lists newest album first). */
data class AppearsOnAlbum(
    val id: String,
    val title: String,
    val year: Int?,
    val posterPath: String?,
    /** Whose album it is ("Various Artists" for a compilation). */
    val artistTitle: String?,
    /** How many of its tracks this artist is on. */
    val trackCount: Int,
)

/** Group appears-on tracks into their albums, in the server's order (each
 *  album where its first track was). */
fun appearsOnAlbums(tracks: List<ArtistAppearance>): List<AppearsOnAlbum> =
    tracks.filter { it.album_id.isNotBlank() }
        .groupBy { it.album_id }
        .map { (id, ts) ->
            val t = ts.first()
            AppearsOnAlbum(
                id = id,
                title = t.album_title.ifBlank { "Untitled album" },
                year = t.album_year,
                posterPath = t.album_poster_path,
                artistTitle = t.album_artist_title,
                trackCount = ts.distinctBy { it.id }.size,
            )
        }

/** Item types an instant mix can be seeded from. */
fun canSeedInstantMix(type: String?): Boolean = type == "artist" || type == "album" || type == "track"

data class MusicExtrasUi(
    /** The artist these rows are for (null: not an artist page). */
    val artistId: String? = null,
    val similar: List<SimilarArtist> = emptyList(),
    val appearsOn: List<AppearsOnAlbum> = emptyList(),
    /** An instant mix is being fetched. */
    val mixLoading: Boolean = false,
)

/**
 * The music page's v2.6 discovery extras: on an artist, "Similar artists"
 * and "Appears on" rows; on an artist, album or track, "Instant mix", which
 * plays GET /music/mix as a queue. Everything is best-effort: an older server
 * (404) simply shows no rows, and a failed mix says so.
 */
@HiltViewModel
class MusicExtrasViewModel @Inject constructor(
    private val music: MusicRepository,
) : ViewModel() {

    private val _state = MutableStateFlow(MusicExtrasUi())
    val state: StateFlow<MusicExtrasUi> = _state.asStateFlow()

    private val _playMix = Channel<String>(Channel.BUFFERED)

    /** The track id to open the player on, once a mix is parked in
     *  [InstantMixQueue]. */
    val playMix: Flow<String> = _playMix.receiveAsFlow()

    private val _messages = Channel<String>(Channel.BUFFERED)
    val messages: Flow<String> = _messages.receiveAsFlow()

    /** A seam for tests. */
    internal var clock: () -> Long = { android.os.SystemClock.elapsedRealtime() }

    fun bind(detail: ItemDetail) {
        if (detail.type != "artist") {
            _state.update { MusicExtrasUi(mixLoading = it.mixLoading) }
            return
        }
        if (_state.value.artistId == detail.id) return
        val id = detail.id
        _state.update { MusicExtrasUi(artistId = id, mixLoading = it.mixLoading) }
        viewModelScope.launch {
            val similar = async { orEmpty { music.similarArtists(id) } }
            val appears = async { orEmpty { music.appearsOn(id) } }
            val s = similar.await()
            val a = appearsOnAlbums(appears.await())
            if (_state.value.artistId != id) return@launch
            _state.update { it.copy(similar = s.filter { x -> x.id != id }, appearsOn = a) }
        }
    }

    /** Fetch a mix seeded by [seedId] and play it. */
    fun instantMix(seedId: String) {
        if (_state.value.mixLoading) return
        _state.update { it.copy(mixLoading = true) }
        viewModelScope.launch {
            val text = try {
                val first = InstantMixQueue.put(music.instantMix(seedId), clock())
                if (first != null) {
                    _playMix.trySend(first)
                    null
                } else {
                    "Nothing to mix from here yet."
                }
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                if ((e as? HttpException)?.code() == 404) {
                    "Instant mix isn’t available on this server."
                } else {
                    "Couldn’t make a mix. Try again."
                }
            }
            _state.update { it.copy(mixLoading = false) }
            text?.let { _messages.trySend(it) }
        }
    }

    private inline fun <T> orEmpty(block: () -> List<T>): List<T> = try {
        block()
    } catch (e: CancellationException) {
        throw e
    } catch (_: Exception) {
        emptyList()
    }
}

/** "Similar artists": round portraits. */
@Composable
internal fun SimilarArtistsRow(
    artists: List<SimilarArtist>,
    serverUrl: String,
    onOpen: (String) -> Unit,
) {
    MusicRow(title = "Similar artists") {
        items(artists, key = { it.id }) { a ->
            MusicCard(
                title = a.title,
                subtitle = a.shared_genres.firstOrNull(),
                art = a.poster_path?.takeIf { serverUrl.isNotEmpty() }?.let { artworkUrl(serverUrl, it, width = 300) },
                shape = CircleShape,
                onClick = { onOpen(a.id) },
            )
        }
    }
}

/** "Appears on": albums by other artists that feature this one. */
@Composable
internal fun AppearsOnRow(
    albums: List<AppearsOnAlbum>,
    serverUrl: String,
    onOpen: (String) -> Unit,
) {
    MusicRow(title = "Appears on") {
        items(albums, key = { it.id }) { al ->
            MusicCard(
                title = al.title,
                subtitle = listOfNotNull(al.artistTitle, al.year?.toString()).joinToString(" · ").ifEmpty { null },
                art = al.posterPath?.takeIf { serverUrl.isNotEmpty() }?.let { artworkUrl(serverUrl, it, width = 300) },
                shape = RoundedCornerShape(8.dp),
                onClick = { onOpen(al.id) },
            )
        }
    }
}

@Composable
private fun MusicRow(title: String, content: androidx.compose.foundation.lazy.LazyListScope.() -> Unit) {
    Column {
        Spacer(Modifier.height(16.dp))
        Text(title, style = MaterialTheme.typography.titleMedium, modifier = Modifier.padding(horizontal = 16.dp))
        Spacer(Modifier.height(8.dp))
        LazyRow(
            contentPadding = PaddingValues(horizontal = 16.dp),
            horizontalArrangement = Arrangement.spacedBy(12.dp),
            content = content,
        )
    }
}

@Composable
private fun MusicCard(
    title: String,
    subtitle: String?,
    art: String?,
    shape: Shape,
    onClick: () -> Unit,
) {
    Column(modifier = Modifier.width(120.dp).clickable(onClick = onClick)) {
        Box(
            modifier = Modifier
                .fillMaxWidth()
                .aspectRatio(1f)
                .clip(shape)
                .background(MaterialTheme.colorScheme.surfaceVariant),
        ) {
            if (art != null) {
                coil.compose.AsyncImage(
                    model = art,
                    contentDescription = null,
                    contentScale = ContentScale.Crop,
                    modifier = Modifier.fillMaxSize(),
                )
            }
        }
        Spacer(Modifier.height(6.dp))
        Text(title, style = MaterialTheme.typography.bodyMedium, maxLines = 2, overflow = TextOverflow.Ellipsis)
        subtitle?.let {
            Text(
                it,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
    }
}
