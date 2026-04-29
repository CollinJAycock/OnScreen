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
import kotlinx.coroutines.channels.awaitClose
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.callbackFlow
import javax.inject.Inject
import javax.inject.Singleton

/** Facade over WorkManager + DownloadStore. UI talks to this; worker
 *  details (constraints, foreground notification, etc.) stay private. */
@Singleton
class OnScreenDownloadManager @Inject constructor(
    @ApplicationContext private val context: Context,
    val store: DownloadStore,
) {
    private val wm: WorkManager get() = WorkManager.getInstance(context)

    /** Enqueue a download for one item file. Existing in-flight or
     *  queued work for this file_id is replaced — re-enqueueing acts
     *  as a "retry" when the previous attempt failed. Worker tag =
     *  file_id so callers can observe progress without juggling
     *  WorkRequest UUIDs. */
    fun enqueue(fileId: String, itemId: String) {
        val req = OneTimeWorkRequestBuilder<DownloadWorker>()
            .setInputData(workDataOf(
                DownloadWorker.KEY_FILE_ID to fileId,
                DownloadWorker.KEY_ITEM_ID to itemId,
            ))
            .setConstraints(
                Constraints.Builder()
                    .setRequiredNetworkType(NetworkType.CONNECTED)
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
        store.remove(fileId)
    }

    /** Live work info for one file's download. Emits as the worker
     *  reports progress, succeeds, or fails. */
    fun observe(fileId: String): Flow<List<WorkInfo>> =
        wm.getWorkInfosForUniqueWorkLiveData(workTag(fileId)).asFlow()

    private fun workTag(fileId: String) = "download_$fileId"
}

private fun <T> androidx.lifecycle.LiveData<T>.asFlow(): Flow<T> = callbackFlow {
    val observer = androidx.lifecycle.Observer<T> { trySend(it) }
    observeForever(observer)
    awaitClose { removeObserver(observer) }
}
