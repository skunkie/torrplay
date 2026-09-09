// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package com.github.torrplay.torrplay;

import android.Manifest;
import android.app.AlertDialog;
import android.content.ContentResolver;
import android.content.Intent;
import android.content.SharedPreferences;
import android.content.pm.PackageManager;
import android.net.Uri;
import android.os.Build;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.util.Base64;
import android.util.Log;
import android.webkit.WebSettings;
import android.webkit.WebView;
import android.widget.Toast;

import androidx.activity.OnBackPressedCallback;
import androidx.annotation.NonNull;
import androidx.core.app.ActivityCompat;
import androidx.core.content.FileProvider;
import androidx.core.content.ContextCompat;

import com.getcapacitor.BridgeActivity;

import java.io.File;
import java.io.FileOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.util.ArrayDeque;
import java.util.Locale;
import java.util.Scanner;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

import org.json.JSONObject;

public class MainActivity extends BridgeActivity {

    private static final int PERMISSIONS_REQUEST_STORAGE = 102;
    private static final int PERMISSIONS_REQUEST_NOTIFICATIONS = 103;
    private static final String PREFS_NAME = "torrplay_prefs";
    private static final String KEY_DISMISSED_VERSION = "dismissed_version";
    private static final int MAX_TORRENT_FILE_BYTES = 16 * 1024 * 1024;
    private static final long TORRENT_DELIVERY_RETRY_DELAY_MS = 250;
    private final ExecutorService torrentIntentExecutor = Executors.newSingleThreadExecutor();
    private final Handler mainHandler = new Handler(Looper.getMainLooper());
    private final ArrayDeque<String> pendingTorrentFiles = new ArrayDeque<>();
    private long backPressedTime;
    private Toast backToast;
    private boolean torrentDeliveryInProgress;
    private boolean destroyed;

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);

        startServiceAndRequestPermissions();

        WebView webView = getBridge().getWebView();
        WebSettings settings = webView.getSettings();
        settings.setUseWideViewPort(true);
        settings.setLoadWithOverviewMode(true);

        OnBackPressedCallback callback = new OnBackPressedCallback(true) {
            @Override
            public void handleOnBackPressed() {
                if (webView.canGoBack()) {
                    webView.goBack();
                } else {
                    if (backPressedTime + 2000 > System.currentTimeMillis()) {
                        if (backToast != null) backToast.cancel();
                        finish();
                    } else {
                        backToast = Toast.makeText(MainActivity.this, "Press back again to exit", Toast.LENGTH_SHORT);
                        backToast.show();
                    }
                    backPressedTime = System.currentTimeMillis();
                }
            }
        };
        getOnBackPressedDispatcher().addCallback(this, callback);

        handleIntent(getIntent());
        checkForUpdates();
    }

    private String getDeviceABI() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.LOLLIPOP) {
            String[] abis = Build.SUPPORTED_ABIS;
            if (abis != null && abis.length > 0) {
                for (String abi : abis) {
                    if (abi != null) {
                        return abi.toLowerCase(Locale.ROOT);
                    }
                }
            }
        }
        return Build.CPU_ABI.toLowerCase(Locale.ROOT);
    }

    private String getAPKUrlForABI(String releaseUrl, String targetVersion) throws Exception {
        String abi = getDeviceABI();
        String targetAPK = null;

        if (abi.contains("arm64")) {
            targetAPK = "torrplay-android-arm64-v8a.apk";
        } else if (abi.contains("armeabi")) {
            targetAPK = "torrplay-android-armeabi-v7a.apk";
        } else if (abi.contains("x86_64")) {
            targetAPK = "torrplay-android-x86_64.apk";
        } else if (abi.contains("x86")) {
            targetAPK = "torrplay-android-x86.apk";
        } else {
            targetAPK = "torrplay-android-universal.apk";
        }

        String baseUrl = releaseUrl.replace("/releases/latest", "");

        return baseUrl + "/releases/download/" + targetVersion + "/" + targetAPK;
    }

    private String normalizeVersion(String version) {
        if (version == null)
            return "";
        if (version.startsWith("v")) {
            return version.substring(1);
        }
        return version;
    }

    private String[] getLatestReleaseInfo(String releaseUrl) {
        try {
            String apiUrl = releaseUrl.replace("https://github.com/", "https://api.github.com/repos/");

            URL url = new URL(apiUrl);
            HttpURLConnection conn = (HttpURLConnection) url.openConnection();
            conn.setRequestMethod("GET");
            conn.setRequestProperty("Accept", "application/vnd.github.v3+json");
            conn.setRequestProperty("User-Agent", "TorrPlay/1.0");

            int responseCode = conn.getResponseCode();
            if (responseCode == HttpURLConnection.HTTP_OK) {
                InputStream inputStream = conn.getInputStream();
                String response = new Scanner(inputStream).useDelimiter("\\Z").next();
                JSONObject json = new JSONObject(response);
                String version = json.getString("tag_name");
                String body = json.optString("body", "");
                if (body.length() > 1000) {
                    body = body.substring(0, 1000) + "...";
                }
                return new String[]{version, body};
            }
        } catch (Exception e) {
            Log.e("MainActivity", "Error fetching latest release", e);
        }
        return null;
    }

    private void checkForUpdates() {
        new Thread(() -> {
            try {
                String releaseUrl = BuildConfig.GITHUB_RELEASES_URL;
                String[] releaseInfo = getLatestReleaseInfo(releaseUrl);

                if (releaseInfo == null) {
                    return;
                }

                String latestVersion = releaseInfo[0];
                String releaseBody = releaseInfo[1];
                String currentVersion = normalizeVersion(BuildConfig.VERSION_NAME);
                String normalizedLatest = normalizeVersion(latestVersion);

                SharedPreferences prefs = getSharedPreferences(PREFS_NAME, 0);
                String dismissed = prefs.getString(KEY_DISMISSED_VERSION, "");

                if (!normalizedLatest.equals(currentVersion) && !dismissed.equals(normalizedLatest)) {
                    String apkUrl = getAPKUrlForABI(releaseUrl, latestVersion);

                    final StringBuilder updateMsgBuilder = new StringBuilder();
                    updateMsgBuilder.append("A new version (").append(latestVersion).append(") is available.");
                    if (!releaseBody.isEmpty()) {
                        updateMsgBuilder.append("\n\n").append(releaseBody);
                    }
                    updateMsgBuilder.append("\n\nDo you want to download and install it?");
                    final String updateMessage = updateMsgBuilder.toString();

                    runOnUiThread(() -> {
                        new AlertDialog.Builder(MainActivity.this)
                                .setTitle("Update Available")
                                .setMessage(updateMessage)
                                .setPositiveButton("UPDATE", (dialog, which) -> {
                                    new Thread(() -> downloadAndInstallAPK(apkUrl)).start();
                                })
                                .setNeutralButton("DON'T ASK AGAIN", (dialog, which) -> {
                                    getSharedPreferences(PREFS_NAME, 0).edit().putString(KEY_DISMISSED_VERSION, normalizedLatest).apply();
                                })
                                .setNegativeButton("CANCEL", null)
                                .show();
                    });
                }
            } catch (Exception e) {
                Log.e("MainActivity", "Error checking for updates", e);
            }
        }).start();
    }

    private void downloadAndInstallAPK(String apkUrl) {
        HttpURLConnection connection = null;
        FileOutputStream fos = null;
        InputStream inputStream = null;

        try {
            URL url = new URL(apkUrl);
            connection = (HttpURLConnection) url.openConnection();
            connection.setRequestMethod("GET");
            connection.setConnectTimeout(30000);
            connection.setReadTimeout(30000);
            connection.setDoInput(true);

            int responseCode = connection.getResponseCode();
            if (responseCode != HttpURLConnection.HTTP_OK) {
                Log.e("MainActivity", "HTTP error: " + responseCode);
                return;
            }

            inputStream = connection.getInputStream();
            File downloadDir = new File(getCacheDir(), "updates");
            if (!downloadDir.exists() && !downloadDir.mkdirs()) {
                throw new IOException("Could not create update directory");
            }
            File apkFile = new File(downloadDir, "torrplay-update.apk");

            fos = new FileOutputStream(apkFile);
            byte[] buffer = new byte[4096];
            int bytesRead;
            while ((bytesRead = inputStream.read(buffer)) != -1) {
                fos.write(buffer, 0, bytesRead);
            }
            fos.flush();

            installAPK(apkFile);

        } catch (Exception e) {
            Log.e("MainActivity", "Error downloading APK", e);
        } finally {
            if (inputStream != null) {
                try {
                    inputStream.close();
                } catch (IOException e) {
                }
            }
            if (fos != null) {
                try {
                    fos.close();
                } catch (IOException e) {
                }
            }
            if (connection != null) {
                connection.disconnect();
            }
        }
    }

    private void installAPK(File apkFile) {
        if (!apkFile.exists()) {
            Log.e("MainActivity", "APK file not found");
            return;
        }

        Uri uri;
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.N) {
            uri = FileProvider.getUriForFile(this, getPackageName() + ".fileprovider", apkFile);
        } else {
            uri = Uri.fromFile(apkFile);
        }

        Intent intent = new Intent(Intent.ACTION_VIEW);
        intent.setDataAndType(uri, "application/vnd.android.package-archive");
        intent.setFlags(Intent.FLAG_ACTIVITY_NEW_TASK | Intent.FLAG_GRANT_READ_URI_PERMISSION);
        intent.putExtra(Intent.EXTRA_NOT_UNKNOWN_SOURCE, true);

        try {
            startActivity(intent);
        } catch (Exception e) {
            Log.e("MainActivity", "Error installing APK", e);
        }
    }

    private void startServiceAndRequestPermissions() {
        if (Build.VERSION.SDK_INT <= Build.VERSION_CODES.S_V2
                && ContextCompat.checkSelfPermission(this, Manifest.permission.WRITE_EXTERNAL_STORAGE) != PackageManager.PERMISSION_GRANTED) {
            ActivityCompat.requestPermissions(
                    this,
                    new String[]{Manifest.permission.READ_EXTERNAL_STORAGE, Manifest.permission.WRITE_EXTERNAL_STORAGE},
                    PERMISSIONS_REQUEST_STORAGE
            );
            return;
        }

        startTorrPlayService();
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU && ContextCompat.checkSelfPermission(this, Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) {
            ActivityCompat.requestPermissions(this, new String[]{Manifest.permission.POST_NOTIFICATIONS}, PERMISSIONS_REQUEST_NOTIFICATIONS);
        }
    }

    @Override
    protected void onNewIntent(Intent intent) {
        super.onNewIntent(intent);
        setIntent(intent);
        handleIntent(intent);
    }

    private void handleIntent(Intent intent) {
        if (intent == null) return;

        Uri uri = null;
        String action = intent.getAction();

        if (Intent.ACTION_SEND.equals(action) && intent.hasExtra(Intent.EXTRA_STREAM)) {
            uri = intent.getParcelableExtra(Intent.EXTRA_STREAM);
        } else if (Intent.ACTION_VIEW.equals(action)) {
            uri = intent.getData();
        }

        if (uri != null && ContentResolver.SCHEME_CONTENT.equals(uri.getScheme())) {
            final Uri contentUri = uri;
            torrentIntentExecutor.execute(() -> readAndQueueTorrentFile(contentUri));
        }
    }

    private void readAndQueueTorrentFile(Uri uri) {
        try (InputStream inputStream = getContentResolver().openInputStream(uri)) {
            if (inputStream == null) {
                throw new IOException("Content provider returned no data");
            }

            byte[] bytes = TorrentFileReader.read(inputStream, MAX_TORRENT_FILE_BYTES);
            String base64Data = Base64.encodeToString(bytes, Base64.NO_WRAP);

            mainHandler.post(() -> {
                if (destroyed) return;
                pendingTorrentFiles.add(base64Data);
                deliverPendingTorrentFile();
            });
        } catch (Exception e) {
            Log.e("MainActivity", "Error processing content URI", e);
            mainHandler.post(() -> {
                if (!destroyed) {
                    Toast.makeText(MainActivity.this, "Unable to open torrent file: " + e.getMessage(), Toast.LENGTH_LONG).show();
                }
            });
        }
    }

    private void deliverPendingTorrentFile() {
        if (destroyed || torrentDeliveryInProgress || pendingTorrentFiles.isEmpty()) return;

        String base64Data = pendingTorrentFiles.peek();
        String js = "(function() {"
                + "if (typeof window.handleTorrentFileBase64 !== 'function') return false;"
                + "window.handleTorrentFileBase64(" + JSONObject.quote(base64Data) + ");"
                + "return true;"
                + "})()";

        torrentDeliveryInProgress = true;
        getBridge().getWebView().evaluateJavascript(js, result -> {
            torrentDeliveryInProgress = false;
            if ("true".equals(result)) {
                pendingTorrentFiles.remove();
                deliverPendingTorrentFile();
            } else if (!destroyed) {
                mainHandler.postDelayed(this::deliverPendingTorrentFile, TORRENT_DELIVERY_RETRY_DELAY_MS);
            }
        });
    }

    @Override
    public void onDestroy() {
        destroyed = true;
        mainHandler.removeCallbacksAndMessages(null);
        torrentIntentExecutor.shutdownNow();
        if (backToast != null) {
            backToast.cancel();
            backToast = null;
        }
        super.onDestroy();
    }

    private void startTorrPlayService() {
        Intent serviceIntent = new Intent(this, TorrPlayService.class);
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            startForegroundService(serviceIntent);
        } else {
            startService(serviceIntent);
        }
    }

    @Override
    public void onRequestPermissionsResult(int requestCode, @NonNull String[] permissions, @NonNull int[] grantResults) {
        super.onRequestPermissionsResult(requestCode, permissions, grantResults);

        if (requestCode == PERMISSIONS_REQUEST_STORAGE) {
            startTorrPlayService();
        }

        if (requestCode == PERMISSIONS_REQUEST_NOTIFICATIONS
                && grantResults.length > 0
                && grantResults[0] != PackageManager.PERMISSION_GRANTED) {
            Toast.makeText(this, "Notifications are disabled; background activity may be less visible.", Toast.LENGTH_LONG).show();
        } else if (requestCode == PERMISSIONS_REQUEST_STORAGE
                && ContextCompat.checkSelfPermission(this, Manifest.permission.WRITE_EXTERNAL_STORAGE) != PackageManager.PERMISSION_GRANTED) {
            Toast.makeText(this, "Shared-storage downloads are unavailable without storage permission.", Toast.LENGTH_LONG).show();
        }
    }
}
