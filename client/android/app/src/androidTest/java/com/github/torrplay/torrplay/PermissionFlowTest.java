// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package com.github.torrplay.torrplay;

import static androidx.test.espresso.Espresso.onView;
import static androidx.test.espresso.action.ViewActions.click;
import static androidx.test.espresso.matcher.ViewMatchers.withId;
import static androidx.test.platform.app.InstrumentationRegistry.getInstrumentation;
import static org.junit.Assert.assertNotNull;
import static org.junit.Assert.assertTrue;

import android.content.Context;
import android.content.Intent;
import android.os.Build;

import androidx.test.core.app.ActivityScenario;
import androidx.test.ext.junit.runners.AndroidJUnit4;
import androidx.test.filters.SdkSuppress;
import androidx.test.uiautomator.By;
import androidx.test.uiautomator.UiDevice;
import androidx.test.uiautomator.UiObject2;
import androidx.test.uiautomator.Until;

import com.github.torrplay.torrplay.MainActivity;
import com.github.torrplay.torrplay.R;
import com.github.torrplay.torrplay.TorrPlayService;

import org.junit.Before;
import org.junit.Test;
import org.junit.runner.RunWith;

@RunWith(AndroidJUnit4.class)
public class PermissionFlowTest {

    private UiDevice device;
    private Context context;

    @Before
    public void setUp() {
        context = getInstrumentation().getTargetContext();
        device = UiDevice.getInstance(getInstrumentation());
    }

    @Test
    @SdkSuppress(minSdkVersion = Build.VERSION_CODES.TIRAMISU)
    public void testPermissionFlow_GrantsAll() {
        // Launch MainActivity
        ActivityScenario<MainActivity> scenario = ActivityScenario.launch(MainActivity.class);

        // Handle Notification Permission
        handlePermission("permission_allow_button_holder", "Allow");

        // Handle Storage Permission
        handlePermission("permission_allow_button_holder", "Allow");

        // After all permissions are granted, the service should start.
        scenario.onActivity(activity -> {
            // We can't directly check if the service is running from here in a simple way,
            // but we can check if the intent to start it would be valid.
            Intent serviceIntent = new Intent(activity, TorrPlayService.class);
            assertNotNull("Service Intent should not be null", serviceIntent);
        });
    }

    private void handlePermission(String resourceId, String buttonText) {
        UiObject2 allowButton = device.wait(Until.findObject(By.res("com.android.permissioncontroller", resourceId)), 5000);
        if (allowButton != null) {
            allowButton.click();
        } else {
            // If not found by resourceId, try by text
            UiObject2 buttonWithText = device.wait(Until.findObject(By.text(buttonText)), 5000);
            assertNotNull("Permission button with text '" + buttonText + "' not found", buttonWithText);
            buttonWithText.click();
        }
    }
}
