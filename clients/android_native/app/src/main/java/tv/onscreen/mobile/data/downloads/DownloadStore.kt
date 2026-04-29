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

    suspend fun remove(fileId: String) = withContext(Dispatchers.IO) {
        mutex.withLock {
            val entry = _state.value.entries.firstOrNull { it.file_id == fileId }
            val next = DownloadManifest(entries = _state.value.entries.filterNot { it.file_id == fileId })
            persist(next)
            _state.value = next
            entry?.let { fileFor(it).delete() }
        }
    }

    /** Local on-disk path for an entry — `<downloadsDir>/<file_id>.<ext>`.
     *  Container falls back to "bin" when the server didn't supply one
     *  (rare; ffprobe should always set this). */
    fun fileFor(entry: DownloadEntry): File {
        val ext = (entry.container ?: "bin").lowercase()
        return File(downloadsDir, "${entry.file_id}.$ext")
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
}
