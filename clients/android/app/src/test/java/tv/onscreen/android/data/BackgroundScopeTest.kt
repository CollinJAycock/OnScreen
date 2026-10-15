package tv.onscreen.android.data

import com.google.common.truth.Truth.assertThat
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineExceptionHandler
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import org.junit.Test

class BackgroundScopeTest {

    @Test
    fun `a failing coroutine is handled, and the scope keeps running`() = runBlocking {
        val scope = backgroundScope("BackgroundScopeTest")
        assertThat(scope.coroutineContext[CoroutineExceptionHandler]).isNotNull()

        scope.launch { throw IllegalStateException("boom") }.join()

        // A sibling still runs: the failure neither escaped nor cancelled
        // the scope.
        val ran = CompletableDeferred<Unit>()
        scope.launch { ran.complete(Unit) }
        withTimeout(5_000) { ran.await() }
    }
}
