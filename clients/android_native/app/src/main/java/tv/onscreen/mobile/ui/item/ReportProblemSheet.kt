package tv.onscreen.mobile.ui.item

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.selection.selectableGroup
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Flag
import androidx.compose.material3.Button
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.RadioButton
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle

/**
 * Top-app-bar action that opens the "Report a problem" sheet for an item.
 * Renders nothing for item types the report doesn't apply to (the web item
 * page offers it on movies, episodes, shows and seasons only). Self-contained
 * so a screen mounts it with one line in its `actions`.
 */
@Composable
fun ReportProblemAction(
    itemId: String,
    itemType: String?,
    fileId: String?,
    itemLabel: String,
) {
    if (itemType !in REPORTABLE_TYPES) return
    var open by rememberSaveable(itemId) { mutableStateOf(false) }
    IconButton(onClick = { open = true }) {
        Icon(Icons.Outlined.Flag, contentDescription = "Report a problem")
    }
    if (open) {
        ReportProblemSheet(
            itemId = itemId,
            fileId = fileId,
            itemLabel = itemLabel,
            onDismiss = { open = false },
        )
    }
}

/**
 * "Report a problem" bottom sheet: pick what's wrong (five kinds, worded as
 * on the web), add an optional note, send. Shows the caller's earlier
 * reports for the item so a second visit reads "You reported this 2 days
 * ago" instead of inviting a duplicate; kinds with an open report are
 * disabled. Refusals (already reported, too many open reports, rate limit)
 * surface as a snackbar inside the sheet — above its scrim, where the host
 * screen's snackbar would be hidden.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ReportProblemSheet(
    itemId: String,
    fileId: String?,
    itemLabel: String,
    onDismiss: () -> Unit,
    vm: ReportProblemViewModel = hiltViewModel(key = "report-problem-$itemId"),
) {
    LaunchedEffect(itemId, fileId) { vm.open(itemId, fileId) }
    val ui by vm.state.collectAsStateWithLifecycle()
    val snackbar = remember { SnackbarHostState() }
    LaunchedEffect(vm) {
        vm.messages.collect { snackbar.showSnackbar(it) }
    }
    val sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)

    ModalBottomSheet(onDismissRequest = onDismiss, sheetState = sheetState) {
        Box {
            Column(
                modifier = Modifier
                    .fillMaxWidth()
                    .verticalScroll(rememberScrollState())
                    .padding(horizontal = 24.dp)
                    .padding(bottom = 16.dp)
                    .imePadding()
                    .navigationBarsPadding(),
            ) {
                Text("Report a problem", style = MaterialTheme.typography.titleLarge)
                if (itemLabel.isNotBlank()) {
                    Text(
                        itemLabel,
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                Spacer(Modifier.height(16.dp))

                if (ui.sent) {
                    Text(
                        "Thanks — an admin has been notified and will take a look.",
                        style = MaterialTheme.typography.bodyLarge,
                        modifier = Modifier.semantics { liveRegion = LiveRegionMode.Polite },
                    )
                    Spacer(Modifier.height(16.dp))
                    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End) {
                        Button(onClick = onDismiss) { Text("Close") }
                    }
                } else {
                    ReportForm(
                        ui = ui,
                        onSelectKind = vm::selectKind,
                        onNoteChange = vm::setNote,
                        onCancel = onDismiss,
                        onSubmit = vm::submit,
                    )
                }
            }
            SnackbarHost(
                hostState = snackbar,
                modifier = Modifier
                    .align(Alignment.BottomCenter)
                    .navigationBarsPadding(),
            )
        }
    }
}

@Composable
private fun ReportForm(
    ui: ReportProblemUi,
    onSelectKind: (IssueKind) -> Unit,
    onNoteChange: (String) -> Unit,
    onCancel: () -> Unit,
    onSubmit: () -> Unit,
) {
    val muted = MaterialTheme.colorScheme.onSurfaceVariant
    when {
        ui.loadingHistory -> Text(
            "Checking your earlier reports…",
            style = MaterialTheme.typography.bodySmall,
            color = muted,
        )
        ui.shownHistory.isNotEmpty() -> Column(
            modifier = Modifier.semantics { contentDescription = "Your earlier reports" },
        ) {
            ui.shownHistory.forEach { issue ->
                Text(
                    describeIssue(issue, ui.nowMs),
                    style = MaterialTheme.typography.bodySmall,
                    color = if (issue.status == "open") MaterialTheme.colorScheme.onSurface else muted,
                    modifier = Modifier.padding(vertical = 2.dp),
                )
            }
        }
    }
    Spacer(Modifier.height(12.dp))

    Text("What's wrong?", style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.Medium)
    Column(Modifier.selectableGroup()) {
        IssueKind.entries.forEach { kind ->
            val taken = kind.wire in ui.takenKinds
            Row(
                verticalAlignment = Alignment.CenterVertically,
                modifier = Modifier
                    .fillMaxWidth()
                    .heightIn(min = 48.dp)
                    .selectable(
                        selected = ui.kind == kind,
                        enabled = !taken && !ui.submitting,
                        role = Role.RadioButton,
                        onClick = { onSelectKind(kind) },
                    ),
            ) {
                RadioButton(
                    selected = ui.kind == kind,
                    onClick = null,
                    enabled = !taken && !ui.submitting,
                )
                Spacer(Modifier.width(12.dp))
                Column {
                    Text(
                        kind.label,
                        style = MaterialTheme.typography.bodyLarge,
                        color = if (taken) muted else MaterialTheme.colorScheme.onSurface,
                    )
                    if (taken) {
                        Text("Already reported", style = MaterialTheme.typography.labelSmall, color = muted)
                    }
                }
            }
        }
    }
    Spacer(Modifier.height(8.dp))

    val over = ui.remaining < 0
    OutlinedTextField(
        value = ui.note,
        onValueChange = onNoteChange,
        label = { Text("Details (optional)") },
        placeholder = { Text("What happened, and roughly when in the video?") },
        supportingText = { Text("${ui.remaining} characters left") },
        isError = over,
        enabled = !ui.submitting,
        minLines = 3,
        maxLines = 6,
        modifier = Modifier.fillMaxWidth(),
    )
    Spacer(Modifier.height(12.dp))

    Row(
        modifier = Modifier.fillMaxWidth(),
        horizontalArrangement = Arrangement.End,
        verticalAlignment = Alignment.CenterVertically,
    ) {
        TextButton(onClick = onCancel) { Text("Cancel") }
        Spacer(Modifier.width(8.dp))
        Button(onClick = onSubmit, enabled = ui.canSubmit) {
            Text(if (ui.submitting) "Sending…" else "Send report")
        }
    }
    // Room for a snackbar over the buttons without hiding them.
    Spacer(Modifier.height(8.dp))
}
