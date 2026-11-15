package tv.onscreen.mobile.playback

import android.content.Context
import android.content.pm.ApplicationInfo
import android.content.pm.PackageManager
import android.content.pm.Signature
import android.os.Build
import java.security.MessageDigest

/**
 * Which controllers are the car hosts [CarLibraryCallback] lets browse the
 * library. A package name alone was the whole check, and two of the three
 * names don't exist on a phone, so any sideloaded app calling itself
 * `com.android.car.media` got the library, search and the artwork grant.
 * Now, as UAMP's PackageValidator does, the name must belong to the
 * controller's uid and the package must be signed by a key known for it:
 *
 * 1. a signing certificate in [KNOWN_CERT_SHA256] for that package;
 * 2. else the platform key (the certificate of the `android` package) —
 *    Android Automotive's media centre is a platform app;
 * 3. else a preinstalled system app: an update to one must carry the
 *    certificate the device image shipped it with.
 */
object CarHosts {

    /** Android Auto (the phone app, which the Desktop Head Unit also
     *  connects through), Android Automotive's media centre, and the in-car
     *  Assistant. */
    val PACKAGES = setOf(
        "com.google.android.projection.gearhead",
        "com.android.car.media",
        "com.google.android.carassistant",
    )

    /**
     * SHA-256 of the DER signing certificate, lower-case hex, by package.
     * Android Auto's three are Google's own list of its keys (release and
     * two debug), from the Car App Library's `hosts_allowlist_sample`
     * (androidx.car.app:app 1.7.0, res/values). No published digest exists
     * for the other two hosts; they pass by rules 2 and 3.
     */
    val KNOWN_CERT_SHA256: Map<String, Set<String>> = mapOf(
        "com.google.android.projection.gearhead" to setOf(
            "fdb00c43dbde8b51cb312aa81d3b5fa17713adb94b28f598d77f8eb89daceedf",
            "70811a3eacfd2e83e18da9bfede52df16ce91f2e69a44d21f18ab66991130771",
            "1975b2f17177bc89a5dff31f9e64a6cae281a53dc1d1d59b1d147fe1c82afa00",
        ),
    )

    /** What the decision needs to know about an installed package. */
    interface PackageFacts {
        /** [packageName] is one of [uid]'s packages. */
        fun uidOwns(uid: Int, packageName: String): Boolean

        /** SHA-256 (lower-case hex) of each of its signing certificates. */
        fun certDigests(packageName: String): Set<String>

        /** Signed with the platform key. */
        fun platformSigned(packageName: String): Boolean

        /** Installed in the system image (possibly updated since). */
        fun systemApp(packageName: String): Boolean
    }

    /**
     * Whether [packageName] on [uid] is a genuine car host. [nameVerified]
     * is Media3's ControllerInfo.isPackageNameVerified: a controller on the
     * platform session reports a name Media3 couldn't check, so then the
     * uid has to own it.
     */
    fun isCarHost(facts: PackageFacts, packageName: String, uid: Int, nameVerified: Boolean): Boolean {
        if (packageName !in PACKAGES) return false
        return try {
            if (!nameVerified && !facts.uidOwns(uid, packageName)) return false
            val known = KNOWN_CERT_SHA256[packageName].orEmpty()
            facts.certDigests(packageName).any { it in known } ||
                facts.platformSigned(packageName) ||
                facts.systemApp(packageName)
        } catch (_: Exception) {
            // Not installed, or not visible: not a car host.
            false
        }
    }

    fun sha256Hex(bytes: ByteArray): String =
        MessageDigest.getInstance("SHA-256").digest(bytes).joinToString("") { "%02x".format(it) }

    /** [PackageFacts] from the PackageManager. The car hosts are declared
     *  in the manifest's <queries>, so they are visible on Android 11+. */
    class Installed(context: Context) : PackageFacts {
        private val pm: PackageManager = context.packageManager

        override fun uidOwns(uid: Int, packageName: String): Boolean =
            pm.getPackagesForUid(uid)?.contains(packageName) == true

        override fun certDigests(packageName: String): Set<String> =
            signatures(packageName).map { sha256Hex(it.toByteArray()) }.toSet()

        override fun platformSigned(packageName: String): Boolean =
            pm.checkSignatures("android", packageName) == PackageManager.SIGNATURE_MATCH

        override fun systemApp(packageName: String): Boolean =
            pm.getApplicationInfo(packageName, 0).flags and ApplicationInfo.FLAG_SYSTEM != 0

        @Suppress("DEPRECATION")
        private fun signatures(packageName: String): List<Signature> {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) {
                val info = pm.getPackageInfo(packageName, PackageManager.GET_SIGNING_CERTIFICATES).signingInfo
                    ?: return emptyList()
                val certs = if (info.hasMultipleSigners()) info.apkContentsSigners else info.signingCertificateHistory
                return certs?.toList().orEmpty()
            }
            return pm.getPackageInfo(packageName, PackageManager.GET_SIGNATURES).signatures?.toList().orEmpty()
        }
    }
}
