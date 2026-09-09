// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package com.github.torrplay.torrplay;

import static org.junit.Assert.assertArrayEquals;
import static org.junit.Assert.assertThrows;

import java.io.ByteArrayInputStream;
import java.io.IOException;

import org.junit.Test;

public class TorrentFileReaderTest {
    @Test
    public void readReturnsDataAtLimit() throws Exception {
        byte[] data = new byte[]{1, 2, 3, 4};

        byte[] result = TorrentFileReader.read(new ByteArrayInputStream(data), data.length);

        assertArrayEquals(data, result);
    }

    @Test
    public void readRejectsDataOverLimit() {
        byte[] data = new byte[]{1, 2, 3, 4, 5};

        assertThrows(
                IOException.class,
                () -> TorrentFileReader.read(new ByteArrayInputStream(data), data.length - 1)
        );
    }

    @Test
    public void readRejectsNegativeLimit() {
        assertThrows(
                IllegalArgumentException.class,
                () -> TorrentFileReader.read(new ByteArrayInputStream(new byte[0]), -1)
        );
    }
}
