package tv.onscreen.mobile.data.network

import android.content.Context
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import android.net.NetworkRequest
import dagger.hilt.android.qualifiers.ApplicationContext
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Singleton wrapper around ConnectivityManager that publishes a
 * StateFlow of "is the device usable for OnScreen API calls right
 * now?" — i.e. has at least one validated network capable of internet
 * access.
 *
 * Drives offline-mode routing: AppNav reads the cold-start value to
 * decide between Hub (online) and Downloads (offline), and the
 * DownloadsScreen subscribes for the live state so its banner
 * updates as the user toggles airplane mode.
 *
 * The NetworkCallback registers once at construction (Singleton
 * scope) and stays for the lifetime of the process.
 */
@Singleton
class ConnectivityObserver @Inject constructor(
    @ApplicationContext private val context: Context,
) {
    private val cm: ConnectivityManager =
        context.getSystemService(Context.CONNECTIVITY_SERVICE) as ConnectivityManager

    private val _isOnline = MutableStateFlow(currentOnlineState())
    val isOnline: StateFlow<Boolean> = _isOnline.asStateFlow()

    init {
        val request = NetworkRequest.Builder()
            .addCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
            // VALIDATED keeps captive-portal Wi-Fi from registering as
            // online — same gate the OS uses for the system Wi-Fi
            // status icon. Without it the user might see "online" but
            // every API call hits the portal page.
            .addCapability(NetworkCapabilities.NET_CAPABILITY_VALIDATED)
            .build()
        cm.registerNetworkCallback(request, object : ConnectivityManager.NetworkCallback() {
            override fun onAvailable(network: Network) { _isOnline.value = true }
            override fun onLost(network: Network) { _isOnline.value = currentOnlineState() }
            override fun onCapabilitiesChanged(network: Network, caps: NetworkCapabilities) {
                _isOnline.value = caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_VALIDATED)
            }
        })
    }

    private fun currentOnlineState(): Boolean {
        val active = cm.activeNetwork ?: return false
        val caps = cm.getNetworkCapabilities(active) ?: return false
        return caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET) &&
            caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_VALIDATED)
    }
}
