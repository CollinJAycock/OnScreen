package tv.onscreen.mobile.ui.library

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.foundation.background
import androidx.compose.foundation.gestures.awaitEachGesture
import androidx.compose.foundation.gestures.awaitFirstDown
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.hapticfeedback.HapticFeedbackType
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.Layout
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalHapticFeedback
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.onClick
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.dp
import tv.onscreen.mobile.BuildConfig
import tv.onscreen.mobile.R
import tv.onscreen.mobile.data.model.LibraryLetter
import kotlin.math.roundToInt

/** Width the grid leaves free at its end for the rail. A Quest is pointed
 *  at with a controller ray, so its rail is a little wider to aim at. */
internal val LetterRailWidth = if (BuildConfig.IS_QUEST) 40.dp else 32.dp

/** Below this much height per letter the labels would crowd, so only every
 *  other (third, …) letter is drawn — drag still reaches every letter. */
private val MinLetterSlot = 16.dp

private val BubbleSize = 64.dp

/** The letter row [y] (in rail pixels) lands on, for a rail [height] px tall
 *  holding [count] letters spread evenly over it. */
internal fun letterIndexAt(y: Float, height: Int, count: Int): Int {
    if (count <= 0 || height <= 0) return -1
    return (y / height * count).toInt().coerceIn(0, count - 1)
}

/** The letters to draw when [count] of them share [slots] spaces: every
 *  letter when they fit, else every n-th one (always the first). */
internal fun visibleLetterIndices(count: Int, slots: Int): List<Int> {
    if (count <= 0) return emptyList()
    if (slots <= 0) return listOf(0)
    if (count <= slots) return (0 until count).toList()
    val step = (count + slots - 1) / slots
    return (0 until count step step).toList()
}

/**
 * A-Z fast-scroll rail at the end edge of the library grid: [letters] spread
 * over the full height, each at its share of it. Touch down anywhere on it
 * jumps to the letter under the finger, and dragging moves through the
 * letters, with a bubble showing the current one beside the rail. Down alone
 * is a jump, so a tap — and a Quest controller-ray click, which arrives as
 * a press and a release in the same place — works as well as a drag.
 * [current] is the letter the grid is at, drawn highlighted.
 *
 * Fills the space it's given (the bubble needs room beside the rail) but
 * only the rail strip takes input; the rest lets touches through to the
 * grid underneath.
 */
@Composable
internal fun LetterRail(
    letters: List<LibraryLetter>,
    current: String?,
    onPick: (LibraryLetter) -> Unit,
    modifier: Modifier = Modifier,
) {
    val latestLetters by rememberUpdatedState(letters)
    val latestOnPick by rememberUpdatedState(onPick)
    val haptics = LocalHapticFeedback.current
    var pressed by remember { mutableStateOf(false) }
    // Finger height on the rail (px) and the letter under it; both outlive
    // the press so the bubble fades out where it was.
    var bubbleY by remember { mutableFloatStateOf(0f) }
    var bubbleLetter by remember { mutableStateOf("") }
    val railLabel = stringResource(R.string.library_az_index)

    BoxWithConstraints(modifier = modifier.fillMaxSize()) {
        val density = LocalDensity.current
        val slots = with(density) { (maxHeight / MinLetterSlot).toInt() }
        val shown = visibleLetterIndices(letters.size, slots)

        // The strip: full height, the whole width takes input; the pill is
        // drawn inset inside it.
        Box(
            modifier = Modifier
                .align(Alignment.CenterEnd)
                .width(LetterRailWidth)
                .fillMaxHeight()
                .semantics { contentDescription = railLabel }
                .pointerInput(Unit) {
                    awaitEachGesture {
                        val down = awaitFirstDown(requireUnconsumed = false)
                        down.consume()
                        pressed = true
                        var last = -1
                        fun pick(y: Float) {
                            val all = latestLetters
                            val i = letterIndexAt(y, size.height, all.size)
                            bubbleY = y.coerceIn(0f, size.height.toFloat())
                            if (i < 0 || i == last) return
                            if (last >= 0) haptics.performHapticFeedback(HapticFeedbackType.TextHandleMove)
                            last = i
                            bubbleLetter = all[i].letter
                            latestOnPick(all[i])
                        }
                        try {
                            pick(down.position.y)
                            while (true) {
                                val event = awaitPointerEvent()
                                val change = event.changes.firstOrNull { it.id == down.id } ?: break
                                change.consume()
                                if (!change.pressed) break
                                pick(change.position.y)
                            }
                        } finally {
                            pressed = false
                        }
                    }
                },
        ) {
            Box(
                modifier = Modifier
                    .fillMaxSize()
                    .padding(horizontal = 4.dp, vertical = 2.dp)
                    .background(
                        MaterialTheme.colorScheme.surfaceVariant.copy(alpha = if (pressed) 0.9f else 0.45f),
                        RoundedCornerShape(percent = 50),
                    ),
            )
            // Each drawn letter centred on its share of the strip, so what's
            // drawn lines up with what a touch there picks.
            Layout(
                modifier = Modifier.fillMaxSize(),
                content = {
                    shown.forEach { i ->
                        val entry = letters[i]
                        val isCurrent = entry.letter == current
                        val jumpLabel = stringResource(R.string.library_jump_to, entry.letter)
                        Text(
                            entry.letter,
                            style = MaterialTheme.typography.labelSmall,
                            fontWeight = if (isCurrent) FontWeight.Bold else FontWeight.Normal,
                            color = if (isCurrent) MaterialTheme.colorScheme.primary
                                else MaterialTheme.colorScheme.onSurfaceVariant,
                            modifier = Modifier.semantics {
                                contentDescription = jumpLabel
                                onClick { latestOnPick(entry); true }
                            },
                        )
                    }
                },
            ) { measurables, constraints ->
                val loose = constraints.copy(minWidth = 0, minHeight = 0)
                val placeables = measurables.map { it.measure(loose) }
                layout(constraints.maxWidth, constraints.maxHeight) {
                    val n = letters.size.coerceAtLeast(1)
                    placeables.forEachIndexed { k, p ->
                        val centreY = (shown[k] + 0.5f) / n * constraints.maxHeight
                        p.place(
                            x = (constraints.maxWidth - p.width) / 2,
                            y = (centreY - p.height / 2f).roundToInt(),
                        )
                    }
                }
            }
        }

        // Bubble beside the rail at the finger's height while pressed. Over
        // the grid's end column, only while a finger is on the rail.
        val heightPx = with(density) { maxHeight.roundToPx() }
        val widthPx = with(density) { maxWidth.roundToPx() }
        val bubblePx = with(density) { BubbleSize.roundToPx() }
        val besidePx = with(density) { (LetterRailWidth + 8.dp).roundToPx() }
        AnimatedVisibility(
            visible = pressed && bubbleLetter.isNotEmpty(),
            enter = fadeIn(),
            exit = fadeOut(),
            modifier = Modifier.offset {
                IntOffset(
                    x = widthPx - besidePx - bubblePx,
                    y = (bubbleY.roundToInt() - bubblePx / 2)
                        .coerceIn(0, (heightPx - bubblePx).coerceAtLeast(0)),
                )
            },
        ) {
            Surface(
                shape = CircleShape,
                color = MaterialTheme.colorScheme.primary,
                contentColor = MaterialTheme.colorScheme.onPrimary,
                shadowElevation = 4.dp,
                modifier = Modifier.size(BubbleSize),
            ) {
                Box(contentAlignment = Alignment.Center) {
                    Text(bubbleLetter, style = MaterialTheme.typography.headlineMedium)
                }
            }
        }
    }
}

/** The letter whose section listing row [row] falls in: the last letter
 *  starting at or before it. */
internal fun letterAtRow(letters: List<LibraryLetter>, row: Int): String? {
    var hit: String? = null
    for (l in letters) {
        if (l.offset <= row) hit = l.letter else break
    }
    return hit ?: letters.firstOrNull()?.letter
}
