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

    /** The whole manifest as on disk, locked or not. Written under [mutex]. */
    @Volatile private var full = DownloadManifest()

    private val _state = MutableStateFlow(DownloadManifest())

    /** What the app may show and play. While the manifest is [locked]
     *  (DownloadManifest.locked) this has no entries: the files belong to a
     *  signed-out identity. */
    val state: StateFlow<DownloadManifest> = _state.asStateFlow()

    /** True while an involuntary sign-out holds the downloads for their owner. */
    val isLocked: Boolean get() = full.locked

    /** Root directory for downloaded media files + the manifest. */
    val downloadsDir: File by lazy {
        File(context.filesDir, "downloads").also { it.mkdirs() }
    }

    private val manifestFile: File get() = File(downloadsDir, "manifest.json")

    private fun publish(next: DownloadManifest) {
        full = next
        _state.value = if (next.locked) DownloadManifest(owner = next.owner, locked = true) else next
    }

    private fun readFromDisk(): DownloadManifest {
        if (!manifestFile.exists()) return DownloadManifest()
        return try {
            adapter.fromJson(manifestFile.readText()) ?: DownloadManifest()
        } catch (_: Exception) {
            // Corrupt manifest — fall back to empty. The user can
            // re-download anything they need; better than crashing.
            DownloadManifest()
        }
    }

    suspend fun load() = withContext(Dispatchers.IO) {
        mutex.withLock { publish(readFromDisk()) }
    }

    suspend fun get(fileId: String): DownloadEntry? =
        _state.value.entries.firstOrNull { it.file_id == fileId }

    /** Writes are refused while [isLocked]: nothing may add to or change a
     *  signed-out identity's downloads (a worker that outlived its cancel,
     *  a late progress tick). */
    suspend fun upsert(entry: DownloadEntry) = withContext(Dispatchers.IO) {
        mutex.withLock {
            if (full.locked) return@withLock
            val current = full.entries
            val replaced = current.filterNot { it.file_id == entry.file_id } +
                entry.copy(updated_at = System.currentTimeMillis())
            val next = full.copy(entries = replaced)
            persist(next)
            publish(next)
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
            if (full.locked) return@withLock
            val current = full.entries.firstOrNull { it.file_id == fileId }
            val updated = mutate(current) ?: return@withLock
            val replaced = full.entries.filterNot { it.file_id == fileId } +
                updated.copy(updated_at = System.currentTimeMillis())
            val next = full.copy(entries = replaced)
            persist(next)
            publish(next)
        }
    }

    suspend fun remove(fileId: String) = withContext(Dispatchers.IO) {
        mutex.withLock {
            if (full.locked) return@withLock
            val entry = full.entries.firstOrNull { it.file_id == fileId }
            val next = full.copy(entries = full.entries.filterNot { it.file_id == fileId })
            persist(next)
            publish(next)
            // runCatching: fileFor now rejects a malformed server-supplied
            // id/container. A bad entry must still leave the manifest, so the
            // user can clear it from the UI rather than being stuck with a row
            // that throws on every removal attempt.
            entry?.let { e -> runCatching { fileFor(e).delete() } }
        }
    }

    /** Drop every download — files and manifest, owner and lock with it.
     *
     *  Downloads belong to the identity that fetched them. Offline playback
     *  deliberately falls back to the local file when the item fetch fails,
     *  which is exactly what a restricted profile hits — so media left for
     *  the next user could be played by an account forbidden to fetch it,
     *  and a "forgotten" server's content would survive on the device.
     *
     *  Called on an explicit sign-out, a forgotten server, and a sign-in by
     *  a different identity than the downloads' [DownloadManifest.owner]. An
     *  involuntary sign-out [lock]s instead.
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
            publish(DownloadManifest())
        }
    }

    /**
     * Hold the downloads for their owner after an INVOLUNTARY sign-out (the
     * refresh was rejected: a remote revoke, an expiry, a refresh answer lost
     * in a network handoff). Wiping them there threw away a user's offline
     * library over a lost response; keeping them visible would let the next
     * person on the phone play them. So the completed files stay on disk,
     * unlisted and unplayable ([state] is empty) until [claim] by the same
     * server + user. Everything unfinished goes now, files and rows, and
     * every other file in the directory (partials, orphans) with it.
     *
     * [owner] is who was signed out, read before the tokens were cleared; it
     * stamps a manifest that has no owner yet.
     */
    suspend fun lock(owner: DownloadOwner?) = withContext(Dispatchers.IO) {
        mutex.withLock {
            val current = readFromDisk()
            val kept = current.entries.filter { it.status == DownloadStatus.COMPLETED }
            val keptFiles = kept.mapNotNull { runCatching { fileFor(it).canonicalPath }.getOrNull() }.toSet()
            downloadsDir.listFiles()?.forEach { f ->
                if (f.name != manifestFile.name && f.canonicalPath !in keptFiles) runCatching { f.delete() }
            }
            val next = DownloadManifest(entries = kept, owner = current.owner ?: owner, locked = true)
            persist(next)
            publish(next)
        }
    }

    /**
     * A sign-in as [owner]: take the downloads when they are this identity's
     * (unlocking any [lock]) or there are none, and stamp the owner. Returns
     * false, changing nothing, when they belong to someone else or to nobody
     * known — the caller then wipes them ([clearAll]) and claims again.
     */
    suspend fun claim(owner: DownloadOwner): Boolean = withContext(Dispatchers.IO) {
        mutex.withLock {
            val current = readFromDisk()
            if (current.entries.isNotEmpty() && current.owner?.matches(owner) != true) return@withLock false
            val next = current.copy(owner = owner, locked = false)
            if (next != current) persist(next)
            publish(next)
            true
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
