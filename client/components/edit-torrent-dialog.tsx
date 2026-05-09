// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import React, { useEffect, useRef, useState } from 'react';
import { toast } from 'sonner';

import { getCategories, updateTorrent } from '@/lib/api/torrents';
import type { Torrent } from '@/lib/types/api';

import { EditTorrentDialogLayout } from './edit-torrent-dialog-layout';

interface EditTorrentDialogProps {
  torrent: Torrent | null,
  open: boolean,
  onOpenChange: (open: boolean) => void,
  onSuccess: () => void
}

export function EditTorrentDialog({ torrent, open, onOpenChange, onSuccess }: EditTorrentDialogProps) {
  const [loading, setLoading] = useState(false);
  const [title, setTitle] = useState('');
  const [poster, setPoster] = useState('');
  const [category, setCategory] = useState('');
  const [useFileStorage, setUseFileStorage] = useState(false);
  const [categories, setCategories] = useState<string[]>([]);
  const [dragOver, setDragOver] = useState(false);
  const [showSuggestions, setShowSuggestions] = useState(false);
  const categoryInputRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    if (torrent) {
      setTitle(torrent.title || '');
      setPoster(torrent.poster || '');
      setCategory(torrent.category || '');
      setUseFileStorage(torrent.storage === 'file');
    }
  }, [torrent]);

  useEffect(() => {
    async function fetchCategories() {
      try {
        const fetchedCategories = await getCategories();
        setCategories(fetchedCategories);
      } catch (error) {
        console.error('Failed to fetch categories', error);
      }
    }

    if (open) {
      fetchCategories();
    }
  }, [open]);

  useEffect(() => {
    const handleClickOutside = (event: MouseEvent) => {
      if (categoryInputRef.current && !categoryInputRef.current.contains(event.target as Node)) {
        setShowSuggestions(false);
      }
    };
    document.addEventListener('mousedown', handleClickOutside);
    return () => {
      document.removeEventListener('mousedown', handleClickOutside);
    };
  }, []);

  const handleSubmit = async () => {
    if (!torrent) return;

    setLoading(true);

    try {
      await updateTorrent(torrent.hash, {
        title: title || undefined,
        poster: poster === '' ? '' : poster || undefined,
        category: category || undefined,
        storage: useFileStorage ? 'file' : 'memory',
      });

      toast.success('Torrent updated', {
        description: 'The torrent metadata has been updated successfully',
      });

      onOpenChange(false);
      onSuccess();
    } catch (error) {
      toast.error('Error', {
        description: error instanceof Error ? error.message : 'Failed to update torrent',
      });
    } finally {
      setLoading(false);
    }
  };

  const handleDragOver = (e: React.DragEvent<HTMLDivElement>) => {
    e.preventDefault();
    e.stopPropagation();
    setDragOver(true);
  };

  const handleDragEnter = (e: React.DragEvent<HTMLDivElement>) => {
    e.preventDefault();
    e.stopPropagation();
    setDragOver(true);
  };

  const handleDragLeave = (e: React.DragEvent<HTMLDivElement>) => {
    e.preventDefault();
    e.stopPropagation();
    setDragOver(false);
  };

  const handleDrop = (e: React.DragEvent<HTMLDivElement>) => {
    e.preventDefault();
    e.stopPropagation();
    setDragOver(false);

    const files = e.dataTransfer.files;
    if (files && files.length > 0) {
      const file = files[0];
      if (file.type.startsWith('image/')) {
        const reader = new FileReader();
        reader.onloadend = () => {
          setPoster(reader.result as string);
          toast.success('Image selected', {
            description: 'The poster has been updated with the selected image.',
          });
        };
        reader.readAsDataURL(file);
      } else {
        toast.error('Invalid file type', {
          description: 'Please drop an image file.',
        });
      }
    }
  };

  const filteredCategories = categories.filter(cat =>
    cat.toLowerCase().includes(category.toLowerCase())
  );

  return (
    <EditTorrentDialogLayout
      torrent={torrent}
      open={open}
      onOpenChange={onOpenChange}
      loading={loading}
      title={title}
      setTitle={setTitle}
      poster={poster}
      setPoster={setPoster}
      category={category}
      setCategory={setCategory}
      useFileStorage={useFileStorage}
      setUseFileStorage={setUseFileStorage}
      dragOver={dragOver}
      showSuggestions={showSuggestions}
      setShowSuggestions={setShowSuggestions}
      categoryInputRef={categoryInputRef}
      filteredCategories={filteredCategories}
      handleSubmit={handleSubmit}
      handleDragOver={handleDragOver}
      handleDragEnter={handleDragEnter}
      handleDragLeave={handleDragLeave}
      handleDrop={handleDrop}
    />
  );
}
