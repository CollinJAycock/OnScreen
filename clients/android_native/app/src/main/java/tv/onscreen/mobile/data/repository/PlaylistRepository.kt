package tv.onscreen.mobile.data.repository

import tv.onscreen.mobile.data.api.OnScreenApi
import tv.onscreen.mobile.data.model.CreatePlaylistRequest
import tv.onscreen.mobile.data.model.Playlist
import tv.onscreen.mobile.data.model.SmartPlaylistRules
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Playlists + smart playlists. Static playlist add/remove items live
 * here too, alongside smart-playlist creation. v1 phone parity covers:
 *   - list (mine)
 *   - create static (no rules)
 *   - create smart (rules object)
 *   - delete
 *
 * Update / item-CRUD on static playlists are deferred — phone v1 ships
 * the create surface so a user can spin up a smart playlist on the go;
 * the manage-items surface for static playlists is a separate UX
 * iteration.
 */
@Singleton
open class PlaylistRepository @Inject constructor(
    private val api: OnScreenApi,
) {
    open suspend fun list(): List<Playlist> = api.listPlaylists().data

    /** Create a static playlist (no rules). */
    open suspend fun createStatic(name: String, description: String? = null): Playlist =
        api.createPlaylist(CreatePlaylistRequest(name = name, description = description)).data

    /** Create a smart playlist. The non-null [rules] flips the
     *  server-side type to `smart_playlist`. */
    open suspend fun createSmart(
        name: String,
        rules: SmartPlaylistRules,
        description: String? = null,
    ): Playlist =
        api.createPlaylist(
            CreatePlaylistRequest(name = name, description = description, rules = rules),
        ).data

    open suspend fun delete(id: String) {
        api.deletePlaylist(id)
    }
}
