package com.omp.mobile.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.Lock
import androidx.compose.material.icons.filled.Notifications
import androidx.compose.material.icons.filled.Settings
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.AssistChip
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.Checkbox
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import com.omp.mobile.data.AppSettings
import com.omp.mobile.data.Approval
import com.omp.mobile.data.NodeEntity
import com.omp.mobile.data.NodeUpdateRequest
import com.omp.mobile.data.PairNewResponse
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter

@Composable
fun SetupScreen(vm: AppViewModel, settings: AppSettings) {
    var server by rememberSaveable { mutableStateOf(settings.baseUrl) }
    var token by rememberSaveable { mutableStateOf(settings.mgmtToken) }
    var name by rememberSaveable { mutableStateOf("Android") }
    var advanced by rememberSaveable { mutableStateOf(false) }
    val busy by vm.busy.collectAsState()
    val status by vm.status.collectAsState()

    Column(
        modifier = Modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(24.dp),
        verticalArrangement = Arrangement.spacedBy(14.dp),
    ) {
        Spacer(Modifier.height(24.dp))
        Text("OMP Mobile", style = MaterialTheme.typography.headlineMedium)
        Text(
            "One phone to open every node and to approve every login.",
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )

        OutlinedTextField(
            value = server,
            onValueChange = { server = it },
            label = { Text("Server URL") },
            placeholder = { Text("http://100.108.43.107") },
            singleLine = true,
            modifier = Modifier.fillMaxWidth(),
            keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri),
        )
        OutlinedButton(onClick = { vm.check(server, token) }, enabled = !busy) {
            Text("Test connection")
        }
        status?.let { st ->
            Card(colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceVariant)) {
                Column(Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                    Text("Gateway enabled: ${st.enabled}", style = MaterialTheme.typography.bodySmall)
                    Text("Phones: ${st.devices} · pending logins: ${st.pending}", style = MaterialTheme.typography.bodySmall)
                    Text(
                        "Requires phone approval: ${if (st.requireConsole) "yes" else "no"}",
                        style = MaterialTheme.typography.bodySmall,
                    )
                }
            }
        }

        Text("Phone enrollment", style = MaterialTheme.typography.titleMedium)
        Text(
            "Enrolling needs the management token from the server's config.toml. " +
                "After that, every change is signed by this phone's private secret.",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        OutlinedTextField(
            value = token,
            onValueChange = { token = it },
            label = { Text("Management token") },
            singleLine = true,
            visualTransformation = PasswordVisualTransformation(),
            modifier = Modifier.fillMaxWidth(),
        )
        OutlinedTextField(
            value = name,
            onValueChange = { name = it },
            label = { Text("This phone's name") },
            singleLine = true,
            modifier = Modifier.fillMaxWidth(),
        )
        Button(
            onClick = { vm.enroll(server, token, name) {} },
            enabled = !busy && server.isNotBlank() && token.isNotBlank(),
            modifier = Modifier.fillMaxWidth(),
        ) {
            if (busy) {
                CircularProgressIndicator(Modifier.size(18.dp), strokeWidth = 2.dp)
            } else {
                Text("Enroll this phone")
            }
        }
        TextButton(onClick = { advanced = !advanced }) {
            Text(if (advanced) "Hide notes" else "How do I get a token?")
        }
        if (advanced) {
            Text(
                "On the server, open config.toml → [management] token. " +
                    "The hub center must have [mobile] enabled. Enrollment works over the LAN " +
                    "or through the tunnel; the phone secret never leaves the app.",
                style = MaterialTheme.typography.bodySmall,
            )
        }
    }
}

@Composable
fun NodesScreen(
    vm: AppViewModel,
    nodes: List<NodeEntity>,
    onOpen: (String, String) -> Unit,
    onAddNode: () -> Unit,
) {
    var keysFor by remember { mutableStateOf<NodeEntity?>(null) }
    var openDialog by remember { mutableStateOf(false) }

    LazyColumn(
        modifier = Modifier.fillMaxSize(),
        contentPadding = androidx.compose.foundation.layout.PaddingValues(16.dp),
        verticalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        item {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(
                    "${nodes.count { it.online }} of ${nodes.size} nodes online",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Spacer(Modifier.weight(1f))
                // The console is gated like a node; opening it from here runs
                // through the same one-time grant so the WebView skips the
                // wait page.
                TextButton(onClick = { onOpen("console", "Console") }) {
                    Text("Console")
                }
            }
        }
        if (nodes.isEmpty()) {
            item {
                Card(Modifier.fillMaxWidth()) {
                    Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                        Text("No nodes cached", style = MaterialTheme.typography.titleMedium)
                        Text(
                            "Make sure this phone is enrolled, the hub center is reachable, " +
                                "and at least one node is paired.",
                            style = MaterialTheme.typography.bodySmall,
                        )
                        Button(onClick = { vm.refreshNodes() }) { Text("Refresh nodes") }
                    }
                }
            }
        }
        items(nodes, key = { it.id }) { node ->
            NodeCard(
                node = node,
                onOpen = { onOpen(node.id, node.name.ifEmpty { node.id }) },
                onKeys = { keysFor = node },
            )
        }
        item {
            TextButton(onClick = onAddNode, modifier = Modifier.fillMaxWidth()) {
                Text("Pair a new node")
            }
        }
    }
    keysFor?.let { node ->
        NodeKeysDialog(vm, node, onDismiss = { keysFor = null })
    }
}

@Composable
private fun NodeCard(node: NodeEntity, onOpen: () -> Unit, onKeys: () -> Unit) {
    Card(modifier = Modifier.fillMaxWidth().clickable(onClick = onOpen)) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Box(
                    Modifier
                        .size(9.dp)
                        .background(
                            if (node.online) Color(0xFF22A06B) else MaterialTheme.colorScheme.outline,
                            CircleShape,
                        ),
                )
                Spacer(Modifier.width(8.dp))
                Text(node.name, style = MaterialTheme.typography.titleMedium)
                Spacer(Modifier.weight(1f))
                Text(
                    if (node.online) "online" else "offline",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            Text(
                node.id + " · ${node.agentCount} agents · ${node.sessionCount} sessions",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                if (node.loginCodeSet) AssistChip(onClick = {}, label = { Text("login code") })
                if (!node.secretSet) AssistChip(onClick = {}, label = { Text("no secret") })
                Text(
                    "seen ${formatTime(node.lastSeen)}",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Button(onClick = onOpen) { Text("Open") }
                OutlinedButton(onClick = onKeys) {
                    Text("Keys")
                }
            }
        }
    }
}

@Composable
fun ApprovalsScreen(vm: AppViewModel, onOpenNode: (String, String) -> Unit) {
    val approvals by vm.approvals.collectAsState()
    var filter by remember { mutableStateOf("pending") }
    val shown = if (filter == "all") approvals else approvals.filter { it.status == "pending" }

    Column(Modifier.fillMaxSize()) {
        Row(
            Modifier.padding(horizontal = 16.dp, vertical = 8.dp),
            horizontalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            FilterChip(
                selected = filter == "pending",
                onClick = { filter = "pending" },
                label = { Text("Pending (${approvals.count { it.status == "pending" }})") },
            )
            FilterChip(
                selected = filter == "all",
                onClick = { filter = "all" },
                label = { Text("All") },
            )
        }
        LazyColumn(
            contentPadding = androidx.compose.foundation.layout.PaddingValues(16.dp),
            verticalArrangement = Arrangement.spacedBy(10.dp),
        ) {
            if (shown.isEmpty()) {
                item {
                    Text(
                        if (filter == "pending") "No pending login requests." else "No requests yet.",
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
            items(shown, key = { it.id }) { approval ->
                ApprovalCard(vm, approval, onOpenNode)
            }
        }
    }
}

@Composable
private fun ApprovalCard(vm: AppViewModel, a: Approval, onOpenNode: (String, String) -> Unit) {
    Card(Modifier.fillMaxWidth()) {
        Column(Modifier.padding(14.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(
                    a.deviceName.ifEmpty { a.device.ifEmpty { "node" } },
                    style = MaterialTheme.typography.titleSmall,
                )
                Spacer(Modifier.weight(1f))
                StatusChip(a.status)
            }
            if (a.ip.isNotEmpty()) Text("from ${a.ip}", style = MaterialTheme.typography.bodySmall)
            if (a.userAgent.isNotEmpty()) {
                Text(
                    a.userAgent,
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            Text(
                formatTime(a.createdAt),
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            if (a.status == "pending") {
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    Button(onClick = { vm.decide(a.id, true) }) { Text("Approve") }
                    OutlinedButton(onClick = { vm.decide(a.id, false) }) { Text("Deny") }
                }
            } else if (a.status == "approved" && a.device.isNotEmpty()) {
                OutlinedButton(onClick = {
                    onOpenNode(a.device, a.deviceName.ifEmpty { "Node" })
                }) {
                    Text("Open node")
                }
            }
        }
    }
}

@Composable
private fun StatusChip(status: String) {
    val color = when (status) {
        "approved" -> Color(0xFF22A06B)
        "denied", "expired" -> MaterialTheme.colorScheme.error
        else -> MaterialTheme.colorScheme.primary
    }
    Text(status, style = MaterialTheme.typography.labelMedium, color = color)
}

@Composable
fun SettingsScreen(vm: AppViewModel, settings: AppSettings) {
    val devices by vm.devices.collectAsState()
    val busy by vm.busy.collectAsState()
    var showSignOut by remember { mutableStateOf(false) }

    Column(
        Modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(16.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        Card(Modifier.fillMaxWidth()) {
            Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                Text("Server", style = MaterialTheme.typography.titleSmall)
                Text(settings.baseUrl, style = MaterialTheme.typography.bodyMedium)
                Text(
                    "Signed in as ${settings.deviceName} (${settings.deviceId})",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }

        Card(Modifier.fillMaxWidth()) {
            Row(Modifier.padding(16.dp), verticalAlignment = Alignment.CenterVertically) {
                Column(Modifier.weight(1f)) {
                    Text("Background monitoring", style = MaterialTheme.typography.titleSmall)
                    Text(
                        "Keep the event stream open to receive approval prompts.",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                Switch(
                    checked = settings.monitoring,
                    onCheckedChange = { vm.setMonitoring(it) },
                )
            }
        }

        Row(verticalAlignment = Alignment.CenterVertically) {
            Text("Phones", style = MaterialTheme.typography.titleSmall, modifier = Modifier.weight(1f))
            TextButton(onClick = { vm.refreshDevices() }, enabled = !busy) { Text("Refresh") }
        }
        devices.forEach { device ->
            Card(Modifier.fillMaxWidth()) {
                Row(Modifier.padding(14.dp), verticalAlignment = Alignment.CenterVertically) {
                    Column(Modifier.weight(1f)) {
                        Text(
                            device.name + if (device.current) " (this phone)" else "",
                            style = MaterialTheme.typography.bodyMedium,
                        )
                        Text(
                            "${device.id} · seen ${formatTime(device.lastSeen.orEmpty())}",
                            style = MaterialTheme.typography.labelSmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                    TextButton(
                        onClick = { vm.revokeDevice(device.id) },
                        enabled = !device.current,
                    ) { Text("Revoke") }
                }
            }
        }

        HorizontalDivider(Modifier.padding(vertical = 8.dp))
        OutlinedButton(
            onClick = { showSignOut = true },
            modifier = Modifier.fillMaxWidth(),
        ) { Text("Sign out of this phone") }
        Text(
            "Signing out deletes the phone secret from this device. The server record can be revoked from another enrolled phone or the web console.",
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Spacer(Modifier.height(24.dp))
    }

    if (showSignOut) {
        AlertDialog(
            onDismissRequest = { showSignOut = false },
            title = { Text("Sign out?") },
            text = { Text("This phone will stop receiving approval prompts until it enrolls again.") },
            confirmButton = {
                TextButton(onClick = {
                    showSignOut = false
                    vm.signOut {}
                }) { Text("Sign out") }
            },
            dismissButton = { TextButton(onClick = { showSignOut = false }) { Text("Cancel") } },
        )
    }
}

@Composable
fun PairNodeDialog(vm: AppViewModel, onDismiss: () -> Unit) {
    var deviceId by rememberSaveable { mutableStateOf("") }
    var name by rememberSaveable { mutableStateOf("") }
    var host by rememberSaveable { mutableStateOf("") }
    var force by rememberSaveable { mutableStateOf(false) }
    var result by remember { mutableStateOf<PairNewResponse?>(null) }
    val busy by vm.busy.collectAsState()

    val done = result
    if (done != null) {
        val clipboard = LocalClipboardManager.current
        AlertDialog(
            onDismissRequest = onDismiss,
            title = { Text("Pairing code") },
            text = {
                Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    Text(
                        "Run this on the node within 10 minutes to complete pairing:",
                        style = MaterialTheme.typography.bodySmall,
                    )
                    Text(
                        done.command,
                        style = MaterialTheme.typography.bodySmall,
                        fontFamily = FontFamily.Monospace,
                    )
                    Text("Device: ${done.deviceId}", style = MaterialTheme.typography.bodySmall)
                    Text("Web URL: ${done.webUrl}", style = MaterialTheme.typography.bodySmall)
                }
            },
            confirmButton = {
                TextButton(onClick = { clipboard.setText(AnnotatedString(done.command)) }) {
                    Text("Copy command")
                }
            },
            dismissButton = { TextButton(onClick = onDismiss) { Text("Done") } },
        )
        return
    }

    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("Pair a node") },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Text(
                    "Pairing is the phone-side equivalent of the node setup command. " +
                        "Use the node's device id and its public host.",
                    style = MaterialTheme.typography.bodySmall,
                )
                OutlinedTextField(
                    value = deviceId,
                    onValueChange = { deviceId = it },
                    label = { Text("Device id") },
                    singleLine = true,
                )
                OutlinedTextField(
                    value = name,
                    onValueChange = { name = it },
                    label = { Text("Display name") },
                    singleLine = true,
                )
                OutlinedTextField(
                    value = host,
                    onValueChange = { host = it },
                    label = { Text("Host (optional)") },
                    placeholder = { Text("omp-pc1.example.com") },
                    singleLine = true,
                )
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Checkbox(checked = force, onCheckedChange = { force = it })
                    Text("Replace existing pairing", style = MaterialTheme.typography.bodySmall)
                }
            }
        },
        confirmButton = {
            TextButton(
                onClick = {
                    vm.pairNew(deviceId, name, host, force) { result = it }
                },
                enabled = !busy && deviceId.isNotBlank(),
            ) { Text("Pair") }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Cancel") } },
    )
}

@Composable
fun NodeKeysDialog(vm: AppViewModel, node: NodeEntity, onDismiss: () -> Unit) {
    var name by rememberSaveable(node.id) { mutableStateOf(node.name) }
    var code by rememberSaveable(node.id) { mutableStateOf("") }
    var confirmRemove by remember { mutableStateOf(false) }
    val busy by vm.busy.collectAsState()

    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(node.name) },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Text(node.id, style = MaterialTheme.typography.labelSmall, fontFamily = FontFamily.Monospace)
                OutlinedTextField(
                    value = name,
                    onValueChange = { name = it },
                    label = { Text("Display name") },
                    singleLine = true,
                )
                OutlinedTextField(
                    value = code,
                    onValueChange = { code = it },
                    label = { Text("Browser login code") },
                    supportingText = { Text("Leave empty to keep the current code.") },
                    singleLine = true,
                )
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    OutlinedButton(
                        onClick = {
                            vm.updateNode(
                                node.id,
                                NodeUpdateRequest(
                                    name = name,
                                    loginCode = code.ifBlank { null },
                                ),
                            )
                            onDismiss()
                        },
                        enabled = !busy,
                    ) { Text("Save") }
                    TextButton(onClick = { vm.updateNode(node.id, NodeUpdateRequest(clearLoginCode = true)); onDismiss() }) {
                        Text("Clear code")
                    }
                }
                HorizontalDivider()
                if (!confirmRemove) {
                    TextButton(onClick = { confirmRemove = true }) {
                        Text("Remove node")
                    }
                } else {
                    Text("Remove this node and its pairing?", style = MaterialTheme.typography.bodySmall)
                    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        Button(onClick = { vm.unpair(node.id); onDismiss() }) { Text("Remove") }
                        TextButton(onClick = { confirmRemove = false }) { Text("Cancel") }
                    }
                }
            }
        },
        confirmButton = { TextButton(onClick = onDismiss) { Text("Close") } },
    )
}

private fun formatTime(raw: String): String {
    if (raw.isBlank() || raw.startsWith("0001")) return "never"
    return runCatching {
        val instant = Instant.parse(raw)
        DateTimeFormatter.ofPattern("MM-dd HH:mm")
            .withZone(ZoneId.systemDefault())
            .format(instant)
    }.getOrElse { raw.take(16).replace('T', ' ') }
}
