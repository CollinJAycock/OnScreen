package tv.onscreen.mobile.playback

import android.content.ContentProvider
import android.content.ContentValues
import android.content.Context
import android.database.Cursor
import android.net.Uri
import android.os.ParcelFileDescriptor
import dagger.hilt.EntryPoint
import dagger.hilt.InstallIn
import dagger.hilt.android.EntryPointAccessors
import dagger.hilt.components.SingletonComponent
import kotlinx.coroutines.runBlocking
import okhttp3.OkHttpClient
import okhttp3.Request
import tv.onscreen.mobile.data.artworkUrl
import tv.onscreen.mobile.data.prefs.ServerPrefs
import java.io.File
import java.io.FileNotFoundException
import java.security.MessageDigest

/**
 * Artwork for the car. Android Auto draws browse tiles and the now-playing
 * cover itself, from the uri in each item's metadata — but the server's
 * /artwork needs the account's credential, and a credential in a uri handed
 * to another app (or published on the platform media session) is readable
 * by whoever holds it. So the uri is `content://<package>.carart/a?p=<path>&w=`
 * and this provider fetches the image with the app's authenticated client
 * (Bearer header, as Coil does), caching it under cacheDir.
 *
 * Not exported: [CarLibraryCallback] grants read access to the media
 * browsers it lets browse (the car), so no other app can use it to read the
 * library's artwork.
 */
class CarArtworkProvider : ContentProvider() {

    @EntryPoint
    @InstallIn(SingletonComponent::class)
    interface Deps {
        fun okHttpClient(): OkHttpClient
        fun serverPrefs(): ServerPrefs
    }

    override fun onCreate(): Boolean = true

    override fun getType(uri: Uri): String = "image/*"

    override fun openFile(uri: Uri, mode: String): ParcelFileDescriptor {
        if (mode != "r") throw SecurityException("read-only")
        val ctx = context ?: throw FileNotFoundException("no context")
        val path = artworkPath(uri) ?: throw FileNotFoundException("not an artwork uri")
        val width = uri.getQueryParameter(PARAM_WIDTH)?.toIntOrNull()?.coerceIn(MIN_WIDTH, MAX_WIDTH) ?: DEFAULT_WIDTH
        val deps = EntryPointAccessors.fromApplication(ctx.applicationContext, Deps::class.java)
        val server = runBlocking { deps.serverPrefs().getServerUrl() }?.trimEnd('/')
            ?: throw FileNotFoundException("signed out")

        val dir = File(ctx.cacheDir, CACHE_DIR).apply { mkdirs() }
        // Keyed by server too: another server's art never shows for this one.
        val file = File(dir, sha1("$server|$path|$width"))
        if (!file.exists()) {
            fetch(deps.okHttpClient(), artworkUrl(server, path, width), file)
            trim(dir)
        }
        return ParcelFileDescriptor.open(file, ParcelFileDescriptor.MODE_READ_ONLY)
    }

    private fun fetch(client: OkHttpClient, url: String, into: File) {
        val tmp = File(into.parentFile, into.name + ".part")
        client.newCall(Request.Builder().url(url).build()).execute().use { resp ->
            val body = resp.body
            if (!resp.isSuccessful || body == null) throw FileNotFoundException("artwork: HTTP ${resp.code}")
            tmp.outputStream().use { out -> body.byteStream().copyTo(out) }
        }
        if (!tmp.renameTo(into)) {
            tmp.delete()
            if (!into.exists()) throw FileNotFoundException("artwork: cache write failed")
        }
    }

    /** Keep the cache to [MAX_FILES]: drop the oldest beyond it. */
    private fun trim(dir: File) {
        val files = dir.listFiles()?.filter { !it.name.endsWith(".part") } ?: return
        if (files.size <= MAX_FILES) return
        files.sortedBy { it.lastModified() }.take(files.size - KEEP_FILES).forEach { it.delete() }
    }

    override fun query(uri: Uri, projection: Array<out String>?, selection: String?, selectionArgs: Array<out String>?, sortOrder: String?): Cursor? = null
    override fun insert(uri: Uri, values: ContentValues?): Uri? = null
    override fun delete(uri: Uri, selection: String?, selectionArgs: Array<out String>?): Int = 0
    override fun update(uri: Uri, values: ContentValues?, selection: String?, selectionArgs: Array<out String>?): Int = 0

    companion object {
        private const val AUTHORITY_SUFFIX = ".carart"
        private const val PATH = "a"
        private const val PARAM_PATH = "p"
        private const val PARAM_WIDTH = "w"
        private const val CACHE_DIR = "car-art"
        private const val MAX_FILES = 400
        private const val KEEP_FILES = 300
        private const val MIN_WIDTH = 64
        private const val MAX_WIDTH = 1024
        const val DEFAULT_WIDTH = 400

        fun authority(context: Context): String = context.packageName + AUTHORITY_SUFFIX

        /** Every artwork uri starts with this (what a browser is granted). */
        fun rootUri(context: Context): Uri =
            Uri.Builder().scheme("content").authority(authority(context)).build()

        fun uri(context: Context, artworkPath: String, width: Int = DEFAULT_WIDTH): Uri =
            Uri.Builder().scheme("content").authority(authority(context))
                .appendPath(PATH)
                .appendQueryParameter(PARAM_PATH, artworkPath)
                .appendQueryParameter(PARAM_WIDTH, width.toString())
                .build()

        /** The server artwork path in [uri], or null when it isn't one of
         *  ours or tries to climb out of /artwork. */
        fun artworkPath(uri: Uri): String? {
            if (uri.pathSegments != listOf(PATH)) return null
            val p = uri.getQueryParameter(PARAM_PATH)?.trim('/') ?: return null
            if (p.isEmpty() || p.contains("://") || p.split('/').any { it == ".." || it == "." }) return null
            return p
        }

        private fun sha1(s: String): String =
            MessageDigest.getInstance("SHA-1").digest(s.toByteArray()).joinToString("") { "%02x".format(it) }
    }
}
