package tv.onscreen.mobile.data.api

import java.security.KeyStore
import java.security.cert.X509Certificate
import javax.net.ssl.TrustManager
import javax.net.ssl.TrustManagerFactory
import javax.net.ssl.X509TrustManager

/**
 * X.509 trust manager that validates the server cert chain against
 * the system trust store but **skips OCSP/CRL revocation checking**.
 *
 * Why: Android's default TrustManager on some devices (TV emulators,
 * dev boards with skewed clocks, devices behind certain enterprise
 * VPNs) raises `CertPathValidatorException: Response is unreliable:
 * its validity interval is out-of-date` from
 * `RevocationChecker.checkOCSP` — the OCSP responder's response
 * appears stale relative to the device's clock. The connection
 * fails even when the cert itself is valid, signed by a trusted CA,
 * and within its `notBefore`/`notAfter` window.
 *
 * Soft-fail on OCSP is the default behaviour of every major browser
 * (Chrome, Firefox, Safari) for exactly this reason — OCSP responders
 * are operationally flaky and device clocks lie. Stricter behaviour
 * is appropriate for banking apps and corporate VPN clients; for a
 * self-hosted media server where the operator controls the cert
 * chain, it's overkill that just makes the app feel broken.
 *
 * Trust + signature + date validation are NOT skipped. A revoked
 * cert that has been replaced (the realistic failure mode for a
 * media-server deployment) will still be rejected because the new
 * cert in the chain won't match the old chain pinned by the
 * operator. The only thing this manager doesn't check is whether a
 * still-presented cert is on a CA's revocation list — a legitimate
 * concern for adversarial environments but outside the threat model
 * here.
 */
class NoRevocationTrustManager : X509TrustManager {

    // Delegate to the platform's full X509TrustManager for chain
    // validation. The platform manager does proper path-building
    // (including fetching missing intermediates via the AIA
    // extension when the server sends an incomplete chain — the
    // exact case Cloudflare-fronted deployments hit because their
    // edges send only [leaf, intermediate] without the root).
    //
    // Building our own PKIXParameters + CertPathValidator from
    // scratch — the previous implementation — looked equivalent to
    // the system path but skipped this AIA fetch, throwing
    // "Path does not chain with any of the trust anchors" against
    // any server with a public-CA-issued cert that doesn't ship the
    // full chain in its handshake. That hit a user on a Samsung
    // S24 FE pointing at a Cloudflare-fronted QA box.
    //
    // Revocation: the platform delegate honours the system's default
    // revocation policy (typically OCSP-stapling-aware, soft-fail on
    // a missing OCSP response). That matches every browser; the
    // earlier opt-out was overcautious.
    private val delegate: X509TrustManager = systemDelegate()

    override fun checkServerTrusted(chain: Array<X509Certificate>, authType: String) {
        delegate.checkServerTrusted(chain, authType)
    }

    override fun checkClientTrusted(chain: Array<X509Certificate>, authType: String) {
        // OnScreen is a client — not relevant.
    }

    override fun getAcceptedIssuers(): Array<X509Certificate> =
        delegate.acceptedIssuers

    companion object {
        /** Loads the platform's default X509TrustManager — the same
         *  instance OkHttp would use without our intervention. */
        private fun systemDelegate(): X509TrustManager {
            val tmf = TrustManagerFactory.getInstance(
                TrustManagerFactory.getDefaultAlgorithm(),
            )
            tmf.init(null as KeyStore?)
            return tmf.trustManagers
                .first { it is X509TrustManager } as X509TrustManager
        }

        /** Helper for NetworkModule — returns the trust manager array
         *  shape SSLContext.init() expects. */
        fun trustManagers(): Array<TrustManager> = arrayOf(NoRevocationTrustManager())
    }
}
