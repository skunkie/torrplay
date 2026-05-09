// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import React, { useEffect, useRef, useState } from 'react';
import { toast } from 'sonner';

import { AddTorrentDialogLayout } from '@/components/add-torrent-dialog-layout';

interface DemoAddTorrentDialogProps {
  open: boolean,
  onOpenChange: (open: boolean) => void,
  initialUrl?: string | null,
  onSuccess?: () => void
}

export function DemoAddTorrentDialog({ open, onOpenChange, initialUrl, onSuccess }: DemoAddTorrentDialogProps) {
  const [activeTab, setActiveTab] = useState('magnet');
  const [magnet, setMagnet] = useState('');
  const [hash, setHash] = useState('');
  const [file, setFile] = useState<File | null>(null);
  const [title, setTitle] = useState('');
  const [poster, setPoster] = useState('');
  const [category, setCategory] = useState('');
  const [useFileStorage, setUseFileStorage] = useState(false);
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
    toast.success('Torrent added', { description: 'Demo mode - torrent not actually added' });
    if (onSuccess) {
      onSuccess();
    }
    handleOpenChange(false);
  };

  const handleMagnetSubmit = () => {
    if (magnet.startsWith('magnet:')) {
      handleSuccess();
    } else {
      toast.error('Error adding torrent', {
        description: 'Invalid input. Please provide a valid magnet link.',
      });
    }
  };

  const handleHashSubmit = () => {
    if (hash.trim()) {
      handleSuccess();
    } else {
      toast.error('Error adding torrent', {
        description: 'Invalid input. Please provide a valid info hash.',
      });
    }
  };

  const handleFileSubmit = () => {
    if (file) {
      handleSuccess();
    } else {
      toast.error('Error adding torrent', {
        description: 'Please select a torrent file.',
      });
    }
  };

  const filteredCategories = category ? ['Movies', 'Series', 'Cartoons', 'Animation'].filter(cat =>
    cat.toLowerCase().includes(category.toLowerCase())
  ) : [];

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
      loading={false}
    />
  );
}
