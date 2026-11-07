package com.omp.mobile.data

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

@Serializable
data class MobileStatus(
    val enabled: Boolean = false,
    val devices: Int = 0,
    val pending: Int = 0,
    @SerialName("require_console") val requireConsole: Boolean = true,
    @SerialName("require_for_keys") val requireForKeys: Boolean = true,
)

@Serializable
data class EnrollRequest(val name: String)

@Serializable
data class EnrollResponse(
    @SerialName("device_id") val deviceId: String,
    @SerialName("device_name") val deviceName: String = "",
    val secret: String,
)

@Serializable
data class DeviceItem(
    val id: String,
    val name: String = "",
    @SerialName("created_at") val createdAt: String? = null,
    @SerialName("last_seen") val lastSeen: String? = null,
    val current: Boolean = false,
)

@Serializable
data class DevicesResponse(val devices: List<DeviceItem> = emptyList())

@Serializable
data class NodeItem(
    val id: String,
    val name: String = "",
    @SerialName("web_url") val webUrl: String = "",
    @SerialName("last_seen") val lastSeen: String? = null,
    val online: Boolean = false,
    @SerialName("agent_count") val agentCount: Int = 0,
    @SerialName("session_count") val sessionCount: Int = 0,
    @SerialName("secret_set") val secretSet: Boolean = false,
    @SerialName("login_code_set") val loginCodeSet: Boolean = false,
    @SerialName("is_self") val isSelf: Boolean = false,
)

@Serializable
data class NodesResponse(
    val nodes: List<NodeItem> = emptyList(),
    val role: String = "",
    @SerialName("can_edit") val canEdit: Boolean = false,
)

@Serializable
data class Approval(
    val id: String,
    val kind: String = "node",
    val device: String = "",
    @SerialName("device_name") val deviceName: String = "",
    val host: String = "",
    val ip: String = "",
    @SerialName("user_agent") val userAgent: String = "",
    val path: String = "",
    val status: String = "pending",
    @SerialName("created_at") val createdAt: String = "",
    @SerialName("expires_at") val expiresAt: String = "",
)

@Serializable
data class ApprovalsResponse(val approvals: List<Approval> = emptyList())

@Serializable
data class DecideRequest(val approve: Boolean)

@Serializable
data class DecideResponse(val status: String = "", val link: String = "")

@Serializable
data class GrantRequest(val device: String)

@Serializable
data class GrantResponse(
    val url: String = "",
    @SerialName("expires_at") val expiresAt: String = "",
    val device: String = "",
)

@Serializable
data class PairNewRequest(
    @SerialName("device_id") val deviceId: String,
    val name: String = "",
    val host: String = "",
    val force: Boolean = false,
)

@Serializable
data class PairNewResponse(
    val code: String = "",
    @SerialName("device_id") val deviceId: String = "",
    @SerialName("device_name") val deviceName: String = "",
    val host: String = "",
    @SerialName("web_url") val webUrl: String = "",
    @SerialName("expires_at") val expiresAt: String = "",
    val command: String = "",
)

@Serializable
data class NodeUpdateRequest(
    @SerialName("device_name") val name: String? = null,
    val secret: String? = null,
    @SerialName("login_code") val loginCode: String? = null,
    @SerialName("clear_login_code") val clearLoginCode: Boolean = false,
)

@Serializable
data class NodeUpdateResponse(val updated: Boolean = false)
