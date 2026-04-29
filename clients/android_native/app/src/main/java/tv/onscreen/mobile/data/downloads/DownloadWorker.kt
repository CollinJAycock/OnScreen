package tv.onscreen.mobile.data.downloads

import android.app.NotificationChannel
import android.app.NotificationManager
import android.content.Context
import android.os.Build
import androidx.core.app.NotificationCompat
import androidx.hilt.work.HiltWorker
import androidx.work.CoroutineWorker
import androidx.work.ForegroundInfo
import androidx.work.WorkerParameters
import androidx.work.workDataOf
import dagger.assisted.Assisted
import dagger.assisted.AssistedInject
import okhttp3.OkHttpClient
import okhttp3.Request
import tv.onscreen.mobile.R
import tv.onscreen.mobile.data.prefs.ServerPrefs
import tv.onscreen.mobile.data.repository.ItemRepository

/** Background download for a single file. WorkManager schedules it,
 *  Hilt injects the dependencies, the foreground-service notification
 *  keeps the OS from killing us mid-download. Single concurrent worker
 *  per download (the manager enqueues serially) so a user with patchy
 *  Wi-Fi doesn't fragment their bandwidth across N parallel streams.
 *
 *  Resume support: writes are append-only and we issue a Range header
 *  pointing at the current on-disk file size, so a partial download
 *  picks up where the previous attempt left off when the user retries
 *  (the manifest's status="failed" entries trigger a manual re-enqueue,
 *  which finds the partial file and resumes from there). */
@HiltWorker
class DownloadWorker @AssistedInject constructor(
    @Assisted appContext: Context,
    @Assisted params: WorkerParameters,
    private val store: DownloadStore,
    private val items: ItemRepository,
    private val prefs: ServerPrefs,
    private val httpClient: OkHttpClient,
) : CoroutineWorker(appContext, params) {

    companion object {
        const val KEY_FILE_ID = "file_id"
        const val KEY_ITEM_ID = "item_id"
        const val PROGRESS_DOWNLOADED = "downloaded_bytes"
        const val PROGRESS_TOTAL = "total_bytes"

        private const val CHANNEL_ID = "downloads"
        private const val NOTIFICATION_ID = 0xD0
    }

    override suspend fun doWork(): Result {
        store.load()
        val fileId = inputData.getString(KEY_FILE_ID) ?: return Result.failure()
        val itemId = inputData.getString(KEY_ITEM_ID) ?: return Result.failure()

        // Resolve the canonical file metadata + stream URL fresh each
        // run rather than trusting the manifest — the user could have
        // deleted then re-added a library, which renames file paths.
        val item = try { items.getItem(itemId) } catch (_: Exception) {
            markFailed(fileId, "Couldn't load item")
            return Result.failure()
        }
        val file = item.files.firstOrNull { it.id == fileId } ?: run {
            markFailed(fileId, "File no longer present")
            return Result.failure()
        }
        val server = prefs.getServerUrl()?.trimEnd('/').orEmpty()
        // Per-file 24h stream token survives the duration of any
        // realistic download; the standard 1h access token would
        // expire mid-fetch on a slow connection.
        val token = file.stream_token ?: prefs.getAccessToken().orEmpty()
        if (server.isEmpty() || token.isEmpty()) {
            markFailed(fileId, "Not signed in")
            return Result.failure()
        }
        val url = "$server${file.stream_url}?token=$token"

        val entry = store.get(fileId) ?: DownloadEntry(
            file_id = fileId,
            item_id = itemId,
            item_title = item.title,
            item_type = item.type,
            container = file.container,
            size_bytes = 0L,
            downloaded_bytes = 0L,
            status = "downloading",
            poster_path = item.poster_path,
        )
        store.upsert(entry.copy(status = "downloading"))

        val outFile = store.fileFor(entry)
        val resumeFrom = if (outFile.exists()) outFile.length() else 0L

        setForeground(makeForegroundInfo(entry.item_title, 0))

        val req = Request.Builder()
            .url(url)
            .apply { if (resumeFrom > 0) header("Range", "bytes=$resumeFrom-") }
            .build()
        return runCatching {
            httpClient.newCall(req).execute().use { resp ->
                if (!resp.isSuccessful && resp.code != 206) {
                    error("HTTP ${resp.code}")
                }
                val body = resp.body ?: error("empty body")
                val contentLength = body.contentLength()
                val totalSize = if (contentLength > 0) resumeFrom + contentLength else 0L

                outFile.parentFile?.mkdirs()
                outFile.outputStream().use { out ->
                    if (resumeFrom > 0) {
                        // Append mode — re-open for write at current EOF.
                        out.close()
                    }
                }
                val sink = if (resumeFrom > 0) {
                    java.io.FileOutputStream(outFile, /*append*/ true)
                } else {
                    java.io.FileOutputStream(outFile, false)
                }
                sink.use { fos ->
                    val buf = ByteArray(64 * 1024)
                    var written = resumeFrom
                    body.byteStream().use { input ->
                        while (true) {
                            if (isStopped) {
                                // Mid-flight cancel — leave the
                                // partial file so a retry can resume.
                                return@runCatching Result.failure()
                            }
                            val n = input.read(buf)
                            if (n <= 0) break
                            fos.write(buf, 0, n)
                            written += n
                            // Progress updates throttled by WorkManager —
                            // every Data write is broadcast to observers.
                            // 64KB chunks at 50 MB/s = ~800 updates/sec
                            // which is fine; the UI debounces.
                            setProgress(workDataOf(
                                PROGRESS_DOWNLOADED to written,
                                PROGRESS_TOTAL to (totalSize.takeIf { it > 0 } ?: written),
                            ))
                        }
                    }
                    val done = entry.copy(
                        size_bytes = if (totalSize > 0) totalSize else written,
                        downloaded_bytes = written,
                        status = "completed",
                        error = null,
                    )
                    store.upsert(done)
                }
            }
            Result.success()
        }.getOrElse { e ->
            markFailed(fileId, e.message ?: "Download failed")
            Result.retry()
        }
    }

    private suspend fun markFailed(fileId: String, message: String) {
        val current = store.get(fileId) ?: return
        store.upsert(current.copy(status = "failed", error = message))
    }

    private fun makeForegroundInfo(title: String, percent: Int): ForegroundInfo {
        val ctx = applicationContext
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            val nm = ctx.getSystemService(NotificationManager::class.java)
            val ch = NotificationChannel(
                CHANNEL_ID,
                ctx.getString(R.string.downloads_channel_name),
                NotificationManager.IMPORTANCE_LOW,
            )
            nm.createNotificationChannel(ch)
        }
        val notif = NotificationCompat.Builder(ctx, CHANNEL_ID)
            .setSmallIcon(android.R.drawable.stat_sys_download)
            .setContentTitle(ctx.getString(R.string.downloading_title, title))
            .setProgress(100, percent, percent <= 0)
            .setOngoing(true)
            .setOnlyAlertOnce(true)
            .build()
        // FOREGROUND_SERVICE_TYPE_DATA_SYNC is required on Android 14+
        // when using setForeground from a worker that does network IO.
        return if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE) {
            ForegroundInfo(NOTIFICATION_ID, notif, android.content.pm.ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC)
        } else {
            ForegroundInfo(NOTIFICATION_ID, notif)
        }
    }
}
