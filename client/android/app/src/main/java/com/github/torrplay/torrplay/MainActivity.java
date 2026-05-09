// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package com.github.torrplay.torrplay;

import android.Manifest;
import android.content.ContentResolver;
import android.content.Intent;
import android.content.pm.PackageManager;
import android.net.Uri;
import android.os.Build;
import android.os.Bundle;
import android.provider.Settings;
import android.util.Base64;
import android.util.Log;
import android.webkit.WebSettings;
import android.webkit.WebView;
import android.widget.Toast;

import androidx.activity.OnBackPressedCallback;
import androidx.annotation.NonNull;
import androidx.core.app.ActivityCompat;
import androidx.core.content.ContextCompat;

import com.getcapacitor.BridgeActivity;

import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;

public class MainActivity extends BridgeActivity {

    private static final int PERMISSIONS_REQUEST_STORAGE = 102;
    private static final int PERMISSIONS_REQUEST_NOTIFICATIONS = 103;
    private long backPressedTime;
    private Toast backToast;

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);

        checkAndRequestPermissions();

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
    }

    private void checkAndRequestPermissions() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU && ContextCompat.checkSelfPermission(this, Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) {
            ActivityCompat.requestPermissions(this, new String[]{Manifest.permission.POST_NOTIFICATIONS}, PERMISSIONS_REQUEST_NOTIFICATIONS);
        } else if (ContextCompat.checkSelfPermission(this, Manifest.permission.READ_EXTERNAL_STORAGE) != PackageManager.PERMISSION_GRANTED) {
            ActivityCompat.requestPermissions(this, new String[]{Manifest.permission.READ_EXTERNAL_STORAGE}, PERMISSIONS_REQUEST_STORAGE);
        } else {
            startTorrPlayService();
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
            try {
                ContentResolver resolver = getContentResolver();
                InputStream inputStream = resolver.openInputStream(uri);
                byte[] bytes = getBytes(inputStream);
                String base64Data = Base64.encodeToString(bytes, Base64.NO_WRAP);

                String jsFunction = "window.handleTorrentFileBase64";
                String js = "if (" + jsFunction + ") { " + jsFunction + "('" + base64Data + "'); }";

                getBridge().getWebView().post(() -> getBridge().getWebView().evaluateJavascript(js, null));
            } catch (IOException e) {
                Log.e("MainActivity", "Error processing content URI to Base64", e);
            }
        }
    }

    private byte[] getBytes(InputStream inputStream) throws IOException {
        if (inputStream == null) return new byte[0];
        ByteArrayOutputStream byteBuffer = new ByteArrayOutputStream();
        byte[] buffer = new byte[1024];
        int len;
        while ((len = inputStream.read(buffer)) != -1) {
            byteBuffer.write(buffer, 0, len);
        }
        return byteBuffer.toByteArray();
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

        switch (requestCode) {
            case PERMISSIONS_REQUEST_NOTIFICATIONS:
                // After notification permission, request storage permission.
                if (ContextCompat.checkSelfPermission(this, Manifest.permission.READ_EXTERNAL_STORAGE) != PackageManager.PERMISSION_GRANTED) {
                    ActivityCompat.requestPermissions(this, new String[]{Manifest.permission.READ_EXTERNAL_STORAGE}, PERMISSIONS_REQUEST_STORAGE);
                } else {
                    startTorrPlayService();
                }
                break;
            case PERMISSIONS_REQUEST_STORAGE:
                // After storage permission, all required permissions have been requested.
                startTorrPlayService();
                break;
            default:
                break;
        }

        if (grantResults.length > 0 && grantResults[0] != PackageManager.PERMISSION_GRANTED) {
            if (requestCode == PERMISSIONS_REQUEST_NOTIFICATIONS) {
                Toast.makeText(this, "Notification permission is required for background tasks.", Toast.LENGTH_LONG).show();
            } else if (requestCode == PERMISSIONS_REQUEST_STORAGE) {
                Toast.makeText(this, "Storage permission is recommended for full functionality.", Toast.LENGTH_LONG).show();
            }
        }
    }
}
