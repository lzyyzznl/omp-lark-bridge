package com.omp.mobile.data

import android.content.Context
import android.content.Intent
import androidx.core.content.ContextCompat
import com.omp.mobile.service.StreamService
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.withContext

class Repository(
    private val context: Context,
    private val settings: SettingsStore,
    database: AppDatabase,
) {
    private val dao = database.nodes()
    val api = ApiHolder()

    val nodes: Flow<List<NodeEntity>> = dao.observe()

    private var loaded = false

    /** Loads persisted settings into the API holder; safe to call repeatedly. */
    suspend fun ensureLoaded(): AppSettings {
        val current = settings.current()
        val base = SettingsStore.normalizeBaseUrl(current.baseUrl)
        api.baseUrl = base
        api.mgmtToken = current.mgmtToken
        val secret = settings.secret()
        api.creds = if (current.deviceId.isNotEmpty() && secret.isNotEmpty()) {
            Creds(current.deviceId, secret)
        } else {
            null
        }
        loaded = true
        return current.copy(baseUrl = base)
    }

    /**
     * Probes the server before enrollment. /status requires the management
     * token, so an unauthenticated holder cannot read it; keep the live
     * client untouched so a failed probe can't disturb an enrolled session.
     */
    suspend fun checkStatus(rawUrl: String, mgmtToken: String = ""): MobileStatus =
        withContext(Dispatchers.IO) {
            val base = SettingsStore.normalizeBaseUrl(rawUrl)
            val token = mgmtToken.trim()
            if (token.isEmpty()) {
                api.baseUrl = base
                api.api().status().unwrap("status")
            } else {
                val probe = ApiHolder()
                probe.baseUrl = base
                probe.mgmtToken = token
                probe.api().status().unwrap("status")
            }
        }

    suspend fun saveServer(rawUrl: String, mgmtToken: String) {
        settings.saveServer(SettingsStore.normalizeBaseUrl(rawUrl), mgmtToken.trim())
        ensureLoaded()
    }

    suspend fun enroll(rawUrl: String, mgmtToken: String, name: String): EnrollResponse =
        withContext(Dispatchers.IO) {
            val base = SettingsStore.normalizeBaseUrl(rawUrl)
            api.baseUrl = base
            api.mgmtToken = mgmtToken.trim()
            api.creds = null
            settings.saveServer(base, mgmtToken.trim())
            val res = api.api().enroll(EnrollRequest(name.trim().ifEmpty { "Android" })).unwrap("enroll")
            settings.saveDevice(res.deviceId, res.deviceName, res.secret)
            api.creds = Creds(res.deviceId, res.secret)
            res
        }

    suspend fun signOut() {
        settings.clearDevice()
        api.creds = null
        stopMonitoring()
    }

    suspend fun refreshNodes(): List<NodeEntity> = withContext(Dispatchers.IO) {
        ensureLoaded()
        val res = api.api().nodes().unwrap("nodes")
        val entities = res.nodes.map { n ->
            NodeEntity(
                id = n.id,
                name = n.name.ifEmpty { n.id },
                online = n.online,
                lastSeen = n.lastSeen.orEmpty(),
                agentCount = n.agentCount,
                sessionCount = n.sessionCount,
                webUrl = n.webUrl,
                secretSet = n.secretSet,
                loginCodeSet = n.loginCodeSet,
            )
        }
        dao.clear()
        dao.upsert(entities)
        entities
    }

    suspend fun approvals(status: String = "all"): List<Approval> = withContext(Dispatchers.IO) {
        ensureLoaded()
        api.api().approvals(status).unwrap("approvals").approvals
    }

    suspend fun decide(id: String, approve: Boolean) = withContext(Dispatchers.IO) {
        ensureLoaded()
        api.api().decide(id, DecideRequest(approve)).unwrap("approval")
    }

    suspend fun revokeDevice(id: String) = withContext(Dispatchers.IO) {
        ensureLoaded()
        validate(api.api().revokeDevice(id), "revoke device")
    }

    suspend fun devices(): List<DeviceItem> = withContext(Dispatchers.IO) {
        ensureLoaded()
        api.api().devices().unwrap("devices").devices
    }

    /** Mints a one-time WebView URL for a node; requires the phone signature. */
    suspend fun grantUrl(device: String): String = withContext(Dispatchers.IO) {
        ensureLoaded()
        api.api().grant(GrantRequest(device)).unwrap("open node").url
    }

    suspend fun pairNew(deviceId: String, name: String, host: String, force: Boolean): PairNewResponse =
        withContext(Dispatchers.IO) {
            ensureLoaded()
            api.api().pairNew(PairNewRequest(deviceId.trim(), name.trim(), host.trim(), force))
                .unwrap("pair node")
        }

    suspend fun unpair(device: String) = withContext(Dispatchers.IO) {
        ensureLoaded()
        validate(api.api().unpair(device), "remove node")
    }

    suspend fun updateNode(device: String, body: NodeUpdateRequest) = withContext(Dispatchers.IO) {
        ensureLoaded()
        api.api().updateNode(device, body).unwrap("update node")
    }

    fun monitoringEnabled(): Boolean = api.creds != null

    fun startMonitoring() {
        ContextCompat.startForegroundService(
            context,
            Intent(context, StreamService::class.java),
        )
    }

    fun stopMonitoring() {
        context.stopService(Intent(context, StreamService::class.java))
    }

    private fun validate(res: retrofit2.Response<Unit>, what: String) {
        if (!res.isSuccessful) {
            throw IllegalStateException("$what failed: HTTP ${res.code()}")
        }
    }

    /** Turns the `{ok,data,error}` envelope into a value or a readable error. */
    private fun <T> Envelope<T>.unwrap(what: String): T {
        if (!ok) throw IllegalStateException(error?.takeIf { it.isNotBlank() } ?: "$what failed")
        return data ?: throw IllegalStateException("$what failed: empty response")
    }

    private suspend fun loadedSettings(): AppSettings = settings.settings.first()
}
