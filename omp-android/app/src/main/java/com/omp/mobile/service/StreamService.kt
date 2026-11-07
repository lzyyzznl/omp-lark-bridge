package com.omp.mobile.service

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.os.Build
import android.os.IBinder
import androidx.core.app.NotificationCompat
import com.omp.mobile.MainActivity
import com.omp.mobile.OmpApp
import com.omp.mobile.R
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener

/** App-wide event bus fed by the stream service. */
object StreamEvents {
    private val _events = MutableSharedFlow<String>(extraBufferCapacity = 16)
    val events: SharedFlow<String> = _events

    fun emit(type: String) {
        _events.tryEmit(type)
    }
}

class StreamService : Service() {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private var socket: WebSocket? = null
    private var reconnectJob: Job? = null
    private val json = Json { ignoreUnknownKeys = true }

    private val repo by lazy { (application as OmpApp).repository }
    private val settings by lazy { (application as OmpApp).settings }

    override fun onCreate() {
        super.onCreate()
        createChannels()
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        startForeground(ONGOING_ID, ongoingNotification("Connecting…"))
        connect()
        return START_STICKY
    }

    override fun onDestroy() {
        reconnectJob?.cancel()
        socket?.close(1000, "service stopped")
        socket = null
        super.onDestroy()
    }

    override fun onBind(intent: Intent?): IBinder? = null

    private fun connect() {
        if (reconnectJob?.isActive == true) return
        reconnectJob = scope.launch {
            while (isActive) {
                val error = connectOnce()
                if (error == null) {
                    delay(1000)
                } else {
                    updateOngoing("Offline: $error")
                    delay(5000)
                }
            }
        }
    }

    private suspend fun connectOnce(): String? {
        return try {
            repo.ensureLoaded()
            val request = repo.api.streamRequest()
            val latch = kotlinx.coroutines.CompletableDeferred<String?>()
            val wsClient = repo.api.client.newBuilder()
                .readTimeout(0, java.util.concurrent.TimeUnit.MILLISECONDS)
                .build()
            socket = wsClient.newWebSocket(request, object : WebSocketListener() {
                override fun onOpen(webSocket: WebSocket, response: Response) {
                    updateOngoing("Connected to OMP server")
                    StreamEvents.emit("connected")
                }

                override fun onMessage(webSocket: WebSocket, text: String) {
                    handleEvent(text)
                }

                override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
                    latch.complete("closed ($code)")
                }

                override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
                    latch.complete(t.message ?: "network error")
                }
            })
            latch.await()
        } catch (t: Throwable) {
            t.message ?: "network error"
        }
    }

    private fun handleEvent(raw: String) {
        val obj = runCatching { json.parseToJsonElement(raw).jsonObject }.getOrNull() ?: return
        val type = obj["type"]?.jsonPrimitive?.contentOrNullSafe() ?: return
        when (type) {
            "approval" -> {
                val approval = obj["approval"]?.jsonObject ?: return
                val id = approval["id"]?.jsonPrimitive?.contentOrNullSafe() ?: return
                val node = approval["device_name"]?.jsonPrimitive?.contentOrNullSafe()
                    ?: approval["device"]?.jsonPrimitive?.contentOrNullSafe()
                    ?: "node"
                val ip = approval["ip"]?.jsonPrimitive?.contentOrNullSafe().orEmpty()
                val ua = approval["user_agent"]?.jsonPrimitive?.contentOrNullSafe().orEmpty()
                notifyApproval(id, node, ip, ua)
                StreamEvents.emit("approval")
            }
            "approval_decided", "devices" -> StreamEvents.emit(type)
        }
    }

    private fun notifyApproval(id: String, node: String, ip: String, userAgent: String) {
        val open = PendingIntent.getActivity(
            this,
            0,
            Intent(this, MainActivity::class.java).putExtra("tab", "approvals"),
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )
        val approve = decisionIntent(id, true, 100)
        val deny = decisionIntent(id, false, 200)
        val notif = NotificationCompat.Builder(this, CHANNEL_ALERTS)
            .setSmallIcon(R.drawable.ic_stat_omp)
            .setContentTitle("Login request: $node")
            .setContentText(
                buildString {
                    if (ip.isNotEmpty()) append("from $ip")
                    if (userAgent.isNotEmpty()) {
                        if (isNotEmpty()) append(" · ")
                        append(userAgent.take(60))
                    }
                }.ifEmpty { "A browser wants to open this node" },
            )
            .setStyle(NotificationCompat.BigTextStyle().bigText("$node\n$ip\n$userAgent"))
            .setPriority(NotificationCompat.PRIORITY_HIGH)
            .setCategory(NotificationCompat.CATEGORY_MESSAGE)
            .setAutoCancel(true)
            .setContentIntent(open)
            .addAction(0, "Deny", deny)
            .addAction(0, "Approve", approve)
            .build()
        manager().notify(id.hashCode().and(0x7fffffff), notif)
    }

    private fun decisionIntent(id: String, approve: Boolean, code: Int): PendingIntent {
        val intent = Intent(this, DecisionReceiver::class.java)
            .putExtra(DecisionReceiver.EXTRA_ID, id)
            .putExtra(DecisionReceiver.EXTRA_APPROVE, approve)
        return PendingIntent.getBroadcast(
            this, code + id.hashCode().and(0xff),
            intent,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )
    }

    private fun ongoingNotification(text: String): Notification {
        val open = PendingIntent.getActivity(
            this, 1, Intent(this, MainActivity::class.java),
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )
        return NotificationCompat.Builder(this, CHANNEL_ONGOING)
            .setSmallIcon(R.drawable.ic_stat_omp)
            .setContentTitle("OMP Mobile")
            .setContentText(text)
            .setOngoing(true)
            .setContentIntent(open)
            .setPriority(NotificationCompat.PRIORITY_LOW)
            .build()
    }

    private fun updateOngoing(text: String) {
        manager().notify(ONGOING_ID, ongoingNotification(text))
    }

    private fun createChannels() {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) return
        val ongoing = NotificationChannel(
            CHANNEL_ONGOING, "Connection", NotificationManager.IMPORTANCE_LOW,
        )
        val alerts = NotificationChannel(
            CHANNEL_ALERTS, "Login approvals", NotificationManager.IMPORTANCE_HIGH,
        )
        manager().createNotificationChannels(listOf(ongoing, alerts))
    }

    private fun manager(): NotificationManager =
        getSystemService(NotificationManager::class.java)

    companion object {
        const val CHANNEL_ONGOING = "omp_ongoing"
        const val CHANNEL_ALERTS = "omp_alerts"
        const val ONGOING_ID = 1001
    }
}

private fun kotlinx.serialization.json.JsonPrimitive.contentOrNullSafe(): String? =
    runCatching { content }.getOrNull()

class DecisionReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        val id = intent.getStringExtra(EXTRA_ID) ?: return
        val approve = intent.getBooleanExtra(EXTRA_APPROVE, false)
        val pending = goAsync()
        val app = context.applicationContext as OmpApp
        CoroutineScope(Dispatchers.IO).launch {
            try {
                app.repository.decide(id, approve)
                StreamEvents.emit("approval_decided")
            } catch (t: Throwable) {
                StreamEvents.emit("error:${t.message}")
            } finally {
                pending.finish()
            }
        }
    }

    companion object {
        const val EXTRA_ID = "approval_id"
        const val EXTRA_APPROVE = "approve"
    }
}
