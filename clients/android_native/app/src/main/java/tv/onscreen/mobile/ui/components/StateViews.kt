package tv.onscreen.mobile.ui.components

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.CloudOff
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp

/**
 * Shared loading / empty / error placeholders so every screen presents the
 * same states consistently. Previously most screens rendered loads/empties
 * ad-hoc and errors as a bare centered `Text(ui.error!!)` (raw exception
 * string, no retry, and a blank screen when the message was null).
 */

/** Centered spinner — use for an initial load before any content exists. */
@Composable
fun LoadingState(modifier: Modifier = Modifier) {
    Box(modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
        CircularProgressIndicator()
    }
}

/**
 * Friendly error state with an optional Retry action. Never shows a raw
 * network exception to the user — null/blank or network-shaped messages
 * coalesce to a generic line (log the real error at the call site); genuine
 * server messages ("Not found", "Forbidden") pass through.
 */
@Composable
fun ErrorState(
    message: String? = null,
    onRetry: (() -> Unit)? = null,
    modifier: Modifier = Modifier,
) {
    Column(
        modifier = modifier.fillMaxSize().padding(32.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.Center,
    ) {
        Icon(
            Icons.Default.CloudOff,
            contentDescription = null,
            tint = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.size(48.dp),
        )
        Text(
            text = friendlyMessage(message),
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            textAlign = TextAlign.Center,
            modifier = Modifier.padding(top = 12.dp),
        )
        if (onRetry != null) {
            Button(onClick = onRetry, modifier = Modifier.padding(top = 16.dp)) {
                Text("Try again")
            }
        }
    }
}

/** Centered empty-state message with an optional leading icon. */
@Composable
fun EmptyState(
    message: String,
    icon: ImageVector? = null,
    modifier: Modifier = Modifier,
) {
    Column(
        modifier = modifier.fillMaxSize().padding(32.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.Center,
    ) {
        if (icon != null) {
            Icon(
                icon,
                contentDescription = null,
                tint = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.size(48.dp).padding(bottom = 12.dp),
            )
        }
        Text(
            message,
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            textAlign = TextAlign.Center,
        )
    }
}

private const val GENERIC_ERROR = "Couldn't load. Check your connection and try again."

private fun friendlyMessage(message: String?): String {
    if (message.isNullOrBlank()) return GENERIC_ERROR
    val low = message.lowercase()
    val networky = listOf(
        "unable to resolve", "failed to connect", "timeout", "timed out",
        "connection", "socket", "no address", "unexpected end of stream",
        "network", "host",
    )
    return if (networky.any { low.contains(it) }) GENERIC_ERROR else message
}
