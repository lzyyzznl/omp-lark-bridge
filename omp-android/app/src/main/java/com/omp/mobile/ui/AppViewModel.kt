package com.omp.mobile.ui

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.omp.mobile.OmpApp
import com.omp.mobile.data.AppSettings
import com.omp.mobile.data.Approval
import com.omp.mobile.data.DeviceItem
import com.omp.mobile.data.MobileStatus
import com.omp.mobile.data.NodeEntity
import com.omp.mobile.data.NodeUpdateRequest
import com.omp.mobile.data.PairNewResponse
import com.omp.mobile.data.SettingsStore
import com.omp.mobile.service.StreamEvents
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

class AppViewModel(app: Application) : AndroidViewModel(app) {
    private val omp = app as OmpApp
    private val repo = omp.repository
    private val store: SettingsStore = omp.settings

    val settings: StateFlow<AppSettings?> = store.settings.stateIn(
        viewModelScope, SharingStarted.Eagerly, null,
    )

    val nodes: StateFlow<List<NodeEntity>> = repo.nodes.stateIn(
        viewModelScope, SharingStarted.Eagerly, emptyList(),
    )

    val approvals = MutableStateFlow<List<Approval>>(emptyList())
    val devices = MutableStateFlow<List<DeviceItem>>(emptyList())
    val busy = MutableStateFlow(false)
    val message = MutableStateFlow<String?>(null)
    val status = MutableStateFlow<MobileStatus?>(null)

    init {
        viewModelScope.launch {
            val loaded = repo.ensureLoaded()
            if (repo.monitoringEnabled() && loaded.monitoring) repo.startMonitoring()
            runCatching { refreshNodesInternal() }
            runCatching { refreshApprovalsInternal() }
        }
        viewModelScope.launch {
            StreamEvents.events.collect { event ->
                when {
                    event == "approval" -> runCatching { refreshApprovalsInternal() }
                    event == "approval_decided" -> runCatching { refreshApprovalsInternal() }
                    event == "devices" -> runCatching { refreshDevicesInternal() }
                }
            }
        }
    }

    fun check(rawUrl: String, token: String) {
        viewModelScope.launch {
            busy.value = true
            try {
                val st = withContext(Dispatchers.IO) { repo.checkStatus(rawUrl, token) }
                status.value = st
                message.value = "Server reachable: ${st.devices} phone(s), ${st.pending} pending"
            } catch (t: Throwable) {
                status.value = null
                message.value = "Connect failed: ${t.message}"
            } finally {
                busy.value = false
            }
        }
    }

    fun enroll(rawUrl: String, token: String, name: String, onDone: () -> Unit) {
        viewModelScope.launch {
            busy.value = true
            try {
                val res = repo.enroll(rawUrl, token, name)
                repo.startMonitoring()
                message.value = "Enrolled as ${res.deviceName}"
                runCatching { refreshNodesInternal() }
                runCatching { refreshApprovalsInternal() }
                onDone()
            } catch (t: Throwable) {
                message.value = "Enroll failed: ${t.message}"
            } finally {
                busy.value = false
            }
        }
    }

    fun refreshNodes() {
        viewModelScope.launch {
            busy.value = true
            try {
                refreshNodesInternal()
            } catch (t: Throwable) {
                message.value = "Refresh failed: ${t.message}"
            } finally {
                busy.value = false
            }
        }
    }

    fun refreshApprovals() {
        viewModelScope.launch { runCatching { refreshApprovalsInternal() } }
    }

    fun refreshDevices() {
        viewModelScope.launch { runCatching { refreshDevicesInternal() } }
    }

    fun decide(id: String, approve: Boolean) {
        viewModelScope.launch {
            try {
                repo.decide(id, approve)
                message.value = if (approve) "Approved" else "Denied"
                refreshApprovalsInternal()
            } catch (t: Throwable) {
                message.value = "Decision failed: ${t.message}"
            }
        }
    }

    suspend fun grantUrl(device: String): String = withContext(Dispatchers.IO) {
        repo.grantUrl(device)
    }

    fun pairNew(deviceId: String, name: String, host: String, force: Boolean, onDone: (PairNewResponse) -> Unit) {
        viewModelScope.launch {
            busy.value = true
            try {
                val res = repo.pairNew(deviceId, name, host, force)
                message.value = "Node paired: ${res.host}"
                refreshNodesInternal()
                onDone(res)
            } catch (t: Throwable) {
                message.value = "Pair failed: ${t.message}"
            } finally {
                busy.value = false
            }
        }
    }

    fun updateNode(device: String, body: NodeUpdateRequest) {
        viewModelScope.launch {
            busy.value = true
            try {
                repo.updateNode(device, body)
                message.value = "Node updated"
                refreshNodesInternal()
            } catch (t: Throwable) {
                message.value = "Update failed: ${t.message}"
            } finally {
                busy.value = false
            }
        }
    }

    fun unpair(device: String) {
        viewModelScope.launch {
            busy.value = true
            try {
                repo.unpair(device)
                message.value = "Node removed"
                refreshNodesInternal()
            } catch (t: Throwable) {
                message.value = "Remove failed: ${t.message}"
            } finally {
                busy.value = false
            }
        }
    }

    fun revokeDevice(id: String) {
        viewModelScope.launch {
            try {
                repo.revokeDevice(id)
                refreshDevicesInternal()
            } catch (t: Throwable) {
                message.value = "Revoke failed: ${t.message}"
            }
        }
    }

    fun setMonitoring(enabled: Boolean) {
        viewModelScope.launch {
            store.saveMonitoring(enabled)
            if (enabled) repo.startMonitoring() else repo.stopMonitoring()
        }
    }

    fun signOut(onDone: () -> Unit) {
        viewModelScope.launch {
            repo.signOut()
            approvals.value = emptyList()
            devices.value = emptyList()
            onDone()
        }
    }

    fun clearMessage() {
        message.value = null
    }

    private suspend fun refreshNodesInternal() {
        repo.refreshNodes()
    }

    private suspend fun refreshApprovalsInternal() {
        approvals.value = repo.approvals("all")
    }

    private suspend fun refreshDevicesInternal() {
        devices.value = repo.devices()
    }

    companion object {
        fun normalize(raw: String) = SettingsStore.normalizeBaseUrl(raw)
    }
}
