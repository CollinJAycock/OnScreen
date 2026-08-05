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

        /** Cap on WorkManager retry attempts before a download is abandoned,
         *  so a permanently-failing transfer can't reschedule forever. */
        private const val MAX_RETRY_ATTEMPTS = 5
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
        // realistic download; fall back to the 24h purpose=asset token
        // (NOT the access token, which the server rejects in a `?token=`
        // URL).
        val token = file.stream_token ?: prefs.getAssetToken().orEmpty()
        if (server.isEmpty() || token.isEmpty()) {
            markFailed(fileId, "Not signed in")
            return Result.failure()
        }
        // Match the player's URL builder: use `&token=` when the
        // stream path already carries a query string. The earlier
        // unconditional `?token=` produced malformed URLs on any
        // stream_url with existing params.
        val sep = if (file.stream_url.contains("?")) "&" else "?"
        val url = "$server${file.stream_url}${sep}token=$token"

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
        // Atomically (re)establish the row as downloading, refreshing the
        // metadata we just resolved — without clobbering the current row from
        // a stale snapshot.
        store.update(fileId) {
            (it ?: entry).copy(
                status = "downloading",
                item_title = item.title,
                item_type = item.type,
                container = file.container,
                poster_path = item.poster_path,
            )
        }

        val outFile = store.fileFor(entry)
        val resumeFrom = if (outFile.exists()) outFile.length() else 0L

        // setForeground throws on devices that haven't granted
        // POST_NOTIFICATIONS (Android 13+) or that reject the
        // dataSync foreground-service type. The worker can still
        // finish a small download as a regular background job, so
        // log + continue rather than aborting the whole transfer.
        try {
            setForeground(makeForegroundInfo(entry.item_title, 0))
        } catch (e: Exception) {
            android.util.Log.w("DownloadWorker", "setForeground failed", e)
        }

        val req = Request.Builder()
            .url(url)
            .apply { if (resumeFrom > 0) header("Range", "bytes=$resumeFrom-") }
            .build()
        return runCatching {
            httpClient.newCall(req).execute().use { resp ->
                // 416 on a resume means the server considers the range past
                // the end — i.e. we already have the whole file. Treating it
                // as a terminal 4xx marked a COMPLETE download "failed".
                if (resp.code == 416 && resumeFrom > 0) {
                    finalizeCompleted(fileId, outFile, resumeFrom)
                    return@use
                }
                if (!resp.isSuccessful && resp.code != 206) {
                    throw DownloadHttpException(resp.code)
                }
                // A resume request the server ignored: it answered 200 with
                // the FULL body starting at byte 0, not 206 with the tail.
                // Appending that to the partial file produced a corrupt
                // (partial + full) blob that was then marked complete — the
                // user's "downloaded" episode was unplayable. Restart clean.
                val ignoredRange = resumeFrom > 0 && resp.code == 200
                val startAt = if (ignoredRange) 0L else resumeFrom
                // Log the path WITHOUT the query string — the URL carries the
                // stream/asset token in `?token=`, which must never reach
                // logcat (any READ_LOGS process could lift a live token).
                android.util.Log.i(
                    "DownloadWorker",
                    "GET ${url.substringBefore("?")} -> ${resp.code} ${resp.header("Content-Type")} len=${resp.header("Content-Length")}",
                )
                val body = resp.body ?: error("empty body")
                val contentLength = body.contentLength()
                val totalSize = if (contentLength > 0) startAt + contentLength else 0L

                outFile.parentFile?.mkdirs()
                // Open the sink in append mode when resuming so the previously
                // downloaded 0..resumeFrom bytes survive. A previous version of
                // this block opened outFile.outputStream() (which defaults to
                // truncate=true) before the append-mode handle, so resume
                // silently wiped the partial file and the new bytes landed at
                // position 0 — the resulting file was missing its head bytes
                // and the cached size_bytes lied. Open the append/truncate
                // sink directly, exactly once.
                // Append ONLY for a genuine 206 resume; a 200 (server
                // ignored the Range) truncates and restarts — see startAt.
                val sink = java.io.FileOutputStream(outFile, /*append*/ startAt > 0)
                sink.use { fos ->
                    val buf = ByteArray(64 * 1024)
                    var written = startAt
                    var lastManifestWriteMs = 0L
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
                            setProgress(workDataOf(
                                PROGRESS_DOWNLOADED to written,
                                PROGRESS_TOTAL to (totalSize.takeIf { it > 0 } ?: written),
                            ))
                            // Periodically push live byte counts into
                            // the manifest so the UI's percentage
                            // ticks up. Without this, the manifest is
                            // only refreshed at start/end and the
                            // button sits at 0% for the entire
                            // download. 1s cadence is fast enough for
                            // a smooth UX without thrashing the
                            // manifest file or the StateFlow.
                            val now = System.currentTimeMillis()
                            if (now - lastManifestWriteMs >= 1_000L) {
                                lastManifestWriteMs = now
                                val bytes = written
                                val size = totalSize.takeIf { it > 0 } ?: written
                                // `it` is null when the user deleted this
                                // download mid-flight: returning `entry` there
                                // RESURRECTED the row (pointing at a file the
                                // delete already removed). Returning null keeps
                                // it deleted; the isStopped check ends the loop.
                                store.update(fileId) {
                                    it?.copy(
                                        size_bytes = size,
                                        downloaded_bytes = bytes,
                                        status = "downloading",
                                    )
                                }
                            }
                        }
                    }
                    val finalSize = if (totalSize > 0) totalSize else written
                    val finalWritten = written
                    // Same delete-race guard as the periodic tick above: a
                    // download removed mid-flight must not come back as
                    // "completed" with no file behind it.
                    store.update(fileId) {
                        it?.copy(
                            size_bytes = finalSize,
                            downloaded_bytes = finalWritten,
                            status = "completed",
                            error = null,
                        )
                    }
                }
            }
            Result.success()
        }.getOrElse { e ->
            markFailed(fileId, e.message ?: "Download failed")
            // Retry only transient failures (network/IO/5xx) and only within
            // the attempt cap — a permanent failure (4xx other than 408/429,
            // disk full, un-refreshable token) must NOT reschedule forever in
            // the background draining battery/data.
            val code = (e as? DownloadHttpException)?.code
            val terminal = code != null && code in 400..499 && code != 408 && code != 429
            if (terminal || runAttemptCount >= MAX_RETRY_ATTEMPTS) Result.failure() else Result.retry()
        }
    }

    private suspend fun markFailed(fileId: String, message: String) {
        store.update(fileId) { it?.copy(status = "failed", error = message) }
    }

    /** Mark an already-complete file done — the 416 path, where the server
     *  says our Range starts past the end because we have every byte. */
    private suspend fun finalizeCompleted(fileId: String, outFile: java.io.File, size: Long) {
        android.util.Log.i("DownloadWorker", "416 on resume — file already complete (${'$'}size bytes)")
        store.update(fileId) {
            it?.copy(
                size_bytes = size,
                downloaded_bytes = size,
                status = "completed",
                error = null,
            )
        }
    }

    /** Carries the HTTP status so the retry policy can tell a permanent 4xx
     *  apart from a transient 5xx/IO error. */
    private class DownloadHttpException(val code: Int) : Exception("HTTP $code")

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
