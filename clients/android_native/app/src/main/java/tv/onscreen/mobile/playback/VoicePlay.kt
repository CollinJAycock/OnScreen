package tv.onscreen.mobile.playback

import android.app.SearchManager
import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.provider.MediaStore
import androidx.core.content.ContextCompat
import androidx.media3.common.MediaItem
import androidx.media3.common.Player
import androidx.media3.session.MediaController
import androidx.media3.session.SessionToken

/**
 * "Play <something> on OnScreen" said to the Assistant arrives as a
 * MEDIA_PLAY_FROM_SEARCH intent at MainActivity (Android Auto's own voice
 * requests reach the session directly). The query goes to the background
 * player as a search request, the same path the car's uses
 * ([CarLibraryCallback.onSetMediaItems]): the best audio match plays, and a
 * blank query resumes what was playing.
 */
object VoicePlay {

    /** The query of a play-from-search [intent] ("" when blank), else null. */
    fun query(intent: Intent?): String? {
        if (intent?.action != MediaStore.INTENT_ACTION_MEDIA_PLAY_FROM_SEARCH) return null
        return intent.getStringExtra(SearchManager.QUERY).orEmpty()
    }

    fun play(context: Context, query: String) {
        val app = context.applicationContext
        val token = SessionToken(app, ComponentName(app, PlaybackService::class.java))
        val future = MediaController.Builder(app, token).buildAsync()
        future.addListener(
            {
                val controller = runCatching { future.get() }.getOrNull() ?: return@addListener
                controller.setMediaItem(searchRequest(query))
                controller.prepare()
                controller.play()
                // Hand the controller back once the request has landed.
                controller.addListener(object : Player.Listener {
                    override fun onEvents(player: Player, events: Player.Events) {
                        if (player.isPlaying || player.playerError != null) controller.release()
                    }
                })
            },
            ContextCompat.getMainExecutor(app),
        )
    }

    /** A queue request carrying only a search query. */
    fun searchRequest(query: String): MediaItem =
        MediaItem.Builder()
            .setRequestMetadata(MediaItem.RequestMetadata.Builder().setSearchQuery(query).build())
            .build()
}
