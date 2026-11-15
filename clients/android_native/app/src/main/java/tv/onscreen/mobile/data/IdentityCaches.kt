package tv.onscreen.mobile.data

import android.content.Context
import coil.annotation.ExperimentalCoilApi
import coil.imageLoader
import dagger.hilt.android.qualifiers.ApplicationContext
import kotlinx.coroutines.launch
import tv.onscreen.mobile.data.repository.ItemRepository
import tv.onscreen.mobile.playback.CarArtworkProvider
import tv.onscreen.mobile.ui.book.EpubCache
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The device-side caches of what the signed-in account could see, emptied by
 * AuthRepository on every identity change (sign-in, sign-out, involuntary
 * sign-out, pairing) alongside its in-memory ones. They used to outlive a
 * sign-out:
 *
 * - Coil's memory and disk caches: posters, backdrops and photos, served to
 *   the next account from cache without asking the server.
 * - `cacheDir/car-art`: the car's artwork, served by CarArtworkProvider on a
 *   hit without the server.
 * - `cacheDir/book-epubs`: whole books ([EpubCache]).
 * - ItemRepository's recent items: up to 64 items, with 24 h stream tokens.
 *
 * The memory caches go at once; the files on a background thread, since
 * sign-in calls this from the main thread.
 */
@Singleton
open class IdentityCaches @Inject constructor(
    @ApplicationContext private val context: Context,
    private val items: ItemRepository,
) {
    private val scope = backgroundScope("IdentityCaches")

    @OptIn(ExperimentalCoilApi::class)
    open fun clear() {
        items.clearCachedItems()
        val images = context.imageLoader
        images.memoryCache?.clear()
        scope.launch {
            runCatching { images.diskCache?.clear() }
            runCatching { CarArtworkProvider.cacheDir(context).deleteRecursively() }
            runCatching { EpubCache.dir(context).deleteRecursively() }
        }
    }
}
