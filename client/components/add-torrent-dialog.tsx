// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import { useEffect, useRef, useState } from 'react';
import { toast } from 'sonner';

import { addTorrent, getCategories } from '@/lib/api/torrents';
import type { TorrentAdd, TorrentAddWithFile } from '@/lib/types/api';

import { AddTorrentDialogLayout } from './add-torrent-dialog-layout';

interface AddTorrentDialogProps {
  open: boolean,
  onOpenChange: (open: boolean) => void,
  onSuccess: () => void,
  initialUrl?: string | null
}

export function AddTorrentDialog({ open, onOpenChange, onSuccess, initialUrl }: AddTorrentDialogProps) {
  const [loading, setLoading] = useState(false);
  const [activeTab, setActiveTab] = useState('magnet');
  const [magnet, setMagnet] = useState('');
  const [hash, setHash] = useState('');
  const [file, setFile] = useState<File | null>(null);
  const [title, setTitle] = useState('');
  const [poster, setPoster] = useState('');
  const [category, setCategory] = useState('');
  const [useFileStorage, setUseFileStorage] = useState(false);
  const [categories, setCategories] = useState<string[]>([]);
  const [showSuggestions, setShowSuggestions] = useState(false);
  const categoryInputRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    if (initialUrl) {
      if (initialUrl.startsWith('magnet:')) {
        setMagnet(initialUrl);
        setActiveTab('magnet');
      }
    }
  }, [initialUrl]);

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

  const resetForm = () => {
    setMagnet('');
    setHash('');
    setFile(null);
    setTitle('');
    setPoster('');
    setCategory('');
    setUseFileStorage(false);
  };

  const handleOpenChange = (isOpen: boolean) => {
    onOpenChange(isOpen);
    if (!isOpen) {
      resetForm();
    }
  };

  const handleSuccess = () => {
    toast.success('Torrent added', { description: 'The torrent has been added successfully' });
    onSuccess();
    handleOpenChange(false);
  };

  const handleMagnetSubmit = async () => {
    setLoading(true);
    try {
      if (magnet.startsWith('magnet:')) {
        const data: TorrentAdd = {
          magnet: magnet,
          ...(title && { title }),
          ...(poster && { poster }),
          ...(category && { category }),
          storage: useFileStorage ? 'file' : 'memory',
        };
        await addTorrent(data);
      } else {
        throw new Error('Invalid input. Please provide a magnet link.');
      }
      handleSuccess();
    } catch (error) {
      toast.error('Error adding torrent', {
        description: error instanceof Error ? error.message : 'An unknown error occurred',
      });
    } finally {
      setLoading(false);
    }
  };

  const handleHashSubmit = async () => {
    setLoading(true);
    try {
      const data: TorrentAdd = {
        hash,
        ...(title && { title }),
        ...(poster && { poster }),
        ...(category && { category }),
        storage: useFileStorage ? 'file' : 'memory',
      };
      await addTorrent(data);
      handleSuccess();
    } catch (error) {
      toast.error('Error adding torrent', {
        description: error instanceof Error ? error.message : 'An unknown error occurred',
      });
    } finally {
      setLoading(false);
    }
  };

  const handleFileSubmit = async () => {
    if (!file) return;
    setLoading(true);
    try {
      const data: TorrentAddWithFile = {
        file,
        ...(title && { title }),
        ...(poster && { poster }),
        storage: useFileStorage ? 'file' : 'memory',
      };
      await addTorrent(data);
      handleSuccess();
    } catch (error) {
      toast.error('Error adding torrent', {
        description: error instanceof Error ? error.message : 'An unknown error occurred',
      });
    } finally {
      setLoading(false);
    }
  };

  const filteredCategories = categories.filter(cat =>
    cat.toLowerCase().includes(category.toLowerCase())
  );

  return (
    <AddTorrentDialogLayout
      open={open}
      onOpenChange={handleOpenChange}
      activeTab={activeTab}
      setActiveTab={setActiveTab}
      magnet={magnet}
      setMagnet={setMagnet}
      hash={hash}
      setHash={setHash}
      file={file}
      setFile={setFile}
      title={title}
      setTitle={setTitle}
      poster={poster}
      setPoster={setPoster}
      category={category}
      setCategory={setCategory}
      useFileStorage={useFileStorage}
      setUseFileStorage={setUseFileStorage}
      showSuggestions={showSuggestions}
      setShowSuggestions={setShowSuggestions}
      categoryInputRef={categoryInputRef}
      filteredCategories={filteredCategories}
      handleMagnetSubmit={handleMagnetSubmit}
      handleHashSubmit={handleHashSubmit}
      handleFileSubmit={handleFileSubmit}
      loading={loading}
    />
  );
}
