package tv.onscreen.mobile.data.downloads

import android.content.Context
import androidx.work.Constraints
import androidx.work.ExistingWorkPolicy
import androidx.work.NetworkType
import androidx.work.OneTimeWorkRequestBuilder
import androidx.work.WorkInfo
import androidx.work.WorkManager
import androidx.work.workDataOf
import dagger.hilt.android.qualifiers.ApplicationContext
import kotlinx.coroutines.flow.Flow
import tv.onscreen.mobile.data.api.OnScreenApi
import tv.onscreen.mobile.data.prefs.PlaybackPrefs
import javax.inject.Inject
import javax.inject.Singleton

/** Facade over WorkManager + DownloadStore. UI talks to this; worker
 *  details (constraints, foreground notification, etc.) stay private. */
@Singleton
class OnScreenDownloadManager @Inject constructor(
    @ApplicationContext private val context: Context,
    val store: DownloadStore,
    private val prefs: PlaybackPrefs,
    // Lazy: only a cancelled conversion calls the server, and the API's
    // OkHttp client already reaches back to the download store.
    private val api: dagger.Lazy<OnScreenApi>,
) {
    private val wm: WorkManager get() = WorkManager.getInstance(context)

    /** Enqueue a download for one item file. Existing in-flight or
     *  queued work for this file_id is replaced — re-enqueueing acts
     *  as a "retry" when the previous attempt failed. Worker tag =
     *  file_id so callers can observe progress without juggling
     *  WorkRequest UUIDs.
     *
     *  When the user has download_on_wifi_only enabled (the default),
     *  the constraint is NetworkType.UNMETERED so WorkManager defers
     *  the download until the device leaves cellular. Otherwise any
     *  connected network qualifies.
     *
     *  Pre-upserts a `queued` manifest entry before scheduling so the
     *  UI shows immediate feedback and the user can tell whether the
     *  worker is queued-but-blocked (constraint not met) vs. actually
     *  running. The worker overwrites this with `downloading` when
     *  it starts. */
    suspend fun enqueue(
        fileId: String,
        itemId: String,
        itemTitle: String,
        itemType: String,
        container: String?,
        posterPath: String? = null,
        /** A converted copy's quality id (v2.6), or null for the original. */
        quality: String? = null,
        qualityLabel: String? = null,
    ) {
        // A failed attempt at another quality left a partial file under its
        // own container's name; this attempt writes a different file, so the
        // stale one goes (a retry of the SAME quality keeps it, to resume).
        store.get(fileId)?.let { prior ->
            if (prior.status == DownloadStatus.FAILED && prior.quality != quality) store.remove(fileId)
        }
        // Atomic check-then-set under the store mutex: only (re)queue when
        // there's no entry or the prior attempt failed. Doing get()+upsert()
        // separately let two concurrent enqueues (or an enqueue racing the
        // worker's first write) both see "absent" and double-write / stomp an
        // in-flight row.
        store.update(fileId) { existing ->
            if (existing != null && existing.status != DownloadStatus.FAILED) {
                null // already present (queued/converting/downloading/completed) — leave it
            } else {
                DownloadEntry(
                    file_id = fileId,
                    item_id = itemId,
                    item_title = itemTitle,
                    item_type = itemType,
                    // A converted copy is always an MP4.
                    container = if (quality != null) CONVERTED_CONTAINER else container,
                    size_bytes = 0L,
                    downloaded_bytes = 0L,
                    status = DownloadStatus.QUEUED,
                    poster_path = posterPath,
                    quality = quality,
                    quality_label = qualityLabel,
                )
            }
        }
        val networkType = if (prefs.getDownloadOnWifiOnly()) {
            NetworkType.UNMETERED
        } else {
            NetworkType.CONNECTED
        }
        val req = OneTimeWorkRequestBuilder<DownloadWorker>()
            .setInputData(workDataOf(
                DownloadWorker.KEY_FILE_ID to fileId,
                DownloadWorker.KEY_ITEM_ID to itemId,
                DownloadWorker.KEY_QUALITY to quality,
            ))
            .setConstraints(
                Constraints.Builder()
                    .setRequiredNetworkType(networkType)
                    .build(),
            )
            .addTag(workTag(fileId))
            .build()
        wm.enqueueUniqueWork(workTag(fileId), ExistingWorkPolicy.REPLACE, req)
    }

    fun cancel(fileId: String) {
        wm.cancelUniqueWork(workTag(fileId))
    }

    suspend fun delete(fileId: String) {
        cancel(fileId)
        val entry = store.get(fileId)
        store.remove(fileId)
        // A copy the server is still encoding for us: drop the job too, which
        // cancels the encode when nobody else is waiting for it. Best-effort —
        // the server expires an abandoned job on its own.
        val job = entry?.job_id?.takeIf { entry.status == DownloadStatus.CONVERTING }
        if (job != null) runCatching { api.get().deleteDownloadJob(job) }
    }

    /** Cancel every in-flight download and erase all downloaded media.
     *
     *  Called on identity transitions (sign-out, server switch). Cancelling
     *  matters as much as deleting: a queued worker holds a baked ?token=
     *  URL that stays valid for hours, so without this an in-flight download
     *  keeps transferring the previous identity's media after sign-out — and
     *  would re-create manifest entries just after they were wiped. */
    suspend fun cancelAllAndClear() {
        // load() first: nothing populates the store at app start, and the
        // common sign-out route (launch → Settings → Sign out) never touches
        // it, so `state` was empty here and every in-flight worker survived
        // the "cancel everything" call. clearAll() is directory-driven and so
        // erases the media regardless, but the workers have to be cancelled by
        // id or they keep transferring on the previous identity's baked token.
        runCatching { store.load() }
        store.state.value.entries.forEach { cancel(it.file_id) }
        store.clearAll()
    }

    /** Live work info for one file's download. Emits as the worker
     *  reports progress, succeeds, or fails. Uses WorkManager's native Flow
     *  API (2.9+) rather than a LiveData→Flow bridge — the old
     *  `observeForever` bridge had to run on the main thread and would throw
     *  if a collector ever ran on a background dispatcher. */
    fun observe(fileId: String): Flow<List<WorkInfo>> =
        wm.getWorkInfosForUniqueWorkFlow(workTag(fileId))

    private fun workTag(fileId: String) = "download_$fileId"

    companion object {
        /** Every converted download is an MP4. */
        const val CONVERTED_CONTAINER = "mp4"
    }
}
