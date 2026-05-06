package tv.onscreen.mobile.ui.photo

import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.viewinterop.AndroidView
import org.osmdroid.tileprovider.tilesource.TileSourceFactory
import org.osmdroid.util.GeoPoint
import org.osmdroid.views.MapView
import org.osmdroid.views.overlay.Marker
import tv.onscreen.mobile.data.model.PhotoMapPoint

/**
 * OpenStreetMap-backed map view, wrapped for Compose.
 *
 * Used by the Geotagged tab in PhotoExtras. Shows one marker per
 * geotagged photo; tapping a marker invokes [onMarkerTap] with the
 * photo's item id so the parent can deep-link into the viewer.
 *
 * OSMDroid's MapView is a classic Android `View`, not a Composable —
 * we host it via [AndroidView]. Lifecycle (onResume / onPause /
 * onDetach) is owned by [DisposableEffect] so the map releases its
 * tile-fetcher threads when the user leaves the screen, and so a
 * config change doesn't leak two MapView instances onto the same
 * tile cache.
 *
 * Tile policy note: we use Mapnik (the standard OSM raster style).
 * That's the public OSM tile server; per their usage policy heavy
 * use should self-host or use a paid provider. For the OnScreen
 * phone client a single user browsing their own geotagged photos
 * is well under the threshold; if traffic ever justifies it we can
 * swap [TileSourceFactory.MAPNIK] for a self-hosted tile-server URL.
 */
@Composable
fun OsmMap(
    points: List<PhotoMapPoint>,
    onMarkerTap: (PhotoMapPoint) -> Unit,
    modifier: Modifier = Modifier,
) {
    val context = LocalContext.current
    // remember holds one MapView for the composition lifetime; the
    // factory recompose path below would otherwise create a new
    // MapView on every emission and leak tile-fetcher threads.
    val mapView = remember { MapView(context) }

    DisposableEffect(Unit) {
        mapView.onResume()
        onDispose {
            mapView.onPause()
            mapView.onDetach()
        }
    }

    AndroidView(
        modifier = modifier,
        factory = { ctx ->
            mapView.apply {
                setTileSource(TileSourceFactory.MAPNIK)
                setMultiTouchControls(true)
                setZoomLevel(2.0)
                setBuiltInZoomControls(false)
            }
        },
        update = { view ->
            // Recompute markers when the points list changes. We
            // clear-and-rebuild rather than diffing because the marker
            // count is bounded (server caps the /photos/map response)
            // and the diff cost would dwarf the rebuild on a phone.
            view.overlays.clear()
            points.forEach { p ->
                val marker = Marker(view).apply {
                    position = GeoPoint(p.lat, p.lon)
                    setAnchor(Marker.ANCHOR_CENTER, Marker.ANCHOR_BOTTOM)
                    title = p.taken_at ?: ""
                    setOnMarkerClickListener { _, _ ->
                        onMarkerTap(p)
                        true
                    }
                }
                view.overlays.add(marker)
            }
            // Auto-zoom to the bounding box of the markers so the
            // user lands on something visible. Single point → city-
            // level zoom; many points → zoomToBoundingBox sizes the
            // viewport to fit them all.
            if (points.isNotEmpty()) {
                val controller = view.controller
                if (points.size == 1) {
                    controller.setZoom(13.0)
                    controller.setCenter(GeoPoint(points[0].lat, points[0].lon))
                } else {
                    val lats = points.map { it.lat }
                    val lons = points.map { it.lon }
                    val box = org.osmdroid.util.BoundingBox(
                        lats.max(), lons.max(),
                        lats.min(), lons.min(),
                    )
                    // Defer until layout settles so the BoundingBox
                    // calculation has real dimensions; otherwise
                    // zoomToBoundingBox is a no-op on the first paint.
                    view.post {
                        view.zoomToBoundingBox(box, false, 64)
                    }
                }
            }
            view.invalidate()
        },
    )
}

