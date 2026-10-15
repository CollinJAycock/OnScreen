package tv.onscreen.android.data

import android.util.Log
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.CoroutineExceptionHandler
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob

/**
 * A scope for app-lifetime work that outlives any screen (fire-and-forget
 * stops and progress reports, the shared notifications stream, sign-out
 * follow-ups). A failure that escapes one of its coroutines is logged under
 * [tag] instead of reaching the thread's uncaught-exception handler, which
 * crashes the app — and, in unit tests, fails whichever test runs next.
 */
fun backgroundScope(tag: String, dispatcher: CoroutineDispatcher = Dispatchers.IO): CoroutineScope =
    CoroutineScope(
        SupervisorJob() + dispatcher +
            CoroutineExceptionHandler { _, e -> Log.w(tag, "background work failed", e) },
    )
