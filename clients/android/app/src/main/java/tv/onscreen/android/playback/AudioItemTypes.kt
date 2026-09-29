package tv.onscreen.android.playback

/**
 * Which item types the TV app plays as audio. Pure, JVM-tested in
 * AudioItemTypesTest.
 *
 * An audio item:
 *  - keeps playing on HOME / BACK — PlaybackFragment parks its player in
 *    [OnScreenMediaSessionService] instead of releasing it, and the service
 *    chains it to what comes next;
 *  - gets the cover-art backdrop instead of a bare video surface;
 *  - chains to the next item at its end with no Up Next card. The card's
 *    lead-in would cover a song's outro, or a chapter's last lines.
 *
 * A multi-file book's `audiobook_chapter` is audio like the book itself;
 * it used to fall through to video, so leaving the player stopped the book.
 * Everything else is video: it pauses on HOME and is released when the
 * player closes. The phone client makes the same call for these types
 * (PlayerScreen's isAudioOnly).
 */
object AudioItemTypes {

    fun isAudio(type: String?): Boolean =
        type == TRACK || type == AudiobookSpeed.AUDIOBOOK || type == AudiobookSpeed.CHAPTER

    const val TRACK = "track"

    /** How an audio item's player declares its output, from its first
     *  frame. The background service needs music attributes; set there
     *  only, they changed on the live player at every BACK / HOME, which
     *  rebuilt the audio output and briefly stalled playback. With the
     *  fragment using the same attributes, the service's call is a no-op.
     *  Video keeps the fragment's movie attributes. */
    val MUSIC_ATTRIBUTES: androidx.media3.common.AudioAttributes =
        androidx.media3.common.AudioAttributes.Builder()
            .setUsage(androidx.media3.common.C.USAGE_MEDIA)
            .setContentType(androidx.media3.common.C.AUDIO_CONTENT_TYPE_MUSIC)
            .build()
}
