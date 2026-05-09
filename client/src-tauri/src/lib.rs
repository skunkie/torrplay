// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

#[cfg(target_os = "macos")]
use std::sync::{Arc, Mutex};
#[cfg(target_os = "macos")]
use tauri::Manager;
#[cfg(target_os = "macos")]
use tauri_plugin_dialog::DialogExt;
#[cfg(target_os = "macos")]
use tauri_plugin_shell::{
    process::{CommandChild, CommandEvent},
    ShellExt,
};

#[cfg(target_os = "macos")]
static SIDECAR_STATE: std::sync::OnceLock<Arc<Mutex<Option<CommandChild>>>> =
    std::sync::OnceLock::new();

#[cfg(target_os = "macos")]
extern "C" fn cleanup_sidecar() {
    if let Some(state) = SIDECAR_STATE.get() {
        if let Ok(mut guard) = state.lock() {
            if let Some(child) = guard.take() {
                if let Err(e) = child.kill() {
                    eprintln!("failed to stop sidecar: {}", e);
                } else {
                    println!("sidecar stopped successfully.");
                }
            }
        }
    }
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    let builder = tauri::Builder::default()
        .plugin(tauri_plugin_shell::init())
        .plugin(tauri_plugin_dialog::init())
        .plugin(tauri_plugin_opener::init())
        .plugin(tauri_plugin_window_state::Builder::default().build());

    let builder = {
        #[cfg(target_os = "macos")]
        {
            let sidecar_state = Arc::new(Mutex::new(None::<CommandChild>));
            SIDECAR_STATE
                .set(sidecar_state.clone())
                .expect("SIDECAR_STATE could not be set");

            builder.setup(move |app| {
                let handle = app.handle().clone();

                unsafe {
                    libc::atexit(cleanup_sidecar);
                }

                let sidecar_command = handle
                    .shell()
                    .sidecar("torrplay")
                    .expect("failed to create `torrplay` sidecar")
                    .env("TORRPLAY_RUNNING_AS_SERVICE", "true");

                match sidecar_command.spawn() {
                    Ok((mut rx, child)) => {
                        *sidecar_state.lock().unwrap() = Some(child);

                        tauri::async_runtime::spawn(async move {
                            while let Some(event) = rx.recv().await {
                                match event {
                                    CommandEvent::Terminated(payload) => {
                                        eprintln!(
                                            "sidecar terminated unexpectedly: {:?}",
                                            payload.code
                                        );
                                        break;
                                    }
                                    CommandEvent::Stderr(line) => {
                                        eprintln!(
                                            "sidecar stderr: {}",
                                            String::from_utf8_lossy(&line)
                                        );
                                    }
                                    _ => {}
                                }
                            }
                        });
                    }
                    Err(e) => {
                        let msg = format!("failed to spawn sidecar:\n{}", e);
                        eprintln!("{}", msg);

                        let h = handle.clone();
                        tauri::async_runtime::spawn(async move {
                            if let Some(window) = h.get_webview_window("main") {
                                let _ = window
                                    .dialog()
                                    .message(&msg)
                                    .title("critical sidecar error")
                                    .show(move |_| {
                                        h.exit(1);
                                    });
                            } else {
                                h.exit(1);
                            }
                        });
                    }
                }
                Ok(())
            })
        }
        #[cfg(not(target_os = "macos"))]
        {
            builder
        }
    };

    builder
        .run(tauri::generate_context!("tauri.conf.json"))
        .expect("error while running tauri application");
}
