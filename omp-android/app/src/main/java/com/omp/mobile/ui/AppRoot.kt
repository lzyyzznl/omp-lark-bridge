package com.omp.mobile.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Home
import androidx.compose.material.icons.filled.Notifications
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material.icons.filled.Settings
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ExtendedFloatingActionButton
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.NavigationBar
import androidx.compose.material3.NavigationBarItem
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import com.omp.mobile.data.AppSettings
import com.omp.mobile.web.WebViewActivity

private data class WebTarget(val title: String, val device: String)

private fun tabIndex(tab: String?): Int = when (tab) {
    "approvals" -> 1
    "settings" -> 2
    else -> 0
}

@Composable
fun AppRoot(vm: AppViewModel, initialTab: String?, tabSignal: Int = 0) {
    val settings by vm.settings.collectAsState()
    val message by vm.message.collectAsState()
    val snackbar = remember { SnackbarHostState() }

    LaunchedEffect(message) {
        message?.let {
            snackbar.showSnackbar(it)
            vm.clearMessage()
        }
    }

    var web by remember { mutableStateOf<WebTarget?>(null) }
    val context = LocalContext.current

    // Resolve the one-time signed URL, then hand the node's web UI to its own
    // Activity. Hosting the WebView inside this Compose tree produced a
    // zero-height Blink viewport (nothing painted), so it is a separate window.
    LaunchedEffect(web) {
        val target = web ?: return@LaunchedEffect
        try {
            val url = vm.grantUrl(target.device)
            context.startActivity(WebViewActivity.intent(context, url, target.title))
        } catch (t: Throwable) {
            vm.message.value = "Open node failed: ${t.message}"
        } finally {
            web = null
        }
    }

    Surface(modifier = Modifier.fillMaxSize()) {
        Box(Modifier.fillMaxSize()) {
            val s = settings
            when {
                s == null -> Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                    CircularProgressIndicator()
                }
                !s.enrolled -> SetupScreen(vm, s)
                else -> MainShell(
                    vm = vm,
                    settings = s,
                    initialTab = initialTab,
                    tabSignal = tabSignal,
                    onOpenNode = { id, name -> web = WebTarget(name, id) },
                )
            }
            if (web != null) {
                Box(
                    Modifier
                        .fillMaxSize()
                        .background(MaterialTheme.colorScheme.scrim.copy(alpha = 0.32f)),
                    contentAlignment = Alignment.Center,
                ) {
                    CircularProgressIndicator()
                }
            }
            SnackbarHost(
                hostState = snackbar,
                modifier = Modifier
                    .align(Alignment.BottomCenter)
                    .padding(16.dp),
            )
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun MainShell(
    vm: AppViewModel,
    settings: AppSettings,
    initialTab: String?,
    tabSignal: Int,
    onOpenNode: (String, String) -> Unit,
) {
    var tab by remember { mutableStateOf(tabIndex(initialTab)) }
    var showPair by remember { mutableStateOf(false) }
    val nodes by vm.nodes.collectAsState()

    // A notification tap while the shell is already composed delivers the tab
    // through onNewIntent; follow it unless the user has since navigated.
    LaunchedEffect(tabSignal) {
        if (tabSignal > 0) tab = tabIndex(initialTab)
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = {
                    Text(
                        when (tab) {
                            0 -> "Nodes"
                            1 -> "Approvals"
                            else -> "Settings"
                        },
                    )
                },
                actions = {
                    if (tab != 2) {
                        IconButton(
                            onClick = {
                                if (tab == 0) vm.refreshNodes() else vm.refreshApprovals()
                            },
                        ) {
                            Icon(Icons.Filled.Refresh, contentDescription = "Refresh")
                        }
                    }
                },
            )
        },
        floatingActionButton = {
            if (tab == 0) {
                ExtendedFloatingActionButton(
                    onClick = { showPair = true },
                    icon = { Icon(Icons.Filled.Home, null) },
                    text = { Text("Add node") },
                )
            }
        },
        bottomBar = {
            NavigationBar {
                NavigationBarItem(
                    selected = tab == 0,
                    onClick = { tab = 0 },
                    icon = { Icon(Icons.Filled.Home, null) },
                    label = { Text("Nodes") },
                )
                NavigationBarItem(
                    selected = tab == 1,
                    onClick = { tab = 1 },
                    icon = { Icon(Icons.Filled.Notifications, null) },
                    label = { Text("Approvals") },
                )
                NavigationBarItem(
                    selected = tab == 2,
                    onClick = { tab = 2 },
                    icon = { Icon(Icons.Filled.Settings, null) },
                    label = { Text("Settings") },
                )
            }
        },
    ) { padding ->
        Box(
            Modifier
                .fillMaxSize()
                .padding(padding),
        ) {
            when (tab) {
                0 -> NodesScreen(vm, nodes, onOpenNode, onAddNode = { showPair = true })
                1 -> ApprovalsScreen(vm, onOpenNode)
                else -> SettingsScreen(vm, settings)
            }
        }
    }

    if (showPair) {
        PairNodeDialog(vm, onDismiss = { showPair = false })
    }
}
