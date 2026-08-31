package tv.onscreen.mobile.data.downloads

import android.content.Context
import com.squareup.moshi.Moshi
import dagger.hilt.android.qualifiers.ApplicationContext
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import java.io.File
import javax.inject.Inject
import javax.inject.Singleton

/** File-backed manifest of offline downloads. JSON on disk, in-memory
 *  StateFlow for the UI to observe. A simple manifest beats Room here
 *  for two reasons: the entry count is small (10s, not 1000s) and the
 *  on-disk format is human-inspectable for support. Atomic writes via
 *  `manifest.json.tmp` + rename so a crash mid-write can't corrupt
 *  the index. */
@Singleton
class DownloadStore @Inject constructor(
    @ApplicationContext private val context: Context,
    moshi: Moshi,
) {
    private val adapter = moshi.adapter(DownloadManifest::class.java)
    private val mutex = Mutex()
    private val _state = MutableStateFlow(DownloadManifest())
    val state: StateFlow<DownloadManifest> = _state.asStateFlow()

    /** Root directory for downloaded media files + the manifest. */
    val downloadsDir: File by lazy {
        File(context.filesDir, "downloads").also { it.mkdirs() }
    }

    private val manifestFile: File get() = File(downloadsDir, "manifest.json")

    suspend fun load() = withContext(Dispatchers.IO) {
        mutex.withLock {
            if (!manifestFile.exists()) {
                _state.value = DownloadManifest()
                return@withLock
            }
            try {
                val parsed = adapter.fromJson(manifestFile.readText())
                _state.value = parsed ?: DownloadManifest()
            } catch (_: Exception) {
                // Corrupt manifest — fall back to empty. The user can
                // re-download anything they need; better than crashing.
                _state.value = DownloadManifest()
            }
        }
    }

    suspend fun get(fileId: String): DownloadEntry? =
        _state.value.entries.firstOrNull { it.file_id == fileId }

    suspend fun upsert(entry: DownloadEntry) = withContext(Dispatchers.IO) {
        mutex.withLock {
            val current = _state.value.entries
            val replaced = current.filterNot { it.file_id == entry.file_id } +
                entry.copy(updated_at = System.currentTimeMillis())
            val next = DownloadManifest(entries = replaced)
            persist(next)
            _state.value = next
        }
    }

    /** Atomic read-modify-write of one entry under the mutex. [mutate] gets
     *  the current entry (or null if absent) and returns the entry to persist,
     *  or null to leave the manifest unchanged. Use this instead of
     *  get()+upsert() so concurrent writers (e.g. a progress tick racing a
     *  re-enqueue) can't clobber each other's field updates with a stale
     *  snapshot. */
    suspend fun update(fileId: String, mutate: (DownloadEntry?) -> DownloadEntry?) = withContext(Dispatchers.IO) {
        mutex.withLock {
            val current = _state.value.entries.firstOrNull { it.file_id == fileId }
            val updated = mutate(current) ?: return@withLock
            val replaced = _state.value.entries.filterNot { it.file_id == fileId } +
                updated.copy(updated_at = System.currentTimeMillis())
            val next = DownloadManifest(entries = replaced)
            persist(next)
            _state.value = next
        }
    }

    suspend fun remove(fileId: String) = withContext(Dispatchers.IO) {
        mutex.withLock {
            val entry = _state.value.entries.firstOrNull { it.file_id == fileId }
            val next = DownloadManifest(entries = _state.value.entries.filterNot { it.file_id == fileId })
            persist(next)
            _state.value = next
            // runCatching: fileFor now rejects a malformed server-supplied
            // id/container. A bad entry must still leave the manifest, so the
            // user can clear it from the UI rather than being stuck with a row
            // that throws on every removal attempt.
            entry?.let { e -> runCatching { fileFor(e).delete() } }
        }
    }

    /** Drop every download — files and manifest.
     *
     *  Downloads belong to the identity that fetched them, but the manifest
     *  is a process-wide singleton with no user or server field, so without
     *  this an identity change left the previous user's media on disk and
     *  listed in the Downloads screen. Offline playback deliberately falls
     *  back to the local file when the item fetch fails, which is exactly
     *  what a restricted profile hits — so the next user could play media
     *  their own account is forbidden to fetch, and a "forgotten" server's
     *  content survived on the device.
     *
     *  Called from every identity transition (sign-out, server switch).
     *
     *  DIRECTORY-driven, not manifest-driven. Iterating `_state` looked
     *  equivalent but was not: nothing loads the store at startup, and the
     *  default route (launch → Settings → Sign out) never touches it — so the
     *  wipe iterated ZERO entries, deleted no media, and then destroyed the
     *  index that named it, leaving unreclaimable orphans. Listing the
     *  directory also sweeps the other two orphan sources: the corrupt-manifest
     *  reset in load(), and a changed `container` writing `<id>.<old>` while
     *  every reader computes `<id>.<new>`. */
    suspend fun clearAll() = withContext(Dispatchers.IO) {
        mutex.withLock {
            downloadsDir.listFiles()?.forEach { runCatching { it.delete() } }
            // listFiles() already covered manifest.json; this is the
            // belt-and-braces path for a listFiles() that returned null.
            runCatching { manifestFile.delete() }
            _state.value = DownloadManifest()
        }
    }

    /** Local on-disk path for an entry — `<downloadsDir>/<file_id>.<ext>`.
     *  Container falls back to "bin" when the server didn't supply one
     *  (rare; ffprobe should always set this).
     *
     *  BOTH components are server-controlled strings off the wire, so both are
     *  validated here rather than trusted. Previously they were interpolated
     *  raw: `File(File, String)` does not resolve `..`, so a hostile or
     *  MITM'd item response could aim the download writer (which mkdirs() the
     *  parent chain and, on a 200-answers-a-Range, opens the sink TRUNCATING)
     *  at any path in the app sandbox. The reachable target that mattered was
     *  `<filesDir>/datastore/server_prefs.preferences_pb` two levels up —
     *  forgeable without the Keystore key, because TokenCrypto.decrypt passes
     *  through anything lacking the `enc:v1:` prefix as legacy plaintext. That
     *  turns a transient LAN position into a client permanently pointed at
     *  attacker infrastructure, which is NOT what the accepted cleartext
     *  trade-off signed up for.
     *
     *  Reject loudly instead of sanitising: a name that fails these rules is a
     *  server bug or an attack, and silently rewriting it would hide both. All
     *  three callers (this, remove(), clearAll()) get the containment assert. */
    fun fileFor(entry: DownloadEntry): File {
        // file_id is a server-side UUID on every real response. Parsing it is
        // both the format check and the traversal check — no separator, dot,
        // or encoded byte survives UUID.fromString.
        val id = runCatching { java.util.UUID.fromString(entry.file_id) }.getOrNull()
        require(id != null) { "download file_id is not a UUID: ${entry.file_id}" }
        val raw = (entry.container ?: "bin").lowercase()
        val ext = if (CONTAINER_RE.matches(raw)) raw else "bin"
        val out = File(downloadsDir, "$id.$ext")
        // Belt-and-braces: even if the rules above are ever loosened, nothing
        // may resolve outside downloadsDir.
        require(out.canonicalPath.startsWith(downloadsDir.canonicalPath + File.separator)) {
            "download path escapes downloadsDir: ${out.path}"
        }
        return out
    }

    private fun persist(manifest: DownloadManifest) {
        val json = adapter.toJson(manifest)
        val tmp = File(downloadsDir, "manifest.json.tmp")
        tmp.writeText(json)
        // POSIX atomic-rename — manifest.json is either fully the old
        // version or fully the new one; never half-written.
        if (!tmp.renameTo(manifestFile)) {
            manifestFile.writeText(json)
            tmp.delete()
        }
    }

    private companion object {
        /** Container extensions we will write to disk. Deliberately a short
         *  allowlist shape rather than a denylist of dangerous characters:
         *  alphanumeric only, so no separator, dot, NUL, or percent-encoded
         *  byte can reach the path. Real values are mkv/mp4/m4v/webm/flac/
         *  mp3/opus/epub and friends; anything else falls back to "bin". */
        val CONTAINER_RE = Regex("^[a-z0-9]{1,8}$")
    }
}
